import { describe, expect, it, vi } from "vitest";

import { IdenqaClient } from "../src/index.js";

const modelRegistryID = "mdl_01M11HEQG00000000000000000";
const promptRegistryID = "prm_01M11HEQG00000000000000000";

describe("ProposalsClient lifecycle", () => {
  it("sends exact activation pins and reads bounded usage reports", async () => {
    const fetchMock = vi.fn<typeof fetch>(async (input, init) => {
      const url = String(input);
      if (url.includes("proposal-activations")) {
        expect(init?.method).toBe("PUT");
        expect(JSON.parse(String(init?.body))).toMatchObject({
          model_registry_id: modelRegistryID,
          model_registry_version: 1,
          prompt_registry_id: promptRegistryID,
          prompt_registry_version: 2,
          expected_revision: 0,
        });
        return jsonResponse({ workflow: "review", revision: 1 }, 200);
      }
      expect(url).toContain(
        "/v1/proposal-usage?from=2026-09-01T00%3A00%3A00Z&to=2026-10-01T00%3A00%3A00Z",
      );
      return jsonResponse({ attempts: 0 }, 200);
    });
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "idq_v1.secret",
      fetch: fetchMock,
    });

    await client.proposals.activate(
      "review",
      {
        model_registry_id: modelRegistryID,
        model_registry_version: 1,
        prompt_registry_id: promptRegistryID,
        prompt_registry_version: 2,
        model_version: "model-2026-09",
        prompt_version: "review-p2",
        expected_revision: 0,
      },
      { idempotencyKey: "activate-review-1" },
    );
    await client.proposals.usage("2026-09-01T00:00:00Z", "2026-10-01T00:00:00Z");
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("rejects incomplete pins and invalid lifecycle revisions locally", () => {
    const client = new IdenqaClient({
      baseUrl: "https://core.example.test",
      apiKey: "idq_v1.secret",
      fetch: vi.fn<typeof fetch>(),
    });

    expect(() =>
      client.proposals.putMode("review", "assist", 0, { idempotencyKey: "mode-review-1" }, [], {
        model_registry_id: modelRegistryID,
        model_registry_version: 0,
        prompt_id: promptRegistryID,
        prompt_registry_version: 1,
        activation_revision: 1,
      }),
    ).toThrow("positive model registry version");
    expect(() =>
      client.proposals.rollback("review", 2, 0, "restore", {
        idempotencyKey: "rollback-review-1",
      }),
    ).toThrow("positive target revision");
    expect(() => client.proposals.usage("2026-10-01T00:00:00Z", "2026-09-01T00:00:00Z")).toThrow(
      "valid usage interval",
    );
  });
});

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json", "X-Request-ID": "req_proposals" },
  });
}
