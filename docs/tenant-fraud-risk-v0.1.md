# Tenant-isolated fraud and risk v0.1

**Status:** Implemented baseline; tenant configuration and accepted input sources determine coverage.
**Date:** 9 September 2026.
**Authority:** Repository/package draft section 6.6.1 and integrated architecture section 21. Cross-tenant intelligence and ML rollout work remain deferred.

## 1. Selected scope

One authenticated tenant API key with `fraud:configure` may activate a configuration using an expected version and idempotency key. Activation, immutable configuration history, the common audit chain and replay result commit together. No second-principal approval is required. Configuration is regional: a tenant's active revision names its processing region; a session in another region receives inconclusive fraud facts.

Portrait reuse means exact equality of SHA-256 digests of trusted, canonical portrait templates supplied by an explicitly configured tenant integration. The digest is tokenised again with Core's tenant/region key. It is not selfie-file equality, face similarity, identity resolution, or proof that a portrait was lawfully acquired. The integration must attest its permitted portrait processing and use the versioned namespace of its template/canonicalisation contract. Core independently requires current processing authority for the fraud purpose.

The baseline introduces no dependency, network intelligence subscription, trained model or cross-tenant data-sharing system. Existing normalized runner observations can supply configured signals; absent or unaccepted inputs remain inconclusive. Model deployment acceptance remains the separate ML workstream.

## 2. Signal semantics

Each `fraud.<signal>` fact describes a **no-risk requirement**: `satisfied` means the configured pattern was not observed in the available window, `not_satisfied` means its threshold was met, and `inconclusive` means the detector is disabled, inputs are missing, authority is unavailable, the region does not match, or bounded correlation coverage was exceeded. A risk finding is evidence for policy, not a fraud conviction or an automatic reject action.

| Signal | Implemented input and counting rule |
| --- | --- |
| `device_reuse` | Exact tenant-attested device tokens; distinct other verification journeys. No claim of hardware attestation or physical-device uniqueness. |
| `identifier_reuse` | Canonical identifiers within a configured namespace; distinct other journeys. |
| `portrait_reuse` | Exact trusted-template digests, only from portrait-permitted configured sources. |
| `document_reuse` | Core-verified plaintext content digests for document images. Identifier tokens separately support reuse of a document identifier across different images. |
| `capture_replay` | Repeated exact content from live-camera acquisition across different journeys. File uploads and transport retries do not count as live-capture replay. |
| `verification_velocity` | Distinct other observed journeys linked by subject, device or identifier tokens within the configured window. Verification-local subject IDs are never assumed to represent the same person. |
| `network_anomaly` | Canonical IP address token frequency across journeys, plus explicitly configured normalized network-intelligence findings when available. Multiple enabled inputs must be available for a clean result. No proxy header or SDK assertion becomes server attestation. |
| `provider_inconsistency` | Explicit provider findings, or `disagreement` mappings comparing equivalent normalized requirements from at least two distinct providers in a declared comparison group. Missing providers cannot establish agreement. |
| `identity_inconsistency` | Different canonical identifier/address tokens within one verification and namespace across evidence or integration sources, plus configured normalized inconsistency findings. The comparison does not decide which identity claim is true. |
| `session_timing` | Durable session creation to capture-completion duration, compared with explicit minimum/maximum seconds. Worker latency is excluded. |
| `failed_liveness` | Configured completed liveness observations across related journeys; each verification counts at most once. Retry attempts and multiple models on one capture cannot inflate the count. Operational failure and timeout do not become failed-liveness findings. |
| `high_risk_model` | Normalized model findings pinned to the configured runner, package digest and signal. Missing or inconclusive results remain inconclusive. |

Mappings declare the runner kind/ID, exact package digest, observation name, risk outcome and correlation group. Actual check/request lineage further collapses repeated evidence within mapped risk counts; changing group labels cannot manufacture independent findings from one check/request. Comparison groups name equivalent provider requirements. Their configuration is an explicit interpretation contract, not empirical proof of source independence. There is no implicit additive risk score.

`window_seconds` and `retention_seconds` must be positive, retention must cover the window, and both are capped at 30 days. Each accepted link pins its original expiry and configuration revision; later settings do not extend that link. The bounded reconciliation and graph reads allow at most 2,000 rows each. Overflow makes fraud facts inconclusive rather than treating a partial scan as clean. This initial bound is an explicit throughput constraint.

## 3. Graph, provenance and privacy

`internal/fraud` owns rules, input validation, token domains, receipts and hypothesis contracts. Its PostgreSQL adapter owns storage, purpose-separated key custody and transactional projection. Graph relationships link a verification and evidence asset to a token, namespace, source class, integration principal, source reference, configuration revision, region and expiry. Token kinds include subject, device, identifier, document, portrait, address, provider event, network and live capture. A provider-event relationship is provenance; reuse alone is not automatically an adverse finding.

Every table has application tenant predicates and forced RLS. Tenant/region keys are independently random 256-bit keys wrapped by the owned KMS interface with the `fraud.correlation.v1` purpose and tenant/region authenticated context. HMAC-SHA-256 additionally separates schema, tenant, region, canonicalisation namespace and token kind. Evidence, webhook and audit key material is not reused as a correlation key. Integration values and source references are tokenised before graph persistence; raw values do not enter logs, tasks, ordinary audit events or policy snapshots.

Current authority must explicitly include `idenqa.purpose.fraud_prevention`, the evidence type and region, with an accepted latest subject response. Registry revision 3 adds this purpose without changing revisions 1 or 2. The deployed catalog and evidence protector honor the exact selected registry. A tenant source binding permits assertions; it does not create processing authority, device attestation or provider/model provenance. Removing a source binding excludes its assertions from new evaluations. Available, integrity-verified evidence is required throughout.

Evidence deletion removes its graph links in the same transaction. Expired links are excluded from new evaluations immediately and physically removed by bounded worker maintenance under the existing owned Headgate duty. Active verification-level legal holds preserve links until release, without making expired links eligible for analysis. Expiry cleanup and its reference-only audit event commit together. Wrapped tenant keys and reference-only decision receipts are not raw biometric material; existing audit/decision retention obligations still apply.

## 4. Policy and hypotheses

The worker's authoritative policy source loads checks and fraud facts within one repeatable-read database transaction. Fraud reconciliation, exact configuration/cutoff selection and immutable receipt insertion occur in that transaction. The normal canonical policy snapshot contains typed fraud facts with `source.kind = fraud` and `fraud_receipt` pointing to the receipt digest. The selected 9 September alpha compatibility exception permits this exact new provenance kind within the existing v1 bundle; consumers must update generated types and exhaustive provenance handling. Replay uses the immutable snapshot rather than querying a changing graph. Existing decisions are not rewritten when another session arrives or configuration changes.

Receipts pin the rule revision/digest, cutoff, normalized findings, source evidence/observation references and correlation groups. They contain no raw graph tokens. Authorized receipt reads verify the receipt digest before returning a summary with source references and groups removed. Risk reason codes use `fraud.review_required`; missing/clean patterns use `fraud.input_unavailable` and `fraud.no_pattern_in_window`. Subject-facing explanations must come from the tenant's policy and must not expose rule configuration, thresholds, tokens or other subjects' identifiers.

An AI or tenant integration may propose a bounded hypothesis code only against an existing receipt and supported adverse findings. Proposals are immutable, attributed and audited. They never change links, facts, authority, policy or verification decisions. Deterministic policy and the existing review boundaries retain consequential authority.

## 5. Operating the public core

Apply migration 48 after migration 47. The API needs the configured regional key wrapper/unwrapper; the worker needs the same unwrapper. The local distribution uses the existing evidence keyring configuration with a separate fraud key purpose. Custom distributions supply the same owned KMS interfaces. Missing key custody fails closed for an enabled configuration.

Runtime roles require SELECT/INSERT on `fraud_configurations`, `fraud_receipts` and `fraud_proposals`; SELECT/INSERT on `fraud_keys`; SELECT/INSERT/DELETE on `fraud_links`; existing idempotency/audit permissions; and EXECUTE on `idenqa.list_expired_fraud_tenants(timestamptz, integer)`. The discovery function reveals tenant routing IDs only. Evidence eraser roles also need DELETE on `fraud_links` for the deletion trigger.

| Operation | Permission |
| --- | --- |
| `PUT /v1/fraud/configuration` | `fraud:configure` |
| `GET /v1/fraud/configuration` | `fraud:read` |
| `GET /v1/fraud/configuration/revisions/{version}` | `fraud:read` |
| `POST /v1/fraud/inputs` | `fraud:write` plus an exact source-key/namespace/kind binding |
| `POST /v1/fraud/proposals` | `fraud:write` |
| `GET /v1/fraud/proposals/{digest}` | `fraud:read` |
| `GET /v1/fraud/receipts/{digest}` | `fraud:read` |

Mutations require `Idempotency-Key`. API-key grants are immutable snapshots: keys issued before these permissions existed need explicit replacement or a newly issued grant. Raw integration values belong only in the authenticated backend ingestion request. Never expose a tenant API key to Capture Web.

The TypeScript tenant client exposes `client.fraud.configure`, `getConfiguration`, `getConfigurationRevision`, `ingest`, `propose`, `getProposal` and `getReceipt`. These use the published OpenAPI types and have no Effect dependency. A tenant must configure accepted sources/mappings and include the required `fraud.*` facts in its deterministic policy. Submit integration inputs before the decision evaluation; late input does not retroactively overwrite a completed decision. Existing review/correction workflows handle later challenges.

## 6. Verification and acceptance boundary

Synthetic PostgreSQL checks exercised all twelve adverse signal paths, configuration version conflicts and idempotent replay, wrapped-key creation and ingestion, deterministic receipt reproduction, safe summary reads, hypothesis validation, restricted-role RLS, evidence deletion, authority withdrawal, legal-hold retention/release, expiry cleanup and receipt immutability, graph overflow before source-trust filtering and independent model coverage during graph overflow. The fixtures used synthetic evidence metadata and normalized runner observations, not production models, live biometrics or third-party network intelligence.

The existing PostgreSQL/Headgate integration suite, relevant Go race checks, Go lint, OpenAPI validation/generation and SDK checks are the regression gates for this increment. No new permanent test suite is added. These checks demonstrate implementation behavior; they do not establish production biometric accuracy, independently verified device identity, a lawful basis, or external-provider acceptance.

Tenant-specific canonicalisation contracts, fraud thresholds, source/model/network-intelligence acceptance and regional operating approval remain deployment decisions. Similarity-based portrait search and cross-tenant correlation remain outside this selected baseline.
