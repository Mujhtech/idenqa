import { expect, it } from "vitest";
import { IdenqaClient } from "../src/index.js";

const endpoint = {
  id: "whk_01M11HEQG00000000000000000",
  url: "https://receiver.example.com",
  version: 2,
  secret_version: 2,
  previous_valid_until: "2026-09-06T01:00:00Z",
  created_at: "2026-09-06T00:00:00Z",
  updated_at: "2026-09-06T00:00:00Z",
};
const delivery = {
  id: "dlv_01M11HEQG00000000000000000",
  endpoint_id: endpoint.id,
  event_id: "evt_01M11HEQG00000000000000000",
  event_type: "verification.completed",
  state: "pending",
  attempt_count: 0,
  max_attempts: 8,
  next_attempt_at: endpoint.created_at,
  created_at: endpoint.created_at,
  updated_at: endpoint.created_at,
  replay_of: "dlv_original",
};

it("maps all webhook operations, replay receipts and bounded inspection", async () => {
  const requests: { url: URL; init: RequestInit }[] = [];
  const responses: unknown[] = [
    { endpoint, replayed: false, signing_secret: "display-once" },
    { endpoint, replayed: true },
    endpoint,
    { endpoint, replayed: false, signing_secret: "rotated" },
    { endpoint, replayed: false },
    { data: [endpoint], page: { has_more: true, next_cursor: "next+cursor" } },
    { data: [delivery], page: { has_more: false } },
    delivery,
    {
      data: [
        {
          number: 1,
          secret_version: 2,
          status_code: 503,
          error_class: "http_retryable",
          retry_after_ms: 1000,
          response_body: '{"error":"temporarily unavailable"}',
          response_truncated: false,
          completed_at: endpoint.created_at,
        },
      ],
    },
    { delivery, replayed: false },
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
    (await client.webhooks.create({ url: endpoint.url }, { idempotencyKey: "same-key" })).data
      .signingSecret,
  ).toBe("display-once");
  const retry = await client.webhooks.create({ url: endpoint.url }, { idempotencyKey: "same-key" });
  expect(retry.data.replayed).toBe(true);
  expect(retry.data).not.toHaveProperty("signingSecret");
  expect((await client.webhooks.get(endpoint.id)).data.secretVersion).toBe(2);
  expect(
    (
      await client.webhooks.rotate(
        endpoint.id,
        { expectedVersion: 1, overlapSeconds: 3600 },
        { idempotencyKey: "rotate" },
      )
    ).data.signingSecret,
  ).toBe("rotated");
  await client.webhooks.disable(
    endpoint.id,
    { expectedVersion: 2, reason: "tenant_requested" },
    { idempotencyKey: "disable" },
  );
  expect(
    (await client.webhooks.list({ limit: 1, cursor: "cursor+opaque" })).data.page.nextCursor,
  ).toBe("next+cursor");
  expect((await client.webhooks.listDeliveries(endpoint.id)).data.data[0]!.eventId).toBe(
    delivery.event_id,
  );
  expect((await client.webhooks.getDelivery(delivery.id)).data.replayOf).toBe(delivery.replay_of);
  const attempt = (await client.webhooks.listAttempts(delivery.id)).data.data[0]!;
  expect(attempt.retryAfterMs).toBe(1000);
  expect(attempt.responseBody).toBe('{"error":"temporarily unavailable"}');
  expect(attempt.responseTruncated).toBe(false);
  expect(
    (
      await client.webhooks.replay(
        delivery.id,
        { reason: "receiver_recovered" },
        { idempotencyKey: "replay" },
      )
    ).data.delivery.id,
  ).toBe(delivery.id);
  expect(requests.map((r) => r.url.pathname)).toEqual([
    "/v1/webhook-endpoints",
    "/v1/webhook-endpoints",
    `/v1/webhook-endpoints/${endpoint.id}`,
    `/v1/webhook-endpoints/${endpoint.id}/rotate`,
    `/v1/webhook-endpoints/${endpoint.id}/disable`,
    "/v1/webhook-endpoints",
    `/v1/webhook-endpoints/${endpoint.id}/deliveries`,
    `/v1/webhook-deliveries/${delivery.id}`,
    `/v1/webhook-deliveries/${delivery.id}/attempts`,
    `/v1/webhook-deliveries/${delivery.id}/replay`,
  ]);
  expect(requests[5]!.url.searchParams.get("cursor")).toBe("cursor+opaque");
  expect(requests[3]!.init.body).toBe(
    JSON.stringify({ expected_version: 1, overlap_seconds: 3600 }),
  );
  expect(new Headers(requests[0]!.init.headers).get("Idempotency-Key")).toBe('"same-key"');
  expect(
    requests.every((r) => new Headers(r.init.headers).get("Authorization") === "Bearer backend"),
  ).toBe(true);
  await expect(client.webhooks.list({ limit: 101 })).rejects.toThrow("100");
  await expect(
    client.webhooks.rotate(
      endpoint.id,
      { expectedVersion: 0, overlapSeconds: 1 },
      { idempotencyKey: "bad" },
    ),
  ).rejects.toThrow("positive");
  expect(requests).toHaveLength(10);
});
