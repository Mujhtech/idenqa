import { expect, it } from "vitest";
import { IdenqaClient, type PolicyDefinition } from "../src/index.js";
const definition: PolicyDefinition = {
  schema_major: 1,
  schema_minor: 0,
  verified_assurance: "synthetic.fixture",
  rules: [
    {
      name: "test",
      when: 'facts["synthetic.document"] == "satisfied"',
      result: {
        state: "satisfied",
        directive: "complete_verified",
        priority: 1,
        contributing_facts: ["synthetic.document"],
        reason_codes: [],
      },
    },
  ],
};
const policy = {
  id: "pol_fixture",
  latest_revision: 2,
  active_revision: 1,
  activation_version: 3,
  created_at: "2026-09-06T00:00:00Z",
  activated_at: "2026-09-06T00:00:03Z",
};
const revision = {
  policy_id: policy.id,
  revision: 1,
  schema_major: 1,
  schema_minor: 0,
  digest: "a".repeat(64),
  evaluator_major: 1,
  evaluator_minor: 0,
  evaluator_digest: "b".repeat(64),
  created_at: policy.created_at,
};
const activation = {
  policy_id: policy.id,
  revision: 1,
  previous_revision: 2,
  version: 3,
  actor_id: "key_fixture",
  activated_at: policy.activated_at,
};
it("maps all policy administration operations while preserving portable policy source", async () => {
  const requests: { url: URL; init: RequestInit }[] = [];
  const responses: unknown[] = [
    { policy, revision, replayed: false },
    {
      valid: true,
      rule_count: 1,
      evaluator_major: 1,
      evaluator_minor: 0,
      evaluator_digest: revision.evaluator_digest,
    },
    policy,
    { policy, revision, replayed: true },
    { ...revision, document: { ...definition, policy_id: policy.id, revision: 1 } },
    { data: [policy], page: { has_more: true, next_cursor: "next" } },
    { data: [revision], page: { has_more: false } },
    { data: [activation], page: { has_more: false } },
    { policy, activation, replayed: false },
    { policy, activation, replayed: true },
  ];
  const client = new IdenqaClient({
    baseUrl: "https://core.example.com",
    apiKey: "backend",
    fetch: async (url, init) => {
      requests.push({ url: new URL(String(url)), init: init! });
      return new Response(JSON.stringify(responses.shift()), {
        status: 200,
        headers: { "Content-Type": "application/json", "X-Request-ID": "req_fixture" },
      });
    },
  });
  expect(
    (await client.policies.create(definition, { idempotencyKey: "create" })).data.policy
      .activationVersion,
  ).toBe(3);
  expect((await client.policies.validate(definition)).data.ruleCount).toBe(1);
  expect((await client.policies.get(policy.id)).data.activeRevision).toBe(1);
  expect(
    (await client.policies.createRevision(policy.id, definition, 1, { idempotencyKey: "revision" }))
      .data.replayed,
  ).toBe(true);
  expect((await client.policies.getRevision(policy.id, 1)).data.document.rules).toEqual(
    definition.rules,
  );
  expect(
    (await client.policies.list({ limit: 1, cursor: "opaque+cursor" })).data.page.nextCursor,
  ).toBe("next");
  expect((await client.policies.listRevisions(policy.id)).data.data[0]!.evaluatorDigest).toBe(
    revision.evaluator_digest,
  );
  expect((await client.policies.listActivations(policy.id)).data.data[0]!.previousRevision).toBe(2);
  await client.policies.activate(
    policy.id,
    { revision: 1, expectedVersion: 0, reason: "tenant_requested" },
    { idempotencyKey: "activate" },
  );
  expect(
    (
      await client.policies.rollback(
        policy.id,
        { revision: 1, expectedVersion: 2, reason: "tenant_requested" },
        { idempotencyKey: "rollback" },
      )
    ).data.replayed,
  ).toBe(true);
  expect(requests.map((r) => r.url.pathname)).toEqual([
    "/v1/policies",
    "/v1/policies/validate",
    "/v1/policies/pol_fixture",
    "/v1/policies/pol_fixture/revisions",
    "/v1/policies/pol_fixture/revisions/1",
    "/v1/policies",
    "/v1/policies/pol_fixture/revisions",
    "/v1/policies/pol_fixture/activations",
    "/v1/policies/pol_fixture/activate",
    "/v1/policies/pol_fixture/rollback",
  ]);
  expect(requests[3]!.init.body).toBe(JSON.stringify({ definition, expected_revision: 1 }));
  expect(JSON.parse(String(requests[8]!.init.body)).expected_version).toBe(0);
  expect(new Headers(requests[1]!.init.headers).has("Idempotency-Key")).toBe(false);
  expect(new Headers(requests[8]!.init.headers).get("Idempotency-Key")).toBe('"activate"');
  expect(requests[5]!.url.searchParams.get("cursor")).toBe("opaque+cursor");
  await expect(
    client.policies.activate(
      policy.id,
      { revision: 1, expectedVersion: -1, reason: "tenant_requested" },
      { idempotencyKey: "bad" },
    ),
  ).rejects.toThrow("nonnegative");
  expect(requests).toHaveLength(10);
});
