import { describe, expect, it, vi } from "vitest";

import {
  CaptureClient,
  IdenqaProtocolError,
  type CaptureWebSocket,
  type VerificationSession,
} from "../src/index.js";

const verificationId = "ver_01M11HEQG00000000000000000";
const connectionId = "con_01M11HEQG00000000000000000";
const commandId = "cmd_01M11HEQG00000000000000000";

describe("CaptureClient realtime observation", () => {
  it("binds hello, acknowledges server events, and resolves a capture command", async () => {
    const socket = new FakeSocket();
    const client = captureClient();
    const observation = client.observe(session(), {
      webSocketFactory: (url, protocols) => {
        expect(url).toContain("ticket=display-once");
        expect(protocols).toEqual(["idenqa.capture.v1"]);
        queueMicrotask(() => socket.open());
        return socket;
      },
    });
    const iterator = observation[Symbol.asyncIterator]();
    await waitFor(() => socket.sent.length === 1);
    expect(socket.message(0)).toMatchObject({
      type: "client.hello",
      verification_id: verificationId,
      sequence: 1,
      payload: {
        last_acknowledged_server_sequence: 0,
        last_acknowledged_event_cursor: 0,
      },
    });

    socket.receive(
      serverMessage("server.welcome", 1, {
        selected_version: 1,
        session_version: 1,
        ping_interval_ms: 15_000,
        pong_timeout_ms: 5_000,
        idle_timeout_ms: 60_000,
      }),
    );
    await expect(iterator.next()).resolves.toMatchObject({
      value: { type: "server.welcome", sequence: 1 },
    });
    expect(socket.message(1)).toMatchObject({
      type: "server.event_ack",
      sequence: 2,
      payload: { server_sequence: 1 },
    });

    const result = observation.reportStep({
      state: "started",
      requirementKey: "selfie",
      artefact: "idenqa.artefact.selfie_image",
      acquisitionMethod: "idenqa.method.live_camera",
    });
    const outbound = socket.message(2);
    expect(outbound).toMatchObject({ type: "capture.step.started", sequence: 3 });
    socket.receive(serverMessage("command.accepted", 2, {}, String(outbound.command_id)));
    await expect(result).resolves.toEqual({
      commandId: outbound.command_id,
      disposition: "accepted",
    });
    observation.close();
  });

  it("rejects duplicate JSON fields and never exposes the ticket URL", async () => {
    const socket = new FakeSocket();
    const observation = captureClient().observe(session(), {
      maximumReconnectAttempts: 0,
      webSocketFactory: () => {
        queueMicrotask(() => socket.open());
        return socket;
      },
    });
    const next = observation[Symbol.asyncIterator]().next();
    await waitFor(() => socket.sent.length === 1);
    socket.receive(
      `{"version":1,"version":1,"message_id":"msg_01M11HEQG00000000000000000","verification_id":"${verificationId}","connection_id":"${connectionId}","sequence":1,"occurred_at":"2026-08-30T12:00:00Z","type":"server.welcome","payload":{}}`,
    );
    const error = await next.catch((cause: unknown) => cause);
    expect(error).toBeInstanceOf(IdenqaProtocolError);
    expect(String(error)).not.toContain("display-once");
  });

  it("reconnects with a fresh ticket and replays the stable command ID", async () => {
    const sockets = [new FakeSocket(), new FakeSocket()];
    let selected = 0;
    const observation = captureClient().observe(session(), {
      reconnectBaseDelayMs: 0,
      reconnectMaximumDelayMs: 0,
      reconnectJitter: 0,
      webSocketFactory: () => {
        const socket = sockets[selected++]!;
        queueMicrotask(() => socket.open());
        return socket;
      },
    });
    const iterator = observation[Symbol.asyncIterator]();
    await waitFor(() => sockets[0]!.sent.length === 1);
    sockets[0]!.receive(serverMessage("server.welcome", 1, welcomePayload()));
    await iterator.next();
    const result = observation.reportStep({
      state: "failed",
      requirementKey: "selfie",
      artefact: "idenqa.artefact.selfie_image",
      acquisitionMethod: "idenqa.method.live_camera",
      code: "camera_capture_failed",
    });
    const firstCommand = sockets[0]!.message(2);
    sockets[0]!.close(1012);

    await waitFor(() => sockets[1]!.sent.length === 1);
    expect(sockets[1]!.message(0)).toMatchObject({
      type: "client.hello",
      payload: { last_acknowledged_server_sequence: 0, last_acknowledged_event_cursor: 0 },
    });
    sockets[1]!.receive(serverMessage("server.welcome", 1, welcomePayload()));
    await waitFor(() => sockets[1]!.sent.length === 3);
    const replay = sockets[1]!.message(2);
    expect(replay.command_id).toBe(firstCommand.command_id);
    expect(replay.message_id).not.toBe(firstCommand.message_id);
    sockets[1]!.receive(serverMessage("command.accepted", 2, {}, String(replay.command_id)));
    await expect(result).resolves.toMatchObject({ disposition: "accepted" });
    observation.close();
  });

  it("carries the durable cursor across nodes and acknowledges its exact event", async () => {
    const sockets = [new FakeSocket(), new FakeSocket()];
    let selected = 0;
    const observation = captureClient().observe(session(), {
      reconnectBaseDelayMs: 0,
      reconnectMaximumDelayMs: 0,
      reconnectJitter: 0,
      webSocketFactory: () => {
        const socket = sockets[selected++]!;
        queueMicrotask(() => socket.open());
        return socket;
      },
    });
    const iterator = observation[Symbol.asyncIterator]();
    await waitFor(() => sockets[0]!.sent.length === 1);
    sockets[0]!.receive(serverMessage("server.welcome", 1, welcomePayload()));
    await iterator.next();
    sockets[0]!.receive(
      serverMessage("capture.progress", 2, { completed_steps: 1, total_steps: 1 }, commandId, 1),
    );
    await expect(iterator.next()).resolves.toMatchObject({
      value: { type: "capture.progress", eventCursor: 1 },
    });
    expect(sockets[0]!.message(2)).toMatchObject({
      type: "server.event_ack",
      payload: { server_sequence: 2, event_cursor: 1 },
    });
    sockets[0]!.close(1012);

    await waitFor(() => sockets[1]!.sent.length === 1);
    expect(sockets[1]!.message(0)).toMatchObject({
      type: "client.hello",
      payload: {
        last_acknowledged_server_sequence: 0,
        last_acknowledged_event_cursor: 1,
      },
    });
    observation.close();
  });

  it("decodes capture-safe verification check progress as a durable event", async () => {
    const socket = new FakeSocket();
    const observation = captureClient().observe(session(), {
      webSocketFactory: () => {
        queueMicrotask(() => socket.open());
        return socket;
      },
    });
    const iterator = observation[Symbol.asyncIterator]();
    await waitFor(() => socket.sent.length === 1);
    socket.receive(serverMessage("server.welcome", 1, welcomePayload()));
    await iterator.next();
    socket.receive(
      serverMessage(
        "verification.check.progress",
        2,
        {
          check_id: "chk_01M11HEQG00000000000000000",
          state: "awaiting_provider",
          check_version: 3,
        },
        commandId,
        1,
      ),
    );
    await expect(iterator.next()).resolves.toMatchObject({
      value: {
        type: "verification.check.progress",
        eventCursor: 1,
        payload: {
          checkId: "chk_01M11HEQG00000000000000000",
          state: "awaiting_provider",
          checkVersion: 3,
        },
      },
    });
    expect(socket.message(2)).toMatchObject({
      type: "server.event_ack",
      payload: { server_sequence: 2, event_cursor: 1 },
    });
    observation.close();
  });

  it("sustains 32 bounded concurrent browser observations", async () => {
    const runs = Array.from({ length: 32 }, async () => {
      const socket = new FakeSocket();
      const observation = captureClient().observe(session(), {
        webSocketFactory: () => {
          queueMicrotask(() => socket.open());
          return socket;
        },
      });
      const next = observation[Symbol.asyncIterator]().next();
      await waitFor(() => socket.sent.length === 1);
      socket.receive(serverMessage("server.welcome", 1, welcomePayload()));
      await expect(next).resolves.toMatchObject({ value: { type: "server.welcome" } });
      observation.close();
    });
    await Promise.all(runs);
  });
});

class FakeSocket extends EventTarget implements CaptureWebSocket {
  protocol = "idenqa.capture.v1";
  readyState = 0;
  readonly sent: string[] = [];

  send(data: string): void {
    this.sent.push(data);
  }

  close(code = 1000): void {
    if (this.readyState === 3) return;
    this.readyState = 3;
    const event = new Event("close") as Event & { code: number };
    Object.defineProperty(event, "code", { value: code });
    this.dispatchEvent(event);
  }

  open(): void {
    this.readyState = 1;
    this.dispatchEvent(new Event("open"));
  }

  receive(data: string): void {
    this.dispatchEvent(new MessageEvent("message", { data }));
  }

  message(index: number): Record<string, unknown> {
    return JSON.parse(this.sent[index]!) as Record<string, unknown>;
  }
}

function captureClient(): CaptureClient {
  return new CaptureClient({
    baseUrl: "https://core.example.test",
    captureToken: "capture-token",
    fetch: vi.fn<typeof fetch>(
      async () =>
        new Response(
          JSON.stringify({
            websocket_url: "wss://core.example.test/v1/capture/socket?ticket=display-once",
            protocol: "idenqa.capture.v1",
            expires_at: "2026-08-30T12:00:30Z",
          }),
          {
            status: 201,
            headers: { "Content-Type": "application/json", "X-Request-ID": "req_realtime" },
          },
        ),
    ),
  });
}

function session(): VerificationSession {
  return { id: verificationId } as VerificationSession;
}

function serverMessage(
  type: string,
  sequence: number,
  payload: Record<string, unknown>,
  command = commandId,
  eventCursor?: number,
): string {
  return JSON.stringify({
    version: 1,
    message_id: `msg_01M11HEQG0000000000000000${sequence}`,
    verification_id: verificationId,
    connection_id: connectionId,
    sequence,
    ...(eventCursor === undefined ? {} : { event_cursor: eventCursor }),
    ...(type.startsWith("command.") ? { command_id: command } : {}),
    occurred_at: "2026-08-30T12:00:00Z",
    type,
    payload,
  });
}

function welcomePayload(): Record<string, unknown> {
  return {
    selected_version: 1,
    session_version: 1,
    ping_interval_ms: 15_000,
    pong_timeout_ms: 5_000,
    idle_timeout_ms: 60_000,
  };
}

async function waitFor(predicate: () => boolean): Promise<void> {
  for (let attempt = 0; attempt < 20; attempt++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  throw new Error("condition not reached");
}
