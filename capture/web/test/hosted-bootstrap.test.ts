import { PassThrough } from "node:stream";
import type { IncomingMessage, ServerResponse } from "node:http";
import type { UserConfigFn, ViteDevServer } from "vite";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const core = vi.hoisted(() => ({
  captureProfiles: {
    get: vi.fn(),
    getRevision: vi.fn(),
    create: vi.fn(),
    publish: vi.fn(),
  },
  policies: { create: vi.fn(), activate: vi.fn() },
  verifications: { create: vi.fn() },
  notices: { create: vi.fn() },
  authorities: { declare: vi.fn() },
  identity: {
    createSubject: vi.fn(),
    getSubject: vi.fn(),
    listVerifications: vi.fn(),
    linkVerification: vi.fn(),
  },
}));

vi.mock("@idenqa/sdk", () => ({
  IdenqaClient: class {
    captureProfiles = core.captureProfiles;
    policies = core.policies;
    verifications = core.verifications;
    notices = core.notices;
    authorities = core.authorities;
    identity = core.identity;
  },
  CaptureClient: class {
    async getExperience() {
      throw new Error("Experience signing is not configured for this fixture.");
    }
  },
  createIdempotencyKey: () => "synthetic-idempotency-key",
}));

const profileId = "prf_01M3VTQXVWEAX20X1ZQNG6N6EC";
const subject = {
  id: "sub_01M3VTQXVWEAX20X1ZQNG6N6EC",
  region: "tenant-local",
  state: "active",
  version: 1,
};
const launchId = "c59b03fc-b0f0-4e76-8f3f-71756e58e4bc";
const selfie = {
  key: "selfie",
  purpose: "idenqa.purpose.identity_verification",
  evidence_type: "idenqa.evidence.selfie_image",
  artefacts: ["idenqa.artefact.selfie_image"],
};
const document = {
  key: "document",
  purpose: "idenqa.purpose.identity_verification",
  evidence_type: "idenqa.evidence.document_image",
  artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
  document_options: [
    { id: "nin", artefacts: ["idenqa.artefact.document_front"] },
    {
      id: "ghana_card",
      artefacts: ["idenqa.artefact.document_front", "idenqa.artefact.document_back"],
    },
    { id: "passport", artefacts: ["idenqa.artefact.document_front"] },
  ],
};

beforeEach(() => {
  vi.resetAllMocks();
  vi.stubEnv("IDENQA_DEMO_CORE_URL", "http://core.example.test");
  vi.stubEnv("IDENQA_DEMO_TENANT_API_KEY", "synthetic-tenant-key");
  vi.stubEnv("IDENQA_DEMO_REGION", "tenant-local");
  vi.stubEnv("IDENQA_DEMO_PROCESSING_PURPOSE", undefined);
  core.captureProfiles.get.mockResolvedValue({
    data: { name: "Synthetic flow", publishedRevision: 2 },
  });
  core.captureProfiles.getRevision.mockResolvedValue({
    data: { document: { requirements: [selfie] } },
  });
  core.captureProfiles.create.mockResolvedValue({
    data: { profileId: "country-profile" },
    etag: "profile-etag",
  });
  core.captureProfiles.publish.mockResolvedValue({ data: { state: "active" } });
  core.policies.create.mockResolvedValue({
    data: { policy: { id: "policy", latestRevision: 1, activationVersion: 1 } },
  });
  core.identity.createSubject.mockResolvedValue({ data: { subject } });
  core.identity.getSubject.mockResolvedValue({ data: { subject } });
  core.identity.listVerifications.mockResolvedValue({ data: { verification_ids: [] } });
  core.identity.linkVerification.mockResolvedValue({
    data: { subject: { ...subject, version: 2 } },
  });
  core.verifications.create.mockResolvedValue({
    data: {
      session: {
        id: "verification",
        version: 1,
        createdAt: "2026-10-04T11:00:00.789Z",
        expiresAt: "2026-10-04T12:00:00Z",
        requirements: { requirements: [selfie] },
      },
      captureToken: "synthetic-capture-token",
      outcomeToken: "synthetic-outcome-token",
    },
  });
  core.notices.create.mockResolvedValue({
    data: { id: "notice", effectiveAt: "2026-10-04T11:00:00Z" },
  });
});

afterEach(() => vi.unstubAllEnvs());

describe("hosted flow bootstrap", () => {
  it("declares the purpose explicitly configured by a published profile", async () => {
    const requirements = [{ ...selfie, purpose: "idenqa.purpose.fraud_prevention" }];
    core.captureProfiles.getRevision.mockResolvedValue({ data: { document: { requirements } } });
    core.verifications.create.mockResolvedValue({
      data: {
        session: {
          id: "verification",
          version: 1,
          createdAt: "2026-10-04T11:00:00.789Z",
          expiresAt: "2026-10-04T12:00:00Z",
          requirements: { requirements },
        },
        captureToken: "synthetic-capture-token",
        outcomeToken: "synthetic-outcome-token",
      },
    });

    const result = await bootstrap({ profile: profileId });

    expect(result.status).toBe(201);
    expect(core.authorities.declare.mock.calls[0]?.[1]).toMatchObject({
      purpose: "idenqa.purpose.fraud_prevention",
    });
    expect(core.captureProfiles.create).not.toHaveBeenCalled();
  });

  it("requires explicit server configuration for a profile with several purposes before creating resources", async () => {
    core.captureProfiles.getRevision.mockResolvedValue({
      data: {
        document: {
          requirements: [document, { ...selfie, purpose: "idenqa.purpose.fraud_prevention" }],
        },
      },
    });

    const result = await bootstrap({ profile: profileId });

    expect(result).toEqual({ status: 400, body: { error: "processing_purpose_required" } });
    expect(core.verifications.create).not.toHaveBeenCalled();
    expect(core.captureProfiles.create).not.toHaveBeenCalled();
    expect(core.policies.create).not.toHaveBeenCalled();
  });

  it("uses a compatible server-configured purpose for a profile with several purposes", async () => {
    vi.stubEnv("IDENQA_DEMO_PROCESSING_PURPOSE", "idenqa.purpose.fraud_prevention");
    const requirements = [document, { ...selfie, purpose: "idenqa.purpose.fraud_prevention" }];
    core.captureProfiles.getRevision.mockResolvedValue({ data: { document: { requirements } } });
    core.verifications.create.mockResolvedValue({
      data: {
        session: {
          id: "verification",
          version: 1,
          createdAt: "2026-10-04T11:00:00.789Z",
          expiresAt: "2026-10-04T12:00:00Z",
          requirements: { requirements },
        },
        captureToken: "synthetic-capture-token",
        outcomeToken: "synthetic-outcome-token",
      },
    });

    const result = await bootstrap({ profile: profileId, country: "GH" });

    expect(result.status).toBe(201);
    expect(core.authorities.declare.mock.calls[0]?.[1]).toMatchObject({
      purpose: "idenqa.purpose.fraud_prevention",
    });
    expect(
      core.captureProfiles.create.mock.calls[0]?.[0].document.requirements.map(
        (requirement: { purpose: string }) => requirement.purpose,
      ),
    ).toEqual(requirements.map((requirement) => requirement.purpose));
  });

  it.each(["", "idenqa.purpose.fraud_prevention"])(
    "rejects an incompatible configured purpose %j without creating a session",
    async (purpose) => {
      vi.stubEnv("IDENQA_DEMO_PROCESSING_PURPOSE", purpose);

      const result = await bootstrap({ profile: profileId });

      expect(result).toEqual({ status: 400, body: { error: "processing_purpose_mismatch" } });
      expect(core.verifications.create).not.toHaveBeenCalled();
      expect(core.policies.create).not.toHaveBeenCalled();
      expect(core.authorities.declare).not.toHaveBeenCalled();
    },
  );

  it("rejects missing requirement purposes without substituting a demo default", async () => {
    core.captureProfiles.getRevision.mockResolvedValue({
      data: { document: { requirements: [{ ...selfie, purpose: undefined }] } },
    });

    const result = await bootstrap({ profile: profileId });

    expect(result).toEqual({ status: 400, body: { error: "profile_purpose_invalid" } });
    expect(core.verifications.create).not.toHaveBeenCalled();
  });

  it("rechecks the pinned session purpose if the published profile changes during bootstrap", async () => {
    core.captureProfiles.getRevision.mockResolvedValue({
      data: {
        document: { requirements: [{ ...selfie, purpose: "idenqa.purpose.fraud_prevention" }] },
      },
    });

    const result = await bootstrap({ profile: profileId });

    expect(result).toEqual({ status: 400, body: { error: "processing_purpose_mismatch" } });
    expect(core.notices.create).not.toHaveBeenCalled();
    expect(core.authorities.declare).not.toHaveBeenCalled();
  });

  it("creates a selfie-only session without requiring country, regardless of the launch hint", async () => {
    const result = await bootstrap({ profile: profileId, journey: "document" });

    expect(result.status).toBe(201);
    expect(result.body).toMatchObject({
      countrySelectionRequired: false,
      selfieRequirementKey: "selfie",
    });
    expect(core.captureProfiles.getRevision).toHaveBeenCalledWith(profileId, 2);
    expect(core.verifications.create.mock.calls[0]?.[0]).toMatchObject({
      captureProfileId: profileId,
    });
    expect(core.captureProfiles.create).not.toHaveBeenCalled();
    expect(core.authorities.declare.mock.calls[0]?.[1]).toMatchObject({
      jurisdiction: "tenant.jurisdiction.local_demo",
    });
  });

  it("does not attach an irrelevant country to a selfie-only flow", async () => {
    const result = await bootstrap({ profile: profileId, country: "unsupported" });

    expect(result.status).toBe(201);
    expect(core.authorities.declare.mock.calls[0]?.[1]).toMatchObject({
      jurisdiction: "tenant.jurisdiction.local_demo",
    });
  });

  it.each([[document], [document, selfie]])(
    "asks for country when the published flow contains documents without creating a session",
    async (...requirements) => {
      core.captureProfiles.getRevision.mockResolvedValue({ data: { document: { requirements } } });
      const result = await bootstrap({ profile: profileId });

      expect(result).toEqual({
        status: 200,
        body: { countrySelectionRequired: true, captureItemCount: requirements.length },
      });
      expect(core.policies.create).not.toHaveBeenCalled();
      expect(core.verifications.create).not.toHaveBeenCalled();
      expect(core.captureProfiles.create).not.toHaveBeenCalled();
      expect(core.identity.createSubject).not.toHaveBeenCalled();
    },
  );

  it("pins only country-relevant documents and preserves the selfie requirement in a mixed flow", async () => {
    core.captureProfiles.getRevision.mockResolvedValue({
      data: { document: { requirements: [document, selfie] } },
    });
    const result = await bootstrap({ profile: profileId, country: "GH" });

    expect(result.status).toBe(201);
    const created = core.captureProfiles.create.mock.calls[0]?.[0];
    expect(
      created.document.requirements[0].document_options.map((option: { id: string }) => option.id),
    ).toEqual(["ghana_card", "passport"]);
    expect(created.document.requirements[1]).toEqual(selfie);
    expect(core.verifications.create.mock.calls[0]?.[0]).toMatchObject({
      captureProfileId: "country-profile",
    });
    expect(core.authorities.declare.mock.calls[0]?.[1]).toMatchObject({
      jurisdiction: "tenant.jurisdiction.country.gh",
    });
  });

  it("rejects an unsupported document country before creating a session", async () => {
    core.captureProfiles.getRevision.mockResolvedValue({
      data: { document: { requirements: [document] } },
    });
    const result = await bootstrap({ profile: profileId, country: "unsupported" });

    expect(result.status).toBe(502);
    expect(core.verifications.create).not.toHaveBeenCalled();
  });

  it.each([false, true])(
    "uses the built-in fixture's document requirements when no profile is supplied",
    async (hasDocuments) => {
      const result = await bootstrap(hasDocuments ? { journey: "document" } : {});

      expect(result.status).toBe(hasDocuments ? 200 : 201);
      expect(result.body.countrySelectionRequired).toBe(hasDocuments);
      expect(core.captureProfiles.get).not.toHaveBeenCalled();
      expect(core.verifications.create).toHaveBeenCalledTimes(hasDocuments ? 0 : 1);
    },
  );

  it("creates a persistent subject and links the verification before declaring authority", async () => {
    const result = await bootstrap({ profile: profileId });

    expect(result.status).toBe(201);
    expect(result.body.subjectId).toBe(subject.id);
    expect(core.identity.createSubject).toHaveBeenCalledWith(undefined, {
      idempotencyKey: `capture_demo_${launchId}_subject`,
    });
    expect(core.identity.linkVerification).toHaveBeenCalledWith(subject.id, "verification", 1, {
      idempotencyKey: `capture_demo_${launchId}_subject_link_v1`,
    });
    expect(core.identity.createSubject.mock.invocationCallOrder[0]).toBeLessThan(
      core.verifications.create.mock.invocationCallOrder[0]!,
    );
    expect(core.verifications.create.mock.invocationCallOrder[0]).toBeLessThan(
      core.identity.linkVerification.mock.invocationCallOrder[0]!,
    );
    expect(core.identity.linkVerification.mock.invocationCallOrder[0]).toBeLessThan(
      core.authorities.declare.mock.invocationCallOrder[0]!,
    );
  });

  it("reuses only an explicitly selected existing subject", async () => {
    const result = await bootstrap({ profile: profileId, subjectId: subject.id });

    expect(result.status).toBe(201);
    expect(result.body.subjectId).toBe(subject.id);
    expect(core.identity.createSubject).not.toHaveBeenCalled();
    expect(core.identity.getSubject).toHaveBeenCalledWith(subject.id);
    expect(core.identity.linkVerification).toHaveBeenCalledWith(
      subject.id,
      "verification",
      1,
      expect.any(Object),
    );
  });

  it.each([
    { state: "suspended" },
    { state: "deleting" },
    { state: "deleted" },
    { region: "other-region" },
  ])(
    "rejects an unavailable existing subject %j before creating a verification",
    async (change) => {
      core.identity.getSubject.mockResolvedValue({ data: { subject: { ...subject, ...change } } });

      const result = await bootstrap({ profile: profileId, subjectId: subject.id });

      expect(result).toEqual({ status: 400, body: { error: "subject_unavailable" } });
      expect(core.identity.createSubject).not.toHaveBeenCalled();
      expect(core.verifications.create).not.toHaveBeenCalled();
    },
  );

  it("does not replace an inaccessible subject with a new customer", async () => {
    core.identity.getSubject.mockRejectedValue(
      Object.assign(new Error("Not found"), { code: "NOT_FOUND" }),
    );

    const result = await bootstrap({ profile: profileId, subjectId: subject.id });

    expect(result.status).toBe(502);
    expect(core.identity.createSubject).not.toHaveBeenCalled();
    expect(core.verifications.create).not.toHaveBeenCalled();
  });

  it("withholds capture credentials when the subject association fails", async () => {
    core.identity.linkVerification.mockRejectedValue(new Error("Subject write is unavailable"));

    const result = await bootstrap({ profile: profileId });

    expect(result).toEqual({ status: 502, body: { error: "core_bootstrap_failed" } });
    expect(core.notices.create).not.toHaveBeenCalled();
    expect(core.authorities.declare).not.toHaveBeenCalled();
  });

  it("uses Core's committed association and stable mutation keys when a bootstrap is retried", async () => {
    const first = await bootstrap({ profile: profileId });
    core.identity.getSubject.mockResolvedValue({ data: { subject: { ...subject, version: 2 } } });
    core.identity.listVerifications.mockResolvedValue({
      data: { verification_ids: ["verification"] },
    });

    // A new middleware instance also models a development-server restart.
    const retried = await bootstrap({ profile: profileId });

    expect(retried).toEqual(first);
    expect(core.identity.linkVerification).toHaveBeenCalledTimes(1);
    for (const mutation of [
      core.identity.createSubject,
      core.policies.create,
      core.policies.activate,
      core.verifications.create,
      core.notices.create,
      core.authorities.declare,
    ]) {
      expect(mutation.mock.calls[1]).toEqual(mutation.mock.calls[0]);
    }
    expect(core.notices.create.mock.calls[0]?.[0].effectiveAt).toBe("2026-10-04T11:00:00.000Z");
  });

  it("finds a committed verification link beyond the first page", async () => {
    core.identity.listVerifications
      .mockResolvedValueOnce({ data: { verification_ids: ["older"], next_cursor: "next-page" } })
      .mockResolvedValueOnce({ data: { verification_ids: ["verification"] } });

    const result = await bootstrap({ profile: profileId, subjectId: subject.id });

    expect(result.status).toBe(201);
    expect(core.identity.listVerifications).toHaveBeenLastCalledWith(subject.id, {
      limit: 100,
      after: "next-page",
    });
    expect(core.identity.linkVerification).not.toHaveBeenCalled();
  });

  it("rereads the subject version when another journey links concurrently", async () => {
    core.identity.getSubject
      .mockResolvedValueOnce({ data: { subject } })
      .mockResolvedValueOnce({ data: { subject } })
      .mockResolvedValueOnce({ data: { subject: { ...subject, version: 2 } } });
    core.identity.linkVerification.mockRejectedValueOnce(
      Object.assign(new Error("Version conflict"), { code: "CONFLICT" }),
    );

    const result = await bootstrap({ profile: profileId, subjectId: subject.id });

    expect(result.status).toBe(201);
    expect(core.identity.linkVerification).toHaveBeenLastCalledWith(subject.id, "verification", 2, {
      idempotencyKey: `capture_demo_${launchId}_subject_link_v2`,
    });
  });

  it.each([{ launchId: "invalid" }, { subjectId: "invalid" }])(
    "rejects malformed launch identifiers %j before creating resources",
    async (change) => {
      const result = await bootstrap({ profile: profileId, ...change });

      expect(result.status).toBe(400);
      expect(core.policies.create).not.toHaveBeenCalled();
      expect(core.identity.createSubject).not.toHaveBeenCalled();
      expect(core.verifications.create).not.toHaveBeenCalled();
    },
  );
});

async function bootstrap(launch: Record<string, string>) {
  // Load the actual Vite middleware without binding a port or using tenant credentials.
  const configPath = "../vite.config.mjs";
  const module = await import(configPath);
  const config = await (module.default as UserConfigFn)({ command: "serve", mode: "test" });
  const plugin = config.plugins
    ?.flat()
    .find(
      (candidate) =>
        candidate &&
        typeof candidate === "object" &&
        "name" in candidate &&
        candidate.name === "idenqa-real-core-demo-bootstrap",
    );
  if (!plugin || !("configureServer" in plugin) || typeof plugin.configureServer !== "function") {
    throw new Error("The hosted bootstrap middleware is missing.");
  }
  let handler: ((request: IncomingMessage, response: ServerResponse) => Promise<void>) | undefined;
  (plugin.configureServer as (server: ViteDevServer) => void)({
    middlewares: {
      use: (_path: string, callback: typeof handler) => {
        handler = callback;
      },
    },
    config: { logger: { error: vi.fn() } },
  } as unknown as ViteDevServer);
  if (handler === undefined) throw new Error("The hosted bootstrap middleware was not registered.");
  const request = new PassThrough() as unknown as IncomingMessage;
  request.method = "POST";
  request.headers = { "content-type": "application/json", "sec-fetch-site": "same-origin" };
  const response = { statusCode: 0, setHeader: vi.fn(), end: vi.fn() };
  const handled = handler(request, response as unknown as ServerResponse);
  (request as unknown as PassThrough).end(
    JSON.stringify({
      launchId,
      controller: "Synthetic tenant",
      recipient: "Synthetic tenant",
      ...launch,
    }),
  );
  await handled;
  return {
    status: response.statusCode,
    body: JSON.parse(response.end.mock.calls[0]?.[0] ?? "null"),
  };
}
