import { describe, expect, it, vi } from "vitest";

import {
  CaptureClient,
  IdenqaAPIError,
  IdenqaClient,
  IdenqaProtocolError,
  IdenqaTransportError,
  OutcomeClient,
  createIdempotencyKey,
  type CaptureProfileDocument,
  type VerificationState,
} from "../src/index.js";

const document: CaptureProfileDocument = {
  schema_version: 1,
  registry: {
    schema_version: 1,
    revision: 1,
    digest: "sha256:71ef9df77044f9bf5eeb7ae448da3d98811ff4cb3f2541f459e60b4628a5364f",
  },
  requirements: [
    {
      key: "selfie",
      purpose: "idenqa.purpose.identity_verification",
      evidence_type: "idenqa.evidence.selfie_image",
      artefacts: ["idenqa.artefact.selfie_image"],
      acquisition: { strategy: "any_of", methods: ["idenqa.method.file_upload"] },
      required_assurances: [],
      constraints: [],
      fallbacks: [],
    },
  ],
};

describe("IdenqaClient", () => {
  it.each([
    "created",
    "collecting",
    "awaiting_input",
    "processing",
    "awaiting_external",
    "manual_review",
    "completed",
    "cancelled",
    "expired",
    "failed",
  ] satisfies VerificationState[])(
    "preserves the %s workflow state on retrieval",
    async (state) => {
      const client = new IdenqaClient({
        baseUrl: "https://core.example.test",
        apiKey: "idq_v1.secret",
        fetch: async () =>
          jsonResponse(
            {
              id: "ver_01M11HEQG00000000000000000",
              state,
              version: 2,
              profile_id: "prf_01M11HEQG00000000000000000",
              profile_revision: 1,
              profile_digest: document.registry.digest,
              policy_id: "pol_01M11HEQG00000000000000000",
              region: "tenant.region.ng",
              requirements: document,
              created_at: "2026-09-06T12:00:00Z",
              updated_at: "2026-09-06T12:01:00Z",
              expires_at: "2026-09-06T13:00:00Z",
            },
            200,
            { ETag: '"2"', "X-Request-ID": "req_lifecycle" },
          ),
      });
      const response = await client.verifications.get("ver_01M11HEQG00000000000000000");
      expect(response.data.state).toBe(state);
      expect(response.etag).toBe('"2"');
      expect(response.data).not.toHaveProperty("outcome");
    },
  );

  it("reads exact and latest decision reports through the public facade", async () => {
    const report = decisionReport();
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      expect(init?.method).toBe("GET");
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer idq_v1.secret");
      const requestId = String(input).includes("/verifications/") ? "req_latest" : "req_exact";
      return jsonResponse(report, 200, { "X-Request-ID": requestId, ETag: '"sha256:eeee"' });
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "idq_v1.secret",
      fetch: fetchMock,
    });

    const exact = await client.decisions.get("dec_01M11HEQG00000000000000000");
    const latest = await client.decisions.getLatest("ver_01M11HEQG00000000000000000");

    expect(exact.data).toMatchObject({
      decisionId: "dec_01M11HEQG00000000000000000",
      policyRevision: 3,
      directive: "complete_verified",
      reproduced: true,
    });
    expect(latest.requestId).toBe("req_latest");
    expect(fetchMock.mock.calls.map(([input]) => String(input))).toEqual([
      "https://core.example.test/v1/decisions/dec_01M11HEQG00000000000000000",
      "https://core.example.test/v1/verifications/ver_01M11HEQG00000000000000000/decision",
    ]);
  });

  it("conditionally reads decisions without parsing a 304 body", async () => {
    const etag = '"sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"';
    const fetchMock = vi.fn<typeof fetch>(async (_input, init) => {
      expect(new Headers(init?.headers).get("If-None-Match")).toBe(etag);
      return new Response(null, {
        status: 304,
        headers: { "X-Request-ID": "req_decision_poll", ETag: etag },
      });
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "idq_v1.secret",
      fetch: fetchMock,
    });

    await expect(client.decisions.poll("dec_01M11HEQG00000000000000000", etag)).resolves.toEqual({
      notModified: true,
      requestId: "req_decision_poll",
      etag,
    });
  });

  it("preserves exact portable bundle text and its closed parsed document", async () => {
    const canonical = JSON.stringify({
      schema_major: 1,
      schema_minor: 0,
      bundle_digest: "f".repeat(64),
      decision_id: "dec_01M11HEQG00000000000000000",
      tenant_id: "ten_01M11HEQG00000000000000000",
      verification_id: "ver_01M11HEQG00000000000000000",
      snapshot_digest: "c".repeat(64),
      evaluation_digest: "d".repeat(64),
      decision_digest: "e".repeat(64),
      snapshot: {},
      evaluation: {},
      decision: {},
    });
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe(
        "https://core.example.test/v1/decisions/dec_01M11HEQG00000000000000000/bundle",
      );
      expect(new Headers(init?.headers).get("Accept")).toBe(
        "application/vnd.idenqa.decision-bundle.v1+json, application/problem+json",
      );
      return new Response(canonical, {
        status: 200,
        headers: {
          "Content-Type": "application/vnd.idenqa.decision-bundle.v1+json",
          "X-Request-ID": "req_bundle",
          ETag: '"sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"',
        },
      });
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "idq_v1.secret",
      fetch: fetchMock,
    });

    const response = await client.decisions.exportBundle("dec_01M11HEQG00000000000000000");

    expect(response.data.canonical).toBe(canonical);
    expect(response.data.document.bundle_digest).toBe("f".repeat(64));
    expect(response.requestId).toBe("req_bundle");
  });

  it("exposes notice and authority operations through the public facade", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe("https://core.example.test/v1/notices");
      expect(init?.method).toBe("POST");
      expect(JSON.parse(String(init?.body))).toMatchObject({
        key: "tenant.notice.identity_verification",
        effective_at: "2026-08-27T12:00:00Z",
      });
      return jsonResponse(
        {
          id: "ntc_01M11HEQG00000000000000000",
          key: "tenant.notice.identity_verification",
          locale: "en-NG",
          controller: "Example Controller",
          recipient: "Example Recipient",
          copy: {
            title: "Identity verification",
            summary: "We verify identity.",
            purpose: "Identity verification only.",
            consequences: "Collection stops if refused.",
          },
          effective_at: "2026-08-27T12:00:00Z",
          created_at: "2026-08-27T12:00:00Z",
          digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        },
        201,
        { "X-Request-ID": "req_notice", Location: "/v1/notices/ntc_01" },
      );
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "idq_v1.secret",
      fetch: fetchMock,
    });

    const response = await client.notices.create(
      {
        key: "tenant.notice.identity_verification",
        locale: "en-NG",
        controller: "Example Controller",
        recipient: "Example Recipient",
        copy: {
          title: "Identity verification",
          summary: "We verify identity.",
          purpose: "Identity verification only.",
          consequences: "Collection stops if refused.",
        },
        effectiveAt: "2026-08-27T12:00:00Z",
      },
      { idempotencyKey: "notice-1" },
    );

    expect(response.data.id).toBe("ntc_01M11HEQG00000000000000000");
    expect(response.data.effectiveAt).toBe("2026-08-27T12:00:00Z");
    expect(response.location).toBe("/v1/notices/ntc_01");
  });

  it("encodes auth, idempotency, base paths, and public response metadata", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe("https://core.example.test/idenqa/v1/capture-profiles");
      const headers = new Headers(init?.headers);
      expect(headers.get("Authorization")).toBe("Bearer idq_v1.secret");
      expect(headers.get("Idempotency-Key")).toBe('"create-1"');
      expect(headers.get("Content-Type")).toBe("application/json");
      expect(JSON.parse(String(init?.body))).toEqual({ name: "Selfie", document });
      return jsonResponse(
        {
          profile_id: "prf_01M11HEQG00000000000000000",
          name: "Selfie",
          state: "draft",
          version: 1,
          latest_revision: 1,
          draft_revision: 1,
          revision: 1,
          digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          updated_at: "2026-08-27T12:00:00Z",
        },
        201,
        { ETag: '"1"', Location: "/v1/capture-profiles/prf_01", "X-Request-ID": "req_01" },
      );
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test/idenqa",
      apiKey: "idq_v1.secret",
      fetch: fetchMock,
    });

    const result = await client.captureProfiles.create(
      { name: "Selfie", document },
      { idempotencyKey: "create-1" },
    );

    expect(result).toEqual({
      data: {
        profileId: "prf_01M11HEQG00000000000000000",
        name: "Selfie",
        state: "draft",
        version: 1,
        latestRevision: 1,
        draftRevision: 1,
        revision: 1,
        digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        updatedAt: "2026-08-27T12:00:00Z",
      },
      requestId: "req_01",
      etag: '"1"',
      location: "/v1/capture-profiles/prf_01",
    });
  });

  it("maps stable problem details without exposing the bearer credential", async () => {
    const fetchMock = vi.fn<typeof fetch>(async () =>
      jsonResponse(
        {
          type: "https://idenqa.dev/problems/not-found",
          title: "Not found",
          status: 404,
          code: "NOT_FOUND",
          detail: "The requested resource was not found.",
          request_id: "req_problem",
        },
        404,
        { "Retry-After": "3", "X-Request-ID": "req_problem" },
      ),
    );
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "credential-that-must-not-appear",
      fetch: fetchMock,
    });

    const error = await client.captureProfiles.get("prf_missing").catch((cause: unknown) => cause);

    expect(error).toBeInstanceOf(IdenqaAPIError);
    expect(error).toMatchObject({
      code: "NOT_FOUND",
      status: 404,
      requestId: "req_problem",
      retryAfter: "3",
    });
    expect(String(error)).not.toContain("credential-that-must-not-appear");
  });

  it("rejects a successful response without a server request ID", async () => {
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "key",
      fetch: vi.fn<typeof fetch>(async () => jsonResponse({}, 200)),
    });

    await expect(client.captureProfiles.list()).rejects.toBeInstanceOf(IdenqaProtocolError);
  });

  it("reports AbortSignal cancellation as a stable transport error", async () => {
    const controller = new AbortController();
    controller.abort();
    const client = new CaptureClient({
      baseUrl: "https://core.example.test",
      captureToken: "capture-token",
      fetch: vi.fn<typeof fetch>(async (_input, init) => {
        if (init?.signal?.aborted) throw new DOMException("aborted", "AbortError");
        throw new Error("expected an aborted signal");
      }),
    });

    const error = await client
      .getSession({ signal: controller.signal })
      .catch((cause: unknown) => cause);

    expect(error).toBeInstanceOf(IdenqaTransportError);
    expect(error).toMatchObject({ code: "SDK_REQUEST_ABORTED", aborted: true });
  });

  it("validates conditional and idempotency headers before sending", async () => {
    const fetchMock = vi.fn<typeof fetch>();
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "key",
      fetch: fetchMock,
    });

    await expect(
      client.captureProfiles.publish("prf_01", { etag: "1", idempotencyKey: "publish" }),
    ).rejects.toThrow("strong quoted entity tag");
    await expect(
      client.captureProfiles.create(
        { name: "Selfie", document },
        { idempotencyKey: "contains\na-newline" },
      ),
    ).rejects.toThrow("printable ASCII");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

function decisionReport() {
  return {
    schema_major: 1,
    schema_minor: 0,
    decision_id: "dec_01M11HEQG00000000000000000",
    tenant_id: "ten_01M11HEQG00000000000000000",
    verification_id: "ver_01M11HEQG00000000000000000",
    policy_id: "pol_01M11HEQG00000000000000000",
    policy_revision: 3,
    policy_digest: "a".repeat(64),
    evaluator_major: 1,
    evaluator_minor: 0,
    evaluator_digest: "b".repeat(64),
    snapshot_digest: "c".repeat(64),
    evaluation_digest: "d".repeat(64),
    decision_digest: "e".repeat(64),
    bundle_digest: "f".repeat(64),
    directive: "complete_verified",
    outcome: "verified",
    assurance: "identity_verified",
    actor: "machine",
    fact_count: 3,
    requirement_count: 1,
    evaluated_at: "2026-08-31T12:00:00Z",
    decided_at: "2026-08-31T12:00:01Z",
    reproduced: true,
  };
}

describe("Capture and outcome clients", () => {
  it("reads the subject-safe authoritative outcome without decision internals", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe("https://core.example.test/v1/capture/outcome");
      expect(init?.method).toBe("GET");
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer outcome-token");
      return jsonResponse(
        {
          verification_id: "ver_01M11HEQG00000000000000000",
          state: "verified",
          session_version: 4,
          updated_at: "2026-08-30T12:00:04Z",
        },
        200,
        { "X-Request-ID": "req_outcome" },
      );
    });
    const client = new OutcomeClient({
      baseUrl: "https://core.example.test",
      outcomeToken: "outcome-token",
      fetch: fetchMock,
    });

    await expect(client.getOutcome()).resolves.toEqual({
      data: {
        verificationId: "ver_01M11HEQG00000000000000000",
        state: "verified",
        sessionVersion: 4,
        updatedAt: "2026-08-30T12:00:04Z",
      },
      requestId: "req_outcome",
    });
  });

  it("issues display-once realtime connection material without a request body", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe("https://core.example.test/v1/capture/connections");
      expect(init?.method).toBe("POST");
      expect(init?.body).toBeUndefined();
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer capture-token");
      return jsonResponse(
        {
          websocket_url: "wss://core.example.test/v1/capture/socket?ticket=idq_wst_v1_display-once",
          protocol: "idenqa.capture.v1",
          expires_at: "2026-08-30T12:00:30Z",
        },
        201,
        { "X-Request-ID": "req_connection", "Cache-Control": "no-store" },
      );
    });
    const client = new CaptureClient({
      baseUrl: "https://core.example.test",
      captureToken: "capture-token",
      fetch: fetchMock,
    });

    await expect(client.createConnection()).resolves.toEqual({
      data: {
        websocketUrl: "wss://core.example.test/v1/capture/socket?ticket=idq_wst_v1_display-once",
        protocol: "idenqa.capture.v1",
        expiresAt: "2026-08-30T12:00:30Z",
      },
      requestId: "req_connection",
    });
  });

  it("recovers the token-scoped accepted capture progress", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      expect(String(input)).toBe("https://core.example.test/v1/capture/progress");
      expect(init?.method).toBe("GET");
      expect(new Headers(init?.headers).get("Authorization")).toBe("Bearer capture-token");
      return jsonResponse(
        {
          verification_id: "ver_01M11HEQG00000000000000000",
          completions: [
            {
              upload_id: "upl_01M11HEQG00000000000000000",
              evidence_id: "evd_01M11HEQG00000000000000000",
              requirement_key: "selfie",
              evidence_type: "idenqa.evidence.selfie_image",
              artefact: "idenqa.artefact.selfie_image",
              acquisition_method: "idenqa.method.file_upload",
              fallback_condition: "capture_failed",
            },
          ],
        },
        200,
        { "X-Request-ID": "req_progress" },
      );
    });
    const client = new CaptureClient({
      baseUrl: "https://core.example.test",
      captureToken: "capture-token",
      fetch: fetchMock,
    });

    await expect(client.getProgress()).resolves.toMatchObject({
      data: {
        verificationId: "ver_01M11HEQG00000000000000000",
        completions: [
          {
            requirementKey: "selfie",
            acquisitionMethod: "idenqa.method.file_upload",
            fallbackCondition: "capture_failed",
          },
        ],
      },
    });
  });

  it("conditionally polls capture progress without parsing a 304 body", async () => {
    const etag = '"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"';
    const fetchMock = vi.fn<typeof fetch>(async (_input, init) => {
      expect(new Headers(init?.headers).get("If-None-Match")).toBe(etag);
      return new Response(null, {
        status: 304,
        headers: { "X-Request-ID": "req_poll", ETag: etag },
      });
    });
    const client = new CaptureClient({
      baseUrl: "https://core.example.test",
      captureToken: "capture-token",
      fetch: fetchMock,
    });

    await expect(client.pollProgress(etag)).resolves.toEqual({
      notModified: true,
      requestId: "req_poll",
      etag,
    });
  });

  it("issues an intent and sends raw evidence with exact integrity preconditions", async () => {
    const digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
    const wireUpload = {
      id: "upl_01M11HEQG00000000000000000",
      evidence_id: "evd_01M11HEQG00000000000000000",
      state: "issued" as const,
      version: 1,
      attempt: 0,
      requirement_key: "selfie",
      evidence_type: "idenqa.evidence.selfie_image",
      artefact: "idenqa.artefact.selfie_image",
      acquisition_method: "idenqa.method.file_upload",
      assurances: [],
      allowed_media_types: ["image/jpeg" as const],
      maximum_bytes: 16_777_216,
      expected_bytes: 4,
      media_type: "image/jpeg" as const,
      region: "idenqa.region.synthetic",
      created_at: "2026-08-30T12:00:00Z",
      updated_at: "2026-08-30T12:00:00Z",
      expires_at: "2026-08-30T12:15:00Z",
    };
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = String(input);
      const headers = new Headers(init?.headers);
      expect(headers.get("Authorization")).toBe("Bearer capture-token");
      if (url.endsWith("/v1/evidence-uploads")) {
        expect(init?.method).toBe("POST");
        expect(headers.get("Idempotency-Key")).toBe('"upload-1"');
        expect(JSON.parse(String(init?.body))).toMatchObject({
          requirement_key: "selfie",
          expected_digest: digest,
        });
        return jsonResponse(wireUpload, 201, { "X-Request-ID": "req_issue", ETag: '"1"' });
      }
      if (init?.method === "GET") {
        expect(url).toBe(
          "https://core.example.test/v1/evidence-uploads/upl_01M11HEQG00000000000000000",
        );
        return jsonResponse(wireUpload, 200, { "X-Request-ID": "req_recover", ETag: '"1"' });
      }
      expect(url).toBe(
        "https://core.example.test/v1/evidence-uploads/upl_01M11HEQG00000000000000000",
      );
      expect(init?.method).toBe("PUT");
      expect(init?.body).toBeInstanceOf(Blob);
      expect(headers.get("Content-Type")).toBe("image/jpeg");
      expect(headers.get("Content-Digest")).toBe(
        "sha-256=:qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqo=:",
      );
      expect(headers.get("If-Match")).toBe('"1"');
      return jsonResponse({ ...wireUpload, state: "accepted", version: 3, attempt: 1 }, 200, {
        "X-Request-ID": "req_accept",
        ETag: '"3"',
      });
    });
    const client = new CaptureClient({
      baseUrl: "https://core.example.test",
      captureToken: "capture-token",
      fetch: fetchMock,
    });

    const issued = await client.createEvidenceUpload(
      {
        requirementKey: "selfie",
        artefact: "idenqa.artefact.selfie_image",
        acquisitionMethod: "idenqa.method.file_upload",
        expectedBytes: 4,
        expectedDigest: digest,
        mediaType: "image/jpeg",
        region: "idenqa.region.synthetic",
      },
      { idempotencyKey: "upload-1" },
    );
    const accepted = await client.uploadEvidence(
      issued.data.id,
      new Blob(["jpeg"], { type: "image/jpeg" }),
      { etag: issued.etag!, digest },
    );
    const recovered = await client.getEvidenceUpload(issued.data.id);

    expect(issued.data.evidenceId).toBe("evd_01M11HEQG00000000000000000");
    expect(accepted.data).toMatchObject({ state: "accepted", version: 3, attempt: 1 });
    expect(recovered.etag).toBe('"1"');
  });

  it("rejects unsafe evidence metadata before fetch", async () => {
    const fetchMock = vi.fn<typeof fetch>();
    const client = new CaptureClient({
      baseUrl: "https://core.example.test",
      captureToken: "capture-token",
      fetch: fetchMock,
    });

    await expect(
      client.uploadEvidence("upl_01", new Blob(["x"], { type: "image/webp" }), {
        etag: '"1"',
        digest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      }),
    ).rejects.toThrow("image/jpeg or image/png");
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe("createIdempotencyKey", () => {
  it("creates a reusable non-secret ASCII value", () => {
    expect(createIdempotencyKey("verification")).toMatch(
      /^verification_[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/,
    );
  });

  it("rejects unsafe prefixes", () => {
    expect(() => createIdempotencyKey("subject data")).toThrow("safe ASCII");
  });
});

function jsonResponse(
  value: unknown,
  status: number,
  headers: Readonly<Record<string, string>> = {},
): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: {
      "Content-Type": status >= 400 ? "application/problem+json" : "application/json",
      ...headers,
    },
  });
}

describe("verification cancellation", () => {
  it("maps tenant and subject cancellation without losing retry identity", async () => {
    const requests: Request[] = [];
    const fetch = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      requests.push(new Request(input, init));
      return jsonResponse(
        {
          event_id: "evt_01M11HEQG00000000000000000",
          verification_id: "ver_01M11HEQG00000000000000000",
          state: "cancelled",
          version: 3,
          occurred_at: "2026-09-06T12:00:00Z",
        },
        200,
        { "X-Request-ID": "req_cancel" },
      );
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "tenant-token",
      fetch,
    });
    const capture = new CaptureClient({
      baseUrl: "https://core.example.test",
      captureToken: "subject-token",
      fetch,
    });
    const result = await client.verifications.cancel("ver_01M11HEQG00000000000000000", 2, {
      idempotencyKey: "cancel-1",
    });
    await client.verifications.cancel("ver_01M11HEQG00000000000000000", 2, {
      idempotencyKey: "cancel-1",
    });
    await capture.cancel(2, { idempotencyKey: "cancel-subject" });
    expect(result.data.state).toBe("cancelled");
    expect(result.data.version).toBe(3);
    expect(
      requests[0]!.url.endsWith("/v1/verifications/ver_01M11HEQG00000000000000000/cancel"),
    ).toBe(true);
    expect(requests[2]!.url.endsWith("/v1/capture/cancel")).toBe(true);
    expect(requests[0]!.headers.get("Idempotency-Key")).toBe(
      requests[1]!.headers.get("Idempotency-Key"),
    );
    expect(requests[2]!.headers.get("Authorization")).toBe("Bearer subject-token");
    expect(await requests[0]!.json()).toEqual({ expected_version: 2 });
    await expect(capture.cancel(0, { idempotencyKey: "invalid" })).rejects.toThrow();
  });
});
