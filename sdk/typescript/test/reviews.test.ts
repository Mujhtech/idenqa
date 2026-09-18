import { expect, it } from "vitest";

import { IdenqaClient } from "../src/index.js";

it("encodes review mutation idempotency keys as RFC 9651 strings", async () => {
  const requests: RequestInit[] = [];
  const assignment = {
    tenant_id: "ten_01M2HY2RN581BZ9YKCNYAZ6VCB",
    api_key_id: "key_01M2HY2RP2VW0SYJ3M775H9JXB",
    operator_id: "reviewer.local_demo",
    permissions: ["reviews:claim", "reviews:find"],
    certifications: ["identity.review"],
    regions: ["tenant-local"],
    not_before: "2026-09-15T07:03:37Z",
    expires_at: "2026-09-15T08:04:37Z",
  };
  const client = new IdenqaClient({
    baseUrl: "https://core.example.com",
    apiKey: "backend",
    fetch: async (_url, init) => {
      requests.push(init!);
      return new Response(
        JSON.stringify({
          kind: "operator",
          reference: assignment.api_key_id,
          version: 1,
          configuration: { assignment, revoked: false },
        }),
        {
          status: 200,
          headers: { "Content-Type": "application/json", "X-Request-ID": "req_fixture" },
        },
      );
    },
  });

  await client.reviews.putReviewOperator(
    assignment.api_key_id,
    { expected_version: 0, configuration: { assignment, revoked: false } },
    { idempotencyKey: 'review"\\key' },
  );

  expect(new Headers(requests[0]!.headers).get("Idempotency-Key")).toBe('"review\\"\\\\key"');
  await expect(
    client.reviews.putReviewOperator(
      assignment.api_key_id,
      { expected_version: 0, configuration: { assignment, revoked: false } },
      { idempotencyKey: "bad\nkey" },
    ),
  ).rejects.toThrow("printable ASCII");
  expect(requests).toHaveLength(1);
});
