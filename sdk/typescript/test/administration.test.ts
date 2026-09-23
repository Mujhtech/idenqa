import { describe, expect, it, vi } from "vitest";
import {
  IdenqaClient,
  IdenqaAPIError,
  IdenqaTransportError,
  CaptureClient,
  OutcomeClient,
} from "../src/index.js";

const suffix = "01M11HEQG00000000000000000";
const id = (prefix: string) => `${prefix}_${suffix}`;
const mutation = { idempotencyKey: 'sdk"\\retry' };
const grant = {
  check_reference: "check-1",
  runner_identity: "model.runner",
  workload_version: "v1",
  purpose: "identity.verification",
  permitted_variants: ["evidence.variant.original"],
  recipient_reference: "tenant.recipient.primary",
  output_destination: "model.runner",
  maximum_uses: 1,
  ttl_seconds: 60,
  reason: "process",
};
const processor = {
  name: "Example",
  role: "processor" as const,
  purpose: "identity.verification",
  data_classes: ["raw_evidence" as const],
  regions: ["ng-1"],
  transfer_mechanism: "tenant.contract",
  expected_version: 0,
};
const disclosure = {
  request_id: id("prq"),
  recipient: "tenant.backend",
  purpose: "access",
  data_class: "subject_export" as const,
  legal_basis: "tenant.contract",
  region: "ng-1",
  reference: "sha256:" + "a".repeat(64),
};
const impact = {
  kind: "review.copilot.summarize" as const,
  assessment: "Human review required",
  risk_level: "high" as const,
};

interface Operation {
  name: string;
  method: string;
  path: string;
  run: (client: IdenqaClient) => Promise<unknown>;
  body?: unknown;
  keyed?: boolean;
}

const operations: Operation[] = [
  {
    name: "evidence",
    method: "GET",
    path: `/v1/evidence/${id("evd")}`,
    run: (c) => c.evidence.get(id("evd")),
  },
  {
    name: "lifecycle",
    method: "GET",
    path: `/v1/evidence/${id("evd")}/lifecycle?limit=3`,
    run: (c) => c.evidence.lifecycle(id("evd"), { limit: 3 }),
  },
  {
    name: "grant",
    method: "GET",
    path: `/v1/evidence-access-grants/${id("grt")}`,
    run: (c) => c.evidence.getGrant(id("grt")),
  },
  {
    name: "issue grant",
    method: "POST",
    path: `/v1/evidence/${id("evd")}/access-grants`,
    body: grant,
    keyed: true,
    run: (c) => c.evidence.createGrant(id("evd"), grant, mutation),
  },
  {
    name: "revoke grant",
    method: "POST",
    path: `/v1/evidence-access-grants/${id("grt")}/revoke`,
    body: { reason: "withdraw" },
    keyed: true,
    run: (c) => c.evidence.revokeGrant(id("grt"), "withdraw", mutation),
  },
  {
    name: "consent",
    method: "GET",
    path: `/v1/consent-receipts/${id("ack")}`,
    run: (c) => c.consents.get(id("ack")),
  },
  {
    name: "withdraw consent",
    method: "POST",
    path: `/v1/consent-receipts/${id("ack")}/revoke`,
    body: { reason: "withdraw" },
    keyed: true,
    run: (c) => c.consents.revoke(id("ack"), "withdraw", mutation),
  },
  {
    name: "history",
    method: "GET",
    path: `/v1/verifications/${id("ver")}/decisions?limit=3&before=${id("dec")}`,
    run: (c) => c.decisions.history(id("ver"), { limit: 3, before: id("dec") }),
  },
  {
    name: "reconsider",
    method: "POST",
    path: `/v1/verifications/${id("ver")}/reconsiderations`,
    body: { decision_id: id("dec") },
    keyed: true,
    run: (c) => c.decisions.reconsider(id("ver"), id("dec"), mutation),
  },
  {
    name: "create impact",
    method: "POST",
    path: "/v1/proposal-impact-assessments",
    body: impact,
    run: (c) => c.proposals.createImpactAssessment(impact),
  },
  {
    name: "get impact",
    method: "GET",
    path: "/v1/proposal-impact-assessments/imp_123",
    run: (c) => c.proposals.getImpactAssessment("imp_123"),
  },
  {
    name: "list impacts",
    method: "GET",
    path: "/v1/proposal-impact-assessments?limit=2&before=2026-09-21T12%3A00%3A00Z",
    run: (c) => c.proposals.listImpactAssessments({ limit: 2, before: "2026-09-21T12:00:00Z" }),
  },
  {
    name: "create request",
    method: "POST",
    path: "/v1/privacy-requests",
    body: { type: "access", region: "ng-1", subject_id: id("sub") },
    run: (c) => c.privacy.createRequest({ type: "access", region: "ng-1", subject_id: id("sub") }),
  },
  {
    name: "get request",
    method: "GET",
    path: `/v1/privacy-requests/${id("prq")}`,
    run: (c) => c.privacy.getRequest(id("prq")),
  },
  {
    name: "list requests",
    method: "GET",
    path: "/v1/privacy-requests?limit=2&cursor=opaque%2B%2F%3D&state=approved&type=access&subject_id=subject+%26+other",
    run: (c) =>
      c.privacy.listRequests({
        limit: 2,
        cursor: "opaque+/=",
        state: "approved",
        type: "access",
        subjectId: "subject & other",
      }),
  },
  {
    name: "approve",
    method: "POST",
    path: `/v1/privacy-requests/${id("prq")}/approve`,
    body: { expected_version: 2, reason_code: "access_approved" },
    run: (c) =>
      c.privacy.approveRequest(id("prq"), { expected_version: 2, reason_code: "access_approved" }),
  },
  {
    name: "deny",
    method: "POST",
    path: `/v1/privacy-requests/${id("prq")}/deny`,
    body: { expected_version: 2, reason_code: "scope_denied" },
    run: (c) =>
      c.privacy.denyRequest(id("prq"), { expected_version: 2, reason_code: "scope_denied" }),
  },
  {
    name: "withdraw",
    method: "POST",
    path: `/v1/privacy-requests/${id("prq")}/withdraw`,
    body: { expected_version: 2 },
    run: (c) => c.privacy.withdrawRequest(id("prq"), 2),
  },
  {
    name: "execute",
    method: "POST",
    path: `/v1/privacy-requests/${id("prq")}/execute`,
    body: { expected_version: 3 },
    run: (c) => c.privacy.executeRequest(id("prq"), 3),
  },
  {
    name: "restrictions",
    method: "GET",
    path: `/v1/privacy-restrictions?subject_id=${id("sub")}`,
    run: (c) => c.privacy.listRestrictions({ subjectId: id("sub") }),
  },
  {
    name: "lift",
    method: "POST",
    path: `/v1/privacy-restrictions/${id("prs")}/lift`,
    body: { expected_version: 1, reason_code: "lifted_by_tenant" },
    run: (c) =>
      c.privacy.liftRestriction(id("prs"), {
        expected_version: 1,
        reason_code: "lifted_by_tenant",
      }),
  },
  {
    name: "disclose",
    method: "POST",
    path: "/v1/privacy-disclosures",
    body: disclosure,
    run: (c) => c.privacy.createDisclosure(disclosure),
  },
  {
    name: "disclosures",
    method: "GET",
    path: `/v1/privacy-disclosures?request_id=${id("prq")}`,
    run: (c) => c.privacy.listDisclosures({ requestId: id("prq") }),
  },
  {
    name: "create processor",
    method: "POST",
    path: "/v1/privacy-processors",
    body: processor,
    run: (c) => c.privacy.createProcessor(processor),
  },
  {
    name: "get processor",
    method: "GET",
    path: `/v1/privacy-processors/${id("prc")}`,
    run: (c) => c.privacy.getProcessor(id("prc")),
  },
  {
    name: "update processor",
    method: "PUT",
    path: `/v1/privacy-processors/${id("prc")}`,
    body: { ...processor, expected_version: 1 },
    run: (c) => c.privacy.updateProcessor(id("prc"), { ...processor, expected_version: 1 }),
  },
  {
    name: "processors",
    method: "GET",
    path: "/v1/privacy-processors?limit=1&cursor=next",
    run: (c) => c.privacy.listProcessors({ limit: 1, cursor: "next" }),
  },
  {
    name: "deletions",
    method: "GET",
    path: `/v1/deletions?aggregate_id=${id("ver")}`,
    run: (c) => c.privacy.listDeletions({ aggregateId: id("ver") }),
  },
  {
    name: "deletion status",
    method: "GET",
    path: `/v1/deletions/${id("del")}`,
    run: (c) => c.privacy.getDeletion(id("del")),
  },
  {
    name: "retention",
    method: "GET",
    path: "/v1/retention/resolutions?aggregate_id=subject+%26+other",
    run: (c) => c.privacy.retention("subject & other"),
  },
];

describe("public administration SDK", () => {
  it.each(operations)("$name preserves the public HTTP contract", async (operation) => {
    const fetchMock = vi.fn<typeof fetch>(async (url, init) => {
      expect(String(url)).toBe("https://core.example.test/mount" + operation.path);
      expect(init?.method).toBe(operation.method);
      const headers = new Headers(init?.headers);
      expect(headers.get("Authorization")).toBe("Bearer tenant-secret");
      expect(headers.get("Idempotency-Key")).toBe(
        operation.keyed ? JSON.stringify(mutation.idempotencyKey) : null,
      );
      expect(init?.body === undefined ? undefined : JSON.parse(String(init.body))).toEqual(
        operation.body,
      );
      return response({ data: [], next_before: id("dec") });
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test/mount",
      apiKey: "tenant-secret",
      fetch: fetchMock,
    });
    const result = await operation.run(client);
    expect(result).toMatchObject({ requestId: "req_sdk", etag: '"v2"', location: "/v1/created" });
    if (operation.name === "history")
      expect(result).toMatchObject({ data: { data: [], nextBefore: id("dec") } });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("rejects invalid identifiers, limits, retry keys and versions before sending", () => {
    const fetchMock = vi.fn<typeof fetch>();
    const c = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "tenant",
      fetch: fetchMock,
    });
    expect(() => c.evidence.get("../other")).toThrow();
    expect(() => c.evidence.get(id("grt"))).toThrow();
    expect(() => c.privacy.listRequests({ limit: 101 })).toThrow();
    expect(() => c.privacy.executeRequest(id("prq"), 0)).toThrow();
    expect(() => c.consents.revoke(id("ack"), "reason", { idempotencyKey: "bad\nkey" })).toThrow();
    expect(() => c.privacy.retention("")).toThrow();
    expect(fetchMock).not.toHaveBeenCalled();
  });

  it("preserves conflict details and sends an abort signal without retries", async () => {
    const controller = new AbortController();
    const fetchMock = vi.fn<typeof fetch>(async (_url, init) => {
      expect(init?.signal).toBe(controller.signal);
      return new Response(
        JSON.stringify({
          type: "https://idenqa.dev/problems/conflict",
          title: "Conflict",
          status: 409,
          detail: "Version changed",
          code: "conflict",
          request_id: "req_conflict",
        }),
        {
          status: 409,
          headers: { "Content-Type": "application/problem+json", "X-Request-ID": "req_conflict" },
        },
      );
    });
    const c = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "tenant",
      fetch: fetchMock,
    });
    const result = c.privacy.executeRequest(id("prq"), 2, { signal: controller.signal });
    await expect(result).rejects.toBeInstanceOf(IdenqaAPIError);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    controller.abort();
    fetchMock.mockRejectedValue(new DOMException("Aborted", "AbortError"));
    await expect(c.evidence.get(id("evd"), { signal: controller.signal })).rejects.toBeInstanceOf(
      IdenqaTransportError,
    );
  });

  it("keeps tenant administration off subject clients", () => {
    expect(CaptureClient.prototype).not.toHaveProperty("privacy");
    expect(OutcomeClient.prototype).not.toHaveProperty("evidence");
    expect(CaptureClient.prototype).not.toHaveProperty("revokeGrant");
  });
});

function response(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: {
      "Content-Type": "application/json",
      "X-Request-ID": "req_sdk",
      ETag: '"v2"',
      Location: "/v1/created",
    },
  });
}
