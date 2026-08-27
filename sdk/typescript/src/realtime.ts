import { IdenqaProtocolError, IdenqaTransportError } from "./errors.js";
import type { CaptureConnection, VerificationSession } from "./types.js";

const PROTOCOL = "idenqa.capture.v1";
const SDK_VERSION = "0.1.0-alpha.1";
const EVENT_BUFFER_LIMIT = 64;
const SOCKET_BUFFER_LIMIT = 64;
const ID_PATTERN = /^[0-9A-HJKMNP-TV-Z]{26}$/;
const SAFE_NAME = /^[a-z0-9_.]{1,200}$/;
const SAFE_TOKEN = /^[!-~]{1,100}$/;
const REQUIREMENT_KEY = /^[a-z][a-z0-9_]{0,63}$/;

export interface CaptureWebSocket {
  readonly protocol: string;
  readonly readyState: number;
  send(data: string): void;
  close(code?: number, reason?: string): void;
  addEventListener(type: string, listener: (event: Event) => void): void;
  removeEventListener(type: string, listener: (event: Event) => void): void;
}

export type CaptureWebSocketFactory = (
  url: string,
  protocols: readonly string[],
) => CaptureWebSocket;

export interface CaptureObserveOptions {
  readonly capabilities?: readonly string[];
  readonly signal?: AbortSignal;
  readonly webSocketFactory?: CaptureWebSocketFactory;
  readonly maximumReconnectAttempts?: number;
  readonly reconnectBaseDelayMs?: number;
  readonly reconnectMaximumDelayMs?: number;
  readonly reconnectJitter?: number;
}

export interface CaptureStepUpdateInput {
  readonly state: "started" | "failed" | "cancelled";
  readonly requirementKey: string;
  readonly artefact: string;
  readonly acquisitionMethod: string;
  readonly code?: string;
}

export interface CaptureCommandAcknowledgement {
  readonly commandId: string;
  readonly disposition: "accepted" | "rejected";
  readonly code?: string;
}

interface CaptureRealtimeEventBase {
  readonly messageId: string;
  readonly verificationId: string;
  readonly connectionId: string;
  readonly sequence: number;
  readonly eventCursor?: number;
  readonly occurredAt: string;
  readonly commandId?: string;
}

export type CaptureRealtimeEvent = CaptureRealtimeEventBase &
  (
    | {
        readonly type: "server.welcome";
        readonly payload: {
          readonly selectedVersion: 1;
          readonly sessionVersion: number;
          readonly pingIntervalMs: number;
          readonly pongTimeoutMs: number;
          readonly idleTimeoutMs: number;
        };
      }
    | {
        readonly type: "capture.command";
        readonly payload: {
          readonly action: "start" | "cancel" | "retry";
          readonly requirementKey: string;
          readonly artefact: string;
          readonly acquisitionMethod?: string;
        };
      }
    | {
        readonly type: "challenge.request";
        readonly payload: {
          readonly challengeId: string;
          readonly kind: string;
          readonly expiresAt: string;
        };
      }
    | {
        readonly type: "challenge.cancelled";
        readonly payload: { readonly challengeId: string; readonly code: string };
      }
    | {
        readonly type: "capture.progress";
        readonly payload: { readonly completedSteps: number; readonly totalSteps: number };
      }
    | {
        readonly type: "verification.check.progress";
        readonly payload: {
          readonly checkId: string;
          readonly state:
            | "queued"
            | "running"
            | "awaiting_input"
            | "awaiting_provider"
            | "completed"
            | "skipped_by_policy"
            | "timed_out"
            | "cancelled"
            | "failed";
          readonly checkVersion: number;
        };
      }
    | {
        readonly type: "session.state_changed";
        readonly payload: { readonly state: string; readonly sessionVersion: number };
      }
    | {
        readonly type: "session.resync_required";
        readonly payload: { readonly reason: string };
      }
    | {
        readonly type: "server.draining";
        readonly payload: { readonly retryAfterMs: number };
      }
    | {
        readonly type: "command.accepted" | "command.rejected";
        readonly payload: { readonly code?: string };
      }
  );

interface ObservationDependencies {
  readonly verificationId: string;
  readonly createConnection: (signal: AbortSignal) => Promise<CaptureConnection>;
  readonly options: CaptureObserveOptions;
}

interface PendingCommand {
  readonly commandId: string;
  readonly input: CaptureStepUpdateInput;
  readonly resolve: (value: CaptureCommandAcknowledgement) => void;
  readonly reject: (reason: unknown) => void;
  sentGeneration: number;
}

interface ConnectionState {
  readonly socket: CaptureWebSocket;
  readonly connectionId: string;
  readonly generation: number;
  nextClientSequence: number;
}

interface RetryPolicy {
  readonly maximumAttempts: number;
  readonly baseDelayMs: number;
  readonly maximumDelayMs: number;
  readonly jitter: number;
}

interface SocketMessage {
  readonly kind: "message";
  readonly data: string;
}

interface SocketClosed {
  readonly kind: "close";
  readonly code: number;
}

type SocketItem = SocketMessage | SocketClosed;

export interface CaptureObservation extends AsyncIterable<CaptureRealtimeEvent> {
  reportStep(input: CaptureStepUpdateInput): Promise<CaptureCommandAcknowledgement>;
  close(): void;
}

class DefaultCaptureObservation implements CaptureObservation {
  readonly #verificationId: string;
  readonly #createConnection: (signal: AbortSignal) => Promise<CaptureConnection>;
  readonly #factory: CaptureWebSocketFactory;
  readonly #capabilities: readonly string[];
  readonly #retry: RetryPolicy;
  readonly #abortController = new AbortController();
  readonly #events = new BoundedAsyncQueue<CaptureRealtimeEvent>(EVENT_BUFFER_LIMIT);
  readonly #pending = new Map<string, PendingCommand>();
  #started = false;
  #closed = false;
  #active: ConnectionState | undefined;
  #lastAcknowledgedServerSequence = 0;
  #lastAcknowledgedEventCursor = 0;
  #retryAfterMs: number | undefined;
  #externalSignal: AbortSignal | undefined;
  #externalAbort: (() => void) | undefined;

  constructor(dependencies: ObservationDependencies) {
    this.#verificationId = verificationId(dependencies.verificationId);
    this.#createConnection = dependencies.createConnection;
    this.#factory = dependencies.options.webSocketFactory ?? defaultWebSocketFactory();
    this.#capabilities = capabilities(dependencies.options.capabilities ?? []);
    this.#retry = retryPolicy(dependencies.options);
    const signal = dependencies.options.signal;
    if (signal !== undefined) {
      if (signal.aborted) this.#abortController.abort(signal.reason);
      else {
        this.#externalSignal = signal;
        this.#externalAbort = () => this.#abortController.abort(signal.reason);
        signal.addEventListener("abort", this.#externalAbort, { once: true });
      }
    }
  }

  [Symbol.asyncIterator](): AsyncIterator<CaptureRealtimeEvent> {
    this.#ensureStarted();
    return this.#events[Symbol.asyncIterator]();
  }

  reportStep(input: CaptureStepUpdateInput): Promise<CaptureCommandAcknowledgement> {
    if (this.#closed || this.#abortController.signal.aborted) {
      return Promise.reject(new IdenqaTransportError("The realtime observation is closed.", true));
    }
    const validated = captureStep(input);
    const commandId = sortableID("cmd");
    const result = new Promise<CaptureCommandAcknowledgement>((resolve, reject) => {
      this.#pending.set(commandId, {
        commandId,
        input: validated,
        resolve,
        reject,
        sentGeneration: 0,
      });
    });
    this.#ensureStarted();
    const active = this.#active;
    if (active !== undefined) this.#sendPending(active);
    return result;
  }

  close(): void {
    if (this.#closed) return;
    this.#closed = true;
    this.#detachExternalSignal();
    this.#abortController.abort();
    this.#active?.socket.close(1000, "observation closed");
    this.#events.end();
    this.#rejectPending(new IdenqaTransportError("The realtime observation was closed.", true));
  }

  #ensureStarted(): void {
    if (this.#started) return;
    this.#started = true;
    void this.#run();
  }

  async #run(): Promise<void> {
    let attempt = 0;
    try {
      while (!this.#closed && !this.#abortController.signal.aborted) {
        try {
          const closeCode = await this.#runConnection(attempt + 1);
          if (this.#closed || closeCode === 1000) break;
          if (closeCode === 1008) {
            throw new IdenqaProtocolError("Idenqa Core rejected the realtime connection.");
          }
        } catch (error) {
          if (this.#closed || this.#abortController.signal.aborted) break;
          if (error instanceof IdenqaProtocolError) throw error;
        }
        if (attempt >= this.#retry.maximumAttempts) {
          throw new IdenqaTransportError("The realtime reconnect budget was exhausted.", false);
        }
        const delay = this.#retryAfterMs ?? reconnectDelay(this.#retry, attempt);
        this.#retryAfterMs = undefined;
        attempt++;
        await abortableDelay(delay, this.#abortController.signal);
      }
      if (!this.#closed) this.#events.end();
    } catch (error) {
      const failure = realtimeFailure(error, this.#abortController.signal.aborted);
      this.#events.fail(failure);
      this.#rejectPending(failure);
    } finally {
      this.#detachExternalSignal();
      this.#active = undefined;
    }
  }

  async #runConnection(generation: number): Promise<number> {
    const signal = this.#abortController.signal;
    const connection = await this.#createConnection(signal);
    if (signal.aborted) throw signal.reason;
    const socket = this.#factory(connection.websocketUrl, [connection.protocol]);
    const channel = new SocketChannel(socket, signal);
    try {
      await channel.opened;
      if (socket.protocol !== PROTOCOL) {
        throw new IdenqaProtocolError("The realtime server selected an unexpected subprotocol.");
      }
      socket.send(
        JSON.stringify({
          version: 1,
          message_id: sortableID("msg"),
          verification_id: this.#verificationId,
          sequence: 1,
          occurred_at: new Date().toISOString(),
          type: "client.hello",
          payload: {
            sdk_version: SDK_VERSION,
            supported_versions: [1],
            last_acknowledged_server_sequence: this.#lastAcknowledgedServerSequence,
            last_acknowledged_event_cursor: this.#lastAcknowledgedEventCursor,
            capabilities: this.#capabilities,
          },
        }),
      );
      const first = await channel.next();
      if (first.kind === "close") return first.code;
      const welcome = decodeServerEvent(first.data);
      if (
        welcome.type !== "server.welcome" ||
        welcome.verificationId !== this.#verificationId ||
        welcome.sequence !== 1
      ) {
        throw new IdenqaProtocolError("The realtime server did not begin with a bound welcome.");
      }
      const active: ConnectionState = {
        socket,
        connectionId: welcome.connectionId,
        generation,
        nextClientSequence: 2,
      };
      this.#active = active;
      this.#lastAcknowledgedServerSequence = 0;
      if (!this.#events.push(welcome)) throw protocol("realtime consumer buffer exhausted");
      this.#acknowledge(active, welcome);
      this.#sendPending(active);
      let expectedServerSequence = 2;
      for (;;) {
        const item = await channel.next();
        if (item.kind === "close") return item.code;
        const event = decodeServerEvent(item.data);
        if (
          event.verificationId !== this.#verificationId ||
          event.connectionId !== active.connectionId ||
          event.sequence !== expectedServerSequence
        ) {
          throw new IdenqaProtocolError(
            "The realtime server message binding or sequence is invalid.",
          );
        }
        expectedServerSequence++;
        this.#acknowledge(active, event);
        this.#settleCommand(event);
        if (event.type === "server.draining") this.#retryAfterMs = event.payload.retryAfterMs;
        if (event.type === "session.resync_required") this.#lastAcknowledgedServerSequence = 0;
        if (!this.#events.push(event)) throw protocol("realtime consumer buffer exhausted");
      }
    } finally {
      this.#lastAcknowledgedServerSequence = 0;
      channel.dispose();
      if (this.#active?.socket === socket) this.#active = undefined;
      socket.close();
    }
  }

  #acknowledge(active: ConnectionState, event: CaptureRealtimeEvent): void {
    active.socket.send(
      JSON.stringify({
        version: 1,
        message_id: sortableID("msg"),
        verification_id: this.#verificationId,
        connection_id: active.connectionId,
        sequence: active.nextClientSequence++,
        correlation_id: event.messageId,
        occurred_at: new Date().toISOString(),
        type: "server.event_ack",
        payload: {
          server_sequence: event.sequence,
          ...(event.eventCursor === undefined ? {} : { event_cursor: event.eventCursor }),
        },
      }),
    );
    this.#lastAcknowledgedServerSequence = event.sequence;
    if (event.eventCursor !== undefined) this.#lastAcknowledgedEventCursor = event.eventCursor;
  }

  #sendPending(active: ConnectionState): void {
    for (const command of this.#pending.values()) {
      if (command.sentGeneration === active.generation) continue;
      command.sentGeneration = active.generation;
      const type = `capture.step.${command.input.state}`;
      active.socket.send(
        JSON.stringify({
          version: 1,
          message_id: sortableID("msg"),
          verification_id: this.#verificationId,
          connection_id: active.connectionId,
          sequence: active.nextClientSequence++,
          command_id: command.commandId,
          occurred_at: new Date().toISOString(),
          type,
          payload: {
            requirement_key: command.input.requirementKey,
            artefact: command.input.artefact,
            acquisition_method: command.input.acquisitionMethod,
            ...(command.input.code === undefined ? {} : { code: command.input.code }),
          },
        }),
      );
    }
  }

  #settleCommand(event: CaptureRealtimeEvent): void {
    if (event.type !== "command.accepted" && event.type !== "command.rejected") return;
    const commandId = event.commandId;
    if (commandId === undefined) return;
    const command = this.#pending.get(commandId);
    if (command === undefined) return;
    this.#pending.delete(commandId);
    command.resolve({
      commandId,
      disposition: event.type === "command.accepted" ? "accepted" : "rejected",
      ...(event.payload.code === undefined ? {} : { code: event.payload.code }),
    });
  }

  #rejectPending(error: unknown): void {
    for (const command of this.#pending.values()) command.reject(error);
    this.#pending.clear();
  }

  #detachExternalSignal(): void {
    if (this.#externalSignal !== undefined && this.#externalAbort !== undefined) {
      this.#externalSignal.removeEventListener("abort", this.#externalAbort);
    }
    this.#externalSignal = undefined;
    this.#externalAbort = undefined;
  }
}

export function createCaptureObservation(
  session: VerificationSession,
  createConnection: (signal: AbortSignal) => Promise<CaptureConnection>,
  options: CaptureObserveOptions = {},
): CaptureObservation {
  return new DefaultCaptureObservation({ verificationId: session.id, createConnection, options });
}

class SocketChannel {
  readonly opened: Promise<void>;
  readonly #queue = new BoundedAsyncQueue<SocketItem>(SOCKET_BUFFER_LIMIT);
  readonly #socket: CaptureWebSocket;
  readonly #signal: AbortSignal;
  readonly #onOpen: (event: Event) => void;
  readonly #onMessage: (event: Event) => void;
  readonly #onClose: (event: Event) => void;
  readonly #onError: (event: Event) => void;
  readonly #onAbort: () => void;

  constructor(socket: CaptureWebSocket, signal: AbortSignal) {
    this.#socket = socket;
    this.#signal = signal;
    let resolveOpen!: () => void;
    let rejectOpen!: (reason: unknown) => void;
    this.opened = new Promise<void>((resolve, reject) => {
      resolveOpen = resolve;
      rejectOpen = reject;
    });
    this.#onOpen = () => resolveOpen();
    this.#onMessage = (event) => {
      const data = (event as MessageEvent<unknown>).data;
      if (typeof data !== "string") {
        this.#queue.fail(new IdenqaProtocolError("The realtime server sent a non-text message."));
        return;
      }
      this.#queue.push({ kind: "message", data });
    };
    this.#onClose = (event) => {
      const code = (event as CloseEvent).code;
      const closed = { kind: "close" as const, code: Number.isInteger(code) ? code : 1006 };
      rejectOpen(new IdenqaTransportError("The realtime connection closed before opening.", false));
      this.#queue.push(closed);
      this.#queue.end();
    };
    this.#onError = () => {
      if (socket.readyState === 0) {
        rejectOpen(new IdenqaTransportError("The realtime connection could not be opened.", false));
      }
    };
    this.#onAbort = () => {
      rejectOpen(new IdenqaTransportError("The realtime connection was aborted.", true));
      this.#queue.end();
      socket.close(1000, "observation aborted");
    };
    socket.addEventListener("open", this.#onOpen);
    socket.addEventListener("message", this.#onMessage);
    socket.addEventListener("close", this.#onClose);
    socket.addEventListener("error", this.#onError);
    signal.addEventListener("abort", this.#onAbort, { once: true });
  }

  next(): Promise<SocketItem> {
    return this.#queue.nextValue();
  }

  dispose(): void {
    this.#socket.removeEventListener("open", this.#onOpen);
    this.#socket.removeEventListener("message", this.#onMessage);
    this.#socket.removeEventListener("close", this.#onClose);
    this.#socket.removeEventListener("error", this.#onError);
    this.#signal.removeEventListener("abort", this.#onAbort);
    this.#queue.end();
  }
}

class BoundedAsyncQueue<T> implements AsyncIterable<T> {
  readonly #limit: number;
  readonly #values: T[] = [];
  readonly #waiters: Array<{
    resolve: (result: IteratorResult<T>) => void;
    reject: (error: unknown) => void;
  }> = [];
  #ended = false;
  #failure: unknown;

  constructor(limit: number) {
    this.#limit = limit;
  }

  [Symbol.asyncIterator](): AsyncIterator<T> {
    return { next: () => this.next() };
  }

  push(value: T): boolean {
    if (this.#ended) return false;
    const waiter = this.#waiters.shift();
    if (waiter !== undefined) {
      waiter.resolve({ done: false, value });
      return true;
    }
    if (this.#values.length >= this.#limit) {
      this.fail(new IdenqaProtocolError("The realtime event buffer was exhausted."));
      return false;
    }
    this.#values.push(value);
    return true;
  }

  nextValue(): Promise<T> {
    return this.next().then((result) => {
      if (result.done) throw new IdenqaTransportError("The realtime connection closed.", false);
      return result.value;
    });
  }

  next(): Promise<IteratorResult<T>> {
    const value = this.#values.shift();
    if (value !== undefined) return Promise.resolve({ done: false, value });
    if (this.#failure !== undefined) return Promise.reject(this.#failure);
    if (this.#ended) return Promise.resolve({ done: true, value: undefined });
    return new Promise<IteratorResult<T>>((resolve, reject) => {
      this.#waiters.push({ resolve, reject });
    });
  }

  end(): void {
    if (this.#ended) return;
    this.#ended = true;
    for (const waiter of this.#waiters.splice(0)) waiter.resolve({ done: true, value: undefined });
  }

  fail(error: unknown): void {
    if (this.#ended) return;
    this.#failure = error;
    this.#ended = true;
    for (const waiter of this.#waiters.splice(0)) waiter.reject(error);
  }
}

function decodeServerEvent(encoded: string): CaptureRealtimeEvent {
  let parsed: unknown;
  try {
    parsed = new StrictJSONParser(encoded).parse();
  } catch (cause) {
    throw new IdenqaProtocolError(
      "The realtime server sent invalid JSON.",
      undefined,
      undefined,
      cause,
    );
  }
  const message = object(parsed, "realtime message");
  exactKeys(message, [
    "version",
    "message_id",
    "verification_id",
    "connection_id",
    "sequence",
    "event_cursor",
    "command_id",
    "correlation_id",
    "causation_id",
    "occurred_at",
    "type",
    "payload",
  ]);
  if (message.version !== 1) throw protocol("unsupported realtime version");
  const base = {
    messageId: ownedID(message.message_id, "msg"),
    verificationId: ownedID(message.verification_id, "ver"),
    connectionId: ownedID(message.connection_id, "con"),
    sequence: positiveInteger(message.sequence, "sequence"),
    ...(message.event_cursor === undefined
      ? {}
      : { eventCursor: positiveInteger(message.event_cursor, "event cursor") }),
    occurredAt: utcTimestamp(message.occurred_at),
    ...(message.command_id === undefined ? {} : { commandId: ownedID(message.command_id, "cmd") }),
  };
  if (message.correlation_id !== undefined) ownedID(message.correlation_id, "msg");
  if (message.causation_id !== undefined) ownedID(message.causation_id, "msg");
  const payload = object(message.payload, "realtime payload");
  const durable =
    message.type === "capture.command" ||
    message.type === "challenge.request" ||
    message.type === "challenge.cancelled" ||
    message.type === "capture.progress" ||
    message.type === "verification.check.progress" ||
    message.type === "session.state_changed";
  if (!durable && message.event_cursor !== undefined)
    throw protocol("event cursor is not valid for this server message");
  switch (message.type) {
    case "server.welcome":
      noCommand(base);
      exactKeys(payload, [
        "selected_version",
        "session_version",
        "ping_interval_ms",
        "pong_timeout_ms",
        "idle_timeout_ms",
      ]);
      if (payload.selected_version !== 1) throw protocol("invalid selected version");
      return {
        ...base,
        type: message.type,
        payload: {
          selectedVersion: 1,
          sessionVersion: positiveInteger(payload.session_version, "session version"),
          pingIntervalMs: boundedInteger(payload.ping_interval_ms, 5000, 30000, "ping interval"),
          pongTimeoutMs: boundedInteger(payload.pong_timeout_ms, 5000, 15000, "pong timeout"),
          idleTimeoutMs: boundedInteger(payload.idle_timeout_ms, 10000, 120000, "idle timeout"),
        },
      };
    case "capture.command": {
      requireCommand(base);
      exactKeys(payload, ["action", "requirement_key", "artefact", "acquisition_method"]);
      const action = enumeration(
        payload.action,
        ["start", "cancel", "retry"] as const,
        "capture action",
      );
      return {
        ...base,
        type: message.type,
        payload: {
          action,
          requirementKey: requirementKey(payload.requirement_key),
          artefact: safeName(payload.artefact),
          ...(payload.acquisition_method === undefined
            ? {}
            : { acquisitionMethod: safeName(payload.acquisition_method) }),
        },
      };
    }
    case "challenge.request":
      requireCommand(base);
      exactKeys(payload, ["challenge_id", "kind", "expires_at"]);
      return {
        ...base,
        type: message.type,
        payload: {
          challengeId: ownedID(payload.challenge_id, "chl"),
          kind: safeToken(payload.kind),
          expiresAt: utcTimestamp(payload.expires_at),
        },
      };
    case "challenge.cancelled":
      requireCommand(base);
      exactKeys(payload, ["challenge_id", "code"]);
      return {
        ...base,
        type: message.type,
        payload: {
          challengeId: ownedID(payload.challenge_id, "chl"),
          code: safeToken(payload.code),
        },
      };
    case "capture.progress":
      noCommand(base);
      exactKeys(payload, ["completed_steps", "total_steps"]);
      return {
        ...base,
        type: message.type,
        payload: {
          completedSteps: boundedInteger(payload.completed_steps, 0, 256, "completed steps"),
          totalSteps: boundedInteger(payload.total_steps, 1, 256, "total steps"),
        },
      };
    case "verification.check.progress":
      noCommand(base);
      exactKeys(payload, ["check_id", "state", "check_version"]);
      return {
        ...base,
        type: message.type,
        payload: {
          checkId: ownedID(payload.check_id, "chk"),
          state: enumeration(
            payload.state,
            [
              "queued",
              "running",
              "awaiting_input",
              "awaiting_provider",
              "completed",
              "skipped_by_policy",
              "timed_out",
              "cancelled",
              "failed",
            ] as const,
            "verification check state",
          ),
          checkVersion: positiveInteger(payload.check_version, "verification check version"),
        },
      };
    case "session.state_changed":
      noCommand(base);
      exactKeys(payload, ["state", "session_version"]);
      return {
        ...base,
        type: message.type,
        payload: {
          state: safeToken(payload.state),
          sessionVersion: positiveInteger(payload.session_version, "session version"),
        },
      };
    case "session.resync_required":
      noCommand(base);
      exactKeys(payload, ["reason"]);
      return { ...base, type: message.type, payload: { reason: safeToken(payload.reason) } };
    case "server.draining":
      noCommand(base);
      exactKeys(payload, ["retry_after_ms"]);
      return {
        ...base,
        type: message.type,
        payload: {
          retryAfterMs: boundedInteger(payload.retry_after_ms, 1000, 60000, "retry delay"),
        },
      };
    case "command.accepted":
      requireCommand(base);
      exactKeys(payload, []);
      return { ...base, type: message.type, payload: {} };
    case "command.rejected":
      requireCommand(base);
      exactKeys(payload, ["code"]);
      return { ...base, type: message.type, payload: { code: safeToken(payload.code) } };
    default:
      throw protocol("unsupported server message type");
  }
}

class StrictJSONParser {
  readonly #text: string;
  #index = 0;

  constructor(text: string) {
    this.#text = text;
  }

  parse(): unknown {
    const value = this.#value();
    this.#space();
    if (this.#index !== this.#text.length) throw new SyntaxError("trailing JSON data");
    return value;
  }

  #value(): unknown {
    this.#space();
    const character = this.#text[this.#index];
    if (character === "{") return this.#object();
    if (character === "[") return this.#array();
    if (character === '"') return this.#string();
    if (character === "t") return this.#literal("true", true);
    if (character === "f") return this.#literal("false", false);
    if (character === "n") return this.#literal("null", null);
    return this.#number();
  }

  #object(): Readonly<Record<string, unknown>> {
    this.#index++;
    const result: Record<string, unknown> = {};
    const keys = new Set<string>();
    this.#space();
    if (this.#text[this.#index] === "}") {
      this.#index++;
      return result;
    }
    for (;;) {
      this.#space();
      if (this.#text[this.#index] !== '"') throw new SyntaxError("object key required");
      const key = this.#string();
      if (keys.has(key)) throw new SyntaxError("duplicate object key");
      keys.add(key);
      this.#space();
      if (this.#text[this.#index++] !== ":") throw new SyntaxError("object colon required");
      result[key] = this.#value();
      this.#space();
      const delimiter = this.#text[this.#index++];
      if (delimiter === "}") return result;
      if (delimiter !== ",") throw new SyntaxError("object delimiter required");
    }
  }

  #array(): readonly unknown[] {
    this.#index++;
    const result: unknown[] = [];
    this.#space();
    if (this.#text[this.#index] === "]") {
      this.#index++;
      return result;
    }
    for (;;) {
      result.push(this.#value());
      this.#space();
      const delimiter = this.#text[this.#index++];
      if (delimiter === "]") return result;
      if (delimiter !== ",") throw new SyntaxError("array delimiter required");
    }
  }

  #string(): string {
    const start = this.#index++;
    let escaped = false;
    for (; this.#index < this.#text.length; this.#index++) {
      const character = this.#text[this.#index]!;
      if (!escaped && character === '"') {
        this.#index++;
        return JSON.parse(this.#text.slice(start, this.#index)) as string;
      }
      if (!escaped && character.charCodeAt(0) < 0x20)
        throw new SyntaxError("control character in string");
      if (!escaped && character === "\\") escaped = true;
      else escaped = false;
    }
    throw new SyntaxError("unterminated string");
  }

  #number(): number {
    const match = this.#text
      .slice(this.#index)
      .match(/^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/);
    if (match === null) throw new SyntaxError("invalid JSON value");
    this.#index += match[0].length;
    const value = Number(match[0]);
    if (!Number.isFinite(value)) throw new SyntaxError("non-finite number");
    return value;
  }

  #literal<T>(encoded: string, value: T): T {
    if (!this.#text.startsWith(encoded, this.#index)) throw new SyntaxError("invalid literal");
    this.#index += encoded.length;
    return value;
  }

  #space(): void {
    while (/\s/.test(this.#text[this.#index] ?? "")) this.#index++;
  }
}

function captureStep(input: CaptureStepUpdateInput): CaptureStepUpdateInput {
  if (input.state !== "started" && input.state !== "failed" && input.state !== "cancelled") {
    throw new TypeError("capture step state is invalid.");
  }
  const code = input.code;
  if ((input.state === "started") !== (code === undefined)) {
    throw new TypeError("capture step code does not match its state.");
  }
  return {
    state: input.state,
    requirementKey: requirementKey(input.requirementKey),
    artefact: safeName(input.artefact),
    acquisitionMethod: safeName(input.acquisitionMethod),
    ...(code === undefined ? {} : { code: safeToken(code) }),
  };
}

function retryPolicy(options: CaptureObserveOptions): RetryPolicy {
  const maximumAttempts = boundedOption(
    options.maximumReconnectAttempts,
    5,
    0,
    10,
    "maximumReconnectAttempts",
  );
  const baseDelayMs = boundedOption(
    options.reconnectBaseDelayMs,
    250,
    0,
    30000,
    "reconnectBaseDelayMs",
  );
  const maximumDelayMs = boundedOption(
    options.reconnectMaximumDelayMs,
    10000,
    baseDelayMs,
    60000,
    "reconnectMaximumDelayMs",
  );
  const jitter = options.reconnectJitter ?? 0.2;
  if (!Number.isFinite(jitter) || jitter < 0 || jitter > 1)
    throw new TypeError("reconnectJitter must be between 0 and 1.");
  return { maximumAttempts, baseDelayMs, maximumDelayMs, jitter };
}

function reconnectDelay(policy: RetryPolicy, attempt: number): number {
  const exponential = Math.min(policy.maximumDelayMs, policy.baseDelayMs * 2 ** attempt);
  const spread = exponential * policy.jitter;
  return Math.max(
    0,
    Math.round(
      exponential -
        spread +
        (globalThis.crypto.getRandomValues(new Uint32Array(1))[0]! / 0xffffffff) * spread * 2,
    ),
  );
}

function abortableDelay(delay: number, signal: AbortSignal): Promise<void> {
  if (delay === 0) return Promise.resolve();
  return new Promise<void>((resolve, reject) => {
    const onAbort = () => {
      clearTimeout(timer);
      reject(signal.reason);
    };
    const timer = setTimeout(() => {
      signal.removeEventListener("abort", onAbort);
      resolve();
    }, delay);
    signal.addEventListener("abort", onAbort, { once: true });
  });
}

function defaultWebSocketFactory(): CaptureWebSocketFactory {
  if (typeof globalThis.WebSocket !== "function") {
    throw new TypeError("A WebSocket-compatible implementation is required for observation.");
  }
  return (url, protocols) => new globalThis.WebSocket(url, [...protocols]);
}

function sortableID(prefix: "msg" | "cmd"): string {
  const alphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ";
  let timestamp = BigInt(Date.now());
  let encodedTime = "";
  for (let index = 0; index < 10; index++) {
    encodedTime = alphabet[Number(timestamp & 31n)]! + encodedTime;
    timestamp >>= 5n;
  }
  const random = globalThis.crypto.getRandomValues(new Uint8Array(10));
  let randomValue = 0n;
  for (const byte of random) randomValue = (randomValue << 8n) | BigInt(byte);
  let encodedRandom = "";
  for (let index = 0; index < 16; index++) {
    encodedRandom = alphabet[Number(randomValue & 31n)]! + encodedRandom;
    randomValue >>= 5n;
  }
  return `${prefix}_${encodedTime}${encodedRandom}`;
}

function exactKeys(value: Readonly<Record<string, unknown>>, allowed: readonly string[]): void {
  const permitted = new Set(allowed);
  for (const key of Object.keys(value))
    if (!permitted.has(key)) throw protocol("unknown realtime field");
}

function object(value: unknown, name: string): Readonly<Record<string, unknown>> {
  if (value === null || typeof value !== "object" || Array.isArray(value))
    throw protocol(`${name} must be an object`);
  return value as Readonly<Record<string, unknown>>;
}

function ownedID(value: unknown, prefix: "msg" | "ver" | "con" | "cmd" | "chl" | "chk"): string {
  if (
    typeof value !== "string" ||
    !value.startsWith(`${prefix}_`) ||
    !ID_PATTERN.test(value.slice(4))
  ) {
    throw protocol(`invalid ${prefix} identifier`);
  }
  return value;
}

function verificationId(value: string): string {
  return ownedID(value, "ver");
}

function requirementKey(value: unknown): string {
  if (typeof value !== "string" || !REQUIREMENT_KEY.test(value))
    throw protocol("invalid requirement key");
  return value;
}

function safeName(value: unknown): string {
  if (typeof value !== "string" || !SAFE_NAME.test(value)) throw protocol("invalid safe name");
  return value;
}

function safeToken(value: unknown): string {
  if (typeof value !== "string" || !SAFE_TOKEN.test(value)) throw protocol("invalid safe token");
  return value;
}

function utcTimestamp(value: unknown): string {
  if (typeof value !== "string" || !value.endsWith("Z") || Number.isNaN(Date.parse(value)))
    throw protocol("invalid UTC timestamp");
  return value;
}

function positiveInteger(value: unknown, name: string): number {
  return boundedInteger(value, 1, Number.MAX_SAFE_INTEGER, name);
}

function boundedInteger(value: unknown, minimum: number, maximum: number, name: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < minimum || (value as number) > maximum)
    throw protocol(`invalid ${name}`);
  return value as number;
}

function boundedOption(
  value: number | undefined,
  fallback: number,
  minimum: number,
  maximum: number,
  name: string,
): number {
  const selected = value ?? fallback;
  if (!Number.isSafeInteger(selected) || selected < minimum || selected > maximum)
    throw new TypeError(`${name} is outside its supported bounds.`);
  return selected;
}

function enumeration<const T extends readonly string[]>(
  value: unknown,
  values: T,
  name: string,
): T[number] {
  if (typeof value !== "string" || !(values as readonly string[]).includes(value))
    throw protocol(`invalid ${name}`);
  return value as T[number];
}

function capabilities(values: readonly string[]): readonly string[] {
  if (
    values.length > 32 ||
    new Set(values).size !== values.length ||
    values.some(
      (value) => typeof value !== "string" || value.length > 64 || !SAFE_TOKEN.test(value),
    )
  ) {
    throw new TypeError("realtime capabilities must be unique safe tokens.");
  }
  return [...values];
}

function requireCommand(value: CaptureRealtimeEventBase): void {
  if (value.commandId === undefined) throw protocol("server command ID is required");
}

function noCommand(value: CaptureRealtimeEventBase): void {
  if (value.commandId !== undefined) throw protocol("server command ID is forbidden");
}

function protocol(detail: string): IdenqaProtocolError {
  return new IdenqaProtocolError(`The realtime server sent an invalid message (${detail}).`);
}

function realtimeFailure(error: unknown, aborted: boolean): Error {
  if (error instanceof IdenqaProtocolError || error instanceof IdenqaTransportError) return error;
  return new IdenqaTransportError(
    aborted ? "The realtime observation was aborted." : "The realtime connection failed.",
    aborted,
    error,
  );
}
