# Provider runtime

**Status:** Implemented local-fixture path; external sandbox acceptance pending.

The first runtime route executes Dojah document analysis using a completed public capture, encrypted evidence, a separately authenticated runner, a persisted normalized result, policy evaluation and the existing signed completion webhook. It supports one explicit tenant, policy, immutable capture-profile digest and document-front requirement per configured worker/API deployment. It does not select Dojah as a universal primary provider.

## Runtime ownership

- `internal/provider` owns route selection, reference-only request digests and dispatch recovery; its PostgreSQL adapter owns immutable request snapshots and dispatch receipts.
- The planning transaction creates the check, attempt, single-use evidence grant, request snapshot, progress records and Headgate task together. Queue failure rolls them all back.
- `cmd/adapter-runner` composes the configured Dojah or Smile ID adapter with mounted credentials, authenticated gRPC and destination-bound HTTPS clients. It has no PostgreSQL, object-store or evidence-key access.
- The API exposes private `POST /internal/v1/provider-evidence` only when configured. Its body contains exactly `attempt_id`, `grant_id` and `redemption_id`. A separate workload bearer credential identifies the configured tenant runner. The API matches the persisted dispatch and grant, locks and rechecks current session authority, authenticates encrypted content and commits the grant-use audit before releasing a bounded plaintext response. An exact successful redemption replay does not release bytes again.
- The worker loads the exact request and credential version saved with the attempt. Changing deployment settings cannot rewrite an in-flight request. Discovery filters the configured route before applying its batch limit. Synthetic execution requires its own explicit opt-in and rejects real-provider provenance.

## Adapter identifiers

Runtime JSON uses the exact built-in adapter identifiers `dojah` and `smileid`. Go code must use `providerv1.AdapterDojah` and `providerv1.AdapterSmileID` from `contracts/provider/v1` for comparisons, manifest provenance and composition rather than repeating those string literals. The constants do not close the provider contract: a conforming third-party adapter may publish another validated identifier. Omitting `adapter` retains the documented Dojah compatibility path; it does not make Dojah a universal primary provider.

## Operator configuration

Apply Core migration 36 and the existing Headgate migrations. In addition to existing runtime grants, grant the runtime role `SELECT, INSERT` on `idenqa.provider_requests`, `SELECT, INSERT, UPDATE` on `idenqa.provider_dispatches`, and `EXECUTE` on `idenqa.list_ready_provider_captures(timestamptz, integer, text, text, text)`. Both tables force tenant RLS. The discovery function is security-invoker and uses the same tenant scope.

Set `IDENQA_PROVIDER_RUNTIME_FILE` on the API and worker to a mounted JSON file with this shape. Replace the descriptive identifiers with the actual tenant, active policy, published profile digest and deployment-assigned configuration references:

```json
{
  "binding": {
    "tenant_id": "ten_<actual-id>",
    "policy_id": "pol_<actual-id>",
    "profile_digest": "sha256:<published-profile-digest>",
    "requirement": "document",
    "region": "tenant.region.ng",
    "purpose": "idenqa.purpose.identity_verification",
    "recipient": "tenant.recipient.primary",
    "configuration": {
      "provider_id": "pvd_<actual-id>",
      "schema_digest": "sha256:1a43389f9f297b42dc8507a353ba5d98f7c7d150527d67c13af117f1e478bae5",
      "secret_reference": "secret://provider/dojah/tenant",
      "credential_version": "v1"
    }
  },
  "runner_address": "adapter-runner:9443",
  "runner_ca_file": "/run/idenqa/runner-ca.pem",
  "runner_server_name": "adapter-runner",
  "runner_credential_file": "/run/idenqa/worker-to-runner.key",
  "gateway_credential_file": "/run/idenqa/runner-to-gateway.key"
}
```

The capture profile must require one document-front image and the authority must permit its purpose, recipient and immutable session region. These settings must describe the actual permitted recipient; the example classification is not provider approval. The policy consumes `idenqa.signal.document_quality`. Passing this document signal alone does not establish subject identity, freshness, liveness or an authority-register match. The automated fixture policy deliberately labels its result `synthetic.fixture`.

The runner configuration contains the same `tenant_id` and `configuration` object plus:

```json
{
  "listen_address": "0.0.0.0:9443",
  "certificate_file": "/run/idenqa/runner.pem",
  "private_key_file": "/run/idenqa/runner-key.pem",
  "credential_file": "/run/idenqa/worker-to-runner.key",
  "tenant_id": "ten_<actual-id>",
  "configuration": {
    "provider_id": "pvd_<actual-id>",
    "schema_digest": "sha256:1a43389f9f297b42dc8507a353ba5d98f7c7d150527d67c13af117f1e478bae5",
    "secret_reference": "secret://provider/dojah/tenant",
    "credential_version": "v1"
  },
  "base_url": "https://sandbox.dojah.io",
  "provider_ca_file": "",
  "app_id_file": "/run/idenqa/dojah-app-id",
  "api_key_file": "/run/idenqa/dojah-secret-key",
  "gateway_url": "https://evidence-gateway",
  "gateway_ca_file": "/run/idenqa/gateway-ca.pem",
  "gateway_credential_file": "/run/idenqa/runner-to-gateway.key",
  "fixture": false
}
```

Run `adapter-runner --config /run/idenqa/runner.json`, then the API and worker with matching configuration. Keep `IDENQA_SYNTHETIC_PROCESSING` disabled. The API's private endpoint requires a TLS reverse proxy; do not expose that path through the public ingress. The worker verifies the runner manifest and configuration at startup. Grant the runner only the mounted files it needs, use a dedicated unprivileged OS identity, and apply deployment memory, CPU, process and network limits. Process separation and application transport checks do not establish container or kernel sandbox acceptance.

All credential files must be owner-only regular files, nonempty and at most 4096 bytes. Generate two independent 32-byte cryptographically random workload secrets, encode each as unpadded Base64URL and prefix each with `idq_wrk_v1_`. They serve different directions and must not be tenant API keys. Keep provider AppId and secret key in their respective files. Configuration contains paths and opaque references, never inline secrets. Reload and rotation require a coordinated restart in this slice.

For the Dojah route, outside tests the runner accepts only `https://sandbox.dojah.io`; production and arbitrary provider origins are rejected. The explicit fixture mode accepts only literal loopback HTTPS with a configured trust root. Provider HTTPS disables proxies, rejects redirects and checks and pins resolved addresses. The private gateway has a separately configured origin and mandatory trust root.

## Dojah recovery and shared limits

A dispatch receipt permits one initial call per persisted attempt. A saved result is reusable without another HTTP call. A concurrent pending receipt returns a retryable error, never a competing terminal result. A lost or invalid RPC reply becomes one persisted reconciliation-required failure when persistence succeeds. If the process crashes or cannot persist that outcome, the receipt remains pending; a new worker does not repeat the external call. Existing deadline and reconciliation handling can stop the attempt, but this slice has no provider-side query that proves whether an ambiguous call ran. An operator must not delete receipts or manufacture a fresh attempt as a retry shortcut.

A failed grant response after the grant-use transaction commits is similarly ambiguous. Plaintext is held only in bounded process memory, not a durable delivery spool, so the grant cannot be transparently replayed. This intentionally favors preventing unauthorized rereads and duplicate submissions over transparent recovery. Cancellation prevents subsequent evidence release and consequential result commits; it cannot recall bytes already delivered to the provider.

The provider response is bounded and only the explicit `entity.status.overall_status` values 1 and 0 map to satisfied and not-satisfied. Missing, unknown or incorrectly typed status is inconclusive. Extracted identity fields and images are discarded. The current Dojah catalogue is version 0.1.2; version 0.1.1 introduced the corrected mapping and request body.

**Unresolved:** official sandbox account proof, external success reconciliation, dynamic secrets, broader route administration, provider budgets and fleet concurrency, provider-side deletion, production regional/recipient approval, hardened deployment evidence. Request and dispatch metadata are workflow records; their bounded retention and purge implementation must be completed before production activation. No new retention duration is selected here.

## Verification and source evidence

**Explicit document sides — 22 September 2026:** Dojah adapter 0.1.2 accepts
exactly one granted `document.front` and optionally one `document.back`, sending
the documented `imagefrontside` and `imagebackside` fields in a single base64
request. Duplicate sides, unrelated variants and missing fronts are rejected
before evidence redemption. A failed side read or cancellation prevents a partial
submission. Granted byte buffers are wiped on success and failure. Returned
extraction remains one transient primary-document observation; this does not
establish front/back correspondence or new assurance.

Worker preparation now resolves Dojah sides from the session's immutable profile
and durable document selection. It prepares a back grant only when the effective
document branch requires back; a front-only selection never inherits the profile's
union of available sides. The requirement's purpose and profile digest must match
the route, and each required side must resolve to exactly one accepted, available,
integrity-verified asset under the tenant, verification and region. Both grants
retain the existing recipient, purpose, runner/version and one-use bounds. Missing
or ambiguous required evidence aborts the planning transaction.

Restricted-role PostgreSQL 16.8 race tests prove selected front-only/two-sided grant
counts and exact artefact/recipient/runner/use bindings, unselected-branch rejection,
missing-back rollback and downstream rollback. The composed two-sided public journey
also passes: distinct front/back bytes traverse encrypted upload, exact one-use
grants, the scoped runner and synthetic TLS provider, then decision and signed
webhook retry across worker replacement. Both grants reject completed replay;
raw extraction values remain absent from persisted results and events. The runner's
former one-image-only guard now admits one or two images, leaving exact side
validation to the pinned adapter. Official-account acceptance remains open.
Existing prepared requests pinned to 0.1.1 must
retain their matching runner or finish before replacing it; 0.1.2 rejects old
manifest pins rather than silently changing their meaning.

The 0.1.2 source pin hashes UTF-8 `adapter.go` then `document.go`, each prefixed
by its filename and a newline, omitting the `packageDigest` declaration line
to avoid self-reference. It is a source pin, not release-image provenance.

**Result-time authorization — 22 September 2026:** A guarded check commit with a
result receipt validates authority at that receipt's time and at the current
observation time, not at a newly scheduled successor's future start time.
A PostgreSQL regression reproduces the former rejection, proves the corrected
commit and immutable replay, and still rejects future-dated result receipts.
Execution must recheck authority when the successor runs. This fix does not
authorize resubmission of ambiguous external operations or expand grant uses.
Focused tests cover front-only compatibility, both side orders, invalid grants,
read failures, cancellation and byte-buffer cleanup.

**Dojah extraction hardening — 22 September 2026:** conflicting valid duplicate
values for a mapped document field now suppress the transient document observation;
response order cannot choose a document number, sex, birth date or expiry date.
Identical trimmed duplicates are deduplicated and malformed individual values are
still ignored. The provider's explicit quality signal is preserved; no derived
document signals are produced from a suppressed observation. Response buffers are
also wiped on oversized or partial-read rejection, not only on successful reads.
Focused tests cover every mapped text-field conflict in both orders, repeated
conflicts, partial/identical values, bounded failure diagnostics and consumption
through Core's raw-field removal boundary. These tests do not establish official
account acceptance, full OCR coverage or persistent structured identity ingestion.

`TestDojahRuntimePublicCaptureThroughDecisionAndWebhook` starts from the public capture and authority APIs and proves encrypted storage, atomic planning rollback, exact plaintext delivery through TLS, swapped-grant denial, normalized completion, immutable receipts and signed webhook retry across worker restart. Unit tests cover simultaneous duplicate dispatch, stable ambiguity, document rejection and unknown status, and private-origin/redirect rejection. These are controlled local fixtures, not official-account evidence or Capture Web product acceptance.

The request body and explicit document status mapping were checked against [Dojah's document-analysis reference](https://docs.dojah.io/api-reference/document-analysis/document-analysis). The sandbox host restriction follows [Dojah's environment reference](https://docs.dojah.io/api-reference/get-started/environments), reviewed 7 September 2026. External sandbox and production verification remain subject to X-03's acceptance evidence.

## Smile ID asynchronous document route

PR-02 adds one explicit Smile ID document-biometric route (adapter 0.1.1, job type 6). Set `adapter` to `smileid` in both the API/worker and runner configuration. Omitted `adapter` retains Dojah compatibility. Use the Smile ID manifest's configuration digest `sha256:6c970ac324797952f7d25b4dd847db9b9b65a86a7fdc9c42e85e36bd72a480a1` and matching tenant-owned configuration references. The profile requires document front and selfie; both must be accepted JPEG evidence under the declared authority. Add these fields to the API/worker binding:

```json
{
  "selfie_requirement": "selfie",
  "inputs": [
    {"name": "idenqa.input.country", "reference": "secret://input/country"},
    {"name": "idenqa.input.id_type", "reference": "secret://input/id-type"}
  ]
}
```

The runner uses these additional settings in place of the Dojah AppId file and origin:

```json
{
  "adapter": "smileid",
  "base_url": "https://testapi.smileidentity.com",
  "partner_id_file": "/run/idenqa/smile-partner-id",
  "api_key_file": "/run/idenqa/smile-api-key",
  "inputs_file": "/run/idenqa/smile-inputs.json",
  "callback_url": "https://tenant.example/smile-callback",
  "upload_origin": "https://smile-uploads-test.s3.us-west-2.amazonaws.com",
  "upload_ca_file": ""
}
```

The `callback_url` value is the base to which the adapter appends the per-attempt opaque callback reference; point it at the Core callback ingress (or another approved receiver) so providers return results through `POST /v1/provider-callbacks/{reference}`. Authenticated callback intake is implemented through the isolated runner, which verifies the provider signature before Core stores a replay-identity receipt; status-only polling remains the fallback when callbacks are unavailable or rejected. The mounted input file binds the same opaque references to non-personal country and document-type codes:

```json
[
  {"name": "idenqa.input.country", "reference": "secret://input/country", "value": "NG"},
  {"name": "idenqa.input.id_type", "reference": "secret://input/id-type", "value": "PASSPORT"}
]
```

The initial country allow-list is NG, GH, KE and ZA. Actual document-type support and entitlements still require official-account confirmation. Reference/name mismatches are rejected. This deployment-bound resolver does not provide a general subject-input store. Values remain inside the runner; Core stores references only.

Apply migration 37 and grant the runtime role `SELECT, INSERT, UPDATE` on `idenqa.provider_async_operations`. This coordination table forces tenant RLS. It stores one immutable provider job reference, monotonic polling fence, bounded lease and next-poll time per attempt. No image, upload URL, credential or provider-native result is persisted there.

The optional additive `ProviderRunnerService.Advance` RPC preserves provider contract v1.1 terminal-result semantics: an absent result means pending. Each RPC is bounded to 30 seconds while retaining the original operation deadline. The first transaction claims submission and a 45-second operation lease together. Subsequent claims recheck authority and the current running attempt, and can only query `/v1/job_status` using the persisted partner job ID (attempt ID) and user ID (verification ID). The returned Smile ID job reference cannot be substituted. Stale fences cannot commit progress. Poll scheduling uses a five-second minimum spacing and a dedicated Headgate task with 100 attempts and ten-second backoff with 20% jitter. Its five-minute completion allowance after the operation deadline exists only to recover a saved result or commit an operational timeout; it does not extend external processing authority.

A lost submission or upload acknowledgement, job-not-found response, or missing upload never authorizes another preparation, upload or evidence redemption. Recovery remains status-only and may end as `provider_job_unresolved` with reconciliation required. Timeout contributes no negative identity evidence. The immutable dispatch receipt preserves a terminal result already received before timeout. No automated resubmission or fallback is enabled.

Uploads require the reviewed sandbox S3 origin and a path bound to the configured partner and returned Smile ID job. HTTPS clients reject redirects, disable proxies and pin validated DNS addresses; fixture origins must be literal loopback. Status responses require a valid fresh timestamp signature and matching job, user and supplied product identifiers. Smile's timestamp signature is not a signature over the entire response payload; TLS and explicit identity checks remain required.

Normalized document and one-to-one face signals can feed policy. Liveness is always inconclusive for this two-image route, even when a fixture returns a passing selfie or liveness action. A static selfie cannot establish active liveness. The test policy remains `synthetic.fixture`, and sandbox document simulation is not authenticity acceptance evidence.

`TestSmileRuntimePublicCaptureThroughRestartDecisionAndWebhook` proves two encrypted public uploads, atomic planning rollback, private grant access, one submission and ZIP upload, persisted pending state, status-only worker restart, polling fencing, job-reference immutability, normalized policy completion and signed-webhook delivery. Adapter tests reject cross-job, cross-user, wrong-product, stale-signature and malformed status responses; task tests exercise operational timeout. These are local fixtures, not official sandbox or Capture Web acceptance.

The wire contract was reviewed against Smile ID's [document verification reference](https://docs.usesmileid.com/products/for-individuals-kyc/document-verification/document-verification) and [job-status reference](https://docs.usesmileid.com/further-reading/job-status), 7 September 2026. External account, recipient/region, provider retention/deletion and hardened deployment evidence remain X-03 gates.
