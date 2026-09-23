# Proposal model runtime v0.1

Set `IDENQA_PROPOSAL_RUNTIME_FILE` on the API to an owner-mounted JSON file. When it is unset, the deterministic reference model remains active. Enabling a runtime file does not enable tenant workflows: the pinned proposal mode must still permit generation, and the existing deterministic guardrails remain authoritative.

```json
{
  "models": [
    {
      "model_id": "ai.review",
      "model_version": "gpt-4o-2024-08-06",
      "prompt_version": "review-p1",
      "instructions": "Return only bounded review actions.",
      "model_registry_id": "mdl_01ARZ3NDEKTSV4RRFFQ69G5FAV",
      "model_registry_version": 1,
      "model_digest": "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "prompt_registry_id": "prm_01ARZ3NDEKTSV4RRFFQ69G5FAV",
      "prompt_registry_version": 1,
      "prompt_digest": "fa59d1615ec3bb19f0b649680c8f65ef00dbb24b9b41926cee37066adbd50ca2",
      "adapter": "openai_compatible",
      "origin": "https://api.openai.com",
      "credential_reference": "secret://file/run/secrets/openai-api-key",
      "max_output_tokens": 2048,
      "max_response_bytes": 131072,
      "input_micros_per_million": 2500000,
      "output_micros_per_million": 10000000
    },
    {
      "model_id": "ai.policy",
      "model_version": "claude-sonnet-snapshot",
      "prompt_version": "policy-p1",
      "instructions": "Return only bounded policy actions.",
      "model_registry_id": "mdl_01ARZ3NDEKTSV4RRFFQ69G5FAW",
      "model_registry_version": 1,
      "model_digest": "1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
      "prompt_registry_id": "prm_01ARZ3NDEKTSV4RRFFQ69G5FAW",
      "prompt_registry_version": 1,
      "prompt_digest": "5552dc249a7c5f83fa9128a0939d5854c078745f80d448a83f75c7d51e4cdad2",
      "adapter": "anthropic",
      "origin": "https://api.anthropic.com",
      "credential_reference": "secret://aws/prod/anthropic-api-key?version=reviewed-version",
      "anthropic_version": "2023-06-01",
      "max_output_tokens": 4096,
      "max_response_bytes": 131072,
      "input_micros_per_million": 3000000,
      "output_micros_per_million": 15000000
    }
  ]
}
```

`model_id` is Idenqa's provider-neutral logical route identifier. `model_version` is the exact upstream model string, and `prompt_version` identifies the reviewed instruction version. Routing matches all three values exactly; it never falls back to a different route. `openai_compatible` uses the official OpenAI Go SDK, targets `/v1/chat/completions`, and is shared by OpenAI and endpoints that implement the same strict JSON-schema response-format contract. `anthropic` uses Anthropic's official Go SDK, targets `/v1/messages`, and forces one schema-bound tool result. The SDKs remain private adapter dependencies; their types and errors do not enter Core contracts. A compatible endpoint that does not implement those semantics needs its own adapter instead of weakening the shared contract.

Every mounted route must bind exact tenant-owned prompt and model registry IDs and versions. Before an outbound request, Core loads both records under tenant RLS and requires the logical model ID, model digest, prompt model ID, prompt digest and mounted instruction digest to match. It also requires the workflow's public activation to be `active` at the exact revision pinned by the versioned mode configuration. A pre-binding model record whose `logical_model_id` is absent, a retired route, a stale activation revision, or an incomplete mode pin cannot authorize generation, and there is no runtime fallback.

Tenant administrators register immutable model and prompt records, publish a CAS-protected workflow activation, and pin that activation plus both registry revisions in the mode configuration through the public API, TypeScript SDK, or `idenqa proposal` CLI. Activation, retirement, and rollback each append an immutable actor-, reason-, and time-attributed history revision; rollback republishes a prior active route as a new revision instead of mutating history.

Every provider invocation stores a content-free receipt keyed by tenant and proposal. It records the exact logical/upstream/prompt versions, provider request ID when available, outcome (`succeeded`, `failed`, `rejected`, or `invalid_output`), whether the provider reported usage, token counts, locally estimated micro-cost, stable error class, and timestamp. Rates are pinned per route in micros per million tokens; they are operator-maintained estimates rather than provider invoices. If receipt persistence fails, the proposal is not returned. `GET /v1/proposal-usage` and the matching SDK/CLI operation aggregate attempts, outcomes, unreported usage, tokens, and estimated cost over a bounded interval. Upstream failures may omit trustworthy token usage; those attempts remain visible with `usage_reported=false`, but their unknown provider spend cannot be inferred or represented as billed cost.

Origins must be HTTPS and contain no path, query, fragment, or user information. The runtime resolves credential references through the configured `IDENQA_SECRETS_PROVIDER`; credential values must not appear in this file. `ca_file` may select an operator-mounted trust root. Requests and responses are bounded, redirects are denied, public DNS answers are pinned and screened, and adapters set SDK retry counts to zero. Adapter composition supplies exact base URLs and credentials and disables ambient SDK credential/base-URL discovery. Production rejects fixture transport.

Provider responses are untrusted. Core requires the exact requested model version and action set, rejects malformed or duplicate JSON, accepts only the closed argument shape for each action kind, and constructs tenant, anchor, mode, identifier, expiry and provenance fields from the validated local request. Raw evidence must never be placed in proposal context or instructions.
