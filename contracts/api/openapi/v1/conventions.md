# Idenqa Core HTTP API v1 conventions

This document explains the reusable rules encoded by `openapi.yaml`. The OpenAPI document is authoritative for wire shapes; this document is authoritative for behaviour that OpenAPI cannot express completely.

## Versioning and compatibility

- The URI major version (`/v1`) is the compatibility boundary.
- Additive endpoints, optional request fields, response fields, and enum values may be introduced within v1 only where clients are required to tolerate them.
- Removing or renaming operations or fields, narrowing accepted input, widening required input, or changing established semantics requires a new URI major version unless preserving the old behaviour would retain a security or correctness defect.
- Public OpenAPI changes must pass Vacuum linting, reproducible generation, and oasdiff against the pull request base contract.
- Generated transport types and clients are committed. They do not become domain models and do not own authentication or authorisation.

The 6 September 2026 alpha decision explicitly permits expanding `VerificationSession.state`
from `collecting` to the ten states in the v0.6 lifecycle. This is a deliberate
compatibility exception: consumers built against the collecting-only alpha must
update their generated clients and exhaustive state handling before processing
activation. It does not make arbitrary response-enum expansion compatible.
The [warning allow-list](lifecycle-alpha-warnings.txt) names the nine added lifecycle
values on the four existing response paths, with exact method, property and
status matching. Other breaking changes still fail the compatibility gate.
Remove this exception when comparison against the collecting-only alpha is no
longer needed. CI compares the base contract through its Git revision so external
schema references resolve against the same revision.

The 9 September 2026 alpha decision also permits exactly the `fraud` value in
`source.kind` for facts returned by `GET /v1/decisions/{decisionID}/bundle`
(status 200). Its exact property path is separately allow-listed. Existing alpha
consumers must update generated types and exhaustive provenance handling before
consuming fraud-enabled bundles. This does not approve any other enum extension
or change the bundle version. Remove this entry when its pre-fraud alpha baseline
is no longer used.

Creation and creation replay still return the original `collecting` session at
version 1. Retrieval returns the current workflow state. `completed` means an
immutable decision exists; its identity outcome is read from the decision
resource by tenant backends or through the minimal subject-safe capture outcome
projection using a still-valid, unrevoked outcome credential. The subject
projection does not expose decision identifiers, policy reasons, assurance,
provider/model details, evidence metadata, or subject data. Cancellation, expiry
and operational failure carry no identity outcome.
Publishing the vocabulary does not activate cancellation, review, callbacks or
processing; each requires its owning runtime integration and acceptance proofs.

## Subject-safe outcome access

`GET /v1/capture/outcome` accepts only the separate `idq_out_v1` outcome bearer.
It must not accept a capture token, and the outcome bearer must not authenticate
session, authority-response, upload, cancellation, connection-ticket, WebSocket,
decision-detail, or tenant operations. The token binds its `otk_` record, tenant,
verification, signing-key version, issuance and expiry; authentication also
rechecks the exact tenant-scoped database record, independent revocation and
expiry. A capture token remains bounded by session expiry. Core, not browser
time, authors the `expired` state.

Verification and recapture creation return display-once capture and outcome
tokens through the trusted tenant bootstrap. Outcome expiry is the session
expiry plus `outcome_token_post_expiry_ttl_seconds`: 24 hours by default, bounded
by a deployment maximum of 168 hours by default and a hard 30-day configuration
cap. Exact creation replay reconstructs the same bearer without storing it. A
pre-D-027 idempotency result has no original outcome credential and therefore
conflicts instead of receiving newly minted authority during replay. Outcome
tokens have no public v1 renewal; recapture capture-token renewal returns the
existing still-live child outcome token.

## JSON and problems

- JSON request objects reject unknown fields unless an operation explicitly documents an extension object.
- Successful single-resource responses contain the resource directly.
- Collections use `{ "data": [...], "page": { ... } }`; `data` is always an array and never `null`.
- Errors use `application/problem+json` with the required Idenqa `code` and server-issued `request_id` extensions to RFC 9457 problem details.
- Internal error text and cross-tenant resource existence are never disclosed.

## Pagination

- Collections use opaque cursor pagination with `cursor` and `limit` query parameters.
- The default limit is 25 and the maximum is 100 unless an operation documents a lower bound.
- Ordering uses an authoritative persisted field followed by the resource identifier as a stable tie-breaker.
- Cursors are integrity protected, tenant- and query-bound, and may expire. Clients must store and replay them unchanged.
- `page.has_more` is required. `page.next_cursor` is present only when another page is available.

## Idempotency

- Consequential `POST` operations require exactly one `Idempotency-Key` encoded as an RFC 9651 String.
- The key is scoped to the authenticated tenant and operation. The server binds it to a canonical request fingerprint.
- Repeating the same key and fingerprint returns the original completed result. A concurrent duplicate is reported as a conflict. Reusing a key with a different fingerprint is `IDEMPOTENCY_CONFLICT`.
- Idempotency records are durable PostgreSQL state. The retention period is operation-specific and must be documented before the operation is published.
- Keys are not secrets, but clients must not embed credentials, subject data, or evidence data in them.
- `POST /v1/capture/connections` is the deliberate exception: replaying a stored response would replay display-once secret material. A retry issues a fresh independently bounded ticket instead of accepting an idempotency key.

## Realtime connection bootstrap

Native clients first redeem their tenant-backend-issued token once through
`POST /v1/capture/native/bootstrap`. The request follows the public native
bootstrap v1 proof contract and binds the token to a configured application
identity and hardware-backed P-256 proof-key digest. Arbitrary identity headers
are never accepted as authentication.

- Browser clients authenticate `POST /v1/capture/connections` with the capture token. The server infers tenant and verification authority from that principal and binds the issued ticket to exactly one configured `Origin` value.
- Missing, duplicated, malformed, or disallowed origins fail before ticket issuance. CORS response policy is not a substitute for this application binding.
- The public WebSocket endpoint and regional data-plane identity are trusted deployment configuration. They are never derived from `Host`, forwarding headers, browser locale, device language, or IP address.
- The successful response is `Cache-Control: no-store`. Its `websocket_url` contains a single-use secret and must not enter application, proxy, access, analytics, or tracing logs.
- This operation has no request body and no idempotency key. Requesting another ticket is the safe recovery path after an ambiguous response, connection failure, or expiry.

## Conditional mutation

- Mutable resources return a strong `ETag` derived from their authoritative optimistic version.
- Mutating an existing conditional resource requires exactly one `If-Match` value previously returned for that resource. Operations explicitly specifying an `expected_version` command body (verification cancellation, webhook rotation/disablement and policy activation/rollback; policy revision append uses `expected_revision`) use that precondition instead.
- A missing precondition is `PRECONDITION_REQUIRED`; a stale or mismatched entity tag is `PRECONDITION_FAILED`.
- A successful mutation returns the new `ETag`. Wildcard `If-Match` is not accepted unless an operation explicitly documents it.

## Evidence uploads

- Capture clients first create a requirement-bound intent with `POST /v1/evidence-uploads`, then send one complete raw JPEG or PNG body to `PUT /v1/evidence-uploads/{uploadID}`. Both operations use the session-bound capture token.
- The upload request requires a positive `Content-Length`, one exact canonical `Content-Type`, one canonical RFC 9530 SHA-256 `Content-Digest`, and one strong `If-Match`. Encoded, ranged, chunked, trailer-based, parameterised, duplicate, or multi-digest variants are rejected before an attempt is claimed.
- An interrupted attempt restarts from byte zero with the intent's latest ETag. A concurrent repeat of the currently claimed version conflicts. A completed repeat may use either the precondition used by the accepted attempt or the terminal ETag and returns the existing result without reading or storing the repeated body.
- Integrity digests are request claims and restricted internal metadata. Upload resources expose neither the digest nor an object-storage location.

## Request, rate-limit, and lifecycle headers

- Every API response carries a newly server-issued `X-Request-ID`. Caller-supplied values are ignored and are not used as authority.
- Rate-aware responses may carry `RateLimit` and `RateLimit-Policy` structured fields. A rejected request also carries `Retry-After` when the server can provide a useful delay.
- These fields report enforcement; they do not select the distributed enforcement implementation, which remains a separate operational decision.
- Deprecation uses the RFC 9745 `Deprecation` structured date, an RFC 8594 `Sunset` date when retirement is planned, and a `Link` relation to migration documentation.

## Verification cancellation

Tenant cancellation uses `POST /v1/verifications/{verificationID}/cancel` with the dedicated `verification_sessions:cancel` permission. Subject cancellation uses `POST /v1/capture/cancel` with its session-bound capture token. Both require `Idempotency-Key` and `{ "expected_version": N }`. Retry the same key, principal and body to receive the original cancellation receipt within verification idempotency retention; a changed body conflicts. Authentication remains required on replay. The subject credential must remain unexpired and unrevoked. Cancellation authentication is separate from normal capture authentication and grants no capture access after a stop.

Fresh cancellation of a terminal session, a stale version or an elapsed session deadline returns `state_conflict`. The worker durably records elapsed deadlines as `expired`. Cancellation does not mean `not_verified`, withdraw authority or delete evidence. Receipt timestamps are UTC microseconds. Existing API-key permission snapshots require explicit issuance or rotation to obtain a newly introduced permission.

## Webhook administration

All nine webhook administration and inspection operations require tenant API credentials. `webhooks:read`, `webhooks:configure` and `webhooks:replay` are separate permissions, checked again by the application. Existing issued permission snapshots must be explicitly replaced to gain new authority. All mutation bodies are closed JSON objects and require `Idempotency-Key`. Command replay is bounded by `IDENQA_VERIFICATION_IDEMPOTENCY_RETENTION` (24 hours by default); keep retries within that retention window. Rotate/disable require `expected_version`; stale versions and active previous-key overlap return `state_conflict`. Rotation overlap is 1–86400 seconds. Disable/replay reasons are non-sensitive codes matching `[a-z][a-z0-9_.:-]*`, at most 64 characters.

Create/rotate return `signing_secret` only on the first successful response: 32 random bytes encoded as unpadded Base64URL. Persist it securely before discarding that response. Retries return the original endpoint metadata with `replayed: true`, omitting the secret. This is an explicit display-once exception to identical response replay. A lost secret requires a new authorised rotation after any active overlap; there is no secret-retrieval endpoint. Requests and successful responses must not be logged, and responses use `Cache-Control: no-store`. The API must have an owned key wrapper configured for create/rotate; the standard composition reuses the mounted evidence keyring with a separate purpose.

Replay accepts an exhausted delivery and enabled endpoint, preserves its event ID and exact body, and creates a new delivery with `replay_of`. The receipt, delivery, audit/outbox and Headgate intent commit together. The API's queue installation/schema and runtime grants must match the worker. A repeated command returns the same delivery without another task. Receivers must deduplicate the signed event ID across retries and manual replay.

Endpoint and delivery lists are ascending by identifier, default 25, maximum 100. A cursor is bound to tenant, collection, endpoint where applicable and page limit; keep those parameters unchanged on continuation. Attempt inspection is bounded to the delivery's maximum of 20 attempts. Responses omit event bodies, wrapped keys and signing/signature material. Attempt records may include `response_body`: at most 4096 bytes of receiver response content, sanitised to valid UTF-8 with invalid sequences replaced, plus `response_truncated` when bytes were dropped. It is untrusted receiver content, never parsed as structured data, and `null` when no response body was received. Root-event uniqueness remains enforced under migration 35; downgrading while deliberate same-event replay records exist fails rather than deleting history.

## Policy administration

The ten policy operations require tenant API credentials. Read metadata/source/history with `policies:read`; create, append and validate with `policies:write`; activate/rollback with `policies:activate`. Existing immutable grants require explicit replacement to gain new permissions. Initial-core activation requires one authorised key, version checks and audit, with no second approver or step-up. This does not grant AI authority.

`definition` contains the portable v1 document fields without server-assigned `policy_id` and `revision`. All fields are required and non-null, including empty `verified_assurance` when appropriate and an empty `reason_codes` array. Keys are case sensitive; unknown, duplicate, null, trailing, invalid UTF-8 and oversized input is rejected. The document and compiler retain their existing byte, rule, expression, provenance and evaluation limits. Validation compiles without persistence, an idempotency key, audit or activation.

Create assigns revision 1 with no active revision. Append requires `expected_revision` and creates the next revision without activation. Activate and rollback require `{ "revision": N, "expected_version": V, "reason": "tenant_requested" }`; first activation expects zero. Missing/invalid fields return `invalid_request`; a stale version or already-current revision returns `state_conflict`. Rollback must target a previously activated revision and records a new activation. Reasons are non-sensitive codes matching `[a-z][a-z0-9._:-]{0,63}`.

Mutation responses return status 200 with safe policy/revision/activation metadata and `replayed`. `Idempotency-Key` is required, scoped to tenant, credential and operation, with the policy ID and preconditions in the fingerprint. Rules, contributing facts and reasons use canonical collection ordering. Exact retries return the original committed metadata with `replayed: true`; changed meaning and concurrent incomplete commands conflict. Receipts use `IDENQA_VERIFICATION_IDEMPOTENCY_RETENTION` (24 hours by default); retry inside that window. State, receipt, common audit and reference-only outbox commit together. No source appears in those receipts or events.

Successful responses use `Cache-Control: no-store`. Lists default to 25 and cap at 100, descending by opaque policy ID, revision or activation version. Keep the limit and collection unchanged when continuing a tenant- and policy-bound signed cursor. Lists omit policy expressions; explicit revision retrieval returns the canonical `document` and requires read permission. Missing and cross-tenant policy resources return the same 404.

Session creation pins policy ID; decision-snapshot authorship pins the active revision. Activation can affect sessions without a snapshot. Already authored snapshots and decisions keep their exact revision. Simulation, diff and regression tooling remain separate capabilities.

## Persistent subjects and identity records

The section 6 implementation decision on 9 September 2026 selects persistent tenant subjects alongside the existing verification-local processing-authority subjects. They have separate server-generated `sub_` IDs and an explicit immutable subject-to-verification association. The API never merges subjects by external reference or identifier. SDK operations live on the tenant backend's `client.identity`; capture credentials confer no identity administration or reveal permission.

Use `subjects:read`, `subjects:write`, `subjects:delete`, `identity:read`, `identity:write`, `identity:reveal` and `identity:configure` as separate permissions. Issued scope snapshots do not expand automatically. Mutations require a quoted `Idempotency-Key`; updates, links, records, rebuilds and deletes require the current subject `expected_version`, passed as the required `expected_version` query parameter on the body-less `DELETE /v1/subjects/{subjectID}`. Configuration uses its own version, starting at zero. Exact retries within the 24-hour identity command retention return the original metadata. Changed meaning conflicts. Create/append return 201, deletion returns 202, other successful operations return 200. Every response is `Cache-Control: no-store`.

Subject state is `active`, `suspended`, `deleting` or `deleted`; tenant updates select only active/suspended. Omit `external_reference` to retain it, or send an empty string to clear it. External references are optional and non-unique. Subject and record lists use ascending IDs or immutable record sequence; `after` is the returned `next_cursor`, and `limit` defaults to 25 and caps at 100. Keep filters and collection unchanged when continuing. Values and original values appear only on explicit `reveal=true` with both the read and reveal scopes; reveals are audited. Identifier metadata uses a computed mask, never the full value. Lookup bodies carry sensitive values and must not be logged.

Identity observations, facts, claims and identifiers are append-only records. Numeric values are canonical strings, preserving precision. Supported types are string, boolean, signed 64-bit integer, bounded decimal, ISO date and UTC timestamp. A correction names the current record in `supersedes`; dependent facts must be explicitly re-derived from the new source. `current=true` means the current head of each record series, not an assurance that the record remains eligible for a new decision. Explicit record retention must be after creation and at most 30 days; validity and freshness remain separate. Transforms are versioned exact, Unicode whitespace trim, or ASCII uppercase plus trim. A transform cannot raise confidence or extend its sources' validity, freshness or retention.

Arbitrary tenant observations are always `tenant_attested`, even when the tenant labels their source as a provider. Trusted provider/model observations are imported from completed Core checks by `origin_observation_id`; callers cannot supply the value, source class, runner provenance or lineage. The current runner contract supplies boolean check outcomes, not general structured PII extraction. Identifier `verification_state` is explicitly attributed to `tenant_attested` and its actor; it does not itself establish policy assurance. Identity configuration maps named facts into reference-only policy requirement states using allowed source classes, confidence, validity, freshness and independent-source counts. Shared origins, source ancestors, evidence content and upstream groups cannot count as independent corroboration. Unclassified providers share a conservative upstream group. Configuration revisions may add correlation but cannot remove recorded lineage.

The user approved one narrow alpha v1 compatibility exception on 9 September 2026: decision-bundle `source.kind` gains `identity` with an `identity_receipt` digest. The receipt pins subject/version, immutable configuration, evaluation instant, contributing record IDs and corroboration counts without plaintext values. Existing snapshot bytes and decisions retain their original source representation. The warning allow-list names only this exact response enum addition; all other compatibility checks remain active.

The selected deletion scope covers identity values, external references, lookup indexes, and all evidence of explicitly linked verifications. The operation atomically stops active linked verifications and creates exact durable deletion targets. Holds on the subject or its linked verifications block erasure; backup expiry runs for 35 days from actual target erasure, and only proof completion moves the subject to `deleted`. Immutable reference-only provenance survives. Initial transactions cap one deletion at 256 linked verifications and 255 evidence objects plus the subject target; excess coverage or linked evidence in another region fails closed with no partial cancellation or target creation. Background retention uses the existing worker maintenance duty and rechecks holds. Restore replay uses durable tombstones to reapply erasure.

## Assurance profiles

The assurance surface publishes and validates immutable profile revisions, discovers capability meanings, selects requirements for future policy sessions, and reads immutable session pins. The TypeScript client exposes these operations through `client.assurance`. Decision reports expose requested and achieved dimensions in `typed_assurance` (SDK `typedAssurance`); detailed source references remain in authorised exports. See [assurance profiles and decision context](../../../../docs/assurance-profiles-v0.1.md) for permissions, idempotency, freshness, correlation and provenance limits.
