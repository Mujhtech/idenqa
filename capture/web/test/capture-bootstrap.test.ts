import { PassThrough } from "node:stream";
import { beforeEach, describe, expect, it, vi } from "vitest";

const core = vi.hoisted(() => ({
  policies: { create: vi.fn(), activate: vi.fn() },
  captureProfiles: { create: vi.fn(), publish: vi.fn() },
  identity: {
    createSubject: vi.fn(),
    getSubject: vi.fn(),
    listVerifications: vi.fn(),
    linkVerification: vi.fn(),
  },
  verifications: { create: vi.fn() },
  notices: { create: vi.fn() },
  authorities: { declare: vi.fn() },
}));

vi.mock("@idenqa/sdk", () => ({
  IdenqaClient: class {
    policies = core.policies;
    captureProfiles = core.captureProfiles;
    identity = core.identity;
    verifications = core.verifications;
    notices = core.notices;
    authorities = core.authorities;
  },
}));

import { createCaptureBootstrapPlugin } from "../demo/capture-bootstrap.mjs";

const launchCredential = "fixture_launch_credential_0123456789";
const subject = {
  id: "sub_01M3VTQXVWEAX20X1ZQNG6N6EC",
  region: "tenant-local",
  state: "active",
  version: 1,
};

beforeEach(() => {
  vi.resetAllMocks();
  core.policies.create.mockResolvedValue({
    data: { policy: { id: "policy", latestRevision: 1, activationVersion: 1 } },
  });
  core.captureProfiles.create.mockResolvedValue({
    data: { profileId: "profile" },
    etag: "profile-etag",
  });
  core.captureProfiles.publish.mockResolvedValue({ data: { state: "active" } });
  core.identity.createSubject.mockResolvedValue({ data: { subject } });
  core.identity.getSubject.mockResolvedValue({ data: { subject } });
  core.identity.listVerifications.mockResolvedValue({ data: { verification_ids: [] } });
  core.verifications.create.mockResolvedValue({
    data: {
      session: {
        id: "verification",
        createdAt: "2026-10-11T00:00:00.456Z",
        expiresAt: "2026-10-11T00:30:00Z",
      },
      captureToken: "capture-token",
      outcomeToken: "outcome-token",
    },
  });
  core.notices.create.mockResolvedValue({
    data: { id: "notice", effectiveAt: "2026-10-11T00:00:00Z" },
  });
});

describe("capture bootstrap", () => {
  it("accepts only an opaque launch credential from the browser", async () => {
    const result = await bootstrap({
      launchCredential,
      controller: "Browser-controlled tenant",
    });

    expect(result).toEqual({ status: 400, body: { error: "launch_invalid" } });
    expect(core.policies.create).not.toHaveBeenCalled();
  });

  it("rejects a credential that is not bound by the server", async () => {
    const result = await bootstrap({ launchCredential: "different_launch_credential_987654321" });

    expect(result).toEqual({ status: 401, body: { error: "launch_credential_invalid" } });
    expect(core.policies.create).not.toHaveBeenCalled();
  });

  it("provisions the fixed server-owned journey without returning tenant authority", async () => {
    const result = await bootstrap({ launchCredential });

    expect(result).toEqual({
      status: 201,
      body: {
        baseUrl: "/core/",
        verificationId: "verification",
        captureToken: "capture-token",
        outcomeToken: "outcome-token",
        region: "tenant-local",
      },
    });
    expect(core.notices.create.mock.calls[0]?.[0]).toMatchObject({
      controller: "Server Controller",
      recipient: "Server Recipient",
    });
    expect(core.authorities.declare.mock.calls[0]?.[1]).toMatchObject({
      recipientDisplayName: "Server Recipient",
      purpose: "idenqa.purpose.identity_verification",
    });
  });
});

async function bootstrap(body: Record<string, unknown>) {
  let middleware:
    | ((
        request: PassThrough & { method?: string; headers: Record<string, string> },
        response: TestResponse,
      ) => Promise<void>)
    | undefined;
  const plugin = createCaptureBootstrapPlugin({
    coreUrl: "http://core.example.test/",
    apiKey: "tenant-api-key",
    region: "tenant-local",
    launchCredential,
    controller: "Server Controller",
    recipient: "Server Recipient",
  });
  const configureServer = plugin.configureServer;
  if (typeof configureServer !== "function") {
    throw new Error("Capture bootstrap plugin has no server hook.");
  }
  configureServer({
    middlewares: {
      use(
        _path: string,
        handler: (
          request: PassThrough & { method?: string; headers: Record<string, string> },
          response: TestResponse,
        ) => Promise<void>,
      ) {
        middleware = handler;
      },
    },
    config: { logger: { error: vi.fn() } },
  });
  if (middleware === undefined) throw new Error("Capture bootstrap middleware was not registered.");

  const request = new PassThrough() as PassThrough & {
    method?: string;
    headers: Record<string, string>;
  };
  request.method = "POST";
  request.headers = {
    "content-type": "application/json",
    "sec-fetch-site": "same-origin",
  };
  const response = new TestResponse();
  request.end(JSON.stringify(body));
  await middleware(request, response);
  return { status: response.statusCode, body: JSON.parse(response.body) };
}

class TestResponse {
  statusCode = 200;
  body = "";
  readonly headers = new Map<string, string>();

  setHeader(name: string, value: string) {
    this.headers.set(name, value);
  }

  end(body: string) {
    this.body = body;
  }
}
