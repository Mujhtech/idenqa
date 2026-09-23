# Idenqa Core Repository Structure and Package Decisions v0.1 Draft

**Status:** Draft for review  
**Date:** 26 August 2026  
**Applies to:** Idenqa open-source core repository  
**Related architecture:** `global-identity-core-technical-architecture-v0.6-draft.md`

This document records the repository layout, Go package boundaries, module strategy, runtime dependencies, build tools, and client-package decisions discussed for Idenqa Core. Once accepted, it supersedes the provisional repository layout and reference-backend package list in sections 33.1 and 33.5 of the v0.6 architecture draft.

---

## 1. Decision status

The dependency tables use these labels:

| Status          | Meaning                                                                                             |
| --------------- | --------------------------------------------------------------------------------------------------- |
| **Selected**    | Explicitly agreed as part of the initial implementation direction                                   |
| **Proposed**    | Current recommendation; confirm when the affected package is implemented                            |
| **Conditional** | Add only when a concrete requirement justifies it                                                   |
| **TBD**         | The capability is selected, but its package name, import path, or final contract remains unresolved |

No dependency is added merely because it appears in this document. Versions, licences, maintenance status, transitive dependencies, and known vulnerabilities must be checked before the dependency is pinned.

---

## 2. Core decisions

### 2.1 Delivery order and source boundary

Idenqa is built open-source-core first. The public repository includes everything required to integrate with and operate the core without the commercial Console or managed Cloud:

- Core API and workers
- Operational CLI
- SDKs
- Basic hosted and embeddable capture pages
- Provider and model contracts
- Example adapters
- Conformance suites
- Self-hosted deployment examples
- Administrative and recovery operations required to run the core

The following remain separate commercial surfaces:

- Console user experience
- Managed Cloud control plane
- Advanced capture-experience authoring
- Fleet, support, billing, entitlement, and managed operations

The Console and Cloud consume documented core APIs, events, contracts, and generated clients. Proprietary source must never be required to compile, run, upgrade, recover, or export data from the open-source core.

### 2.2 Architecture style

The Go implementation uses **modular hexagonal architecture**:

- Packages are organised around bounded identity contexts rather than technical layers such as `service`, `repository`, or `handler` at the repository root.
- Domain types and application rules do not import HTTP, PostgreSQL, telemetry, queue, object-storage, KMS, or cloud packages.
- Interfaces are owned by the package that consumes them.
- Adapters implement those interfaces and depend inward on the consuming context.
- Each context starts as one package. Child adapter packages are introduced only when working code justifies the split.

### 2.3 Dependency injection

The initial implementation uses **manual constructor injection**.

- Constructors receive dependencies explicitly.
- Bootstrap packages are the composition roots.
- Domain and application packages never discover dependencies through global registries.
- Mutable global service state and side-effectful `init` functions are prohibited.
- A DI framework may be reconsidered only if wiring and lifecycle management become a demonstrated maintenance problem.

### 2.4 Go toolchain

The project standardises on **Go 1.27.1**.

The root module and each Go submodule use:

```go.mod
go 1.27.1
```

The exact patch version is also pinned in CI, development containers, and release images. The `go` directive establishes the minimum module language/toolchain requirement; reproducible build environments enforce the exact patch version.

Go tools are pinned with Go 1.24+ `tool` directives. The repository does not use the legacy blank-import `tools.go` pattern.

### 2.5 Open-source licence

The open-source core, SDKs, capture packages, contracts, conformance suites, and bundled examples use the **Apache License 2.0**. Third-party dependencies and contributed adapters must have redistribution terms compatible with an Apache-2.0 distribution. A dependency or adapter with reciprocal or field-of-use obligations requires explicit legal and maintainer review before inclusion.

### 2.6 Deployment baseline

The initial core is easy to operate as a single deployment while remaining safe to run with multiple API and worker instances. PostgreSQL is the durable coordination system and an S3-compatible object store holds evidence. Redis is not mandatory. Correctness must not depend on process-local state, sticky sessions, or a commercial control plane.

---

## 3. Repository split

### 3.1 Public core repository

The public repository owns the domain, runtime processes, public contracts, SDKs, capture page, conformance suites, examples, and deployment references.

### 3.2 Private commercial repositories

Console and Cloud remain separate repositories with independent dependency graphs and build pipelines. They may use service names such as `core-api` and `core-worker` when those names distinguish core workloads from commercial control-plane workloads.

That naming does not need to leak into the public source tree. The public repository uses the simpler `api` and `worker` entry-point names.

---

## 4. Proposed public repository layout

```text
idenqa/
├── cmd/
│   ├── idenqa/
│   │   └── main.go
│   ├── api/
│   │   └── main.go
│   ├── worker/
│   │   └── main.go
│   ├── evidence/
│   │   └── main.go
│   ├── adapter-runner/
│   │   └── main.go
│   └── model-runner/
│       └── main.go
│
├── internal/
│   ├── bootstrap/
│   │   ├── idenqa/
│   │   ├── api/
│   │   ├── worker/
│   │   ├── evidence/
│   │   ├── adapterrunner/
│   │   └── modelrunner/
│   ├── cli/
│   ├── config/
│   ├── tenant/
│   ├── access/
│   ├── identity/
│   ├── authority/
│   ├── evidence/
│   ├── fraud/
│   ├── verification/
│   ├── policy/
│   ├── provider/
│   ├── model/
│   ├── proposal/ # Proposed: AI proposals, guardrails, accepted commands (M6)
│   ├── review/
│   ├── privacy/
│   ├── audit/
│   ├── delivery/
│   ├── transport/
│   │   ├── httpapi/
│   │   │   ├── respond/
│   │   │   └── apierror/
│   │   ├── realtime/
│   │   ├── callback/
│   │   └── eventconsumer/
│   ├── platform/
│   │   ├── breaker/
│   │   ├── postgres/
│   │   ├── id/
│   │   ├── idempotency/
│   │   ├── inbox/
│   │   ├── outbox/
│   │   ├── task/
│   │   ├── objectstore/
│   │   ├── kms/
│   │   ├── crypto/
│   │   ├── secrets/
│   │   ├── ratelimit/
│   │   ├── httpclient/
│   │   ├── telemetry/
│   │   ├── httpserver/
│   │   ├── health/
│   │   └── clock/
│   └── gen/
│       ├── openapi/
│       ├── proto/
│       └── sqlc/
│
├── contracts/
│   ├── api/
│   ├── events/
│   ├── provider/
│   ├── model/
│   ├── policy/
│   ├── proposal/ # Proposed: AgentProposal, bounded actions, accepted command (M6)
│   ├── capture/
│   │   ├── profile/
│   │   └── realtime/
│   └── audit/
│
├── sdk/
│   ├── go/
│   ├── typescript/
│   ├── swift/
│   ├── kotlin/
│   ├── flutter/
│   └── react-native/
│
├── capture/
│   └── web/
│
├── adapters/
│   ├── objectstore/
│   │   └── s3/
│   ├── providers/
│   ├── models/
│   └── proposals/
│       ├── openaicompatible/
│       └── anthropic/
├── distributions/
│   └── s3/
│
├── db/
│   └── migrations/
│
├── conformance/
│   ├── provider/
│   ├── model/
│   ├── webhook/
│   ├── policy/
│   ├── capture/
│   └── sdk/
│
├── test/
│   ├── integration/
│   ├── security/
│   └── e2e/
│
├── examples/
├── deploy/
├── docs/
├── scripts/
├── go.mod
├── go.sum
├── go.work
├── Makefile
├── .golangci.yml
├── LICENSE
└── README.md
```

This is a target layout, not a requirement to create empty directories. A directory is added when it owns working code, a contract, a test suite, or required documentation.

---

## 5. Command and binary names

### 5.1 Public command directories

| Directory            | Built artifact   | Responsibility                                                                                                  |
| -------------------- | ---------------- | --------------------------------------------------------------------------------------------------------------- |
| `cmd/idenqa`         | `idenqa`         | Operational CLI for setup, migration, inspection, policy operations, recovery, export, and conformance commands |
| `cmd/api`            | `api`            | Public and administrative HTTP API process                                                                      |
| `cmd/worker`         | `worker`         | Durable background execution process                                                                            |
| `cmd/evidence`       | `evidence`       | Evidence upload, retrieval, transformation, and controlled delivery process when separately deployed            |
| `cmd/adapter-runner` | `adapter-runner` | Isolated provider-adapter execution                                                                             |
| `cmd/model-runner`   | `model-runner`   | Isolated model execution                                                                                        |

`api` and `worker` are intentionally used instead of `core-api` and `core-worker`. The public repository is already the core repository, so repeating `core` adds no information.

`adapter-runner` and `model-runner` retain explicit compound names because they run different isolated workloads and the word `runner` communicates their execution boundary.

### 5.2 Thin entry points

Every `cmd/*/main.go` must remain small:

1. Establish process-level context and signal handling.
2. Call the matching bootstrap package through the shared Cobra process boundary.
3. Exit with the returned process status.

Each matching `internal/bootstrap/*` package owns its binary-specific command and flags, configuration loading, dependency composition, and lifecycle. `internal/cli` owns only shared Cobra root defaults, version and completion commands, test argument injection, output routing, and exit/error classification. Business rules, SQL, HTTP handlers, configuration decoding, and provider logic do not belong in `cmd` or `internal/cli`.

---

## 6. Bounded-context packages

### 6.1 `tenant`

Owns tenants, tenant lifecycle, tenant-scoped configuration references, and the tenant boundary required by repositories and application services.

Tenant-aware ports and repository methods make the tenant identifier explicit. No adapter may infer a tenant from mutable global state.

### 6.2 `access`

Owns principals, credentials, authentication results, permissions, delegated authority, effective tenant actors, step-up requirements, support grants, break-glass state, and application-level authorisation decisions.

HTTP, WebSocket, worker, and runner adapters authenticate their transport credential and construct an access context. Application services authorise the requested operation. Access rules must not be hidden solely inside HTTP middleware, and resource-existence responses must not leak cross-tenant objects.

The initial credential surfaces are scoped tenant API keys for backend integrations, short-lived capture-session tokens for capture clients, single-use WebSocket connection tickets, and workload credentials for provider and model runners. Human administration is CLI-first in the open-source core; OIDC-backed human login belongs to the later Console surface unless a non-Console core use case requires it. Credential storage, display-once behaviour, hashing or encryption, expiry, rotation, revocation, scope narrowing, and audit semantics are part of this boundary.

The initial tenant API-key contract is **Selected**. A displayed key has the form `idq_v1_<tenant-ulid-payload>_<key-ulid-payload>_<secret>`, where the secret is 32 cryptographically random bytes encoded as unpadded Base64URL. The full value is returned exactly once and accepted only as an HTTP Bearer credential; it must never appear in a URL, cookie, persisted plaintext field, ordinary log, trace, metric, or audit payload. PostgreSQL stores the `key_` identifier, tenant, label, HMAC, pepper version, immutable requested scope patterns, immutable resolved scopes, lifecycle version and timestamps, and rotation lineage. Verification uses a domain-separated HMAC-SHA-256 over the version, tenant, key identifier, and secret with a versioned deployment pepper, followed by constant-time comparison. Unknown identifiers perform equivalent dummy-MAC work and all invalid-credential states remain non-disclosing.

The tenant payload in the presented key is an untrusted authentication lookup hint, not an application tenant scope. A narrow authentication adapter may use it only to enter an RLS-scoped credential-verification transaction. A verified `tenant.Scope` and access context are constructed only after the credential MAC, lifecycle, expiry, and tenant lifecycle all succeed. Application code cannot reuse the pre-authentication hint as authority.

The initial authentication service is transport-neutral. It performs real or dummy HMAC work before returning the single public invalid-credential result, preserves database and pepper-configuration failures as operational errors, and reads the key plus current tenant lifecycle in one scoped lookup. Its access context contains the API-key principal, verified effective tenant scope, and immutable exact grant. Application services require exact permissions from this context even when invoked outside HTTP; transport middleware only extracts the accepted credential and carries the authenticated context inward.

Privileged self-hosted API-key administration is also part of `internal/access`, not a parallel CLI domain model. `idenqa api-key create`, `list`, `rotate`, and `revoke` require an asserted actor and reason backed by the separately supplied administrative database credential. Each successful bypass operation appends an API-key administrative audit record in the same PostgreSQL transaction. Create and rotate reveal only the new credential and only after commit; list and revoke never return credential or digest material. Rotation and revocation require explicit confirmation, and revocation additionally requires the expected lifecycle version.

Scopes use the two-segment grammar `<resource>:<action>`. A grant may be exact or use only a complete-segment wildcard: `<resource>:*`, `*:<action>`, or `*:*`; partial globs are invalid. Wildcards resolve only against the registry of tenant-assignable permissions and can never match platform-administration, support, break-glass, workload, or other internal permissions. Issuance stores both the requested patterns and their resolved exact-scope snapshot, so a later registry addition never silently expands an existing credential. Scope changes require rotation to a newly issued credential.

Creation requires an explicit expiry or an explicit no-expiry choice; deployment policy may prohibit non-expiring keys and impose a maximum lifetime. Rotation creates a new display-once credential, links predecessor and successor records, and supports a bounded overlap before the predecessor retires. Expiry, retirement, and revocation are irreversible. Missing, malformed, unknown, mismatched, expired, retired, revoked, or disabled-tenant credentials produce the same authentication failure; a successfully authenticated credential lacking an application permission produces an insufficient-scope failure; cross-tenant resource probes remain non-disclosing.

### 6.3 `identity`

Owns subjects, claims, identifiers, observations, immutable facts, provenance, and identity-level invariants.

The earlier separate `subject` and `fact` packages are consolidated because facts exist in relation to a subject and share lifecycle and provenance rules. They may be split later if the package becomes incohesive.

**Selected on 9 September 2026:** implement the complete gap-audit section 6 as one core workstream. `internal/identity` owns a persistent tenant/region subject, encrypted immutable observations, facts, claims and identifiers, explicit verification links, current projection rebuilding, keyed lookup and versioned corroboration configuration. `internal/identity/postgres` owns migration 49, forced RLS, encrypted value custody, source/evidence eligibility, immutable lineage, configuration and decision-input receipts. HTTP composition exposes public tenant routes and the dependency-free TypeScript tenant client exposes `identity`. No dependency is added.

The existing D-019 `subjects` table remains the PII-free verification-local authority principal. A new persistent `identity_subjects` record receives an independently generated opaque ID; `identity_subject_verifications` explicitly associates a verification with at most one persistent subject. Association is tenant authorised and immutable. External references and identifiers never merge records or establish equivalence, and there is no global identifier or cross-tenant matching key. Reusing a caller-chosen source label cannot establish independent provenance.

Structured values use typed canonical scalars with versioned normalisation, separate original values, confidence, units, validity, freshness, retention, source records, evidence references and append-only supersession. Values are encrypted with authenticated per-subject random keys wrapped through the owned key provider; AAD binds tenant, region, subject or record and purpose. Independent random tenant/region lookup keys feed domain-separated HMAC tokens for identifier, external-reference, evidence-lineage and command fingerprints. Plaintext does not enter idempotency receipts, outbox, jobs, ordinary audit, logs or policy bundles. Default reads disclose metadata and computed identifier masks; full reveals require separate permission and audit.

Tenant-supplied observations are explicitly tenant attested. Trusted provider/model imports use an existing completed Core check observation and inherit its exact source provenance; the initial check contract represents boolean outcomes, and production structured field extraction is not implicitly selected. Claims and identifiers derive from facts. Identifier verification status records a tenant attestation and actor, not independently verified assurance. New decisions recheck all source authority, current ancestry, evidence availability, retention and validity. Correlated transformations share transitive roots; tenant assertions share one root, unknown providers share a conservative root, and versioned runner/package bindings can add upstream correlation. Immutable identity receipts feed the existing policy transaction through the owned fact-enrichment port, exposing only requirement states and reference provenance.

The user explicitly approved the narrow alpha v1 `identity` fact-source/receipt extension, preserving existing snapshot canonical bytes. The user also selected subject deletion to cover all linked verification evidence, not only structured values. Deletion atomically stops active linked sessions, creates exact targets and enters the existing hold-aware, backup-aware privacy workflow. Subject and linked-verification holds protect retained values; erasure removes values, per-subject key material and lookup indexes while immutable reference-only provenance remains. Completion requires deletion proof and the 35-day backup interval measured from actual erasure. Bounded expiry cleanup runs under existing Headgate maintenance, with tenant discovery exposing IDs only. Initial deletion bounds are 256 links and 255 evidence objects plus the identity target; exceeding bounds rolls back the entire request. Values require an explicit retention deadline no later than 30 days.

Acceptance requires tenant and region isolation, non-expanding scope snapshots, encrypted original/normalised values, lookup without implicit merging, mutation replay and version conflicts, immutable corrections/rebuild, authority withdrawal and expired-source exclusion, independent-source enforcement, unchanged historical policy reproduction, and hold/erasure/backup/restore checks. Production provider-specific structured extractors, automatic source ingestion, richer country-specific normalisation and larger incremental deletion planning remain **TBD**; none is implied by this core model or turns untrusted observations into trusted assurance. Operational usage and exact scopes are documented in [the public API conventions](../contracts/api/openapi/v1/conventions.md#persistent-subjects-and-identity-records).

**Selected — 22 September 2026:** Dojah document analysis is the first provider path for the identity/provider/document gap-closure workstream. This selects implementation order, not production acceptance or permission to persist extracted document fields. Existing transient-field and source/assurance boundaries remain unchanged. Persistent structured ingestion, its field allow-list, processing authority and trigger remain unresolved. Acceptance of the first slice requires bounded documented field mappings, malformed/partial/conflicting-response tests and proof that raw values remain excluded from persisted runner results and telemetry; live official-account and production-quality evidence remain separate gates. See the [delivery plan](identity-provider-document-delivery-plan-v0.1.md).

### 6.4 `authority`

Owns processing authority, notices, consent records where consent is the applicable authority, restrictions, expiry, withdrawal, and scoped evidence-processing grants.

`authority` remains separate from `privacy`. Authority answers whether processing is permitted; privacy applies retention, deletion, minimisation, and data-right operations to permitted processing.

### 6.5 `evidence`

Owns evidence metadata, evidence and artefact types, acquisition-method vocabulary, assurance metadata, content references, integrity state, controlled access, transformations, quarantine, and lifecycle state. Raw evidence bytes are accessed through ports rather than embedded in domain objects or queue payloads.

### 6.6 `verification`

Owns verification state, versioned capture profiles, immutable per-session requirement snapshots, requirement-satisfaction expressions, checks, signals, immutable evaluation snapshots, decisions, attempts, and business workflow transitions.

The earlier standalone `decision` package is consolidated into `verification` because decisions are produced from verification snapshots and share lineage and supersession rules.

Domain workflow belongs here. Durable execution mechanics, leases, retries, and scheduling belong in `platform/task`.

The initial V-02 execution model is **Selected and implemented** under `internal/verification`: `chk_` identifies a versioned check aggregate, shared `atm_` records form immutable attempt history, and `obs_` identifies immutable normalised observations. Check execution state remains separate from completed `passed`, `not_passed`, or `inconclusive` results; operational failure, timeout, cancellation, retry, duplicate delivery, and stale fencing cannot manufacture subject evidence. Every observation pins the exact runner kind, runner and contract version, package, request, and configuration digests carried by its attempt. Provider and model results enter one owned normalisation path, and the controllable fakes in `internal/verification/synthetic` use no real evidence.

The consuming package owns narrow tenant-scoped optimistic repository, result-inbox, safe progress-publication, and reconciliation interfaces. Migrations 17 and 18 plus `internal/verification/postgres` implement forced-RLS checks, one-way attempt finalisation, append-only observations and delivery diagnostics, exact-result inbox deduplication, leased reconciliation, and an atomic `verification.check.progress.v1` outbox intent. The check version, attempt transition, observations, receipt, reconciliation request, and outbox intent share one transaction and roll back together. Equivalent result replays do not reapply the aggregate effect but remain protected duplicate-delivery diagnostics. The deterministic memory repository remains the fast conformance model.

The initial verification tasks are **Selected**. `verification.execute` payload version 1 carries only `check_id` and `attempt_id`, runs on `verification`, uses the tenant ID as its partition, and uses `verification.execute:<attempt_id>` as its semantic idempotency key. It has five attempts, deterministic 20% jitter, one-second initial and 30-second maximum backoff, the durable attempt deadline capped at ten minutes from scheduling, and 30-day successful Headgate metadata retention. `verification.reconcile` payload version 1 carries the same two identifiers on `maintenance`, uses `verification.reconcile:<attempt_id>`, and has eight attempts, deterministic 20% jitter, 30-second initial and 15-minute maximum backoff, a 24-hour deadline, and the same 30-day successful metadata retention. Neither payload can represent raw evidence, credentials, provider results, identity claims, object locations, or unrestricted metadata.

Provider or model execution finishes before the effect transaction opens. The handler then reloads authoritative tenant state and commits the exact-result inbox receipt, aggregate transition, immutable attempt and observations, reconciliation intent, safe outbox intent, Headgate effect claim, and Headgate fence-verified completion through one PostgreSQL transaction. The immutable domain-attempt fence and the changing Headgate lease fence are intentionally distinct. The retry-policy decorator preserves Headgate's optional transactional capability instead of erasing it; a lost queue lease makes `CompleteTx` fail and rolls back the application effect. Worker composition currently installs the deterministic synthetic provider and model implementations for V-02.

The reconciliation and progress coordination path is **Selected and implemented**. Headgate's application-confined duty lease elects one installation-wide scheduler for an immediate startup sweep and configurable one-minute repetitions. Identifier-only `SECURITY DEFINER` functions discover at most 100 due reconciliation targets or tenants with pending progress; public execution is revoked and the restricted worker role receives explicit function grants. Every discovered item re-enters mandatory tenant scope. Reconciliation targets are enqueued independently so an expected semantic-uniqueness replay cannot suppress unrelated new work from the same discovery batch. `verification.reconcile` claims only its exact check and attempt for the configured two-minute application lease, distinguishes an already resolved item from an active conflicting lease, and resolves the claim in the same transaction as fenced Headgate completion without changing a check outcome or authoring a policy decision.

Every check-progress outbox insert emits a tenant-ID-only PostgreSQL notification after commit. The worker uses one dedicated listener for latency and a configurable one-second durable fallback poll for correctness. Projection strictly decodes and cross-checks the outbox envelope, appends one durable `verification.check.progress` event under the session's expiry, and marks the exact source published in the same tenant-scoped transaction. The Go wire contract, public JSON Schema, TypeScript SDK, and Capture Web expose only check ID, operational state, and check version; outcome, signals, reason codes, provider data, and evidence remain absent. SDK acknowledgement/replay and Capture Web REST snapshot recovery use the existing durable realtime contract.

The L-01 lifecycle foundation belongs to `internal/verification`, with its PostgreSQL primitive in `internal/verification/postgres`. It follows the integrated architecture's section 12 transition graph. A transition uses explicit tenant scope, expected version, stable event identity, bounded authenticated-principal reference, UTC occurrence time and, only for completion, an immutable decision reference. PostgreSQL atomically commits the state, replay receipt, common audit chain and outbox; the caller-owned transaction path is available for composition with task intent and fenced completion. Exact replay returns the original receipt rather than the latest session. The primitive is not an application authorisation API and must not be exposed as a public generic state setter.

Migration 30 expands the state vocabulary, binds completed sessions to a same-verification decision, and adds forced-RLS append-only transition receipts. Existing creation remains `collecting`. Deployment provisioning must grant the restricted runtime role only `SELECT, INSERT` on `idenqa.verification_transitions`; `UPDATE` and `DELETE` remain denied, and the append-only trigger also rejects administrative mutation. Runtime activation is a separate L-02 boundary: evidence acceptance, check writes and policy effects must share parent-session locking and authoritative commit-time checks before new transitions are exposed. No task type or dependency is selected by L-01.

The first L-02 prerequisite is implemented in `internal/evidence/postgres` and `internal/authority/postgres`: acceptance holds the tenant-scoped parent session before locking its upload or reading progress, and authority transitions and subject responses acquire that same parent lock. The authority adapter reuses the owned evaluator with the exact latest response inside the acceptance transaction. Token locking precedes clock observation; elapsed session/token/upload deadlines, revoked tokens, changed responses and withdrawn authority deny acceptance before evidence availability or publication. Acceptance uses read committed isolation so the next serialized upload observes prior accepted artefacts. The processing coordinator consumes the durable capture-completion marker in a separate serializable transaction; acceptance remains read committed. `SessionStore` reconstructs creation's original state/version/time on idempotent replay; current retrieval retains current lifecycle fields. Public contracts and running processing-state activation are unchanged by this prerequisite.

**Selected — 6 September 2026:** Expand the current alpha OpenAPI `VerificationSession.state` and TypeScript `VerificationState` to all ten v0.6 workflow states: `created`, `collecting`, `awaiting_input`, `processing`, `awaiting_external`, `manual_review`, `completed`, `cancelled`, `expired`, and `failed`. This is an explicit compatibility exception within `/v1`, not a general rule that response-enum additions are safe. Existing alpha clients must update their generated types and exhaustive state handling before processing activation. The exact nine additions are allow-listed by method, path, response status and property in `contracts/api/openapi/v1/lifecycle-alpha-warnings.txt`; unrelated warnings and errors continue to fail the gate. CI resolves the baseline and its external schema references from the same Git revision. Generated Go and TypeScript contracts and the public SDK own the wire vocabulary; lifecycle implementation remains in `internal/verification`. Creation/replay retain the original collecting snapshot. This resolves public state compatibility; the subsequent L-02 implementation below activates the synthetic processing path.

**Implemented — 6 September 2026, L-02 processing slice:** `internal/verification` owns the `CheckPlanner` contract; `internal/verification/syntheticplan` supplies the explicit v1 synthetic success fixture. Worker composition enables discovery only when `IDENQA_WORKER_SYNTHETIC_PROCESSING=true` (default false). This installation setting is for synthetic data: its document and liveness signals do not establish identity or evidence assurance. Production policy-to-check mapping and runner selection remain **TBD**. No provider, model or queue dependency changes are implied.

`internal/verification/postgres.ProcessingStore` discovers bounded tenant/verification identifiers from durable completed captures at worker startup and each progress poll. It locks the tenant-scoped session, verifies current authority and accepted evidence bindings, pins check/attempt provenance and deadlines, and commits checks, execution task intents, `collecting -> processing`, receipt, audit and outbox together. Failed enqueue rolls everything back. The locked lifecycle identifies an already committed plan; concurrent starts and worker restart cannot generate another plan. Serialization/deadlock retries repeat only this database unit, never external execution. The accepted capture marker survives a stopped worker and is the recovery source; capture completion and processing start are separate commits.

The runnable worker uses guarded check and policy stores. Current session state, deadline, authority, notice and exact latest response are rechecked before dispatch and/or consequential commit; already committed result/decision receipts remain replayable without new effects. Authority transitions and response appends issue a parent-row MVCC fence so a waiting serializable snapshot fails instead of reading stale permission. Headgate effect transactions establish serializable isolation before effect claims; lost lease completion rolls back application effects. Migrations 31–32 add capture discovery and restrict policy discovery to eligible processing sessions before applying the batch limit. Provisioning must explicitly grant the restricted worker role `EXECUTE` on `idenqa.list_ready_verification_captures(timestamptz, integer)` and `idenqa.list_ready_policy_authorships(timestamptz, integer)`; public execution remains revoked.

**Implemented — 6 September 2026, L-02 completion and delivery:** The policy worker composes `verification/postgres.CompletionStore` into its existing fenced effect. The immutable decision, assigned session's `completed` transition, completion receipt/audit/outbox and one delivery intent plus task per enabled tenant endpoint commit together. Completion verifies the pinned policy, authority, region and assigned decision, terminal checks and current processing permission. Exact completed replay preserves the original event and subscriber snapshot even after expiry, withdrawal or endpoint changes. It never creates a new notification for a later subscriber. The reference-only envelope follows v0.6 section 30.3 (`verification.completed`, schema `1.0`); its event ID is the completion receipt ID. Outcome details remain behind the authorised decision API. Synchronous fanout currently fails the entire effect beyond 1024 enabled endpoints; scalable fanout remains TBD rather than silently omitting receivers.

Worker delivery composition accepts an owned key-unwrapper, callback sender and lifecycle. The default local composition opens the mounted local keyring and wraps signing secrets under the separate `delivery.webhook-secret` purpose. Alternate distributions inject their KMS through the same boundary. Production callbacks retain HTTPS, public-DNS pinning, TLS verification and SSRF restrictions; the integration journey injects a TLS loopback receiver at the sender port without weakening production transport.

**Implemented — 6 September 2026, L-03 cancellation and expiry:** `internal/verification.CancellationService` owns tenant and subject cancellation. `POST /v1/verifications/{verificationID}/cancel` requires the exact `verification_sessions:cancel` permission; existing immutable API-key grants do not expand automatically. `POST /v1/capture/cancel` addresses only the session bound to a valid signed capture credential. Its separate authenticator accepts processing and terminal sessions for command evaluation/replay without granting capture access to those states. The transaction locks and rechecks the exact subject token, including revocation and expiry, after acquiring the parent session. Cancellation does not require permission to continue processing and does not itself withdraw authority or delete retained evidence.

Both operations require an `Idempotency-Key` and `expected_version`. The canonical fingerprint includes verification and expected version and is scoped by tenant, authenticated principal and operation. The configured verification idempotency retention applies. An identical authorised retry returns the original reference-only receipt (`event_id`, `verification_id`, `state`, `version`, `occurred_at`), including after session expiry while the credential and replay record remain valid. Changed input conflicts. A fresh request against a terminal state, wrong version or elapsed session deadline returns `state_conflict`; the expiry worker records the elapsed state independently. Request and effect timestamps are UTC microseconds. Lifecycle, receipt, audit, outbox and idempotency result commit together; bounded serialization retries cannot duplicate effects.

`verification.expire` v1 is a reference-only maintenance task carrying `verification_id`. `StopStore` re-enters tenant scope and observes the clock after locking the parent in Headgate's serializable fenced effect. Terminal, missing and not-yet-due sessions are no-ops. Migration 34 adds a fair discovery cursor and bounded installation function `idenqa.list_due_verification_expirations(timestamptz, integer)`; provisioning must grant the restricted worker role explicit `EXECUTE`, with public execution revoked. Startup and periodic discovery recover sessions overdue during downtime. The bound is 100 identifiers per pass; eight attempts use the existing 30-second to 15-minute reconciliation backoff and 20% jitter. The stable key includes verification and a UTC hourly recovery window; its task deadline is the window start plus two hours, so an elapsed session deadline never prevents expiry execution and exhausted work can be rediscovered. None of this extends the immutable session deadline. The existing `created -> expired` question remains TBD because public creation directly activates `collecting`.

Cancellation, expiry and completion serialize on the same parent row, so only one terminal state wins. Fresh upload creation and claim now lock parent then token before writing; evidence acceptance, guarded check commits and policy completion reject stopped sessions. Compensating upload cleanup and exact committed replay remain permitted. Pending external calls are not guaranteed to be recalled; late effects cannot author a decision or complete a stopped session. New cancellation/expiry webhook event subscriptions remain part of the general event-catalogue gap rather than being invented here.

**Selected — 19 September 2026, general resume:** A tenant backend resumes an `awaiting_input` verification through a dedicated `verification_sessions:resume` command carrying `expected_version` and `Idempotency-Key`; no generic lifecycle setter is exposed. A still-live, unrevoked capture credential is reused. Otherwise Core atomically revokes the previous credential and issues one replacement bound to the same verification, immutable profile, region and original session deadline. Resume never extends the session deadline, never restores a terminal session and always requires fresh subject authorization before new evidence or processing. Lifecycle transition, credential replacement when required, audit, outbox and idempotency receipt commit together. Exact replay returns the original reference-only result and never remints bearer material; the tenant backend remains responsible for authenticated subject handoff.

**Selected — 19 September 2026, asynchronous results and semantic recovery:** Provider adapters authenticate provider callbacks or polling responses and submit a closed reference-only durable receipt binding tenant, verification, check, attempt, provider job identity, provider/configuration revision, result digest and provider replay identity. Provider-specific signatures and credentials remain inside the isolated adapter; Core accepts only the owned receipt contract and reuses the result inbox and attempt fence. A check permits at most three semantic attempts under its immutable plan. Provider `Retry-After` is clamped to one second through one hour; semantic retry/reconcile intent, replacement attempt and task enqueue commit atomically before policy authorship may proceed. The parent enters `awaiting_external` only when at least one external operation is pending and no local check remains runnable, then returns to `processing` in the same fenced transaction that accepts the authoritative external result or schedules the next owned effect. A callback and a polling result with the same provider replay identity are exact duplicates, not separate evidence. `failed` remains terminal; recovery occurs before terminal failure or through a new verification.

#### 6.6.1 Tenant-local fraud boundary

**Selected and implemented — 8 September 2026:** `internal/fraud` owns tenant-local correlation rules, exact token domains, evidence-backed relationships, bounded hypotheses and immutable risk receipts. `internal/fraud/postgres` implements tenant/region-purpose-separated HMAC keys wrapped through the owned KMS boundary, mandatory tenant predicates plus forced RLS, current authority/evidence eligibility, retention/deletion, common audit and policy projection. No dependency or commercial control plane is added. The policy source supplies a transaction-aware adapter seam so checks, configuration, graph coverage and receipt provenance share one database snapshot; domain/application interfaces remain free of database and KMS SDK types.

The user selected one authenticated `fraud:configure` key with expected-version checks and common audit for activation, and exact legally permitted trusted-template reuse as the initial portrait mode. Tenant integration assertions remain distinguishable from Core evidence and completed provider/model observations. Portrait similarity, cross-tenant correlation and ML rollout acceptance are not implied. `fraud.*` facts are no-risk requirements; missing or untrusted inputs, disabled rules, wrong regions and bounded-read overflow remain inconclusive. Hypotheses cannot author decisions or activate policy.

**Selected — 9 September 2026:** Permit the narrow alpha `/v1` decision-bundle extension adding `fraud` to fact `source.kind`, with no new bundle version. The compatibility warning is allow-listed only for the exact GET bundle endpoint, 200 response and `snapshot/facts/items/source/kind` property. Existing alpha consumers must update generated types and exhaustive provenance handling before consuming fraud-enabled bundles; other enum additions remain subject to the compatibility gate.

Migration 48 implements immutable rule revisions/receipts/proposals and evidence-backed token links. Registry revision 3 adds `idenqa.purpose.fraud_prevention` while preserving earlier registry identities. Public API and dependency-light TypeScript operations support configuration, pinned revisions, ingestion, proposals and safe receipt reads. Physical evidence deletion removes links atomically; bounded hold-aware worker expiry runs under the existing owned Headgate duty. The initial active tenant configuration names one region and uses a window/retention of at most 30 days, with 2,000-row correlation/reconciliation bounds. See [tenant fraud and risk v0.1](tenant-fraud-risk-v0.1.md) for counting semantics, runtime grants and acceptance evidence.

### 6.7 `policy`

Owns policy documents, type checking, compilation, evaluation inputs and outputs, reason codes, simulation, activation, versioning, and reproducibility contracts. Assurance profiles, capability meanings, requested/achieved assurance, and the complete decision-reference context are also owned here.

The policy package presents an Idenqa-owned API even when CEL is used internally. CEL types must not leak into other domain packages or public contracts.

**Selected — section 8 implementation:** version-one assurance is a set of independent typed dimension requirements, with a closed platform capability catalog and exact runner/package/configuration/threshold mappings. Profiles are tenant-owned and immutable from publication. Existing `policies:write`, `policies:read`, and `policies:activate` permissions govern publication, discovery/validation, and expected-version assignment for future sessions. Session creation atomically pins the exact requested profile; recapture preserves the parent pin. There is no selected production default, scalar ranking, framework equivalence, or production biometric threshold.

`internal/policy/postgres` projects complete reference context within the same tenant-scoped repeatable-read transaction as facts and immutable identity/fraud receipts. It retains applicable claims/identifiers, normalised facts, evidence and lineage, checks/attempts/signals, review findings, authority/response/region and configured transfer-policy context, and exact assurance/provider/model/runtime/preprocessing/configuration/threshold/policy/evaluator references. Missing resources are not invented, and potential shared inputs remain conservative when field-level provenance is unavailable. Sources retain original collection/validity times; shared transitive roots count once. SDK advertisements, uploaded-file timestamps, and evaluation-only model results cannot upgrade assurance. A would-be verified result with unmet requested assurance routes to manual review; fresh commits fence profile substitution and expiry, while exact committed replay remains unchanged.

The additive canonical `context` is omitted for legacy snapshots, preserving existing bytes and reproduction. Ordinary decision reports expose a source-free `typed_assurance` summary; detailed context requires the existing export permission. Migration 50 supplies forced-RLS immutable profiles and session pins plus versioned future-session assignments. Eight API/TypeScript operations are open-source Core surfaces. The initial context bound is 128 KiB within existing snapshot/bundle limits; overflow fails closed. See [assurance profiles and decision context](assurance-profiles-v0.1.md) for capability semantics, provenance limits, API use, and remaining decisions.

The first V-03 foundation is **Implemented without resolving the CEL proposal**. `internal/policy` now owns closed requirement states, workflow directives, terminal outcomes, reference-only fact sources, immutable canonical snapshots, deterministic priority and conflict resolution, and reproducible terminal decisions. `pol_` and `dec_` are Idenqa-owned sortable identifiers; the underlying ULID remains hidden by `internal/platform/id`. Evaluation takes an explicit UTC time, cannot obtain providers, models, storage, networks, filesystems, or clocks through its type surface, and rejects stale facts, unknown states, duplicate or conflicting bindings, incompatible versions, and digest mismatches. Raw evidence, signals, credentials, object locations, and arbitrary maps are not representable in its fact contract.

The second V-03 foundation is also **Implemented without resolving the CEL proposal**. Strict restoration accepts only the closed canonical snapshot, evaluation, and decision representations within owned byte limits, rebuilds them through the original constructors and resolution rules, and requires exact canonical bytes plus SHA-256 digests. Migration 19 stores snapshots, evaluations, and decisions separately as immutable tenant-owned records. Composite foreign keys bind the chain to an existing verification and permit decisions to reference only terminal evaluations. Forced row-level security, append-only triggers, one-root and one-successor uniqueness, explicit predecessor locking, exact replay, and leaf-based latest lookup preserve a linear tenant-scoped lineage. The PostgreSQL adapter is confined to `internal/policy/postgres`; SQL and pgx types do not enter the domain contract.

The third V-03 foundation is **Implemented without resolving the CEL proposal**. `internal/policy` owns a versioned, bounded, closed portable decision bundle containing the exact canonical snapshot, evaluation, and decision plus their digests and a digest of the complete bundle payload. Restoration re-enters the original snapshot constructor, deterministic resolver, terminal-authorisation path, and exact canonical-byte checks without a database, provider, model, network, task runtime, or expression engine. The safe reproduction report contains only pinned identifiers, versions, digests, directive, outcome, assurance, actor, lineage, timestamps, and counts; it omits facts, requirement details, reason codes, evidence references, and canonical inputs.

The Cobra `idenqa policy decision reproduce` command performs an exact migration preflight and tenant-scoped runtime-role repository lookup before writing a summary, JSON report, or byte-exact bundle. `idenqa policy decision verify` consumes a bounded canonical file or standard input and performs the same reproduction offline. The embedded payload digest detects accidental or unexplained changes but is not a digital signature or an external trust anchor.

The fourth V-03 foundation is **Implemented without resolving the CEL proposal**. `internal/policy.Reader` rechecks tenant application authority above the repository and separates bounded report reads from richer portable exports through `decisions:read` and `decisions:export`. The API exposes exact and latest reproduced summaries plus explicit byte-canonical bundle export with strong digest ETags, private revalidation, generic tenant-invisible 404s, and no capture-token route. The PostgreSQL adapter remains behind the owned reader. OpenAPI defines the complete closed canonical bundle shape, and generated Go contracts plus the dependency-free TypeScript `IdenqaClient.decisions` facade consume only published HTTP meaning. The SDK keeps portable snake-case document fields and exact canonical text together so callers can store or verify the original export without lossy re-encoding.

**Public parity increment — 21 September 2026:** decision history is a bounded newest-first tenant read, and `POST /verifications/{verification_id}/reconsiderations` opens the existing independent correction workflow only after the challenged decision is proven to belong to that verification; no decision is mutated. Safe evidence reads project reference-only metadata and append-only lifecycle events without content locations, envelope material, digests or bytes. General processing-grant creation, inspection and revocation use distinct `evidence_grants:read`/`evidence_grants:write` permissions; creation and revocation reserve and complete their idempotency receipt in the same PostgreSQL transaction as the grant audit, while live processing authority is re-evaluated before issuance. Consent creation remains capture-token and exact-notice bound. Tenant consent inspection uses `consents:read`; `consents:write` withdrawal appends a refusal plus event and webhook intent atomically and never changes the original consent receipt. These resources are represented in OpenAPI, generated Go/TypeScript contracts and the CLI.

The fifth V-03 foundation was **Implemented before resolving D-012**. `internal/policy.Author` owns the CEL-neutral machine-decision application seam over three narrow consumer-owned capabilities: exact decision persistence, authoritative input loading, and bounded evaluation. A replay request pins decision and verification identities, optional predecessor, evaluation time, and decision time. Existing durable meaning is reproduced before reuse; changed meaning conflicts. Fresh input re-enters canonical snapshot construction, evaluator output re-enters deterministic resolution, and only a terminal authorised evaluation can create a machine-authored decision. A conflicting append is accepted as a concurrent replay only when the complete canonical decision digest matches. The author reads and reproduces the durable result before returning it, propagates cancellation unchanged, and cannot represent raw evidence or engine-specific values.

The sixth V-03 foundation **Resolves D-012 and is implemented**. `contracts/policy/v1` defines a dependency-free, bounded canonical JSON document with exact policy identity and revision, uniquely named boolean rules, explicit owned result state, directive, priority, exact contributing-fact provenance, reason codes, and a canonical SHA-256 digest. JSON is the v1 identity format; YAML may later be an authoring format only if it compiles to the same canonical JSON. Unknown and duplicate fields, trailing values, unsupported schema versions, invalid vocabularies, ambiguous ordering, and oversized documents fail closed.

`internal/policy/cel` confines `github.com/google/cel-go` v0.31.0 and implements the existing owned evaluator port. CEL sees only a freshly constructed `facts: map<string,string>` and `region: string`. Macros, comprehensions, functions, receiver calls, dynamic indexing, field selection, object or collection construction, arithmetic, conditionals, native Go structs, clocks and I/O are excluded by parser limits plus a checked-AST allow-list. Every fact index is a static validated key and must exactly match the rule's declared contributing facts. Expressions are boolean, at most 2,048 bytes and 64 AST nodes, with depth 32 and runtime cost 1,000. Compiled programs are immutable and concurrently reusable. The evaluator pins the CEL release, subset and limits in its implementation digest and requires the snapshot's policy and evaluator references to match before evaluation.

The seventh V-03 foundation **is implemented**. `internal/policy` now owns immutable canonical revisions, exact evaluator references, a compiler-before-write catalog service, and monotonic actor-attributed activations. Migration `000020_policy_catalog` stores tenant-scoped policy roots, append-only revisions and append-only activation history. Registration retries must match canonical bytes, evaluator identity and creation time exactly. Active-pointer changes use an explicit expected activation version, advance it by exactly one and append previous revision, new revision, actor and UTC time atomically. Forced row-level security, foreign keys and append-only triggers provide database defence in depth.

The same activation primitive may select any registered immutable revision, including an older one. It does not itself decide whether that action is an operational rollback or whether approval, step-up authentication or dual control is required; those remain application and administration policy.

The eighth V-03 foundation **is implemented**. `internal/policy/cel.Resolver` implements the existing owned evaluator port by loading and compiling the exact immutable revision pinned in each snapshot; it never follows the mutable active pointer during evaluation. Its explicit-capacity LRU is bounded to at most 4,096 compiled entries, keys tenant, policy, revision, public schema and digest plus evaluator identity, deduplicates one cold key, allows waiters to cancel independently, retries after a cancelled owner, starts no goroutines and does not retain failures. Durable reference, canonical digest and evaluator identity are verified before reuse.

`internal/policy.ActiveInputLoader` combines one atomically supplied `AuthoritativeState` with the catalog's active immutable revision. The projection contains selected policy, authority and acknowledgement references, explicit region, bounded owned facts, and the additive complete reference context described above; it cannot contain raw evidence, identity values, or provider payloads. For v1 the tenant explicitly selects a policy when creating a verification session, and that policy identifier is an immutable part of the session snapshot. The active revision is resolved and pinned when the decision snapshot is authored. Existing upgraded sessions without a trustworthy assignment fail closed rather than receiving an invented assignment.

The ninth V-03 foundation originally deferred assignment and fact semantics. The selected v1 completion contract is: every new verification session immutably pins the tenant-selected policy identifier; each validated namespaced normalised signal maps directly to the same public policy fact key; and `satisfied`, `not_satisfied`, and `inconclusive` map one-to-one. `unavailable` and `prohibited` are never inferred from those signal outcomes; they are derived only from authoritative terminal workflow or authority state with exact provenance. Duplicate projected fact keys fail closed. The projection remains reference-only and never loads raw evidence or provider payloads.

The tenth V-03 foundation originally deferred its production trigger. The selected v1 trigger enqueues `policy.author` only after capture completion and after every required verification check is terminal. The durable intent uses a stable decision identity and readiness timestamp so rediscovery is an exact idempotent replay. `internal/policy.Builder`, the decision store, and the transactionally fenced handler retain their existing deterministic and at-least-once semantics.

The eleventh V-03 foundation **was implemented before selecting the public policy-testkit package split**. `internal/policy.Simulator` accepts exact canonical policy bytes plus explicit synthetic tenant, verification, authority, subject-response, region, evaluation-time and reference-only fact inputs. It compiles through a narrow owned port without registering or activating the revision, constructs the same immutable snapshot used by production, evaluates through the same owned evaluator boundary, and re-enters deterministic resolution for terminal and non-terminal directives. It has no repository, clock, provider, model, task, network or filesystem capability and cannot author an authoritative decision or workflow effect. `internal/policy/cel.Compiler` implements the simulation compiler port without leaking CEL types. A separately versioned, bounded simulation bundle closes over canonical policy, snapshot and evaluation bytes plus their nested digests and a complete payload digest. Restoration strictly rejects malformed, unknown, duplicate, trailing, non-canonical, oversized, cross-identity or changed-digest meaning and re-enters snapshot and evaluation restoration without CEL or PostgreSQL. Its safe report contains only identifiers, versions, digests, selected directive, optional terminal outcome and assurance, counts, explicit evaluation time and reproduction status; it omits facts, source references, reason codes, expressions and canonical inputs. This internal foundation did not determine the later selection of the root-module `conformance/policy` package.

The twelfth V-03 foundation **was implemented before selecting the public policy-testkit package split**. `internal/policy.ScenarioSuite` runs at most 256 uniquely named synthetic examples through the existing simulator in canonical name order. Each expectation is expressed as owned requirement results and optional assurance, then resolved against the scenario's immutable snapshot through the same production resolver; an expectation mismatch is report data so one run retains every mismatch, while malformed input, invalid expected meaning, cancellation or execution failure aborts the run. Results include the self-checking actual simulation bundle, exact expected evaluation digest and safe actual report. The suite report is input-order independent, carries a digest over its safe canonical payload and excludes facts, provenance, expressions, requirement details, reasons and canonical policy bytes. The runner is sequential, starts no goroutines, owns returned slices and remains an internal foundation rather than a substitute for the now-selected public `conformance/policy` API.

The thirteenth V-03 foundation **was implemented before the P-01 policy administration surface**. `internal/policy.CatalogInspector` consumes narrow metadata-only revision and activation-history ports and returns immutable newest-first pages of at most 100 items. Zero means the newest internal boundary; subsequent pages use exclusive numeric revision or activation-version boundaries, while P-01 now wraps those boundaries in signed public cursors. The PostgreSQL adapter selects revision identity, evaluator identity, actor, previous revision and UTC timestamps without loading canonical policy bytes, forces tenant scope in a read-only transaction, validates every durable field, and rejects oversized, unordered, cross-policy or malformed repository results. Existing migration 20 primary keys support these reads, so no schema change or new mutable capability is introduced. Empty cross-tenant pages preserve non-disclosure. This inspection seam cannot register or activate a policy and does not define HTTP, CLI, permissions, approval, rollback or Console behaviour.

The public policy test-kit split is now **Selected and implemented** as `conformance/policy` in the root Go module. It depends only on `contracts/policy/v1` and exposes a narrow engine interface plus bounded portable cases. The suite runs each case twice, compares exact ordered results, verifies defensive input ownership and cancellation, and rejects deliberately non-conforming implementations. It does not import `internal/policy`, expose CEL types, register or activate policies, or author authoritative decisions. P-01 below implements public administration separately from the conformance package.

**Implemented — 6 September 2026, P-01 public policy administration:** `internal/policy.Management` owns list/get, effect-free validation, creation with server-assigned identity and inactive revision 1, contiguous immutable revision append, source retrieval, revision/history inspection, activation and rollback. `internal/policy/postgres.ManagementStore` reuses migration 20 behind consumer-owned ports. Tenant-scoped serializable transactions reserve the idempotency receipt, lock the policy root, check the expected revision or activation version, and commit the change with common actor-attributed audit, reference-only outbox and safe receipt. Policy source never enters audit or task payloads. No dependency or migration is added.

**Selected initial-core authority:** One tenant API key with `policies:activate` may activate or roll back with an explicit target revision, expected activation version and non-sensitive reason code. No second principal or step-up is required for this surface. `policies:read` and `policies:write` remain separate; previously issued immutable permission snapshots require explicit replacement. Rollback must target a previously activated revision and append a new activation record. Same-current-revision and stale-version commands conflict. Creation and append never activate implicitly. Activation uses the exact supported stored evaluator identity.

Ten OpenAPI operations are composed through `httpapi.PolicyRoutes`, the public TypeScript `policies` facade and `idenqa policy` commands. Validation compiles without persistence. List responses omit source; explicit revision retrieval returns the canonical document. Lists default to 25 and cap at 100, descending by opaque policy ID, revision or activation version. Signed cursors bind tenant, collection, policy where applicable and limit. Commands use the existing verification-idempotency retention (24 hours by default), with canonical policy collection ordering in fingerprints; exact retries return original metadata with `replayed: true`. Strict required, non-null, case-sensitive JSON fields, duplicate rejection, bounded bodies and compiler limits fail closed. Responses use `no-store`.

A session pins its policy ID at creation; its active revision is selected when authoring the immutable decision snapshot. Therefore activation may affect sessions without a snapshot, but cannot change an already pinned snapshot or decision. Public simulation, revision diff and scenario regression tooling are implemented through the API and CLI. Production policy-to-check planning and future operator/Console approval workflows remain separate gaps.

### 6.8 `provider`

Owns provider capabilities, configuration validation, selection, execution requests, stable results, stable error classification, and provider lifecycle metadata.

External provider implementations live under `adapters/providers`, not inside this domain package.

The dependency-free public v1 adapter vocabulary lives under `contracts/provider/v1`. It uses same-major, supported-minor compatibility and structurally permits only pinned provenance, capability and restriction snapshots, secret references, scoped evidence-grant redemptions, bounded stable results, redacted failures, and safe health. Version 1.1 adds at most 32 uniquely named opaque structured-input references. Every name must be declared by the pinned capability, input-only checks require no dummy evidence grant, and a v1.0 request cannot smuggle the additive field. The runner resolves values only inside the isolated adapter boundary; identifier values never appear in the public attempt envelope, task payload, ordinary telemetry, or adapter manifest. The package exports `AdapterDojah` and `AdapterSmileID` as the canonical built-in adapter identifier constants so manifests, composition, configuration validation and provider routing do not duplicate string literals. These constants are conveniences, not a closed enum: third-party adapters retain validated independent identifiers. The public conformance harness lives under `conformance/provider`; it does not import `internal` packages.

D-014 selects Smile ID and Dojah as the first real adapters for mobile-first regulated fintech onboarding of adult individuals in Nigeria, Ghana, Kenya, and South Africa. Each adapter lives under `adapters/providers`, runs through the isolated provider-runner contract, uses tenant-owned secret references, and publishes an exact versioned country/capability/restriction manifest. Neither provider name enters domain policy as a universal primary. Routing and fallback operate on owned capabilities and may substitute a provider only when the processing authority, recipient, region, evidence semantics, and assurance remain compatible. Sandbox support is not evidence of production entitlement; production startup fails closed without the tenant's provider agreement, credentials, and compatible regional configuration.

The pre-release implementations are `adapters/providers/dojah` and `adapters/providers/smileid`. They consume only Idenqa-owned secret, structured-input, evidence, HTTP and clock/wait ports. Dojah maps the reviewed synchronous HTTP operations; Smile ID maps signed preparation, bounded package upload, signed job-status polling and duplicate-job reconciliation. Provider HTTP shapes stop at the adapter. Their checked-in manifests are catalogue review records, not claims of tenant entitlement or provider certification. Whether these adapters remain in the root release or become independently versioned modules remains unresolved under section 17.

**Selected implementation sequence — 7 September 2026:** the user prioritised real-provider runtime over further policy tooling, proving one operation through one provider first. PR-01 implements the first Dojah document-analysis route; this is not a universal primary-provider decision. `internal/provider` owns the exact tenant/policy/profile route and durable request/dispatch meaning, `internal/provider/postgres` atomically prepares grants and immutable request snapshots with checks and tasks, `cmd/adapter-runner` composes isolated provider I/O, and the API owns the separately authenticated private evidence gateway. The worker uses the existing public runner contract and current authority checks. Runtime settings are reference-only mounted configuration; tenant provider registration, dynamic secret management and production routing remain unresolved. The Dojah route permits its reviewed sandbox origin or explicit loopback fixture origin. The [first-provider runtime guide](provider-runtime-v0.1.md) records configuration, recovery limits and acceptance boundaries.

**PR-02 implementation — 7 September 2026:** The user authorised Smile ID asynchronous document-and-selfie execution next. `internal/provider` now owns optional async progress and status-only execution; its PostgreSQL adapter owns atomic initial dispatch and bounded polling coordination. Migration 37 forces tenant RLS on provider job references, leases and fences. The optional additive `Advance` RPC keeps provider v1.1 terminal results unchanged, with pending represented outside `Result`. The runner owns mounted non-personal country/document-type resolution and the reviewed Smile ID sandbox API/S3 egress boundary. API evidence access remains separately authorised and limited to the exact two grants. The worker uses a dedicated bounded async task and a completion allowance for operational timeout, never resubmission. No static-image liveness assurance is selected. Exact configuration, retry/lease limits and acceptance evidence are in the [runtime guide](provider-runtime-v0.1.md#smile-id-asynchronous-document-route). Callback intake, general subject-input storage, live-account evidence, production routing and retention purge remain unresolved.

The shared initial capability baseline is live selfie, liveness/PAD, one-to-one face comparison, national identity document, passport and driving-licence capture, document quality, and MRZ/barcode where declared. Authority-backed v1 identifiers are Nigeria NIN/VNIN with BVN only in an explicitly banking-compatible profile, Ghana Card, Kenya National ID or passport, and South Africa National ID. English is the initial catalogue language. NFC, voice, KYB, AML, address, tax, phone, additional countries, non-English localisation, and documents without a tested pack remain outside D-014.

### 6.9 `model`

Owns model manifests, capabilities, input/output contracts, version selection, evaluation metadata, and stable errors.

External model implementations live under `adapters/models` and execute through the model-runner boundary where isolation is required.

**Selected — 7 September 2026:** ONNX Runtime is the initial inference runtime for locally hosted predictive models. Runtime integration belongs under `adapters/models` and executes behind the isolated `model-runner` boundary; `cmd/model-runner` owns only composition and lifecycle. ONNX-specific sessions, tensors, execution-provider types and native-library bindings must not enter domain/application packages, public model contracts or SDKs. Existing provider-based verification and alternative customer-supplied model adapters remain valid through the replaceable model contract.

**Selected first capability — 7 September 2026:** Presentation-attack detection (PAD) / liveness. The implemented reference slice uses official Python ONNX Runtime 1.29.0 with CPUExecutionProvider under `adapters/models/onnx`, after runtime/dependency intake. `internal/model` owns reference-only planning and durable execution; `internal/model/postgres` owns immutable requests/receipts and atomic grant preparation. API/worker composition connects the private evidence gateway and model gRPC client to the persisted workflow. See [ONNX runtime v0.1](onnx-runtime-v0.1.md) for pins, operational configuration and verification evidence.

**Selected first-party direction — 22 September 2026:** Idenqa-owned biometric models are the primary path; Smile ID, Dojah and other providers remain optional adapters for tenants that cannot operate the models themselves or require capabilities outside the accepted first-party set. Routing remains capability- and policy-based rather than provider-name-based. This direction does not convert an evaluation model into accepted assurance or make a provider fallback equivalent without explicit signal, provenance, region, authority and correlation compatibility.

The initial passive-PAD adapter is **evaluation-only**: every successful inference is inconclusive and cannot establish liveness. A candidate artefact has native smoke-test evidence, but a production trained model remains **TBD**. Pinned YuNet detection, contextual cropping and an offline evaluation command are implemented in the reference adapter/composition; [PAD evaluation v0.1](pad-evaluation-v0.1.md) owns the operational details and public dataset shortlist. The public evidence contract and migration 76 persist complete ordered Web challenge sequences as digest chains, delay capture completion until the declared chain is present, and allow a temporal-capable model request to bind every frame grant. Production acceptance of face localisation/preprocessing, temporal active-liveness meaning, thresholds, model/dataset rights, representative quality evaluation, hardened OCI packaging, kernel resource/egress limits and supported hardware remain unresolved. The Python CPU binding is the implemented reference, not a decision to require Python in Core or exclude future bindings/accelerators. Runtime availability or temporal persistence does not establish PAD, face-match or document-authenticity performance.

**Evaluation tooling increment — 7 September 2026:** `internal/bootstrap/modelrunner` owns local inventory import, bounded sequential dataset evaluation and baseline/candidate comparison orchestration. It consumes the owned ONNX adapter boundary, emits content-pinned manifests or aggregate-only reports, and does not change public model contracts or production activation. Import must preserve supplied pins and reject mismatches/duplicates; comparison must use identical dataset/threshold pins and expose nonresponses and missing denominators. Native CLI fixtures test these invariants. No dependency change is required. Real-data quality, calibration and dataset rights remain unresolved under section 17.

**Face-matching engineering increment — updated 21 September 2026:** `adapters/models/onnx` owns an evaluation-only whole-image document/selfie portrait-selection, five-landmark alignment and embedding/cosine path; `internal/model` owns its explicit two-requirement route and its PostgreSQL adapter creates both scoped grants atomically. Invalid landmark geometry is a typed inconclusive quality result. Embeddings, aligned portraits, tensors and coordinates never cross the native workload boundary into Core, and model-contract v1 forbids their persistence or non-zero retention. [Face-matching runtime](face-matching-runtime-v0.1.md) defines input roles, preprocessing/output pins, bounds and the representative acceptance still required. This implementation does not select a trained face-recognition model or threshold.

**Composed-runtime increment — updated 21 September 2026:** Bootstrap composes one provider with a bounded model bundle for the same tenant/policy/profile and dispatches exact saved configurations. Model bindings may name a distinct check, priority, dependencies, an exact fallback predecessor and a correlation group. Composition rejects duplicate checks, missing/cyclic edges, non-equivalent fallback signals and accidental overlap; the same public signal may overlap only within one connected, correlation-labelled equivalent fallback chain, so only its selected branch can emit the fact. Migration 75 persists the immutable graph, and task admission enforces dependency completion and operational-failure fallback before external execution. Policy assurance reproduction separately retains shared evidence/request lineage for correlated distinct signals. The processing transaction still owns all request/grant/check/task preparation atomically, and API model-evidence dispatch still requires distinct per-model gateway credentials. [Composed runtime](composed-verification-runtime-v0.1.md) records configuration and the remaining public-administration, hot-provisioning and production-acceptance boundaries. No dependency selection changes.

**Selfie-analysis engineering increment — 22 September 2026:** the public model vocabulary now owns canonical identifiers for first-party PAD, face matching and selfie analysis plus their normalized signals. Model bindings can select an exact evaluation rather than inferring capability from evidence cardinality. The ONNX adapter implements a detector-only, evaluation-only selfie route over one still image or a complete temporal sequence. It emits independent face-count, face-position, head-pose, image-quality and optional temporal-integrity results, exposes only bounded reason codes, and keeps pixels and face geometry inside the native workload. Temporal PAD is rejected until its predictor consumes every frame. Current heuristics and duplicate/duration checks are version-pinned diagnostics only; production classifiers, calibration and representative acceptance remain unresolved.

The dependency-free public model vocabulary lives under `contracts/model/v1`. Additive contract revision 1.1 pins model, runtime, preprocessing, configuration, output-schema, capability and resource restrictions while accepting evidence only through scoped grant redemptions. Temporal-capable requests bind one ordered digest-chained sequence to every exact frame grant. Revision 1.1 forbids persisted derived model data and non-zero derived retention, and typed unacceptable quality is valid only with an inconclusive signal. Version 1.0 envelopes remain consumable under same-major minor compatibility. The public conformance harness lives under `conformance/model`; runner transport and process isolation remain separate from the model contract.

**Model registry increment — 8 September 2026:** The user selected one tenant API key with `models:activate`, expected-version checks and audit for evaluation-only deployment changes. `internal/model` owns model/threshold revision validation and management; its PostgreSQL adapter owns migration 43, forced-RLS registry roots, immutable independent revisions and command history with atomic idempotency/audit/outbox. Thresholds bind exact execution provenance and configuration. Optional mounted registry pins are checked inside attempt preparation; retirement fences new preparation while saved requests remain immutable. Core HTTP routes, exported-threshold validation in the PAD evaluator, experimental report gates and per-dispatch readiness checks are implemented. [Model registry v0.1](model-registry-v0.1.md) defines contracts, limitations and verification. Production approval, live shadow/canary orchestration, automated technical rollback, drift monitoring, hardened deployment and generated registry contracts remain open.

### 6.10 `review`

Owns review cases, assignments, findings, reason codes, resolution, appeals, and review audit events. It does not own a commercial reviewer dashboard.

**Selected — 7 September 2026:** Initial review recapture creates a new verification session linked to the review case, requires fresh subject authorization and reruns the child's configured checks. It preserves the original verification, evidence and decisions; in-place capture rounds are deferred. `review` owns request/lineage, `verification` owns child creation and processing, and `policy` owns subsequent decision authorship. Atomic linkage, optimistic case versions, exact idempotent replay and immutable history are mandatory acceptance criteria.

**Implemented prerequisite:** Reviewer actions resolve stable operator identity, permissions, certifications, allowed regions and validity from server-managed assignments through the owned `review.Authority` port. The initial API adapter reloads the bounded `IDENQA_REVIEW_AUTHORITY_FILE` on each action, failing closed on missing or invalid authority. Request JSON cannot assert reviewer identity or certifications. This does not select SSO or a real external certification issuer. Automatic routing and the composed review, recapture, recovery, escalation, correction and appeal integrations are implemented below; third-party issuer trust/status integration and reviewer/subject acceptance remain open. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Implemented routing increment — 7 September 2026:** Worker policy authorship now persists nonterminal canonical snapshots/evaluations and an immutable routing receipt, creates a unique routed case and applies `processing -> manual_review` with audit/outbox in one serializable Headgate effect. Receipt replay validates its original case and lifecycle event without reevaluating current facts or sending completion webhooks. `IDENQA_REVIEW_ROUTING_FILE` supplies explicit certification/oversight for an exact tenant/policy revision/digest; absent matches fail closed. Migration 39 keeps decision and routing origins distinct and immutable. The existing reserved terminal decision ID is preserved. Accepted-finding re-evaluation is implemented below; linked child creation is implemented. The operational mapping does not select the full public review-policy schema.

**Implemented finding increment — 7 September 2026:** `review` owns immutable permitted resolution/reason pairs and accepted-case digests; `review/postgres` atomically records resolved-case evaluation requests and checks current case-bound evidence grants. `review/task` discovers and executes `review.evaluate` v1 through the owned task boundary. It retains original policy/evaluator/facts and adds `review.resolution`; dual-review disagreement escalates without evaluation. Migration 40 stores immutable forced-RLS requests and receipts. Terminal results use the existing reserved decision and shared completion transaction; nonterminal receipts do not complete the session. Replay restores saved results. Acceptance covers unauthorized grants, stale or changed inputs, missing review provenance, rollback and exact replay. Just-in-time grant issuance, escalation resolution and correction/supersession remain pending; the subsequent increment implements explicit child-outcome re-evaluation. Operational details and runtime grants are in [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Linked recapture increment — 7 September 2026:** The user selected exact parent policy and capture-profile revisions for the child. `review` now authorizes and records linked creation after a persisted `request_input` evaluation; `verification/postgres` creates a fresh collecting session in the same transaction. Migration 41 enforces immutable tenant-scoped case/version uniqueness. Separate `reviews.recapture` idempotency and durable linkage restore the same child; policy loading preserves its parent revision while using fresh child observations. The internal route requires both review-write and session-create scopes plus current certified operator authority. Rollback, replay, fresh-child state, pinned inputs and transport guards have synthetic boundary tests. Generated public contracts, complete subject-facing handoff and correction/supersession remain pending; subsequent increments below implement Core credential recovery and explicit follow-up. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Selected child-outcome behavior and recovery increment — 7 September 2026:** The child's committed outcome is attached to its original case for authorized follow-up; it does not automatically re-evaluate the parent. A reference-only read projection follows immutable linkage to child completion and exposes current token/expiry metadata without bearer credentials. The initial expired-token renewal covered pre-capture work; the selected 8 September increment below adds progress-preserving recovery, with current reviewer authority, child/session locks, original deadline limits, irreversible old-token revocation, distinct idempotency and atomic audit. Creation/renewal responses include handoff expiry metadata. Synthetic tests cover renewal rollback/replay, stale-token denial and child-outcome visibility with unchanged parent state. Full subject-facing handoff, generated contracts and correction/supersession remain pending; O-03 is still in progress. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Selected acknowledgement increment — 7 September 2026:** The first follow-up action records an audited acknowledgement of the child's exact completed outcome, leaving parent state and decisions unchanged. Migration 42 binds an immutable receipt to tenant/case/version, exact child/decision, stable operator and API-key actor. The internal acknowledgement route requires review-write scope, current `reviews:resolve` authority and case certification; closed request bodies cannot assert reviewer identity. Receipt, audit and distinct idempotency commit atomically, and retries preserve original attribution. Status clears the follow-up flag for the acknowledged outcome. Synthetic tests cover denial, rollback, replay, immutability and absence of additional workflow effects. Correction/supersession remains pending; progress-preserving recovery is implemented in the subsequent increment. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Selected recovery increment — 8 September 2026:** Continue the same child with a replacement credential, retain accepted evidence under its original provenance, require fresh subject authorization and abandon unfinished uploads for restart. `verification/postgres` owns atomic token replacement and migration-44 recovery lineage; `evidence` owns authorized progress projection; `authority` prevents old-response reuse and permits retained evidence processing only through explicit recovery bindings after fresh authorization. Session deadlines and evidence assurance/age remain unchanged. The review service retains current reviewer authority, expected-case/token checks, audit and idempotency. [Manual-review recovery](manual-review-recapture-v0.1.md#51-progress-preserving-credential-recovery) defines acceptance and cleanup semantics. Full subject-facing visual acceptance, just-in-time evidence display, escalation and correction/supersession remain pending.

**Selected consequential follow-up — 8 September 2026:** An authorized reviewer may explicitly request parent policy re-evaluation after acknowledging the exact completed child outcome. The request pins that acknowledgement and child decision, increments the case version and atomically records an immutable request, audit and durable evaluation intent. The parent’s original policy, capture profile and historical facts remain pinned. The worker adds a separately sourced `review.recapture` outcome fact; only the original policy may complete the parent. Automatic re-evaluation on child completion remains forbidden. Implementation and acceptance evidence are tracked in [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Implemented explicit recapture follow-up — 8 September 2026:** A reviewer with current `reviews:resolve` authority may request parent policy re-evaluation after acknowledging the exact child decision. Migration 45 atomically records immutable acknowledgement-bound lineage, a new case version, audit, idempotency and durable evaluation intent. The existing fenced worker preserves the parent’s pinned policy and original fact timestamps and adds `review.recapture` from the child’s immutable outcome. Only policy may complete the parent; nonterminal results keep manual review. Child completion does not automatically schedule evaluation. Controlled evidence access, escalation/correction/supersession, generated public contracts and accepted subject-facing UI remain pending. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Selected review operations completion — 8 September 2026:** One tenant API key with `reviews:admin`, optimistic version checks and atomic audit may manage operator assignments and tenant-attested certifications. Database-backed assignments are checked transactionally on consequential operations; revocation must not fall back to file authority. External certification verification stays behind an owned adapter. Escalation uses a certified supervisor independent of both findings to author an audited arbitration fact; only the pinned policy decides the outcome. Correction and appeal decisions require independent review and a policy-authored successor, preserving the challenged decision; fresh linked sessions collect new evidence. The user requested implementation of review evidence access, queue operations, operator administration, corrections/appeals and public integration, without writing new tests. Existing checks and explicit end-to-end/visual acceptance remain distinct.

**Implemented ownership:** `review` owns queue/settings, independent follow-up and administration ports; its PostgreSQL adapter owns migrations 46–47, transactional assignment revocation, immutable display/arbitration/correction/appeal receipts and atomic delivery intent. Evidence decryption stays behind `evidence` ports; HTTP's bounded receiver releases only explicitly redacted and watermarked raster output. The public OpenAPI/TypeScript review client and Capture Web recapture handoff depend on published contracts. Queue labels support filtered work selection; advanced assignment automation and external certificate issuer adapters remain outside this increment. Full reviewer/subject journey acceptance remains required. See [manual review operations](manual-review-recapture-v0.1.md#6-review-operations-and-public-integration) for contracts, runtime grants and acceptance boundaries.

### 6.11 `privacy`

Owns retention classification, deletion requests, erasure progress, backup/tombstone semantics, exports, and privacy-operation proofs.

### 6.12 `audit`

Owns audit event semantics, append-only records, integrity checkpoints, verification, and authorised export.

The O-01 v1 audit format is **Selected and implemented**. Each tenant has a monotonic sequence whose closed canonical reference-only records are linked with SHA-256; Ed25519 checkpoints sign an exact sequence and chain head under an immutable key ID. Migration 24 stores serialised tenant heads, forced-RLS append-only records, public-key history, and forced-RLS append-only checkpoints. `internal/audit/postgres` owns persistence and repeatable-read export, while the application boundary rechecks `audit:export`. The dependency-free verifier and `idenqa audit verify` consume a bounded portable export and public-key file without opening PostgreSQL or trusting the running core. This detects gaps, deletion, reordering, mutation, wrong keys, and invalid signatures; it does not claim protection from compromised signing-key custody or deletion of every independently controlled export.

### 6.13 `delivery`

Owns tenant webhook endpoints, endpoint event subscriptions, signing-key references, delivery intents, attempts, retry state, endpoint disablement, replay authorisation, bounded resumable fanout, the delivery side of the versioned public event catalogue, and bounded delivery diagnostics including a sanitised receiver response excerpt.

`delivery` owns the durable customer-facing operation. `transport/callback` performs HTTP delivery and callback receipt. Transport failures never become identity outcomes without an explicit workflow rule.

The V-04 delivery contract is **Selected and implemented**. Migration 23 persists forced-RLS endpoints, append-only KMS-wrapped signing-secret versions, deliveries with explicit replay lineage, and append-only attempts. `webhooks:configure` and `webhooks:replay` are separate application permissions. The reference-only `webhook.deliver` v2 payload contains the delivery ID and logical attempt number. The worker registers only that version; pre-release development retains no v1 task compatibility path. It uses the `delivery` queue, eight bounded callback attempts, one-second-to-one-hour backoff with deterministic 20% jitter (explicit Retry-After is preserved), a fixed 24-hour delivery window and 30-day successful task metadata retention. External HTTP happens before the short fenced effect. That effect atomically records bounded attempt diagnostics — status, class, retry advice, and at most 4096 bytes of receiver response content sanitised to valid UTF-8 with an explicit truncation flag — and enqueues the next logical attempt before completing the current task; infrastructure retries may repeat HTTP without advancing the durable callback attempt. Signed event identity/body remain stable for tenant deduplication.

Migration 33 adds bounded recovery discovery through `idenqa.list_ready_webhook_deliveries(timestamptz, integer)`. The function advances only a scheduling-discovery cursor with `SKIP LOCKED`, so already queued or infrastructure-failed deliveries cannot monopolise the first batch. Runtime provisioning must grant worker execution explicitly; public execution remains revoked. Expired pending deliveries receive bounded cleanup work and become exhausted without another callback. Endpoint disablement likewise cancels pending delivery without sending. The callback adapter requires HTTPS and TLS 1.2+, pins approved public DNS answers for each attempt, refuses redirects and unsafe addresses, and bounds response handling to the same 4096-byte excerpt cap. The public Go SDK verifies the exact signed body under active-plus-overlap secrets and documents timestamp-window checks and durable event-ID deduplication.

**Implemented — 6 September 2026, H-01 public webhook administration:** `internal/delivery.Management` owns create, rotate, disable, safe inspection and deliberate replay. `ManagementRepository` supplies a transaction-bound delivery repository to the existing manager. `internal/delivery/postgres.ManagementStore` commits configuration or replay delivery, safe idempotency receipt, reference-only outbox, common audit-chain record and any Headgate task together in a serializable tenant-scoped transaction. Application authorisation requires `webhooks:read`, `webhooks:configure` or `webhooks:replay` as appropriate. Existing immutable grants do not gain these permissions automatically.

`transport/httpapi.WebhookRoutes` publishes the nine operations in the authoritative OpenAPI contract; `IdenqaClient.webhooks` and `idenqa webhook` consume them without Console or Cloud. Lists use ascending identifier order, a default of 25 and maximum of 100 entries, and signed cursors bound to tenant, collection, endpoint where applicable, and limit. Attempt inspection returns at most 20 records: safe diagnostic metadata plus, when the receiver returned content, a `response_body` excerpt of at most 4096 bytes sanitised to valid UTF-8 with `response_truncated`, treated strictly as untrusted receiver content. Read DTOs exclude event bodies, wrapped keys, signing secrets and signature material. Mutation reasons are bounded non-sensitive codes. Responses are `Cache-Control: no-store`.

Command receipts use the current API composition's `IDENQA_VERIFICATION_IDEMPOTENCY_RETENTION` (default 24 hours). All mutations require principal-scoped `Idempotency-Key`; rotate and disable additionally require the current `expected_version`. Create and rotate return a 32-byte secret encoded as unpadded Base64URL only on their first successful response. Idempotent retries preserve the original safe metadata with `replayed: true` and omit `signing_secret`; secret material is never retained in idempotency receipts. This display-once exception is explicit in the public contract. If the initial response is lost, an authorised new rotation is the recovery path. Rotation rejects an active previous-key overlap instead of shortening the promised interval; overlap is bounded to 1–86400 seconds. The API reuses its provider-neutral evidence infrastructure key wrapper with the distinct webhook-secret purpose; create/rotate return unavailable when no wrapper is configured. Replay requires the API's Headgate installation/schema and runtime privileges to match the worker.

Public replay accepts an exhausted delivery and an enabled endpoint, preserves the original signed event identifier and exact body, and creates a new delivery identifier with `replay_of` and a fresh bounded delivery window. Migration 35 replaces the unconditional endpoint/event uniqueness constraint with uniqueness for original deliveries only; command idempotency prevents accidental replay duplication. The queue task pins the new delivery's persisted schedule and deadline. Receivers must continue deduplicating event identifiers across manual replay. Rollback to schema 34 is intentionally rejected while same-event replay rows make its older uniqueness constraint unsatisfiable; rollback must never delete those rows silently.

The 18 September 2026 webhook evolution decision is **Selected**, and its repository-owned runtime scope is implemented. `contracts/webhook/v1` owns the versioned public event catalogue: one canonical envelope (`id`, `type`, `schema_version`, `created_at`, `tenant_id`, `region`, `data`) with one JSON schema and compatibility fixture per event type and version. `data` carries a nested resource snapshot in the Persona-style shape: the resource `id` and `type`, status/outcome, full lifecycle timestamps, `checks[]` with provider/model provenance, assurance lists, tags, relationships and type-specific extracted fields, with `redacted_at` recording redaction. Extracted fields are populated at construction; delivery bodies are KMS-encrypted at rest and delivery inspection never returns raw values. Envelope signing, byte-stable retries and event-ID deduplication are unchanged. This supersedes the earlier reference-plus-summary-fields wording. **Implemented — 18 September 2026:** the nested snapshot, check arrays and construction-time extracted fields are emitted, and catalogue event bodies are KMS-wrapped under the distinct `delivery.webhook-body` purpose when the owning feature writes them. `webhook.fanout` copies the event wrapping unchanged to every subscribed endpoint, the delivery send boundary unwraps only to sign and transmit, and replay preserves the original event identity and wrapping. Integration coverage proves no plaintext `verification.completed` body rests in `webhook_events` or `webhook_deliveries`. Evolution is additive within a major `schema_version`; incompatible changes require a new version. Endpoints carry a validated `event_types` subscription list of exact dotted catalogue names or the single `*` entry, bounded to 64 entries and defaulting to `["verification.completed"]` on create so existing integrations keep their behaviour; `*` includes newly added catalogue events except explicit-only ones. Emission is outbox-first: each owning feature commits its domain transition, one reference-only event and a transactionally enqueued fanout task together. A fenced, resumable `webhook.fanout` task pages subscribed enabled endpoints by opaque identifier cursor in bounded batches, commits delivery intents and delivery tasks per batch, and restarts from the persisted cursor after a crash. This replaces the fail-closed 1024-endpoint transaction guard; one event never creates two intents for the same endpoint. An endpoint created or re-enabled while an event is still pending may receive that event; once fanout completes, exact replay never retroactively delivers the committed event to later subscribers. The selected v1 catalogue is the v0.6 section 30.1 list plus `verification.cancelled`, `decision.corrected` and `provider.degraded` (emitted on health transitions into degraded/not_ready now that provider health routing exists). `webhooks:configure` and `webhooks:read` continue to gate subscription changes and inspection respectively.

**Selected — 19 September 2026, endpoint version and retention:** Every endpoint pins one supported envelope `schema_version`; existing and newly created endpoints default to exact version `1.0`. Configuration rejects unsupported versions. Fanout delivers only an event whose stored schema version matches the endpoint pin and fails closed otherwise; support for a future incompatible version requires an explicit version projector or separately stored representation and is not inferred by fanout. Payload-bearing webhook events, deliveries and attempt diagnostics expire after seven days by default, may be shortened by tenant policy under the deployment cap, and remain protected by applicable legal holds. Expiry removes encrypted bodies and attempt excerpts; minimal reference-only event/delivery tombstone metadata remains for the selected 365-day workflow-metadata period. List and SSE omit expired payloads, and replay is unavailable after payload expiry. Attempt diagnostics expire with their delivery. Bounded maintenance discovery, purge/redaction, replay denial and tombstone authorship must be idempotent and recoverable through owned Headgate maintenance work.

The CLI obtains API credentials from `--api-key-file` or `IDENQA_API_KEY`. Create/rotate reserve a new exclusive owner-only `--secret-out` file before calling the API, write signing material there and print safe metadata only. Rotation, disablement and replay require `--confirm`. Redirects and unbounded responses are refused; operational failures have runtime exit status. Production KMS selection is resolved as AWS KMS with AWS Secrets Manager for secret references. Endpoint version pinning and webhook retention follow the selected 19 September contract above.

**Selected — 19 September 2026, developer event stream and local forwarding:** `webhooks:read` gates a read-only tenant event feed; no new permission is added. `GET /v1/webhook-events` pages canonical catalogue events in ascending durable insertion-sequence order with a signed opaque cursor and returns each event's decrypted canonical envelope, omitting events whose payload retention has expired; `GET /v1/webhook-events/stream` streams the same events over `text/event-stream` with `Last-Event-ID` resume, periodic heartbeat comments, and an optional validated `event_types` filter. Wake-up follows the selected LISTEN/NOTIFY-as-optimisation pattern: one API-process notification connection for the delivery channel, notifications carry the tenant identifier only, connected streams always reread the durable table, and a bounded polling fallback preserves correctness when notifications are lost. Bodies are unwrapped only at this authorised read boundary under the delivery body purpose; endpoint delivery signing, endpoint secrets, delivery inspection and attempt inspection are unchanged and still never return payloads. The open-source `idenqa webhook listen` CLI consumes the stream, prints safe event summaries or `--json` canonical envelopes, reconnects with bounded backoff and resume, and with `--forward-to` forwards exact bytes to a loopback URL signed locally with the canonical v1 `Idenqa-Signature`, `Idenqa-Timestamp` and `Idenqa-Event-ID` scheme. Forwarding requires exactly one of `--secret-file` (an existing 32-byte secret) or `--secret-out` (a new owner-only generated secret); generated secrets persist across restarts so receiver verification configuration stays stable, and `--print-secret` prints only the configured secret and exits. Redirects are refused, forwarded responses are bounded, and `--skip-verify` permits a self-signed HTTPS forward target in development only.

**Implemented — 19 September 2026, local listener refinements:** repeatable `--forward-header "Name: Value"` entries are validated once (bounded token names and values, no CR/LF, duplicates and reserved signature, content and transport headers rejected) and applied after the mandatory canonical headers. `--load-from-webhooks-api` pages `GET /v1/webhook-endpoints`, skips disabled endpoints and derives the union of their `event_types` subscriptions, collapsing to `*` when any enabled endpoint subscribes to all events; it is mutually exclusive with an explicit `--event-types` filter. `--thin` projects each canonical envelope locally to a reference-only body that preserves envelope metadata and reduces `data` to `id`, `type` and `_id`-suffixed reference keys, signing the projected bytes with the same canonical v1 headers; server-side envelopes, delivery signing and endpoint subscriptions are unchanged. `--backfill` starts the stream at `Last-Event-ID: 0`, replaying all retained durable history before continuing live, with the existing reconnect, bounded duplicate suppression and receiver event-ID deduplication. Server-side retention still bounds recoverable history to the seven-day payload window.

### 6.14 `proposal` — AI-native orchestration (Mixed status)

**Status:** The model-agnostic provider boundary is **Selected** and the repository baseline described below is implemented. Exact live provider/model routes and production enablement are not Selected; AI remains `disabled` by default and does not block version one. Implements gap-audit section 3 and architecture section 18.

`internal/proposal` owns the non-authoritative proposal lifecycle. `contracts/proposal/v1` (Proposed) owns the public `ProposalModel` port and immutable `AgentProposal` envelope. `contracts/model/v1` remains the signal-only contract (`evidence references -> scored signals -> deterministic policy`) and must not gain proposal semantics.

**Selected — 21 September 2026, model-agnostic provider boundary:** Core retains `contracts/proposal/v1.ProposalModel` as its provider-neutral non-deterministic port. `internal/proposal.Generator` is the narrower consuming port for schema-constrained text generation; exact logical `model_id` + upstream `model_version` + reviewed `prompt_version` routes bind that port to one adapter and never silently fall back to another model, prompt, or provider. `adapters/proposals/openaicompatible` uses the official OpenAI Go SDK for the OpenAI-compatible chat-completions protocol and is shared by OpenAI and conforming compatible endpoints. `adapters/proposals/anthropic` uses Anthropic's official Go SDK for Anthropic Messages tool use. Provider request/response types, SDKs and credentials do not enter domain or public contracts. Both adapters retain the owned destination-pinned and response-bounded HTTP client, resolve only `secret://` credential references, disable SDK retries and ambient credential/base-URL discovery, require schema-constrained output, and return provider-neutral usage/provenance metadata. Model output remains untrusted: Core validates it, rejects changed model versions or action sets, and authors the authoritative proposal envelope locally. Deterministic mode, allow-list, reference, authority, residency and generation-rate preflight runs before any outbound provider call. This selection does not select a particular provider, model, prompt, jurisdiction, or production enablement.

**Implemented — 21 September 2026, binding, lifecycle and accounting increment:** Each mounted route names exact tenant-owned immutable prompt/model registry IDs and versions plus their digests. Core validates those records under tenant scope, including the provider-neutral logical model ID and mounted instruction digest, before provider egress; missing, pre-binding or stale records fail closed without fallback. Tenant-facing API, TypeScript SDK and CLI operations register model revisions and administer CAS-protected activation, retirement and rollback with append-only actor/reason/time history. Versioned workflow modes pin the exact model revision, prompt revision and active lifecycle revision; retired or stale routes fail closed. Every provider invocation writes a content-free outcome receipt, including failed, rejected and invalid-output attempts, and the tenant usage report aggregates outcomes, provider-reported tokens, unreported-usage count and locally estimated micro-cost. Local estimates are not provider invoices; an attempt whose provider supplies no trustworthy usage remains visible as unreported but does not invent token or spend data.

The mounted runtime shape and fail-closed operating constraints are documented in [Proposal model runtime v0.1](proposal-model-runtime-v0.1.md).

**Proposed contract:**

- `AgentProposal` is immutable after creation with tenant-scoped `proposal_id`, `verification_id`, `automation_mode`, bounded `actions[]` (allow-listed `kind` + JSON-Schema `args`), `evidence_refs`/`signal_refs` (references only, no raw evidence/claims/credentials), pinned `model_id`/`model_version`/`prompt_version`/`context_digest`, `expires_at`, optional `supersedes`, `status` (`pending|approved|rejected|expired|cancelled|superseded`), `created_at`, and audit actor. Bounded contexts are redacted; raw evidence, biometric bytes, national identifiers, and high-cardinality PII never enter the envelope, task payload, log, trace, or audit bytes.
- `ProposalModel.Propose(ctx, BoundedContext) -> (AgentProposal, error)` is the only non-deterministic entry point. A sibling `contracts/proposal/v1` also defines `AcceptedCommand` — the deterministic, versioned command derived from an approved proposal. Replay re-executes the stored `AcceptedCommand` without re-querying the model.
- Action and argument schemas are closed, versioned, and allow-listed per tenant/workflow. Unknown `kind`, unknown fields, oversized `args`, duplicate keys, or missing `evidence_refs` fail closed. `evidence_refs` must exist in the session and be validated before persistence.

**Implemented persistence baseline:**

- PostgreSQL owns `proposals`, `accepted_commands`, workflow modes, immutable prompt/model records, activation current/history projections and content-free generation receipts with forced RLS and tenant predicates. Expected-version checks and monotonic proposal/configuration/activation versions protect consequential changes; activation history is append-only and rollback publishes a new revision. Migrations 51–53 and 72–74 plus the owned PostgreSQL adapters implement this baseline. No proposal background task is required by the current synchronous generation path; any future asynchronous proposal work must use the owned `platform/task` boundary.
- Lifecycle uses explicit UTC microsecond `occurred_at`, expected version, and bounded expiry/cancellation/supersession. Supersession appends a new proposal referencing `supersedes`; cancellation and expiry are terminal and append audit without mutating earlier rows. Exact proposal replay returns byte-exact original receipts.

**Proposed guardrails (deterministic, fail-closed):**

1. Output-schema validation (closed JSON Schema per `kind`).
2. Evidence/signal reference validation (must resolve to session-owned immutable observations/signals).
3. Tool and action allow-list validation.
4. Tenant-policy validation (workflow's configured `automation_mode` permits the `kind`).
5. Jurisdiction, residency, and processing-authority validation (no cross-region transfer, no `lawful_basis` selection, no consent override).
6. Cost and rate-limit validation.
7. Raw-evidence/sensitive-context rejection (no raw bytes, no biometric templates, no national identifier values).
8. Human approval when `kind` is high-risk or when `human_required` mode is set.
9. Deterministic execution of the stored `AcceptedCommand` only; direct tool execution by the model is prohibited.
10. Audit linkage — every guardrail decision, approval, and effect is an immutable audit record with tenant, actor, digest, and timestamp.

AI must never independently verify or reject a subject, grant evidence access, activate a policy, select a lawful basis, override or infer consent, change retention, transfer evidence across regions, confirm a sanctions match, or change biometric thresholds — enforced by guardrails, not by model instruction.

**Proposed automation modes (tenant/workflow, versioned, session-pinned):**

- `disabled` — `ProposalModel` not invoked; default for version one.
- `assist` — proposals visible in review workspace only; no auto-accept.
- `recommend` — guardrail-passed proposals surfaced as one-click approvals.
- `guardrailed_auto` — only low-risk allow-listed `kinds` may auto-accept after deterministic guardrail pass; else escalate.
- `human_required` — all proposals require explicit human approval before an `AcceptedCommand` is recorded.

Mode, allow-list version, cost limits, and prompt/model pins are immutable versions stored in tenant configuration and pinned at session creation (mirrors assurance-profile pinning). Unknown mode or kind fails closed with `rejected` and audit.

**Implemented repository operations and products; production acceptance remains separate:**

- Review copilot, adaptive-route proposals, natural-language policy drafts/diffs, adversarial policy scenarios, accessibility/exception-path proposals, unfamiliar-document layout proposals, prompt/model registries and lifecycle, content-free usage/cost reporting, and impact assessments are implemented behind the non-authoritative proposal boundary. Representative live-model evaluation, provider-invoice reconciliation, production monitoring/SLO evidence, hardened egress/deployment and independent guardrail acceptance remain M-6 gates. Tenant-local fraud hypotheses (`internal/fraud` proposals) remain a separate bounded non-AI proposal pattern and do not use `ProposalModel`.

**Boundaries:**

- Domain/application packages must not import HTTP routers, SQL drivers, task-library types, telemetry SDKs, object-store SDKs, KMS SDKs, or cloud-provider SDKs; `internal/proposal` follows the same rule.
- Interfaces are owned at the consuming boundary and remain narrow; `internal/verification`, `internal/policy`, and `internal/review` consume `proposal` ports, not vice versa for authoritative decisions.
- Package name is singular, lowercase; no `utils/helpers/common` packages.
- Public SDKs depend only on published contracts; they must not import `internal/proposal`.

---

## 7. Adapter placement inside a context

A context begins as one package:

```text
internal/verification/
  verification.go
  service.go
  repository.go
  errors.go
```

When adapters become substantial, they become child packages:

```text
internal/verification/
  verification.go
  service.go
  repository.go
  errors.go
  postgres/
  httpapi/
  worker/
```

The parent package owns the interfaces. Child packages import the parent to implement those interfaces. The parent must not import its adapters.

Shared infrastructure packages remain narrow:

- `platform/postgres` owns connection pools, transaction primitives, health checks, and shared database instrumentation.
- Context-specific SQL mapping stays with that context's PostgreSQL adapter.
- `transport/httpapi` owns router assembly, middleware, shared response encoding, and shared HTTP error presentation.
- `transport/realtime` owns WebSocket handshake, connection, framing, backpressure, and protocol adaptation; it does not own capture-session state.
- Context-specific routes and DTO mapping may live in context child packages.
- `platform/idempotency`, `platform/inbox`, and `platform/outbox` implement cross-transport delivery correctness without becoming domain models.
- `platform/task` adapts the selected background-work library.
- `platform/id`, `platform/clock`, `platform/health`, `platform/breaker`, and `platform/httpclient` provide narrow testable process primitives.
- `platform/telemetry` owns OTel providers, exporters, resource configuration, and instrumentation setup.

---

## 8. Dependency rules

| From              | May depend on                                                               | Must not depend on                                                    |
| ----------------- | --------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| `cmd/*`           | Matching bootstrap package and process primitives                           | Domain implementation details                                         |
| `bootstrap/*`     | Config, contexts, transports, platform adapters                             | Other command packages                                                |
| Bounded contexts  | Standard library, owned value types, explicitly shared contracts            | HTTP routers, SQL drivers, telemetry SDKs, cloud SDKs, task libraries |
| Context adapters  | The context whose port they implement and required infrastructure libraries | Unrelated context internals                                           |
| `transport/*`     | Context application APIs, generated transport types, HTTP/event libraries   | Database implementations                                              |
| `platform/*`      | Infrastructure libraries and narrow shared primitives                       | Domain business rules                                                 |
| `sdk/*`           | Published contracts and language-standard facilities                        | Go `internal/*` packages                                              |
| External adapters | Public provider/model SDK contracts                                         | Go `internal/*` packages                                              |

Additional rules:

- Import cycles are prohibited.
- Generic `utils`, `helpers`, `common`, and root-level `service` packages are prohibited.
- Package names are lowercase and singular.
- Interfaces are declared at the consumption boundary and kept small.
- Generated code is not hand-edited.
- Dependency direction is enforced in CI with an import-policy linter such as `depguard`.

---

## 9. Foundational technology contracts

The following mechanisms are architectural contracts rather than optional conveniences. They must be designed before feature packages depend on them. Not every mechanism requires a third-party dependency.

### 9.1 Distributed-correctness spine

A consequential command follows this logical path:

```text
Request or realtime command
          ↓
Authenticate principal and resolve tenant
          ↓
Authorise action and processing purpose
          ↓
Reserve idempotency key or command ID
          ↓
Begin PostgreSQL transaction
          ↓
Check aggregate version and fencing token
          ↓
Apply domain transition
          ↓
Write outbox event and durable job intent
          ↓
Commit
          ↓
Worker performs external effect
          ↓
Inbox deduplicates callback or result
          ↓
Next transaction advances state
          ↓
Realtime event and webhook delivery
```

The implementation distinguishes mechanisms that solve different failure modes:

| Mechanism                  | Failure mode addressed                                                |
| -------------------------- | --------------------------------------------------------------------- |
| Idempotency                | The same command is retried by a client or SDK                        |
| Optimistic concurrency     | Different commands race to update the same resource                   |
| Inbox deduplication        | The same callback or inbound event arrives repeatedly                 |
| Transactional outbox       | A state mutation and its event must commit together                   |
| Fencing token              | A stale worker resumes after its lease was superseded                 |
| Idempotent external effect | A provider call or webhook delivery is retried                        |
| Replay                     | An authorised operator intentionally reprocesses an event or delivery |
| Reconciliation             | Idenqa and an external system disagree about state                    |

Idenqa promises at-least-once execution with idempotent effects. It does not claim exactly-once delivery across external systems.

### 9.2 Identifiers, time, and error taxonomy

Identifiers use typed, opaque, prefixed values rather than unvalidated strings. The prefix communicates resource type at operational boundaries, for example `ten_`, `sub_`, `ver_`, `evd_`, `evt_`, `cmd_`, and `req_`. Prefixes are part of the public contract and are not reused for a different resource type.

ID generation uses cryptographic entropy. Domain packages expose their own ID types or constructors; they do not let a shared ULID type erase resource-type safety.

Time rules:

- Persistent timestamps are UTC.
- A clock is injected where behaviour depends on time.
- Expiry comparisons, retry scheduling, retention, and audit timestamps define clock-skew tolerances.
- Policy evaluation receives an explicit evaluation time and never reads the system clock directly.
- Tests use deterministic clocks rather than sleeps.

Errors are classified independently from their transport representation:

- Invalid input
- Unauthenticated
- Unauthorised or prohibited
- Not found without cross-tenant disclosure
- Conflict or stale version
- Idempotency conflict or operation still in progress
- Rate limited or quota exhausted
- Retryable dependency failure
- Terminal dependency rejection
- Inconclusive result
- Internal operational failure

Each public error has a stable code. Internal causes remain wrapped for logs and traces but are not exposed to capture clients or tenant integrations.

### 9.3 Transaction and concurrency model

`platform/postgres` owns a narrow transaction runner implemented with pgx. Context repositories accept the transaction abstraction they consume; domain packages never depend on pgx types.

Before implementation, each operation class declares:

- Transaction isolation level
- Aggregate version or compare-and-swap condition
- Whether row locking is allowed
- Whether a PostgreSQL advisory lock is required and how its key is derived
- Retry behaviour for serialisation or deadlock failures
- Maximum transaction duration
- Which outbox records and job intents commit with the mutation

Mutable aggregates carry an explicit version. HTTP uses `ETag` and `If-Match` where the caller controls the mutation; workers use expected versions and fencing tokens. Distributed Redis locks never protect domain invariants.

### 9.4 Idempotency contract

`platform/idempotency` supplies one durable model used by HTTP commands, WebSocket commands, background jobs, provider attempts, webhook receipts, and authorised administrative actions.

An idempotency record includes at least:

- Tenant
- Authenticated principal or credential
- Operation identity, HTTP method and route where applicable
- Idempotency key or command ID
- Canonical request fingerprint
- State: `pending` or `completed`
- Safe result status and response or resource reference
- Creation, completion, and expiry timestamps

Required behaviour:

- The same scoped key and fingerprint returns the original committed result.
- Reusing a key with different input returns a stable conflict.
- Concurrent duplicates cannot execute the mutation twice.
- The idempotency reservation, domain mutation, and outbox records share a PostgreSQL transaction where the operation permits it.
- A duplicate still in progress receives an explicit retryable response rather than starting another operation.
- Retention is declared per operation class.
- Raw evidence or unnecessary PII is not retained merely to reproduce a response.
- Failures before a committed effect may be retried; committed outcomes are replayed according to the operation contract.

HTTP clients use `Idempotency-Key`. Realtime clients use a stable `command_id`. Provider requests use a stable attempt ID and provider idempotency key. Webhook consumers deduplicate by the signed event ID, including manual replay with a new delivery ID. Other callbacks follow their published attempt identity.

For capture-profile commands, the selected initial contract is a configurable 24-hour retention period. The reservation, profile mutation, safe application-result snapshot, and audit event commit in one PostgreSQL transaction. A transaction-scoped advisory lock derived from the complete idempotency scope suppresses concurrent execution of the same key; profile consistency still uses an explicit aggregate version and SQL compare-and-swap. Other operation classes retain independently declared policies.

Capture-profile resources use stable `prf_` identifiers and positive numeric revisions. A profile has at most one mutable draft. Published revisions are immutable; creating a superseding draft leaves the currently published revision active until the replacement is successfully published. Deactivation preserves every revision and audit record.

Public list cursors are short-lived integrity-protected tokens. Their payload binds the tenant, canonical query identity, expiry, and last persisted sort position. HMAC-SHA-256 keys are versioned, independently configured, and purpose-separated from API-key peppers; key retirement must allow every cursor issued under that version to expire first.

### 9.5 Inbox, outbox, and event envelope

`platform/outbox` atomically records an event or delivery intent with a domain transition. Dispatch is at least once. `platform/inbox` records inbound callbacks and events before their consequential effect is applied.

Every durable event envelope contains:

- Event ID
- Event type and schema version
- Tenant and region
- Aggregate type, aggregate ID, aggregate version, and per-aggregate sequence
- Correlation and causation IDs
- Safe principal or actor reference
- Occurred and recorded timestamps
- Safe payload
- Trace context where propagation is appropriate

The event contract defines ordering scope, retention, replay, poison-event quarantine, compatibility, and redaction. Event payloads contain references instead of raw evidence wherever possible.

### 9.6 Background execution

`platform/task` adapts Headgate's Go SDK and PostgreSQL driver. Idenqa-owned task types contain opaque identifiers and stable payload versions; Headgate types never appear in domain packages or public contracts.

The execution contract includes transactional enqueueing, uniqueness, delayed work, retry budgets, backoff with jitter, leases, heartbeats, fencing, cancellation, graceful drain, queue-level concurrency, poison-work quarantine, payload migration, telemetry hooks, and a deterministic test driver.

Rolling deployments declare the job and workflow versions each worker understands. A worker that cannot interpret claimed work releases or quarantines it rather than guessing.

The selected deployment layout uses a dedicated `headgate` PostgreSQL schema in the same database as Idenqa application state. A stable, deployment-unique installation identity scopes worker identities, singleton duties, and telemetry attributes. Headgate schema changes are applied only through an explicit deployment or `idenqa migrate headgate` operation using the migrations pinned with the selected Headgate release; `api` and `worker` startup validate compatibility but never migrate automatically. Deployment provisioning grants the restricted runtime role `USAGE` on that schema plus only the table and sequence privileges required by Headgate; schema ownership and migration authority remain with the administrative role.

The initial logical queues are `verification`, `evidence`, `delivery`, and `maintenance`. Tenant-owned work uses the tenant ID as its partition key. Installation-wide maintenance uses an explicit system partition rather than an empty tenant surrogate. Retention is mandatory on every owned task definition and may vary by task within the bounded owned contract; there is no implicit keep-forever default.

Headgate's Go runtime requires compile-time dispatch kinds, while Idenqa's owned registry resolves runtime task-name and payload-version pairs. The adapter therefore uses one private carrier kind per selected queue. The carrier is never a public task identity: the original Idenqa task name and exact version remain durable envelope metadata, and the bridge reconstructs the owned immutable intent before resolving a handler. A worker with no handler for the task name snoozes the job for a rolling deployment; a worker that supports the name but not that exact version quarantines it rather than guessing.

The initial process defaults are configurable rather than protocol constants: `verification=8`, `evidence=4`, `delivery=8`, and `maintenance=2` process-local workers; a 30-second lease; a 25-second graceful-shutdown bound; empty-poll backoff from 50 milliseconds through 2 seconds; crash quarantine after three crash-attributed attempts; and deployment-wide fallback retry backoff from a one-second base through a one-hour cap. Fleet rate defaults per minute are `verification=120` with burst 20, `evidence=60` with burst 10, `delivery=300` with burst 50, and `maintenance=30` with burst 5. Per-tenant partition concurrency ceilings are respectively 4, 2, 8, and 1. Saturation queues work. Every value is validated deployment configuration and workers idempotently reconcile the same fleet policy before admission. A zero memory limit disables Headgate's rolling-restart memory guard.

The owned task contract retains each task's maximum attempts, initial backoff, maximum backoff, and jitter. Headgate v0.1.10 enforces the maximum-attempt budget but does not invoke its declared `Config.RetryPolicy`. The Idenqa Store decorator therefore reconstructs the policy from each claimed durable envelope and passes the exact owned delay through Headgate's supported Ack override. The delay uses the task identity and one-based attempt, preserving deterministic-driver parity across worker restarts. Deployment-wide pgx base and cap remain a fallback for non-Idenqa retries and crash recovery rather than replacing task policy.

The decorator must preserve `TransactionalStore` only when the selected backend supplies it. Transactional verification handlers prepare external work outside PostgreSQL, then use Headgate `Job.Once` so its effect claim, the application mutation, and current-lease `CompleteTx` succeed or roll back together. Retry bookkeeping is released only after that transaction commits.

Headgate lifecycle events feed Idenqa-owned telemetry hooks using the OpenTelemetry providers created by process composition. Headgate does not install exporters, sampling, resources, or global providers, and the optional `headgateotel` module is not required for the initial adapter.

### 9.7 Realtime capture control channel

An active capture session uses a versioned WebSocket control channel where bidirectional communication is required. SSE is not the primary capture transport. It may still be used later for one-way operational views.

Transport responsibilities are divided as follows:

| Flow                                                                   | Transport                             |
| ---------------------------------------------------------------------- | ------------------------------------- |
| Create, resume, recover, and issue a connection ticket                 | REST                                  |
| Live capture commands, challenges, acknowledgements, and safe progress | WebSocket                             |
| Evidence and large asset transfer                                      | Direct HTTP upload                    |
| Authoritative snapshot after a gap                                     | REST                                  |
| Final tenant integration notification                                  | Signed webhook                        |
| WebSocket unavailable                                                  | REST commands and conditional polling |
| Camera, NFC, and byte-upload progress                                  | Local SDK events                      |

Connection bootstrap:

1. The SDK authenticates with its short-lived capture-session token.
2. `POST /v1/capture/connections` infers the verification from that capture principal and returns a display-once WebSocket URL containing a single-use connection ticket, the supported protocol, and expiry.
3. The client connects with the `idenqa.capture.v1` subprotocol.
4. The server atomically redeems the ticket and validates its tenant, verification, capture-token record, region, exact browser origin or native application identity, and protocol binding.

The ticket is not the capture-session token. It defaults to a 30-second lifetime within a configurable 10–60-second range. The display-once `idq_wst_v1` credential contains a 256-bit random secret; PostgreSQL stores only its domain-separated SHA-256 digest and digest-format version. Issuance takes a shared authority lock and succeeds only when the exact capture token and session remain active through ticket expiry. Redemption is one conditional update under the same authority lock, so it linearizes against capture-token revocation and permits exactly one concurrent winner. Browser WebSocket constructors cannot attach an `Authorization` header, so the display-once URL carries the ticket in its query. The full URL and query string must be redacted from application, reverse-proxy, access, analytics, and trace data. `Sec-WebSocket-Protocol` identifies the application protocol and does not carry credentials.

The selected browser issuance adapter requires exactly one allowed HTTP `Origin` and binds it independently of CORS. Missing, duplicate, malformed, or disallowed values fail before persistence. `IDENQA_REGION` explicitly identifies the serving regional data plane, `IDENQA_REALTIME_WEBSOCKET_URL` supplies the exact public `ws` or `wss` `/v1/capture/socket` endpoint, and `IDENQA_REALTIME_TICKET_LIFETIME` defaults to 30 seconds within the protocol-owned 10–60-second bound. The URL is never derived from `Host` or forwarding headers, and the region is never inferred from IP, locale, or device language. At least one `IDENQA_HTTP_CORS_ALLOWED_ORIGINS` entry is required for browser capture. A successful response is `Cache-Control: no-store`. Connection issuance deliberately has no idempotency key because replay would redisclose secret material; a retry creates a new independently expiring ticket. This deployment-region binding does not resolve D-013's separate retention and session region-pinning policy.

Every realtime message contains a protocol version, `msg_` message ID, type, `ver_` verification ID, connection-local sender sequence, `cmd_` command ID for consequential messages, optional correlation and causation identifiers, UTC timestamp, and typed payload. The public v1 schema and protocol notes live under `contracts/capture/realtime/v1`; Go transport adapters map that wire contract to immutable owned types under `internal/realtime`.

The closed initial catalogue is `client.hello`, `server.welcome`, `server.event_ack`, `capture.step.started`, `capture.step.failed`, `capture.step.cancelled`, `capture.command`, `capture.command_result`, `challenge.request`, `challenge.response`, `challenge.cancelled`, `capture.progress`, `session.state_changed`, `session.resync_required`, `server.draining`, `command.accepted`, and `command.rejected`. Additive optional fields may evolve within v1; removal, changed meaning, or changed required behaviour requires the `idenqa.capture.v2` subprotocol.

Protocol lifecycle includes:

- `client.hello` with SDK version, supported protocol versions, the connection-local acknowledgement, and the last acknowledged durable event cursor
- `server.welcome` with selected protocol, connection ID, current session version, and heartbeat settings
- Connection-local client and server sequences beginning at one independently in each direction
- `session.resync_required` when the replay cursor is unavailable
- Explicit acknowledgements for consequential client commands
- Bounded outbound queues and slow-consumer handling
- Heartbeats, idle timeouts, bounded reconnect backoff, and jitter
- Graceful server drain with reconnect instructions

Connection sequences remain local and reset on every socket. The selected R-02
contract adds an optional verification-local `event_cursor` only to safe
durable server messages, carries `last_acknowledged_event_cursor` in the next
hello, and persists monotonic acknowledgement for the exact capture-token
record. PostgreSQL assigns ordered cursors and stores capture-visible events;
application adapters commit a consequential state transition, its outbox
intent, and its durable capture notification atomically. A cursor ahead of the
stream or below the retained floor requires `session.resync_required` and
authoritative REST recovery.

PostgreSQL `LISTEN/NOTIFY` is selected only as a process-wide wake-up
optimisation. Connected nodes always reread the durable event table and retain
a bounded polling path when notifications are lost or unavailable. Reconnect
may reach any API node and correctness does not require sticky sessions or
Redis. `GET /v1/capture/progress` supplies a strong opaque ETag and supports
`If-None-Match`; the TypeScript SDK and Capture Web use conditional polling
after bounded WebSocket reconnect exhaustion. Raw evidence remains excluded
from the durable stream, acknowledgement records, notification payloads, and
REST progress projection.

Raw images, video frames, NFC payloads, biometric templates, provider secrets, and unrestricted provider results never enter realtime messages. WebSocket compression is disabled initially because messages are small and may mix security-sensitive challenge material.

The selected defaults are a 16 KiB maximum message, outbound queue depth 64, at most 16 unacknowledged commands, 5-second hello and write deadlines, 15-second native ping interval, 10-second pong deadline, 45-second idle timeout, and 30-minute connection lifetime capped by session expiry. Deployment configuration is validated within protocol-owned bounds: 4–64 KiB messages, queue depth 16–256, unacknowledged limit 1–64 and never above queue depth, hello/write deadlines 1–10 seconds, ping 5–30 seconds, pong 5–15 seconds, idle at least ping plus pong and at most two minutes, and connection lifetime 5–60 minutes.

`observe(session)` merges safe server events with local device events and exposes an idiomatic stream per platform: TypeScript `AsyncIterable`, Swift `AsyncStream`, Kotlin `Flow`, Dart `Stream`, and typed React Native events. Flutter and React Native bridges receive only safe events; native SDKs own the connection and raw capture pipeline.

### 9.8 API, webhook, and evidence-transfer conventions

The HTTP API standard must define resource naming, versioning, cursor pagination, filtering, sorting, request-size limits, unknown-field behaviour, problem details, idempotency, conditional mutation, rate-limit headers, deprecation, CORS, CSRF, and correlation IDs.

The initial browser boundary is deny-by-default for cross-origin requests. Self-hosted operators configure exact HTTP or HTTPS origins for capture pages and browser SDK consumers; wildcard origins and origins containing credentials, paths, query strings, or fragments are rejected. Approved preflights use a fixed method and header allow-list and do not enable credentialed cookies. WebSocket upgrades bypass the ordinary short HTTP-handler deadline and receive protocol-specific lifetime, idle, and message deadlines in the realtime adapter.

**Selected capture-profile model:** a tenant configures what evidence a verification requires and which acquisition methods a subject may use. Evidence type and acquisition method are separate concepts. A profile is versioned, validated before activation, and copied into an immutable requirement snapshot when a verification session is created. Later profile edits do not change an active session.

Each requirement declares:

- a stable requirement ID, evidence type, purpose, and required artefacts;
- an `any_of` or `all_of` acquisition strategy and the allowed acquisition methods;
- required assurance properties such as freshness, provenance, passive or active liveness, and capture integrity;
- policy-approved fallbacks and the conditions under which they are available;
- document, country, media, quality, size, duration, retention, consent, and region constraints where applicable.

For example, `selfie_image` may allow `file_upload`, `live_camera`, or either method. Requiring both submissions uses `all_of`; offering both as choices uses `any_of`. An uploaded selfie may satisfy face matching but cannot satisfy active liveness or trusted live-capture requirements. Likewise, `document_image`, `document_chip`, and `document_barcode` are distinct evidence types because an uploaded image does not provide NFC chip provenance.

The platform maintains a versioned evidence-type and acquisition-method registry. SDKs advertise implemented capture capabilities, while provider and model adapters declare the evidence and assurance they accept. An SDK capability advertisement is routing input, not proof that an assurance property was achieved. Profile activation and session creation fail with actionable validation errors when no permitted method can satisfy a requirement. Capability negotiation may choose only a profile-approved method or fallback; the client cannot weaken the session snapshot.

**Selected registry contract:** portable v1 profile and registry documents live under `contracts/capture-profile/v1`. A profile pins the registry schema version, positive revision, and canonical SHA-256 content digest. Idenqa-owned names use `idenqa.<kind>.<name>` for the `evidence`, `artefact`, `method`, `purpose`, `assurance`, and `constraint` kinds. Extensions require an owner-controlled namespace with at least two segments before the kind, such as `com.example.method.secure_camera`; they cannot use or nest below `idenqa`. Published registry revisions are immutable, unknown or unpinned definitions fail closed, and a changed meaning requires a new name or revision and digest. Extension definitions may add vocabulary but cannot redefine reserved names or acquire trust merely through an SDK capability advertisement.

Every `any_of` branch independently produces the required artefacts and acquisition assurance. Every `all_of` method produces the required artefacts while their assurance capabilities may combine. Policy-approved fallbacks are validated against the same requirements and cannot reduce assurance. SDK capabilities only narrow eligible methods; the evidence recorded for an actual acquisition determines achieved assurance.

The public mobile acquisition-plan v1 schema lives under `contracts/capture/acquisition/v1`. It carries ordered requirements with one evidence type, artefact, `idenqa.method.live_camera`, camera selection, bounded local quality policy, and optional ordered liveness prompts. Swift and Kotlin implement equivalent acquisition coordinators over their native camera sources. The additive Pan-African evidence registry is revision 2; revision 1 and its digest remain unchanged for already pinned sessions. Local image measurements, face count, and a completed prompt transcript are capture metadata only and never establish PAD, face-match, document-authenticity, MRZ, or barcode assurance; those require a declared provider/model result. NFC and voice remain absent from v1.

Canonical v1 serialisation is compact UTF-8 JSON. Ordered requirements, acquisition methods, and fallbacks retain their semantic order; set-valued artefacts, assurances, constraint entries, fallback conditions, registry entries, and string-list constraint values are sorted. Constraint values are limited to strings, non-negative integers, booleans, and unique string lists. Richer shapes require a future schema version instead of arbitrary JSON. Content digests use lowercase `sha256:<hex>` over canonical bytes.

Uploads are requirement-driven rather than arbitrary. The server issues an upload grant for a specific session requirement and artefact, including its accepted method, media and signature constraints, maximum size or duration, digest requirement, expiry, encryption and retention class, and region. The server records the method actually used and the assurance evidence it can legitimately establish.

Webhook delivery defines canonical signing input, timestamp and replay window, endpoint secret rotation, attempt ordering, retry schedule, disablement, manual replay, payload retention, DNS rebinding protection, SSRF protection, and safe diagnostics.

The selected v1 evidence-upload protocol uses requirement-bound single-request HTTP uploads directly from capture clients to Idenqa evidence ingress. Idenqa streams plaintext through application encryption into the owned object-store boundary; tenant backends, WebSockets, PostgreSQL, logs, tasks, and storage-direct plaintext URLs never carry raw evidence. An interrupted attempt restarts the complete body under the same durable intent rather than persisting plaintext or introducing a second chunk-encryption format.

Each opaque `upl_` intent immutably binds tenant, capture principal, verification, subject, evidence identity, exact profile snapshot, requirement, evidence type, artefact, actual acquisition method, legitimate assurances, accepted media, expected bytes and SHA-256 digest, region, retention, and encryption purpose. The default effective maximum is 16 MiB, bounded by a deployment ceiling configurable from 1 MiB through 64 MiB and narrowed where present by the tenant profile. JPEG and PNG are the initial deployment allow-list. Intent lifetime defaults to 15 minutes within a configurable 5–60-minute range and never exceeds session expiry; a fenced attempt defaults to a 10-minute maximum duration.

Initiation is idempotent. The upload requires `Content-Length`, exact canonical `Content-Type`, and RFC 9530 `Content-Digest` using SHA-256. The server independently counts and hashes plaintext, compares the digest without early-exit leakage, validates media signature, and verifies ciphertext size and checksum. Acceptance re-evaluates current processing authority and atomically commits the evidence record, terminal upload state, audit, and `evidence.ready.v1` outbox intent. Completed replay returns the existing result; changed immutable input conflicts. Failed, interrupted, rejected, and expired attempts create no available evidence and synchronously delete exact object versions or durably record tenant-scoped reconciliation. Malware and richer media checks remain owned policy hooks. Byte-offset resumability is conditional on a later encryption-compatible design for large media rather than part of E-03 v1.

The selected public v1 upload routes are `POST /v1/evidence-uploads` for capture-token intent issuance, `GET /v1/evidence-uploads/{uploadID}` for capture-bound lifecycle and ETag recovery, and `PUT /v1/evidence-uploads/{uploadID}` for the complete raw body. All return a safe upload resource and strong ETag; the resource omits the integrity digest and object-store identity. The recovery read uses the exact tenant, capture-token, and verification binding and hides mismatches as not found. An accepted replay may present the precondition used for the successful attempt or the returned terminal ETag and short-circuits before reading the repeated body. A concurrent duplicate conflicts, while a stale unrelated version fails its precondition. `GET /v1/capture/progress` separately returns the bounded authoritative list of accepted upload and evidence identifiers with their immutable requirement, evidence type, artefact, actual acquisition method, and optional fallback condition for the exact authenticated capture token and verification. It excludes raw evidence, filenames, digests, object identity, and subject data. The dependency-free TypeScript capture client exposes all four operations using caller-supplied `AbortSignal` values and, for writes, a raw `Blob`, canonical digest, idempotency key, and ETag.

The larger upload body ceiling and attempt deadline apply only to `PUT /v1/evidence-uploads/{uploadID}` when `{uploadID}` is a canonical `upl_` identifier. Intent-creation JSON, malformed identifiers, nested paths, and unrelated methods retain the ordinary HTTP body and request limits. The shared HTTP boundary applies the selected attempt deadline to both request context and connection I/O, with a small response/cleanup grace, while the server read/write timeout remains a longer outer backstop. WebSocket upgrades remain excluded because the realtime adapter owns their protocol-specific deadlines. The exception remains disabled unless a validated evidence-upload policy is explicitly supplied during runnable-process composition.

Capture completion and verification completion are distinct. Capture can finish when the server acknowledges all required evidence; detailed verification outcomes continue asynchronously to the authorised tenant backend. Fresh-page recovery reconstructs capture completion from PostgreSQL-backed accepted intents rather than browser persistence. The fallback condition used to issue an intent is immutable durable metadata so a runtime `capture_failed` branch can be reconstructed exactly. Recovery fails closed when a returned completion does not match exactly one step or repeats a logical step.

### 9.9 Resilience, rate limiting, and backpressure

Each external dependency declares timeout, retry owner, retry budget, backoff, circuit-breaker state, concurrency limit, and failure classification. Retries check context cancellation and never multiply silently across SDK, API, worker, and provider layers.

**Selected — 21 September 2026:** `internal/platform/breaker` owns the reusable bounded in-process circuit state machine and generic keyed registry. It owns policy validation, closed/open/half-open transitions, half-open probe admission, deterministic clock injection, bounded registry capacity and adoption of persisted open state. A consuming feature owns its key shape and validation, success/failure classification, persistence, metrics, fallback result and event meaning. The provider context therefore retains provider request classification, health persistence and `provider.degraded` emission while delegating the generic mechanism to the platform package. Other features must reuse this platform primitive when its semantics fit rather than copy the provider breaker; multi-process coordination still requires feature-owned durable state and must not be inferred from an in-process registry.

Rate limits and quotas are tenant-, credential-, route-, provider-, and workload-aware. Because Redis is not a baseline dependency, multi-instance rate limiting requires an explicit implementation decision. In-process limits may protect one process but cannot be represented as global quota enforcement.

Bounded resources include HTTP body size, database pool size, worker concurrency, per-provider concurrency, queued work, WebSocket message size, per-connection send queue, upload size, model input size, and policy evaluation budget. The system sheds non-essential load before exhausting resources required for authorised cleanup, audit, or recovery.

### 9.10 Process lifecycle and health

Every executable follows one lifecycle contract:

- Validate configuration before serving traffic.
- Expose startup, liveness, and readiness separately.
- Report dependency health without leaking credentials or topology.
- Stop accepting new HTTP work during drain.
- Notify or close WebSockets with a reconnectable shutdown reason.
- Stop claiming jobs, allow bounded completion, and release leases safely.
- Flush logs, metrics, traces, and audit checkpoints.
- Close database, storage, runner, and network resources in dependency order.
- Publish safe build, contract, migration, and version information.

`platform/health` owns the health model and aggregation. Bootstrap packages own shutdown ordering because they constructed the process graph.

The API bind host and port are configured separately; the safe default host is loopback. Direct server TLS is optional so the core works both behind a trusted TLS-terminating proxy and as its own TLS endpoint. File mode requires a complete certificate/private-key pair, validates it before startup and readiness, enforces TLS 1.2 or newer, and never logs the configured paths. The initial file adapter loads certificates at startup and requires a graceful restart for rotation. Proxy-header trust and any future reload, ACME, managed-certificate, or client-mTLS adapters remain explicit later contracts rather than implicit behaviour.

### 9.11 Security, tenant isolation, and data lifecycle

Tenant isolation is enforced below the HTTP layer. Every tenant-owned repository operation receives a verified tenant scope, includes it in reads and writes, and fails closed when it is absent. Resource lookup must not disclose whether another tenant owns an identifier. Tenant-owned PostgreSQL tables use row-level security as defence in depth in addition to mandatory application-level tenant scoping. Migrations, maintenance roles, workers, and administrative commands must not silently bypass this model; any privileged bypass is narrow, explicit, audited, and tested.

The selected PostgreSQL implementation forces RLS on tenant-owned tables and supplies tenant scope through transaction-local state. Runtime roles must neither own those tables nor hold `BYPASSRLS`. Database role and credential provisioning stays outside schema migrations so reviewed migrations never create login roles or passwords. CLI administration uses a separately supplied privileged database URL; successful bypass operations require bounded actor and reason assertions and atomically append an administrative audit record. API and worker deployments must not receive that credential. Authenticated administrative principals supersede the initial asserted actor when F-06 introduces access contexts.

`platform/crypto` owns reusable authenticated-encryption values and the small key-wrapping port consumed by encryption implementations. `platform/kms` owns provider-neutral wrapped-key metadata, while `platform/objectstore` carries exact versioned ciphertext-object references. Streaming encryption and object-storage interfaces are defined at their consuming application boundary. Implementations use reviewed standard primitives, Tink Streaming AEAD, or external KMS/HSM integrations; the core does not invent cryptographic algorithms. Provider, library, and cloud SDK types never cross these owned boundaries.

Evidence uses one unique random content key or Streaming AEAD keyset per object with no baseline data-key reuse or cache. The versioned ciphertext envelope records content algorithm and format, wrapped key material, stable provider-native key identity and version, wrapping algorithm, and the digest and schema of canonical non-secret authenticated context. The context binds tenant, verification, evidence, requirement, evidence type, artefact, acquisition method, object purpose, and content revision. Mismatched context, tenant, ciphertext, key version, or unavailable KMS fails closed without fallback.

The open-source local provider is a versioned file-backed 256-bit KEK keyring with an explicit active version, atomic replacement, and restrictive permissions suitable for a mounted secret. `idenqa evidence-key init --keyring-file <path>` is the selected setup surface: its parent directory must already exist, creation is atomic and non-overwriting, the file is owner-only, cancellation before mutation creates nothing, and output contains only the initial active version without the path, keyring identity, or key material. The file is then mounted into `api` or a provider distribution and referenced by `IDENQA_EVIDENCE_LOCAL_KEYRING_FILE`. Deterministic in-memory keys are test-only, and plaintext inline environment keys are not a production mode. Optional production KMS adapters wrap and unwrap only the small per-object content key or keyset through the same port, store immutable provider-native key identity rather than a mutable alias, and expose no cloud SDK types. CLI rotation remains unresolved until cadence, fleet rewrap completion, recovery validation, and retirement approval are selected; the existing provider primitive retains old versions and does not imply that policy.

New writes use the active KEK. Ordinary KEK rotation rewraps only the per-object content key or keyset; object re-encryption is reserved for a content-format or algorithm migration. Old keys remain until audited rewrap and recovery validation succeeds. Key destruction occurs only through the authorised retention/deletion workflow. Ciphertext alone enters object storage; provider-side storage encryption supplements application encryption. PostgreSQL, logs, traces, audit, outbox, idempotency, and task payloads never contain raw evidence, plaintext keys, or ordinary copies of restricted plaintext digests.

The selected rewrap execution primitive operates on one exact tenant-scoped evidence aggregate and expected version. It unwraps through the recorded provider identity, purpose, and authenticated context; wraps through the provider's active key; immediately unwraps the target and constant-time verifies that the keyset is unchanged; clears plaintext copies; and only then persists. PostgreSQL locks the expected aggregate version and atomically changes only provider-neutral wrapped-key metadata, aggregate version, and update time. The same transaction appends the normal aggregate event plus immutable principal, effective tenant actor, reason, and old/new key identities without storing either wrapped-key value. Ciphertext, object identity, content revision, integrity, lifecycle, and quarantine state remain unchanged, including when the asset is already quarantined. The open-source Cobra CLI exposes this primitive as `idenqa evidence-key rewrap` with an exact tenant, evidence ID, version, explicit confirmation, and complete attribution. Fleet discovery, batching, retry scheduling and verified destruction are implemented (migration 70), and AWS KMS is the selected first production KMS/HSM adapter; rotation cadence, the content-algorithm migration rollout and live AWS acceptance evidence remain unresolved under decision 15.

Security and data-lifecycle rules include:

- secrets and reusable credentials never appear in URLs, telemetry, audit payloads, or ordinary errors;
- sensitive values use redacting wrapper types where accidental formatting is a risk;
- logs, traces, events, jobs, and idempotency records follow explicit data-classification rules;
- evidence storage uses encryption at rest plus application-controlled envelope encryption where the threat model requires it;
- key rotation supports dual-read or verification windows and produces an auditable migration outcome;
- audit records are append-only at the application boundary and have a documented tamper-evidence and export-verification strategy;
- retention, legal hold, deletion, and anonymisation are explicit workflows rather than ad hoc table deletes;
- deletion propagates to object storage, derived artefacts, search or model outputs, delivery payloads, and other retained copies;
- backups have encryption, restore tests, retention, regional placement, and a documented deleted-data expiry boundary.

### 9.12 Observability, testing, and release foundations

The telemetry contract defines safe span names, metric names, attribute allow-lists, cardinality limits, redaction, sampling, task and realtime propagation, SLOs, and alert semantics. `github.com/riandyrn/otelchi` instruments the HTTP request and WebSocket upgrade; realtime message handling requires separate spans and metrics.

Foundation tests include deterministic clocks and IDs, transaction and idempotency races, outbox/inbox duplication, stale-worker fencing, WebSocket replay and resynchronisation, provider fault injection, migration compatibility, backup restoration, deletion-tombstone replay, tenant-isolation attempts, SDK conformance, and graceful-drain tests.

Release foundations include reproducible generation and builds, committed checksums, SBOMs, signed tags and containers, provenance, licence checks, vulnerability checks, mixed-version deployment tests, and documented recovery procedures.

---

## 10. Go module and workspace strategy

The repository starts as a small multi-module workspace:

1. The repository root is the core Go module.
2. `sdk/go` is a separate public Go SDK module.
3. Each independently released external provider, model, or infrastructure adapter may use its own Go module.
4. `go.work` connects these modules for local development.
5. Bounded contexts under `internal` are packages, not separate modules.

Illustrative workspace:

```text
go.work
go.mod
sdk/go/go.mod
adapters/providers/example/go.mod
adapters/models/example/go.mod
adapters/objectstore/s3/go.mod
distributions/s3/go.mod
```

The canonical root module path is **Selected** as `github.com/Mujhtech/idenqa`, matching the public repository. The S3 adapter module path is **Selected** as `github.com/Mujhtech/idenqa/adapters/objectstore/s3`. Future independently released SDK and other adapter module paths remain launch decisions and must match their published repository or submodule paths. Outside the narrow pre-release integration-test exception below, local `replace` directives must not be committed as a substitute for correctly versioned releases; `go.work` is used for local composition.

**Selected pre-release integration-test exception — 13 September 2026; extended 22 September 2026:** The root integration suite imports the public Go SDK to independently verify signed webhook delivery and exercise public administration resources over HTTP. Until `sdk/go` has its first compatible published version, the root module records a test-only `v0.0.0` requirement and a local replacement to `./sdk/go` so root `go mod tidy` remains reproducible. No production package or distributed binary may import the SDK through this edge. The replacement must be removed in favour of the first compatible published SDK version before an external release.

During pre-release development, `go.work` resolves the S3 adapter's import of the root-owned object-store contract and the S3 distribution's imports of both modules. Because no root or adapter version exists yet, these independent modules cannot record or tidy valid inter-module requirements without inventing unpublished versions. Before either module's first independent release, its `go.mod` must require the first compatible published dependencies, then pass standalone `go mod tidy`, `go mod verify`, tests, lint, and vulnerability checks with `GOWORK=off`. This is a release gate, not permission for either independent S3 module to commit a local `replace`.

---

## 11. Go runtime dependencies

### 11.1 HTTP, response, and realtime handling

| Package                              | Status       | Scope                                                                     |
| ------------------------------------ | ------------ | ------------------------------------------------------------------------- |
| `github.com/go-chi/chi/v5`           | **Selected** | HTTP routing and middleware composition                                   |
| `github.com/go-chi/cors`             | **Selected** | Standards-compliant browser CORS handling                                 |
| `github.com/go-chi/render`           | **Selected** | Request decoding and shared response rendering                            |
| `github.com/coder/websocket` v1.8.15 | **Selected** | Capture-session WebSocket upgrade, framing, deadlines, and close handling |
| `github.com/riandyrn/otelchi`        | **Selected** | Chi-aware OpenTelemetry server spans and HTTP metrics                     |

The HTTP boundary composes established middleware where its behaviour matches the Idenqa contract. `go-chi/cors` owns CORS header and preflight protocol handling; Idenqa owns validation of the configured exact-origin list and supplies a deny-by-default origin predicate. Credentials remain disabled. A disallowed origin receives no CORS permission headers, but CORS is not treated as authentication or authorisation and the underlying application request may still execute where the Fetch standard requires it.

Tenant API-key middleware accepts exactly one `Authorization` field using the case-insensitive Bearer scheme. It never falls back to URLs, query parameters, form values, cookies, or application-defined proxy headers. A successful `access.Context` is carried using an unexported Go context key and remains bound to the request cancellation chain. Missing, malformed, and invalid credentials return the same stable 401 problem with a minimal Bearer challenge. Insufficient scope is a stable 403 only after authentication, while cross-tenant and absent resource probes share the stable 404 response. Operational authentication failures are mapped to the generic 500 response; neither credentials nor wrapped infrastructure errors are written to ordinary request logs.

Route permission middleware is an early rejection layer, not the authorisation owner. Each consequential application operation must call `access.Context.Require` for its exact permission and pass the verified `tenant.Scope` into persistence. This rule applies equally when the operation is invoked from HTTP, WebSocket, CLI, worker, replay, or a future commercial surface.

Chi's generic `middleware.Recoverer` is not the selected recovery handler because it logs the recovered panic value and stack and emits only a bare HTTP 500 status. The small Idenqa recovery adapter instead preserves panic-value redaction, the stable JSON problem contract, request IDs, and response-write safety. Shared request logging must likewise use the Idenqa telemetry allow-list rather than logging raw paths, client addresses, user agents, query strings, credentials, or identity-derived values.

`github.com/coder/websocket` v1.8.15 is selected after focused release, maintenance, licence, module-graph, security, protocol, and concurrent-admission review. The signed current release is ISC-licensed, targets Go 1.23, has no transitive module requirements, passes the Autobahn protocol suite, and supplies context-bounded I/O, native ping/pong, close handling, and a hard message-read limit. It remains isolated inside `internal/transport/realtime`; domain and application packages depend on owned realtime ports and message contracts, not on library types.

Idenqa performs its own exact allowed-Origin comparison before upgrade because the library's origin facility intentionally supports patterns and same-host defaults. Only after exact Origin and `idenqa.capture.v1` offer validation does the adapter upgrade, atomically redeem the query ticket, and expose a bounded text-only connection to the protocol handler. Missing, malformed, unknown, mismatched, expired, revoked, and reused tickets share the same post-upgrade policy-violation close. Query shape and ticket material are never logged. Compression remains explicitly disabled because capture control messages are small and may coexist with secrets or user-supplied values.

The shared response package lives under:

```text
internal/transport/httpapi/
  respond/
    respond.go
  apierror/
    error.go
```

The response layer follows these rules:

- Successful resource responses return the resource directly.
- Collection responses may use a `data` plus pagination metadata envelope.
- Errors use an Idenqa problem-details representation with a stable code and request ID.
- Internal, database, provider, and model error strings are never exposed directly.
- Rendering errors are returned or recorded; they are not silently discarded.
- Created responses set `Location` where a stable resource URL exists.
- Rate-limit and availability responses support `Retry-After`.
- Authentication responses support `WWW-Authenticate`.
- Mutable resources support `ETag` and conditional requests where defined by the API contract.

Example error:

```json
{
  "type": "https://idenqa.dev/problems/processing-authority-required",
  "title": "Processing authority required",
  "status": 403,
  "code": "PROCESSING_AUTHORITY_REQUIRED",
  "detail": "A valid processing authority is required.",
  "request_id": "req_01J..."
}
```

### 11.2 Configuration

| Package                                 | Status          | Scope                                                             |
| --------------------------------------- | --------------- | ----------------------------------------------------------------- |
| `github.com/kelseyhightower/envconfig`  | **Selected**    | Decode `IDENQA_*` environment variables into typed configuration  |
| `github.com/joho/godotenv`              | **Selected**    | Explicit local-development and test `.env` loading                |
| `github.com/go-ozzo/ozzo-validation/v4` | **Conditional** | Typed configuration validation and selected cross-field DTO rules |

Configuration lives in `internal/config` and follows this order:

```text
explicitly requested .env file
              ↓
real process environment wins
              ↓
envconfig decodes IDENQA_* variables
              ↓
typed validation runs
```

Rules:

- Use `envconfig.Process`; startup errors are returned rather than converted into panics.
- Use the `IDENQA` prefix consistently.
- Reject unknown prefixed variables when supported so configuration typos fail fast.
- Never import `github.com/joho/godotenv/autoload`.
- Never use `godotenv.Overload` in application startup.
- Production does not depend on a `.env` file.
- Configuration and startup logs must redact secrets.
- Different processes validate the configuration they require instead of forcing every binary to provide every possible setting.

Evidence-upload deployment policy is decoded by a reusable process-neutral configuration block from `IDENQA_EVIDENCE_UPLOAD_MAXIMUM_BYTES`, `IDENQA_EVIDENCE_UPLOAD_INTENT_LIFETIME`, `IDENQA_EVIDENCE_UPLOAD_ATTEMPT_TIMEOUT`, and `IDENQA_EVIDENCE_UPLOAD_ALLOWED_MEDIA_TYPES`, then passed as one validated immutable `evidence.UploadPolicy` to issuance, ingress, and HTTP transport composition. The API configuration embeds this block; a future dedicated evidence process may embed the same block without duplicating rules. Defaults are 16 MiB, 15 minutes, 10 minutes, and `image/jpeg,image/png`. Startup rejects sizes outside 1–64 MiB, intent lifetimes outside 5–60 minutes, attempt timeouts outside 1–15 minutes, sub-millisecond durations, duplicate media values, empty media lists, and media outside the canonical v1 JPEG/PNG allow-list. Tenant profiles may narrow this deployment policy but never expand it.

Realtime browser bootstrap additionally requires an explicit lowercase deployment `IDENQA_REGION`, an exact query-free `IDENQA_REALTIME_WEBSOCKET_URL` ending at `/v1/capture/socket`, and at least one exact CORS origin. The ticket lifetime is configurable through `IDENQA_REALTIME_TICKET_LIFETIME`; protocol-owned validation rejects values outside 10–60 seconds. These values are required and validated by API process composition before it opens serving resources, then passed into owned realtime and HTTP boundaries rather than read from environment variables inside domain or application code. Administrative CLI commands that currently reuse the core database configuration schema do not require public WebSocket or browser-origin settings when none are supplied.

The root `api` and `worker` keep cloud-SDK-free local compositions selected for development and simple self-hosting. Evidence ingress is enabled only when `IDENQA_EVIDENCE_LOCAL_DIRECTORY` and `IDENQA_EVIDENCE_LOCAL_KEYRING_FILE` are both set to existing resources; partial pairs and surrounding whitespace fail validation. `IDENQA_EVIDENCE_PROTECTION_CLEANUP_TIMEOUT` defaults to five seconds and bounds cancellation-independent exact-object compensation. Provider distributions inject the same owned object, key, deletion, and lifecycle ports without setting a local storage directory. The selected production composition module is `github.com/Mujhtech/idenqa/distributions/s3`: its public `api` binary retains evidence ingress and composes the independently versioned S3 adapter plus Tink and the mounted local keyring, while its public `worker` binary injects the same S3 namespace into worker-owned exact evidence deletion. Both keep AWS dependencies outside the root module. Their strict combined environment schemas add required `IDENQA_S3_BUCKET` and `IDENQA_S3_REGION`; optional prefix, endpoint, path-style, explicit plain-HTTP opt-in, and a 30-second bounded ambiguous-upload cleanup timeout remain distribution-owned settings. The API requires the selected KMS configuration, and the worker reuses it when core delivery and rewrap duties are enabled. AWS credentials and custom certificate authorities use the SDK's standard external chain. A dedicated `evidence` process remains a later deployment option only when demonstrated isolation or scaling needs justify the extra service boundary.

Tenant API-key peppers use the selected `IDENQA_API_KEY_ACTIVE_PEPPER_VERSION` and `IDENQA_API_KEY_PEPPERS` configuration pair. The latter is a comma-separated set of `version=unpadded-base64url` entries; each decoded pepper is exactly 32 bytes, version zero and duplicate versions are invalid, and the active version must exist in the set. Both variables are mandatory for the `api` process and for CLI create or rotate operations because those surfaces compose API-key cryptography. CLI list and revoke do not require pepper material. Rotation adds a new version and makes it active while retaining older material for credentials that still reference it. Generic string, Go-syntax, JSON, configuration, and log rendering must redact the material.

Expiry and rotation policy are deployment-configurable rather than globally fixed. `IDENQA_API_KEY_ALLOW_NO_EXPIRY` defaults to false; callers must still make an explicit expiry or no-expiry choice. `IDENQA_API_KEY_MAXIMUM_LIFETIME` optionally caps fixed lifetimes, while `IDENQA_API_KEY_MAXIMUM_ROTATION_OVERLAP` bounds how long a predecessor remains usable after its successor is issued and is required when issuance operations are composed. A scheduled retirement is irreversible, but a predecessor remains active until its deadline and may be revoked immediately during the overlap.

Validation responsibilities remain distinct:

| Concern                                          | Owner                           |
| ------------------------------------------------ | ------------------------------- |
| HTTP payload shape                               | OpenAPI request validation      |
| Configuration and selected cross-field DTO rules | Ozzo when needed                |
| Identity and verification invariants             | Domain constructors and methods |
| Customer policy expressions                      | Policy engine and CEL           |

### 11.3 PostgreSQL and migrations

| Package                                | Status            | Scope                                                                        |
| -------------------------------------- | ----------------- | ---------------------------------------------------------------------------- |
| `github.com/jackc/pgx/v5`              | **Selected**      | PostgreSQL driver, pooling, transactions, and PostgreSQL-specific facilities |
| `sqlc`                                 | **Selected tool** | Generate typed Go query code from reviewed SQL                               |
| `github.com/golang-migrate/migrate/v4` | **Selected**      | Embedded schema migration library used by the Idenqa CLI                     |

The implementation uses explicit SQL rather than an ORM. PostgreSQL is authoritative for domain state, workflow state, attempts, timers, inbox entries, outbox entries, and durable job intent.

Reviewed SQL migrations are embedded in the `idenqa` operational binary and executed through `idenqa migrate`. API and worker startup must never apply migrations automatically. They may check connectivity and schema compatibility and must remain unready when the schema is incompatible. A restricted runtime role used by a process that performs this check must have `SELECT` on `public.schema_migrations` in addition to its narrow application schema and table grants; it must not receive migration authority. Production operations expose preflight, version inspection, and forward migration; rollback is restricted to explicit development or test use with confirmation. Migration metadata is owned at `public.schema_migrations` and must not depend on the connection role's mutable PostgreSQL search path. The standalone upstream migration CLI is not a required runtime or operator dependency.

### 11.4 Background work

| Package                                              | Status       | Scope                                                                 |
| ---------------------------------------------------- | ------------ | --------------------------------------------------------------------- |
| `github.com/mujhtech/headgate/go`                    | **Selected** | Headgate v0.1.10 Go task definitions, enqueueing, runners, and hooks  |
| `github.com/mujhtech/headgate/go/driver/headgatepgx` | **Selected** | Headgate v0.1.10 PostgreSQL backend and transactional pgx integration |
| `github.com/mujhtech/headgate/go/headgatemigrate`    | **Selected** | Headgate v0.1.10 migration-only engine used by the explicit CLI path  |

Headgate v0.1.10 is the selected production background-work system. Idenqa uses its PostgreSQL backend; Headgate's MySQL and Redis backends, Rust SDK, workflow module, UI, and optional integrations are not selected merely by choosing the core library. River and Asynq are not part of the Idenqa dependency set.

The migration module is now a direct, release-pinned dependency solely because `idenqa migrate headgate` invokes Headgate's official embedded PostgreSQL migrations and validation. The pgx driver's published graph also references Headgate's test module, whose support references MySQL and Redis clients. Those transitive modules are supply-chain inputs, not enabled Idenqa backends or runtime services. Runtime execution imports and configures only the core and pgx packages; dependency review and vulnerability scanning still cover the complete resolved module graph.

Headgate remains an execution mechanism rather than the source of truth for verification state. Headgate owns queue envelopes, scheduling, policy admission, claims, leases, attempts, and worker execution state. Idenqa owns verification and other business state, stable task meaning and payload versions, idempotent effects, inbox/outbox records, audit records, and reconciliation decisions. The adapter under `platform/task` translates between Idenqa-owned task types and Headgate jobs; Headgate types must not appear in domain packages, public contracts, or SDKs.

An Idenqa state change and its resulting job must commit atomically by using Headgate's supported PostgreSQL transaction adapter with the same application transaction. The adapter must not reproduce or write Headgate's internal tables directly. Jobs carry opaque Idenqa identifiers and safe control metadata, never raw evidence bytes, credentials, identity claims, or unrestricted trace baggage.

Headgate's at-least-once execution, caller-provided idempotency identifiers, uniqueness controls, bounded retries, crash-attempt accounting, leases, renewal, cancellation on lease loss, and fencing complement rather than replace application-level idempotency and optimistic concurrency. A worker must present the current Idenqa fence when committing a consequential effect; stale or duplicate executions must be harmless.

Required capabilities:

- Transactional enqueueing with domain changes
- Unique and idempotent jobs
- Delayed and scheduled work
- Retry classification and configurable backoff
- Leases and worker heartbeats
- Cancellation and graceful shutdown
- Queue-level concurrency and isolation
- Terminal-failure or dead-letter handling
- Versioned payloads
- Test helpers or a test driver
- Lifecycle hooks for logs, metrics, and traces
- Trace-context propagation from enqueue to execution

PostgreSQL is the selected Headgate backend and remains sufficient for durable recovery. Redis and Asynq are not baseline dependencies. Redis may be added later as an optional accelerator behind a separate owned adapter, but loss of Redis must not prevent recovery from PostgreSQL.

The selected W-02 configuration is: a stable deployment-unique installation identity; dedicated `headgate` PostgreSQL schema; explicit pinned Headgate migrations outside process startup; `verification`, `evidence`, `delivery`, and `maintenance` queues and rate classes; tenant-ID partitioning with an explicit system partition for installation-wide maintenance; mandatory task-specific bounded retention; exact task-specific retry timing; Idenqa-owned lifecycle telemetry connected to process-owned OpenTelemetry providers; and validated, deployment-configurable process concurrency, fleet rate/burst, per-tenant concurrency, lease, polling, crash, fallback retry, graceful-shutdown, and memory-guard controls. Concrete task-retention durations are selected with each task definition rather than guessed before those tasks exist.

### 11.5 Policy evaluation

| Package                            | Status       | Scope                                                                                                               |
| ---------------------------------- | ------------ | ------------------------------------------------------------------------------------------------------------------- |
| `github.com/google/cel-go` v0.31.0 | **Selected** | Typed, side-effect-free policy-expression evaluation confined to `internal/policy/cel` behind the Idenqa policy API |

Idenqa owns policy documents, allowed variables and functions, type environments, evaluation limits, snapshots, reason codes, versioning, and audit semantics. CEL is an implementation dependency, not the public policy contract.

The domain and PostgreSQL durability foundations contain no `cel-go` import. The selected adapter translates validated matches into Idenqa-owned requirement results but does not own snapshot, precedence, terminal-authorisation, lineage, persistence, activation or reproduction semantics. The v0.31.0 intake is Apache-2.0/BSD-3-Clause, supports the selected Go toolchain, is checksum-locked, follows the actively maintained upstream repository move to `cel-expr`, and is newer than the v0.30.0 fix for the published native-struct-field advisory. Idenqa does not use native-struct registration.

OPA is not an initial runtime service or dependency.

### 11.6 Security identifiers and JOSE

| Package                                    | Status       | Scope                                                                                  |
| ------------------------------------------ | ------------ | -------------------------------------------------------------------------------------- |
| `github.com/go-jose/go-jose/v4`            | **Proposed** | JWK, JWS, JWE, and JWT operations where standards require them                         |
| `github.com/oklog/ulid/v2`                 | **Selected** | Opaque, sortable public identifiers generated with cryptographic entropy               |
| `github.com/tink-crypto/tink-go/v2` v2.8.0 | **Selected** | Authenticated streaming encryption of large evidence objects behind Idenqa-owned ports |

General cryptographic operations use the Go standard library. Tink Go v2.8.0 provides the reviewed Streaming AEAD implementation for large evidence objects, initially using `AES256_GCM_HKDF_1MB`, but its types never cross `platform/crypto`. The v2.8.0 intake is Apache-2.0, compatible with the selected Go 1.27.1 toolchain, checksum-locked, and includes the upstream correction for silent truncation in the streaming reader. Provider-specific KMS and HSM SDKs remain isolated in adapter modules.

F-03 uses `github.com/oklog/ulid/v2` v2.1.2 behind Idenqa-owned prefixed value types and an injected generator. Domain packages expose resource-specific ID types rather than the library type. Production generation uses cryptographic, concurrency-safe monotonic entropy; clocks and entropy remain injectable for deterministic tests. ULID timestamps are not authoritative business timestamps, and pagination uses an authoritative ordering field plus the identifier as a stable tie-breaker.

### 11.7 CLI

| Package                          | Status       | Scope                                                                                          |
| -------------------------------- | ------------ | ---------------------------------------------------------------------------------------------- |
| `github.com/spf13/cobra` v1.10.2 | **Selected** | Shared command trees, flags, help, version output, and shell completion for public Go binaries |

`internal/cli` constructs the shared root conventions while each `internal/bootstrap/*` package owns its process-specific command tree and composition. Every execution receives a fresh tree, uses `RunE` for cancellable operational commands, writes only through Cobra's command output streams, and preserves exit code `2` for invalid usage separately from exit code `1` for operational failure. Production execution lets Cobra read process arguments; explicit argument injection exists only for tests and embedded callers. Viper is not selected. Configuration is loaded explicitly through `envconfig`, with `.env` support limited to `godotenv` at the bootstrap boundary.

The initial self-hosted CLI covers migrations and preflight checks, tenant and API-key management, health diagnostics, webhook replay, live webhook event streaming with local signed forwarding, failed-work inspection and recovery, retention execution, authorised export, and deletion workflows. These capabilities are not reserved for Console or Cloud.

### 11.8 Observability

| Package                                                                               | Status                        | Scope                                            |
| ------------------------------------------------------------------------------------- | ----------------------------- | ------------------------------------------------ |
| `log/slog`                                                                            | **Selected standard library** | Structured application logging                   |
| `go.opentelemetry.io/otel` module family                                              | **Selected**                  | Traces, metrics, propagation, SDK, and exporters |
| `github.com/riandyrn/otelchi`                                                         | **Selected**                  | Chi route instrumentation                        |
| `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc` v0.71.0 | **Selected**                  | gRPC runner client/server instrumentation        |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` v1.46.0             | **Selected**                  | OTLP/gRPC trace export                           |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` v1.46.0             | **Selected**                  | OTLP HTTP/protobuf trace export                  |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc` v1.46.0           | **Selected**                  | OTLP/gRPC metric export                          |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp` v1.46.0           | **Selected**                  | OTLP HTTP/protobuf metric export                 |

Observability rules:

- `platform/telemetry` configures providers, resources, exporters, sampling, and shutdown.
- HTTP span names and metric attributes use route patterns such as `/v1/verifications/{id}`, never raw paths.
- Subject, document, evidence, verification, and tenant identifiers are prohibited as metric labels.
- Sensitive values are not placed in baggage.
- Application logs include safe request, trace, and operation correlation without raw identity data.
- Background tasks propagate trace context or create span links according to task age and execution semantics.

The self-hosted default constructs and injects trace and metric providers but configures no exporter and makes no outbound connection. When enabled, each process selects exactly one vendor-neutral OTLP transport: `grpc` or `http/protobuf`; the core supports and tests both. A collector endpoint is explicit. TLS is mandatory except for an explicitly insecure loopback endpoint, and optional custom CA, server-name, and paired client-certificate/key settings support private trust and mTLS. Authentication headers use a bounded redacting configuration type and are never logged.

Trace sampling is parent-based with a configurable root ratio defaulting to 0.10. The trace batch processor uses a queue of 2,048 spans, batches of at most 512, a five-second batch delay, and a ten-second default export timeout. Metrics are not sampled and export periodically every 60 seconds by default. Export timeouts and metric intervals are bounded configuration; exporter transient retries use the OpenTelemetry SDK's bounded default policy. Invalid configuration fails process startup. Runtime collector failure may drop bounded diagnostic telemetry but must not expose data, create unbounded memory growth, or terminate request and worker processing. `log/slog` remains the application log sink; OTLP log export is not selected by this decision.

### 11.9 Runner contracts

| Package                               | Status                                                                         | Scope                                                                                                                                                                          |
| ------------------------------------- | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `google.golang.org/grpc` v1.83.2      | **Selected**                                                                   | Adapter-runner and model-runner RPC transport                                                                                                                                  |
| `google.golang.org/protobuf` v1.36.12 | **Selected**                                                                   | Versioned runner messages and generated Go types                                                                                                                               |
| `buf.build/go/protovalidate` v1.3.0   | **Selected runtime**                                                           | Generated-message boundary validation                                                                                                                                          |
| Buf CLI v1.72.0                       | **Selected tool**                                                              | Protobuf linting, breaking-change checks, and reproducible generation                                                                                                          |
| `protoc-gen-go` v1.36.12              | **Selected tool**                                                              | Reproducible Go message binding generation                                                                                                                                     |
| `protoc-gen-go-grpc` v1.6.2           | **Selected tool**                                                              | Reproducible Go gRPC binding generation                                                                                                                                        |
| OpenCV headless                       | **Reference 5.0.0.93 implemented**                                             | Image transforms/NMS inside `adapters/models/onnx`; both detector and PAD inference remain ONNX Runtime; [intake](pad-evaluation-v0.1.md#2-pinned-contextual-preparation)      |
| ONNX Runtime                          | **Selected runtime; reference 1.29.0 official Python CPU binding implemented** | Evaluation-only PAD under `adapters/models/onnx`; hash-locked native dependencies, no Go core linkage; see [intake and limits](onnx-runtime-v0.1.md#2-runtime-intake-and-pins) |

gRPC and Protobuf are used for isolated runner contracts, not as a requirement for the public customer API. HTTP remains the primary public API transport.

The reviewed runner intake found active upstream maintenance, a Go-version requirement compatible with Go 1.27.1, no direct OSV advisories for the exact selected versions, Apache-2.0 licensing for gRPC, Buf, Protovalidate, `protoc-gen-go-grpc`, and `otelgrpc`, and BSD-3-Clause licensing for Protobuf Go. The full reachability scan finds no vulnerable imported packages or called symbols. It separately reports GO-2026-5932 against the unmaintained `golang.org/x/crypto/openpgp` package in a required module, but Idenqa does not import that package and the advisory has no fixed release; the module remains monitored rather than being misreported as a reachable runner vulnerability. Protovalidate's current canonical Go module is `buf.build/go/protovalidate`; the retired GitHub import path must not be reintroduced. Generated Go bindings are committed under `internal/gen/proto/runner/v1`, while the authoritative source is the typed v1 schema under `contracts/runner`.

The v1 runner transport uses a per-runner bearer credential with the display form `idq_wrk_v1_<secret>`, where `secret` is 32 cryptographically random bytes encoded as unpadded Base64URL. It is accepted only as exactly one `Authorization: Bearer` gRPC metadata value over server-authenticated TLS 1.2 or newer. The client requires transport security before sending it; the server retains only SHA-256 digests in its authentication set after startup. One current and one previous credential may overlap for rotation. Missing, malformed, unknown, and removed credentials are non-disclosing; credential material must never enter Protobuf messages, URLs, logs, traces, metrics, audit payloads, or adapter contracts. Each remote runner has an explicit certificate-authority trust root and expected server name. Adapter and model runners now resolve and dynamically reload server credentials, gateway credentials, provider/model credentials and TLS identities from the selected secret provider with last-good fallback and overlap. API/worker outbound runner clients still use mounted credentials and trust files; resolving those through the secret provider with safe redial/overlap remains open. Client mTLS remains optional future scope.

Every runner RPC requires a caller deadline within a process-configured maximum capped at ten minutes. gRPC context cancellation reaches the adapter. The v1 wire ceiling is 320 KiB and the existing public result ceiling remains 256 KiB, enforced on both service and client adapters. Compression is not enabled. `otelgrpc` uses process-owned providers and W3C propagation; runner payload, credential, tenant, subject, verification, evidence, and grant values are never telemetry attributes or baggage.

### 11.10 Object storage and cloud adapters

The root core module defines object-storage, KMS, and secret-provider ports. Cloud SDKs are confined to optional adapter modules.

| Package/module                                     | Status       | Scope                                                                           |
| -------------------------------------------------- | ------------ | ------------------------------------------------------------------------------- |
| `github.com/aws/aws-sdk-go-v2` v1.47.0             | **Selected** | AWS configuration and shared types inside the optional S3 adapter               |
| `github.com/aws/aws-sdk-go-v2/config` v1.33.5      | **Selected** | External credential, region, and HTTP-transport configuration                   |
| `github.com/aws/aws-sdk-go-v2/service/s3` v1.113.1 | **Selected** | S3-compatible immutable ciphertext PUT, exact GET/DELETE, and bounded inventory |

The independently versioned `adapters/objectstore/s3` module implements the owned exact-version object-store boundary. It streams bounded ciphertext without whole-object buffering, creates a random physical version under the logical tenant-scoped key, uses `If-None-Match: *`, records the ciphertext size and SHA-256 digest, verifies both while reading, and deletes only the exact physical version. An ambiguous PUT attempts bounded cancellation-independent cleanup; if cleanup cannot be confirmed, the caller receives the exact owned reference for durable reconciliation. A failed create precondition never deletes another writer's object.

The same owned boundary exposes a separate bounded, cursor-paginated inventory capability for residual orphan discovery. Inventory records contain the logical key, exact physical version, provider size, and modification time but no authenticated checksum, so they cannot be promoted into readable evidence objects or persisted evidence metadata. The core constructs a tenant evidence prefix, accepts only canonical Idenqa evidence keys and generated physical versions, waits beyond the maximum upload-attempt lease plus a provider/application clock-skew allowance, and asks PostgreSQL whether an evidence asset or active reconciliation record protects the exact key and version before deletion. Provider entries outside that shape are ignored. A failed scan, classification, or deletion replays the page; the object store remains the durable inventory. Both the local adapter and S3 adapter implement this capability; production scheduling enters through the selected Headgate adapter behind the owned `platform/task` boundary.

HTTPS is the default and uses the SDK's standard TLS-aware payload-signing behaviour. A deployment may explicitly allow an HTTP endpoint for a controlled development or private-network environment; because the body is deliberately non-seekable, that mode selects S3's supported `UNSIGNED-PAYLOAD` signing form. Application-layer authenticated encryption and digest verification still apply, but they do not make an untrusted network safe; production deployments should use HTTPS and an externally configured trusted certificate chain.

The reviewed dependency intake found Apache-2.0 licensing, active upstream maintenance, and Go 1.24 minimum declarations compatible with Go 1.27.1. The selected S3 v1.109.1 is newer than v1.97.3, which fixed Go vulnerability advisory `GO-2026-5764`. Credentials and custom certificate authorities remain external AWS SDK configuration rather than Idenqa secret fields. Provider errors and SDK types do not cross the owned core boundary.

No AWS, GCP, Azure, or proprietary provider SDK belongs in the root core module merely to satisfy an optional deployment.

### 11.11 Generative-model provider SDKs

| Package                                      | Status       | Scope                                                                                  |
| -------------------------------------------- | ------------ | -------------------------------------------------------------------------------------- |
| `github.com/openai/openai-go/v3` v3.64.0     | **Selected** | OpenAI-compatible Chat Completions protocol inside `adapters/proposals/openaicompatible` |
| `github.com/anthropics/anthropic-sdk-go` v1.74.0 | **Selected** | Anthropic Messages tool-use protocol inside `adapters/proposals/anthropic`              |

These official SDKs are implementation dependencies of their concrete adapters, not the Core model contract. The adapters instantiate them with exact configured origins and per-call resolved credentials, zero SDK retries, the owned destination-pinned HTTP client, and owned success/error response limits. OpenAI-compatible endpoints reuse the OpenAI adapter only when they implement the selected strict JSON-schema Chat Completions semantics. SDK types, environment-derived configuration, provider errors and provider credentials must not cross into `internal/proposal`, public contracts or SDKs.

The 21 September 2026 intake reviewed the current signed releases, release activity, Go-version requirements and resolved module graph. OpenAI v3.64.0 requires Go 1.25 and is Apache-2.0 licensed; Anthropic v1.74.0 requires Go 1.24 and is MIT licensed. Both requirements are compatible with the selected Go 1.27.1 toolchain and both licences are compatible with Idenqa's Apache-2.0 distribution. `go.mod` and `go.sum` pin the exact reviewed versions and checksums. The intake also moved `golang.org/x/crypto` to v0.56.0, resolving GO-2026-6354 and GO-2026-6355 found at module level during the first scan. The final reachability scan reports no vulnerable imported package or called symbol; it reports only GO-2026-5932 against the unimported, unmaintained `openpgp` package, which has no fixed release and remains monitored.

---

## 12. Contract and generation tools

| Tool                                      | Status            | Responsibility                                                                                                 |
| ----------------------------------------- | ----------------- | -------------------------------------------------------------------------------------------------------------- |
| `github.com/oapi-codegen/oapi-codegen/v2` | **Selected tool** | Generate committed Go transport types and a minimal client from OpenAPI; generated types are not domain models |
| `github.com/daveshanley/vacuum`           | **Selected tool** | Lint the OpenAPI 3.1 source using the committed Idenqa ruleset                                                 |
| `github.com/oasdiff/oasdiff`              | **Selected tool** | Reject breaking public-API changes against the pull-request base contract                                      |
| `sqlc`                                    | **Selected**      | Generate typed database query code                                                                             |
| Buf CLI v1.72.0                           | **Selected tool** | Lint, generate, and check compatibility of Protobuf contracts                                                  |
| `github.com/golang-migrate/migrate/v4`    | **Selected**      | Apply and inspect embedded schema migrations through `idenqa`                                                  |
| `github.com/golangci/golangci-lint/v2`    | **Selected tool** | Repository lint policy                                                                                         |
| `golang.org/x/vuln/cmd/govulncheck`       | **Selected tool** | Reachability-aware Go vulnerability checks                                                                     |
| `openapi-typescript`                      | **Selected tool** | Generate committed private TypeScript contract types from the authoritative OpenAPI document                   |
| `tsdown`                                  | **Selected tool** | Build the TypeScript SDK as ESM and CommonJS with declarations and source maps                                 |

Generated artifacts must be reproducible from committed source contracts. CI fails when generated code differs from a clean regeneration.

OpenAPI is the source contract for the public HTTP API. Protobuf is the source contract for isolated runner RPC. SQL files are the source contract for `sqlc` query generation.

The initial OpenAPI source lives at `contracts/api/openapi/v1/openapi.yaml`. URI major versioning is the compatibility boundary. Generated Go code is committed under `internal/gen/openapi/v1` for reproducible builds and handler/client conformance, while public SDKs generate or implement their own language-appropriate layer from the same contract. Vacuum runs in ordinary repository verification. Pull requests compare the proposed contract to the base revision with oasdiff and fail on error- or warning-level breaking changes.

The initial reviewed pins are `github.com/oapi-codegen/oapi-codegen/v2` v2.8.0, `github.com/daveshanley/vacuum` v0.30.1, `github.com/oasdiff/oasdiff` v1.29.1, and the generated-client `github.com/oapi-codegen/runtime` v1.7.0. The generator, runtime, and oasdiff use Apache-2.0; Vacuum uses MIT. Vacuum and oasdiff remain tool-only dependencies even though their transitive graphs appear in the root module. In particular, Vacuum's transitive Viper dependency is not an Idenqa runtime configuration choice and does not replace `envconfig` or `godotenv`.

---

## 13. Testing packages and layout

Unit tests and package-level integration tests are colocated with their Go packages. Repository-level suites live under `test`.

| Package                                     | Status          | Scope                                                           |
| ------------------------------------------- | --------------- | --------------------------------------------------------------- |
| Standard `testing`, `httptest`, and fuzzing | **Selected**    | Default unit, handler, fuzz, and contract tests                 |
| Testcontainers for Go                       | **Proposed**    | PostgreSQL and compatible-service integration tests             |
| `pgregory.net/rapid`                        | **Conditional** | Property and state-machine tests for policy/workflow invariants |
| `go.uber.org/goleak`                        | **Conditional** | Goroutine lifecycle checks for workers and runners              |
| Vitest                                      | **Selected**    | TypeScript SDK unit and real-API conformance tests              |

The initial codebase does not require a mocking framework. Handwritten fakes at owned interfaces are preferred. A test assertion library may be added later if it produces a demonstrated readability benefit.

---

## 14. SDK and capture packages

SDKs and basic capture pages are part of the open-source core deliverable.

### 14.1 SDKs

| Directory          | Public package role                                                                 |
| ------------------ | ----------------------------------------------------------------------------------- |
| `sdk/go`           | Go API client plus public webhook/provider/model test helpers as the module evolves |
| `sdk/typescript`   | Browser and server-side TypeScript API client                                       |
| `sdk/swift`        | Native Apple client and capture integrations                                        |
| `sdk/kotlin`       | Native Android client and capture integrations                                      |
| `sdk/flutter`      | Flutter-facing package backed by native integrations where required                 |
| `sdk/react-native` | React Native package backed by native integrations where required                   |

The Go SDK should prefer `net/http` and avoid unnecessary runtime dependencies. Other language SDKs should use idiomatic platform networking and security facilities.

The X-01 Android foundation, reviewed dependency pins, and Swift foundation are **Selected**:

**Selected — 22 September 2026: native Capture Web parity.** Swift and Kotlin capture must match Capture Web's Core-authoritative notice/consent, immutable-profile document choices and required sides, guided dark document camera/review with framing and Help, acquisition-plan/quality enforcement, measured directional/blink challenge progress, recovery, and completion semantics. Apple Vision is the selected native iOS measurement framework, preserving the first-party-only runtime decision. MediaPipe is selected for Android tracker evaluation. Tracker/model production acceptance, cross-platform angle/eye calibration, supported-device performance and human visual/interaction acceptance remain unresolved; this choice does not establish PAD assurance. SDK-owned pose ports must keep framework types and raw pixels inside native acquisition boundaries. See [native capture evidence](native-capture-evidence-v0.1.md) for implementation and acceptance status.

| Native SDK candidate                                                                   | Status       | Scope                                                                                                                                                                 |
| -------------------------------------------------------------------------------------- | ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Swift tools 6.2, Apple first-party frameworks, iOS 16+                                 | **Selected** | Foundation HTTP/WebSocket and cancellation, strict concurrency, Keychain, Secure Enclave P-256 proof, and AVFoundation capture with no third-party runtime dependency |
| Android Gradle Plugin 9.4.0 with built-in Kotlin 2.3.21, Gradle 9.6.0, JDK 17, API 26+ | **Selected** | Android library build; the wrapper records the official Gradle distribution checksum                                                                                  |
| `org.jetbrains.kotlinx:kotlinx-coroutines-core` 1.11.0                                 | **Selected** | Cancellable suspend operations and public `Flow` realtime observation                                                                                                 |
| `com.squareup.okhttp3:okhttp` 5.5.0                                                    | **Selected** | Android HTTPS and WebSocket transport behind SDK-owned interfaces                                                                                                     |
| `com.squareup.moshi:moshi` 1.15.2                                                      | **Selected** | Bounded Android JSON mapping without reflection or code generation                                                                                                    |
| `com.google.mediapipe:tasks-vision` 1.0.0                                              | **Selected for evaluation** | Android face-landmarker measurement behind the SDK pose port; the digest-pinned model asset is packaged with the application and never downloaded at runtime. Production tracker/model acceptance, device calibration and PAD assurance remain unresolved |
| `com.google.guava:guava` 33.7.1-android and `com.google.protobuf:protobuf-javalite` 4.36.2 | **Selected override** | The published MediaPipe POM requests `guava` 27.0.1-android and `protobuf-javalite` 4.26.1; the older protobuf is affected by CVE-2024-7254, so both are raised explicitly. Every resolved module was checked against OSV and returned no advisory on 22 September 2026 |

Both native SDKs provide platform secure-token stores, generate non-exportable hardware-backed P-256 proof keys, sign the published native-bootstrap transcript, and expose optional platform-attestation provider boundaries. The server has a matching owned verifier port and rejects supplied attestation when no verifier is composed. The tenant backend delivers a single-use Idenqa capture token; first redemption binds that token atomically to an allow-listed application identifier and proof-key digest. Capability advertisements narrow method choice and never prove assurance. Native raw capture remains owned by these SDKs rather than a Flutter or React Native bridge.

SDKs depend only on published contracts. They never import or reproduce internal domain implementations.

The TypeScript workspace uses pnpm. `sdk/typescript` is published as `@idenqa/sdk` for Node.js 22+ and modern browsers. Strict TypeScript and tsdown produce ESM, CommonJS, declarations, and source maps; Vitest owns package tests. `openapi-typescript` generates committed internal contract types from the authoritative OpenAPI document, and CI rejects regeneration drift.

Generated contract symbols are implementation details and are not exported as the public SDK API. The handwritten, zero-runtime-dependency facade owns clients, stable errors, request-ID propagation, idempotency-key behaviour, `AbortSignal` cancellation, and the mapping from wire representations to public SDK types. Networking uses `globalThis.fetch` by default with explicit fetch injection for tests and compatible custom runtimes. This boundary allows the OpenAPI generator to change without silently changing customer code.

**Current implementation boundary — 22 September 2026:** the handwritten TypeScript facade covers the capture/session walking slice and the review, fraud, identity, assurance, policy, webhook and proposal clients recorded by their owning bricks. The [public SDK resource increment](public-sdk-resources-v0.1.md) adds handwritten TypeScript methods and typed Go clients for 30 published decision-history/reconsideration, evidence/grant, consent, impact-assessment and privacy-administration operations. Generated TypeScript contract symbols remain internal; the dependency-free Go SDK generates resource types from public OpenAPI behind handwritten request methods. A subsequent Go-only extension adds 19 published provider/model administration methods (49 typed Go operations total), with request-contract tests, race tests, lint and vet passing; it is not a new live provider/model proof. This is not blanket parity with every Core endpoint: broader Go resource clients, deeper adapter conformance helpers, and undocumented deletion-run/legal-hold writes remain outside this increment. Focused verification evidence and remaining integration boundaries are recorded in the linked resource guide.

The initial reviewed tool pins are pnpm v11.24.0, TypeScript v5.9.3, tsdown v0.22.14, Vitest v4.1.11, `openapi-typescript` v7.13.0, Prettier v3.9.6, publint v0.3.24, and `@arethetypeswrong/cli` v0.18.5. TypeScript remains on the latest 5.x patch because the selected OpenAPI generator declares TypeScript 5.x as its supported peer range; forcing TypeScript 7 would create an unsupported graph. Node.js 22.18 or newer is required to run the development tools, while the published runtime contract remains Node.js 22+.

### 14.2 Capture Web package

```text
capture/web        → published as @idenqa/capture
sdk/typescript     → published as @idenqa/sdk

@idenqa/capture
    └── depends on @idenqa/sdk
```

TypeScript plus Lit v3.3.3 is **Selected** for the Web capture component under D-020. Lit produces standard Web Components, allowing the same open-source capture package to run in plain HTML, React, Vue, Angular, or the future Console without making any one framework a requirement. tsdown remains the published ESM/CommonJS/declaration builder; Vite v8.2.2 is selected only for local development and browser fixtures, and `@playwright/test` v1.62.1 owns real-browser tests alongside Vitest's pure-logic tests. React v19.2.8, React DOM v19.2.8, `@types/react` v19.2.18, and `@types/react-dom` v19.2.5 are pinned **development-only** for framework-host conformance; they must not enter the package's runtime dependencies or published bundle.

The implemented `capture/web` foundation establishes `@idenqa/capture` with the selected pnpm and strict TypeScript tooling and depends only on the public `@idenqa/sdk` contract plus Lit. Its renderer-independent planner intersects immutable tenant requirements with methods implemented by the integration and methods currently usable by the device, preserves required artefacts and `any_of`/`all_of` semantics, distinguishes method from capability unavailability, and selects only fallbacks approved for the actual reason without treating capability advertisements as assurance proof. The Lit component registers explicitly, renders inside open Shadow DOM, emits identifier-only composed selection events, and never accepts capture tokens as attributes or handles raw evidence in its rendering contract. Its programmatic start boundary passes the token directly into the SDK-backed controller, concurrently retrieves the session and current authority snapshot, validates verification, subject, authority, notice, recipient, permitted purpose/evidence scope, and latest-response correlation, renders every mandatory notice field as escaped exact text, and reveals the capture plan only after the required acknowledgement or consent. Refusal and inactive authority remain capture-blocking. Retry attempts for the same response reuse one idempotency key, cancellation aborts in-flight work, and failed, cancelled, refused, blocked, or detached experiences discard the private controller reference. Safe composed events contain only stable receipt identifiers and actions. Package-owned interface messages accept a validated optional catalogue and resolve exact locale, base language, then built-in English while formatting numbers and presentation direction with the canonical server-notice locale. The immutable server notice remains exact copy outside that fallback catalogue. Effect is not a dependency.

**Implementation-status correction — 7 September 2026:** this is a functional and conformance foundation, not the completed subject-facing Capture Web experience. The files under `capture/web/demo` are development fixtures. A fixture that renders every requirement and acquisition control simultaneously, uses hard-coded session data, or depends on mocked Core routes must never be described as the product demo or used as completion evidence for the hosted or embeddable capture application.

**Implementation update — updated 21 September 2026:** `@idenqa/capture` now implements the selected mobile-first guided safe default with one active task per screen, subject-friendly capture labels, explicit preparation, method choice, preview, retake or replacement, upload confirmation, per-step confirmation, recovery, processing, and completion states. Its semantic design tokens cover responsive phone, tablet, and desktop layouts, light and dark modes, forced colours, visible focus, reduced motion, large text, right-to-left direction, and safe areas. The hosted and embedded pages use a same-origin server bootstrap to create a fresh policy, profile, notice, verification, processing authority, and capture token through the public SDK, then complete a real synthetic upload against self-hosted Core without exposing the tenant API key to the browser. The accepted basic direction remains covered by responsive goldens and browser accessibility checks. After the user clarified that advanced acquisition must be implemented immediately, the package added strict acquisition-plan parsing, ordered active-liveness challenges with deadlines and fail-closed host-supplied quality measurements, abort-safe camera cleanup, and a public programmatic boundary for arbitrary namespaced acquisition-method adapters. Adapter return never completes a step: the component refreshes Core and requires the exact requirement, evidence type, artefact, method, and fallback binding in authoritative progress. Missing or ambiguous adapters fail closed. A third live-Core Chromium journey proves the challenge flow and PostgreSQL-backed receipts alongside the hosted and embedded upload journeys. Public evidence contracts now preserve the complete ordered, digest-chained Web challenge-frame sequence, require all declared frames before capture completion, and bind every frame grant into a temporal-capable model request. Local prompts, transcripts, adapter results, sequence metadata and capability advertisements prove no assurance; the synthetic demo does not claim production PAD. E-04 and M-2 are **In review** pending explicit acceptance of the advanced interaction. The public portable experience schema and immutable configuration lifecycle are implemented under `contracts/experience/v1` with signed manifests, session pins and signed safe-default fallback; managed editing/publishing UI, DNS-verified custom domains and native rendering remain gated.

**Outcome update — 14 September 2026:** Capture Web consumes a subject-safe outcome projection through D-027's separate read-only outcome credential and moves directly from final evidence acceptance to Core processing. Atomic policy routing maps `request_input` to `awaiting_input` and `fail_workflow` to `failed`, while retaining `route_manual_review` as the only case-creating directive. Nine live-Core Chromium journeys prove hosted, embedded and active-liveness verification plus action required, not verified, inconclusive, subject cancellation, operational failure and authoritative expiry. Capture Web keeps the outcome credential separate from the capture client and uses it only for the closed projection. E-04 and M-2 remain **In review** pending explicit advanced-interaction acceptance.

The selected safe-default UI is mobile-first and guided. It presents one primary task per screen using a compact journey grammar: getting started; required notice or consent; optional server-authorised country and document choices; preparation; capture; review with use or retake; processing; and an authoritative success, targeted-retry, or terminal-failure outcome. A choice screen is omitted when the subject has no meaningful policy-approved choice. Country selection that changes applicable policy or profile occurs before session creation; no Capture Web choice may rewrite an immutable active-session snapshot. **Selected — 22 September 2026:** allowed document types and their required artefacts belong to the capture profile and are pinned into the immutable session snapshot. The signed experience may present permitted copy but is not the authority for which document types or sides are required.

Core owns the durable per-requirement selection of a pinned document branch.
The capture-token command is application-authorised, expected-version and
idempotency bound. Selection activates already-pinned artefacts rather than
rewriting the profile, policy or session snapshot. Upload authorisation and
capture completion must use that same selection and reject unselected branches;
recovery returns the Core-recorded type. Switching after a requirement has begun
collecting evidence must not reclassify or reuse existing evidence. Host-only
catalogues and locally restored IDs are not authority for this choice.

The selected liveness branch is deliberately shorter: preparation, guided live capture, processing, then an authoritative success, retry, or failure outcome. It provides subject-friendly progress, intentional camera framing and readiness feedback, actionable permission and network recovery, and separate document-front and document-back guidance. Subject copy must not expose domain or implementation terminology. A capture error says that capture failed; a verification-failure screen requires an authoritative verification result. Capture completion remains distinct from verification processing, verification completion, terminal verification outcome, and tenant action. CSS custom properties may theme permitted appearance locally, while the future versioned experience contract owns which optional screens, structured copy, approved assets, and policy-bound branches are presented.

Capture Web package acceptance requires all of the following:

- a real self-hosted Core-backed synthetic journey using a server-created session plus separate capture and outcome tokens;
- hosted and embedded demonstrations using the same published component contract;
- profile-backed document choices with different required sides, durable selection/recovery, unchanged snapshot bytes, and denial of unselected or out-of-branch uploads;
- an accessible safe-default theme with semantic tokens for colour, typography, spacing, radius, focus, motion, light and dark modes;
- responsive phone, tablet, and desktop layouts without presenting the complete plan as one administrative form;
- keyboard, screen-reader, contrast, large-text, reduced-motion, right-to-left, permission, interruption, and recovery evidence; and
- explicit user-facing visual and interaction review in addition to automated unit, browser, contract, and framework-host conformance tests.

| Package                    | Status            | Capture-Web scope                                                    |
| -------------------------- | ----------------- | -------------------------------------------------------------------- |
| `lit` v3.3.3               | **Selected**      | Framework-neutral Web Component runtime                              |
| `@mediapipe/tasks-vision` v1.0.1 | **Proposed — implemented for evaluation** | Local face landmarks/blendshapes in a separate self-hosted worker; no PAD claim |
| `vite` v8.2.2              | **Selected tool** | Development server and plain-HTML browser fixtures only              |
| `@playwright/test` v1.62.1 | **Selected tool** | Real Chromium, Firefox, and WebKit browser tests as coverage expands |

The reviewed intake found active upstream maintenance and BSD-3-Clause, MIT, and Apache-2.0 licensing respectively, all compatible with Idenqa's Apache-2.0 distribution. React, React DOM, and their type packages are actively maintained and MIT-licensed. The initial production graph contained Lit and its five BSD/MIT dependencies; the measured-liveness increment adds MediaPipe Tasks Vision 1.0.1 (Apache-2.0, no declared npm dependencies), bundled only in the separate worker. Its registry release metadata, official documentation/licence and npm advisory endpoint were checked on 22 September 2026; no advisory was returned for that version. The version-1 float16 Face Landmarker model is digest-pinned by the asset-preparation script. Model/device evaluation and production approval remain open. Vite, Playwright, React, React DOM, and the React type packages remain development-only. Neither Vite nor React is used to build the published package, avoiding coupling the library format to a development fixture or framework host.

**Selected behaviour — measured liveness:** a timer alone must never advance a
challenge. The Capture Web-owned tracker port measures subject-relative pose;
the coordinator requires neutral calibration, target tolerance and contiguous
hold, resets on tracking/quality loss, and automatically captures only a passing
frame. The ring reports those measurements. Acquisition schema 1.1 carries
bounded per-challenge policies; 1.0 remains readable with non-bypassable defaults.
Raw transient tracking frames stay local to the worker; accepted frames use the
existing evidence-upload boundary. Acceptance must prove still/wrong-way/no-face
and poor-quality inputs cannot submit, correct holds can submit through Core,
and cancellation/retry release resources. Real-camera direction/accuracy,
mobile performance, calibrated thresholds and explicit interaction acceptance
remain required; local pose compliance never establishes PAD assurance.

React is not a runtime requirement of the open-source capture package. The commercial Console may use its own React stack separately.

The capture component renders the immutable server-issued requirement snapshot and offers only acquisition methods allowed by that snapshot and implemented by the current SDK and device. Tenant presentation settings may affect labels, ordering, help text, and branding, but cannot weaken evidence, assurance, consent, or retention requirements.

Initial implementation order is TypeScript SDK plus Web capture, Go SDK, Swift and Kotlin, then Flutter and React Native. This is a delivery sequence only; all listed SDK and capture packages remain part of the open-source boundary.

### 14.3 Effect for TypeScript surfaces

[`effect`](https://effect.website/) is a **conditional** implementation dependency, not a public SDK requirement.

It may be evaluated for internal orchestration in `capture/web` and future private TypeScript services where typed errors, cancellation, resource scopes, retries, and concurrency materially simplify the implementation. The public `@idenqa/sdk` API must remain idiomatic and usable without Effect:

- ordinary requests return `Promise` values;
- cancellation uses `AbortSignal`;
- realtime streams use `AsyncIterable` or standard DOM event interfaces;
- public types do not expose `Effect`, `Layer`, `Scope`, or other Effect-specific types.

If customers later want native Effect composition, provide a separate optional `@idenqa/sdk-effect` adapter. Adoption requires a production bundle-size measurement, browser compatibility validation, and a stable reviewed release. Effect does not replace the Go background-work system or the durable PostgreSQL workflow contracts.

---

## 15. Dependencies intentionally excluded from the initial core

| Dependency or category                       | Initial decision                                                                                    |
| -------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| Gin, Fiber, Echo                             | Use Chi and standard `net/http` compatibility                                                       |
| GORM, Ent, general-purpose ORM               | Use pgx, reviewed SQL, and sqlc                                                                     |
| Viper                                        | Use envconfig plus explicit dotenv loading                                                          |
| Wire, Dig, Fx, service locators              | Use manual constructor injection                                                                    |
| River                                        | Replaced by the Idenqa-selected background-work library                                             |
| Redis and Asynq                              | Not required for the minimum self-hosted core                                                       |
| OPA service                                  | Use the Idenqa policy contract with proposed embedded CEL evaluation                                |
| Cloud-provider SDKs in the root module       | Keep them in optional adapter modules                                                               |
| Generic domain-wide validation framework     | Keep invariants in domain constructors and methods                                                  |
| React in the capture runtime                 | Use a framework-neutral Web Component                                                               |
| SSE as the primary active-capture transport  | Use WebSocket for bidirectional capture control; reserve SSE for possible read-only views           |
| Effect as a mandatory TypeScript SDK runtime | Keep the public SDK Promise- and platform-interface-based; allow only internal or optional adapters |
| Mocking framework                            | Start with narrow owned interfaces and handwritten fakes                                            |

---

## 16. Dependency governance

Before adding or upgrading a dependency:

1. Confirm that the standard library does not already meet the need.
2. Review licence compatibility.
3. Review maintenance activity and release history.
4. Review the transitive dependency graph.
5. Check known vulnerabilities.
6. Pin a reviewed version rather than relying on an unreviewed floating latest version.
7. Run formatting, linting, tests, and vulnerability checks.
8. Commit `go.mod` and `go.sum` together.

Required checks for dependency changes:

```text
go mod tidy
go mod verify
go test ./...
go vet ./...
go tool golangci-lint run ./...
go tool govulncheck ./...
```

Routine upgrades should prefer patch-level changes. Networking, persistence, serialisation, authentication, authorisation, cryptographic, and public-contract dependencies require explicit changelog review before upgrade.

`//nolint` directives must identify the linter and explain the exception. Security-linter suppression requires security review.

---

## 17. Decisions still required

- Accept or replace the evaluated MediaPipe tracker/model and calibrate acquisition
  pose thresholds after representative real-camera, subject and supported-device
  evaluation. Initial 20° target, ±7° tolerance and 450 ms holds are engineering
  defaults; synthetic tests do not resolve this decision.

The following items remain deliberately unresolved:

1. Canonical published paths for future independently released SDK and adapter modules other than the selected S3 adapter; the root module is selected as `github.com/Mujhtech/idenqa` and the S3 module as `github.com/Mujhtech/idenqa/adapters/objectstore/s3`.
2. The retention duration of each future concrete task type other than the selected 30-day successful-metadata retention for `verification.execute` v1 and `verification.reconcile` v1. Mandatory bounded retention is selected, but future durations belong with the task definition introduced by its owning brick. Headgate installation identity, schema and migration composition, queue and rate-class catalogue, partition mapping, retry enforcement, fleet rate/burst defaults, per-tenant concurrency defaults, process controls, telemetry ownership, v0.1.10 release, and Go/PostgreSQL module paths are selected.
3. **Resolved:** D-016 selects per-process OTLP over either gRPC or HTTP/protobuf, disabled-by-default self-hosting, TLS except explicit loopback development, redacting authentication headers, optional private CA and mTLS, parent-based configurable trace sampling, bounded batching and retry, and periodic unsampled metrics.
4. Whether Ozzo is needed beyond configuration validation.
5. Which adapters other than the independently versioned S3 adapter ship in the root release versus independently versioned modules. The S3 distribution-composition module is selected as `github.com/Mujhtech/idenqa/distributions/s3`; its `api` keeps evidence ingress in the public API process and its `worker` supplies worker-owned exact deletion against the same S3 namespace.
6. The public API client and webhook verifier are **Selected** in the dependency-light `sdk/go` module. Provider and model contracts and conformance suites are selected under `contracts/{provider,model}/v1` and `conformance/{provider,model}`. The public policy engine test kit is selected under `conformance/policy` and depends only on `contracts/policy/v1`. Future independently released module paths for these root-module packages remain unresolved under item 1.
7. Detailed dependency-licence allow-list and review process within the selected Apache-2.0 distribution policy.
8. **Partly resolved — 20–21 September 2026:** adapter/model runner server credentials, gateway credentials, provider/model credentials and TLS identities resolve and dynamically reload from the selected secret provider with last-good fallback and overlap. Delegated, time-bounded support access and approval-gated break-glass access are implemented with append-only use records and API/CLI surfaces. Remaining: API/worker outbound runner client credentials and trust material through the secret provider with safe redial/overlap, optional client mTLS, and credential formats beyond the selected initial tenant API key and v1 runner transport credential. OAuth client credentials remain **TBD** until issuer ownership, token format, audience, tenant/scope mapping, issuance, rotation, revocation and overlap are selected; do not infer an internal issuer or external-JWT trust model. Future capture-token versions also remain unresolved.
9. Durable WebSocket event replay retention and deployment topology. D-010 selected the v1 catalogue, single-use query-ticket bootstrap and lifetime, connection-local sequence policy, version rules, and bounded R-01 limits.
10. Per-operation idempotency retention, response replay, and duplicate-in-progress behaviour beyond the selected capture-profile command policy.
11. Transaction isolation, row-locking, optimistic-version, and PostgreSQL advisory-lock policy for workflows beyond the selected capture-profile command policy.
12. Distributed rate-limit enforcement strategy that preserves the no-Redis baseline.
13. A future byte-offset resumable protocol and its encryption-compatible staging design for large video or other media. The E-03 v1 single-request limits, SHA-256 integrity, expiry, full-body retry, exact-object cleanup, and reconciliation contract are selected under D-009.
14. Whether a future optional `@idenqa/sdk-effect` adapter or internal Effect adoption meets the bundle budget, browser-compatibility, and stable-release requirements. Effect is excluded from the current public SDK and Web capture runtime.
15. **Partly resolved — 20 September 2026:** AWS KMS and AWS Secrets Manager are the selected first production KMS/HSM and secret-manager providers. `adapters/kms/aws` implements the owned ports with fail-closed provider selection; tenant HMAC key custody, runner secret resolution and dynamic reload, the fenced fleet rewrap duty, verified destruction receipts and dual-control recovery ceremonies are implemented (migrations 69–70). Remaining: the operational rotation cadence, the trigger and rollout policy for a content-algorithm migration, live AWS acceptance evidence, and migrating the identity keyed lookup onto the custody catalog. Tink Go v2.8.0, the local provider, provider-neutral adapter boundary, rewrap-first key rotation, and cryptographic-agility envelope remain selected.
16. The deletion boundary for backups and externally delivered payloads. The v1 audit tamper-evidence format is selected as a canonical tenant-sequenced SHA-256 chain with Ed25519 checkpoints, immutable key IDs, bounded portable export, and offline verification.
17. **Resolved:** deployment defaults are raw and derived evidence 30 days, webhook payloads 7 days, workflow metadata 365 days, reference-only audit records and deletion tombstones 7 years, and backup expiry 35 days. Tenant policy may shorten retention; extension requires an explicit deployment cap. Legal holds override expiry. Processing and storage are immutably pinned to the session region with no silent cross-region fallback.
18. **Selected — 6 September 2026; implementation updated 19 September 2026:** initial-core policy activation and rollback use one authenticated tenant API key with dedicated `policies:activate` permission, expected activation version and common audit. A separate approver or step-up is not required for this initial-core surface. Read and immutable revision authorship use separate `policies:read` and `policies:write` permissions. Rollback explicitly selects a previously activated revision and appends a new activation; it never rewrites history. The v1 tenant-to-policy assignment, direct namespaced signal-to-fact mapping, one-to-one normalised outcomes, authoritative-only `unavailable`/`prohibited` derivation, and capture-complete plus required-checks-terminal `policy.author` trigger are **Selected**. P-01 implements public administration and signed cursors. Public policy simulation, revision diff and scenario regression are implemented through the API and CLI. Future operator approval and Console workflows remain separate from the selected initial-core API-key boundary.

19. **Partly resolved — 19 September 2026:** D-028's dedicated tenant resume command is implemented with its permission boundary, expected-version/idempotency contract, live-credential reuse, bounded replacement and fresh subject-authorisation requirements. D-029's maximum-three-attempt semantic retry path is implemented with a one-second-to-one-hour clamp and atomic predecessor completion, successor-attempt persistence and Headgate task intent. The asynchronous provider path now enters `awaiting_external` only while a durable external dispatch is pending and no local check remains runnable, and returns to `processing` inside the fenced task transaction that accepts the authoritative result, under the already-established serializable Headgate effect isolation. Tenant verification reads project the optional current decision and current review case under their separate read permissions. D-029 callback intake is implemented as Core public ingress plus runner-side adapter verification: an opaque per-attempt `pcb_` reference is bound when the provider request is persisted, the provider posts to an unauthenticated bounded route, Core resolves tenant/attempt/config and calls the isolated runner's `VerifyProviderCallback` RPC, and only the adapter verifies the provider signature (Smile ID's confirmed scheme) before a normalized replay-identity receipt is stored under forced RLS; duplicate replay identities are exact duplicates, conflicting content is rejected, and the existing fenced result path performs the `awaiting_external -> processing` transition with polling retained as fallback. Failed sessions project a bounded operational `failure` class/code through the lifecycle record; the policy `fail_workflow` directive writes `policy`/`workflow_prohibited`, and `failed` remains terminal and decision-free. Policy `request_input` routing records a bounded structured input request (migration 63) projected as `requested_input` while the session is `awaiting_input`, with a composed non-review proof through action-required outcome, fresh-authorisation resume and completion. Callback and worker-loss recovery evidence now covers receipt survival across worker replacement, callback/poll first-terminal-wins convergence, external-success followed by local-failure reconciliation, and runner/credential loss with status-only polling fallback. Remaining implementation and deployment work is production authoritative check-plan selection, live provider-account callback operation, and the deployment acceptance gates. Automatic expiry of a future separately persisted `created` phase remains conditional because creation currently persists `collecting` directly. Scalable completion fanout is resolved by item 20. L-03 resolves tenant/subject cancellation permissions, replay and current-session expiry as recorded above. Creation replay preserves its original result under the existing idempotency convention.

20. **Implemented selected scope — 19 September 2026:** webhook evolution beyond H-01 provides the versioned public event catalogue, exact names or `*` subscriptions, outbox-first owning-feature emission, exact endpoint schema-version `1.0` pins, fenced resumable fanout with a 257-endpoint replacement-worker proof, authorised list/SSE, local signed forwarding, and bounded hold-aware seven-day payload/attempt expiry with 365-day reference tombstones. Expired payloads cannot be listed, streamed or replayed. H-01 resolves public endpoint administration, bounded delivery inspection, display-once secret receipts and exhausted-delivery replay before expiry. The packaged self-hosted stack now passes its thirteen-step fixture-backed clean-usability gate; production webhook networking, real runner operation and external release evidence remain separate gates.

21. Provider runtime beyond PR-01/PR-02: official-account sandbox evidence, D-029 external-success reconciliation and durable evidence delivery recovery, provider-side deletion, cost budgets and attribution, production recipient/regional approval, callback operation under a tenant-owned official account (the transport, runner verification, receipt deduplication and Core ingress are implemented; live provider-account callback evidence is not), and workflow-metadata retention/purge for immutable request and dispatch receipts. Provider health snapshots, bounded circuit breaking through the shared `platform/breaker` primitive, health-based registration routing, provider credential resolution/rotation through AWS Secrets Manager, per-provider concurrency/rate limits and degraded-mode visibility are implemented (migrations 68, 70, 71). D-029 fixes the owned receipt, deduplication, semantic retry and lifecycle rules; the initial sandbox route and its at-most-one initial dispatch receipt do not resolve those implementation or operational gates.

22. ONNX/biometric acceptance: production licensed model and dataset rights; production acceptance of the implemented face localisation/contextual transform and its preprocessing provenance; approved evaluation data acquisition; trained face-matching and active-liveness models; acceptance of document portrait/alignment and identity-separated pair evaluation; accepted selfie quality/pose/framing thresholds; any future occlusion/accessory classifiers; public route administration and registry-driven hot provisioning beyond the implemented immutable dependency/fallback graph; operational acceptance of configured fallback sets; native/mobile temporal-sequence integration; production threshold calibration and representative evaluation; hardened OCI packaging, kernel resource/egress limits and supported hardware. ONNX Runtime, PAD as first capability and first-party biometric models as the primary direction are **Selected** under section 6.9. The Python 1.29.0 CPU reference, bounded subprocess, durable workflow, contextual preparation, offline evaluation, complete Web temporal-sequence persistence/consumption, evaluation-only selfie diagnostics and repository-owned graph admission are implemented; model quality and production deployment gates remain open.

23. Review acceptance and external integrations: the selected review policy settings, independent escalation/correction rules, versioned operator revocation, controlled display and public contracts are implemented in section 6.10. External certification is now an owned fail-closed Ed25519 assertion adapter configured additively in the existing authority file, with the tenant-attested path preserved when no issuer is configured; a real third-party issuer trust/status integration (remote key discovery and revocation checks) remains unresolved. Advanced queue assignment automation, tenant notification product surfaces and composed reviewer/subject acceptance also remain unresolved. SSO remains deferred.

24. Fraud deployment configuration: tenant-specific canonicalisation namespaces, thresholds, legally permitted trusted-template sources, accepted network/provider/model signal mappings and regional operating approval. Similarity-based portrait search, simultaneous per-region active fraud configurations and cross-tenant networks remain outside the implemented initial baseline.

25. **TBD — public model health:** select whether the tenant resource is a live runner probe, durable supervised state, or a bounded aggregate of registry/runtime evidence, including freshness and failure semantics. Evaluation-model registry state and provider health are not substitutes and must not be published as model health without this decision.

26. Identity extensions: production provider-specific structured extractors and automatic ingestion, country-specific normalisation beyond the three selected versioned transforms, and incremental deletion planning above the selected atomic limits remain **TBD**. The persistent subject model, source-trust distinction, identity receipt extension and linked-evidence deletion scope are selected in section 6.3.

27. Assurance follow-ups: production profiles and calibrated biometric operating points; jurisdiction/framework mapping records and approval; richer capability versions; field-level runner manifests; larger graph planning; and recovery workflows for assurance that expires between evaluation and commit. The version-one profile, pinning, source-time, independence, public API and canonical-context mechanisms above are implemented and do not resolve these production choices.

28. **Selected — 14 September 2026:** Post-expiry subject-outcome access uses a distinct `idq_out_v1` signed bearer backed by an `otk_` durable credential record. It has a separate HMAC-SHA-256 keyring and domain separator from capture credentials, and binds its token identifier, tenant, verification, key version, issuance time and expiry. Core creates exactly one outcome credential atomically with each verification or recapture session and reconstructs the same deterministic bearer on exact idempotent replay; the tenant backend delivers it beside, but separately from, the capture token at its trusted subject bootstrap. Its expiry is the immutable session expiry plus a selected post-expiry interval: the deployment default is 24 hours, the default deployment maximum is 168 hours, and the hard configuration cap is 30 days. A create request may choose a positive whole-second interval within the deployment maximum. The credential is independently revocable and remains subject to its own key-version retention; its database row follows workflow-metadata retention, which does not extend bearer validity. It grants only the closed subject-safe `GET /v1/capture/outcome` projection and cannot load or exercise capture-session authority, respond to authority, initiate or upload evidence, cancel, open WebSockets, read decision details, or call tenant APIs. No public outcome-token renewal exists in v1. Recapture capture-token renewal reconstructs the existing still-live child outcome credential rather than replacing it. A capture token remains bounded by session expiry and is never accepted for post-expiry outcome access; browser time never invents the authoritative `expired` transition. Idempotency results written before D-027 cannot be expanded safely and therefore conflict instead of minting a new outcome credential during replay.

---

## 18. Acceptance criteria

This structure is accepted when:

- Assurance publication and assignment enforce application permissions, immutable revision contents, tenant scope/RLS, expected-version conflicts and atomic idempotency/audit/outbox effects. Future assignment changes cannot alter existing session or recapture pins.
- Requested assurance cannot be achieved from expired sources, uploaded evidence with inadequate acquisition provenance, evaluation-only models or correlated roots counted independently. Fresh verified commits recheck the immutable pin and source age; exact committed replay remains stable after expiry.
- Decision context preserves applicable identity ancestry, evidence/attempt/signal references, accepted review findings, authority/region context and exact profile/configuration/runtime/policy/evaluator provenance through review, correction, recapture, export and reproduction, while legacy canonical bytes remain unchanged.

- The public core builds with Go 1.27.1 without a commercial repository.
- The core, SDKs, capture packages, contracts, conformance suites, and bundled examples carry Apache-2.0 licensing and pass the dependency-licence policy check.
- `api`, `worker`, `evidence`, `adapter-runner`, `model-runner`, and `idenqa` entry points contain only composition and process lifecycle code.
- Domain packages compile without HTTP router, SQL driver, task-library, telemetry SDK, or cloud SDK imports.
- The Go SDK compiles without access to root `internal` packages.
- The optional S3 adapter passes real AWS SDK conformance over HTTPS and explicitly enabled HTTP, streams without whole-object buffering, detects ciphertext modification, preserves exact versions, and leaves the root module free of cloud SDK dependencies. The independent S3 distribution composes both public `api` ingress and `worker` exact deletion against one configured namespace without exposing SDK types across owned boundaries.
- The Web capture package can be embedded in a non-React page and uses the public TypeScript SDK.
- Model registry mutations must reject stale versions, incompatible threshold provenance/configuration and production-mode requests; replay preserves original actor/history, retirement fences new registered attempts, and injected audit/outbox failure leaves no partial registry mutation.
- The ONNX model runner loads only the pinned model/runtime/preprocessing/configuration/threshold/output-schema revisions and rejects incompatible or mismatched artefacts. Runtime-specific types remain outside owned and public contracts.
- ONNX conformance proves scoped evidence access, bounded inference, deadline/cancellation handling, malformed-output rejection, safe unavailable/inconclusive outcomes, and reproducible normalized results within a declared numerical tolerance and recorded hardware/runtime class. Model-specific quality and assurance gates must pass before production activation.
- PostgreSQL remains sufficient to recover durable workflow and job intent.
- The selected background library is replaceable through `platform/task` without changing domain APIs.
- A state transition, its outbox event, and resulting task intent commit atomically or not at all.
- Policy decision append, completion receipt/audit/outbox, endpoint deliveries and callback tasks commit in one fenced effect; failed enqueue or lost lease leaves no partial completion. Exact completion replay cannot notify new endpoints retroactively.
- The running synthetic journey reaches completed version 3, returns a reproducible decision through the tenant API, and delivers a webhook independently verified by the public SDK. A 503 followed by worker restart resumes the durable retry with the same signed event/body; tampering fails verification. Disabled or expired deliveries send nothing, and bounded discovery rotates past pending work.
- A capture accepted while the worker is stopped is discovered on startup; atomic processing-start failure leaves no check, attempt, task, transition receipt or audit/outbox effect. Concurrent starts and restart preserve one plan. The public session advances to processing version 2 while creation replay remains collecting version 1.
- Prepared results cannot commit or claim inbox receipts after session expiry/cancellation or authority withdrawal/refusal. Authority changes invalidate stale serializable snapshots; ineligible capture/policy rows are excluded before the discovery batch limit. Synthetic processing is disabled by default and establishes no real identity assurance.
- Lifecycle primitive tests prove the exact section 12 transition graph, terminal immutability, optimistic version conflicts, monotonic time, deadline boundaries, same-verification decision binding, exact receipt replay, changed-meaning rejection, forced-RLS isolation, append-only receipts, and atomic audit/outbox rollback when the caller's effect fails. Runtime cancellation is not advertised until every consequential writer enforces parent-session state at commit.
- Inbox deduplication and task fencing prevent duplicate delivery from producing duplicate domain effects.
- Public webhook commands enforce separate read/configure/replay permissions below HTTP. Cross-tenant resource and cursor access fails; create/rotate retry cannot reveal or regenerate signing material; stale or overlapping rotations conflict. Failed replay enqueue rolls back delivery, receipt, audit and outbox together; exact replay preserves event/body identity and returns the same new delivery. CLI secrets never appear in standard output or diagnostics.
- Retrying an idempotent operation with the same key and request fingerprint replays the recorded result (with the documented display-once webhook secret omission); a different fingerprint conflicts; concurrent duplicates do not execute the operation twice.
- Authorisation is enforced by application services using an explicit access context, even when an operation is invoked outside HTTP.
- The API process fails before opening PostgreSQL when its active API-key pepper or pepper set is absent, and `GET /v1/tenant` derives its sole target from the verified access context while repeating `tenant:read` authorisation below HTTP.
- API-key CLI create and rotate reveal the new credential exactly once after an atomically audited commit; list and revoke remain secret-free; destructive operations require explicit confirmation; and stale revocation versions fail without a lifecycle or audit write.
- API-key scope tests cover exact grants, resource wildcards, action wildcards, full wildcards, invalid partial globs, registry snapshot non-expansion, and exclusion of non-tenant permissions.
- Tenant-owned repository operations fail without verified tenant scope, and isolation tests attempt cross-tenant reads, writes, identifier probes, jobs, events, and realtime subscriptions.
- PostgreSQL row-level security denies tenant-table access without the expected tenant scope; every privileged bypass path is explicitly authorised and audited.
- Canonical policy snapshots, evaluations, and decisions restore only through owned validators; durable records are append-only, tenant isolated, bound to an existing verification, restricted to one linear decision lineage, and reject changed-meaning replay.
- Canonical policy revisions compile before registration, replay only on exact immutable meaning, remain tenant isolated and append-only, and switch active pointers only through an expected-version compare-and-swap that atomically records previous revision, selected revision, actor, and UTC time.
- Public policy administration proves read/write/activate separation, strict input, canonical idempotent replay, stale-version and previously-activated rollback guards, tenant-bound pagination and atomic rollback on audit/outbox failure. The runnable synthetic journey configures both policy and receiver through the public API before producing an independently verified signed webhook.
- Machine decision authoring pins exact request identity, lineage, and time; reproduces durable replays; rejects changed or non-terminal meaning; and accepts concurrent replay only when the complete canonical decision digest agrees.
- `policy.author` task evaluation occurs before its bounded effect transaction; tenant-scoped replay verification, append and reread commit atomically with Headgate's live completion fence, and a lost fence rolls the decision effect back.
- Policy simulation compiles canonical supplied meaning without registration or activation, uses only explicit synthetic reference-only inputs, reproduces from a bounded self-checking bundle without CEL or PostgreSQL, and never authors an authoritative decision or workflow effect.
- Named policy scenarios resolve exact expectations through production semantics, report every mismatch in canonical name order, produce an input-order-independent safe digest, and remain bounded, cancellation-aware and effect free.
- Internal policy-catalog inspection returns bounded newest-first metadata pages through exclusive boundaries, never loads canonical policy documents, validates durable ordering and identity, and remains non-disclosing under forced tenant RLS.
- Encrypted records carry a key purpose and version; rotation tests prove old and new records remain readable only through authorised paths.
- Retention, legal-hold, deletion, and backup-expiry tests account for primary data, evidence, derived artefacts, events, jobs, and delivery payloads.
- A verification session retains its immutable capture-profile snapshot after the tenant edits or deactivates the source profile.
- Capture-profile conformance tests cover `any_of` choices, `all_of` requirements, approved fallbacks, unsupported SDK capabilities, and impossible assurance combinations.
- Uploaded evidence cannot satisfy live-capture, freshness, active-liveness, NFC provenance, or other assurance properties the acquisition method cannot establish.
- Every accepted upload is bound to a server-issued session requirement and artefact; arbitrary or profile-disallowed evidence is rejected.
- Barrier-controlled acceptance tests prove simultaneous final artefacts cannot lose capture completion, lock waits cannot bypass expiry, and withdrawn authority, changed subject responses, revoked tokens or terminal sessions prevent evidence availability and publication. Creation replay retains the original state/version/time after lifecycle changes.
- The subject-safe outcome projection exposes no decision or evidence internals; policy-authored `request_input` and `fail_workflow` transitions commit atomically with exact routing provenance and replay. The separate D-027 outcome credential remains read-only, independently expiring and revocable, cannot authenticate any capture operation, and can read the Core-authored `expired` projection after the capture credential and session have expired. Creation and recapture replay reproduce their original outcome credential without storing a bearer.
- Capture clients can reconnect by cursor, replay retained events in order, and fall back to REST snapshot recovery after a replay gap.
- Reconciliation discovery exposes only bounded routing identifiers, every item re-enters tenant scope, and only one installation scheduler enqueues exact attempt work while application and Headgate fences reject stale owners.
- Capture-safe check progress is atomically projected from the outbox, remains correct without PostgreSQL notifications, and cannot expose outcomes, signals, reason codes, provider data, or evidence through Go, JSON Schema, SDK, or Capture Web contracts.
- Realtime tests cover connection-ticket consumption, command deduplication, heartbeat timeout, slow consumers, clean shutdown, and protocol-version rejection.
- Raw evidence bytes and reusable credentials never travel in WebSocket messages; uploads use dedicated authenticated HTTP endpoints.
- Rate limits, bounded queues, deadlines, retry budgets, and backpressure prevent an overloaded dependency or slow client from creating unbounded work.
- Readiness, liveness, startup, and drain behaviour are verified for every long-running process.
- The documented self-hosted administrative and recovery operations are usable through `idenqa` without Console or Cloud.
- HTTP errors expose stable codes and request IDs without leaking internal error strings.
- The versioned OpenAPI source lints at the repository threshold, regenerates committed Go transport types and the minimal client without drift, rejects breaking pull-request changes except the exact selected alpha lifecycle-state and decision-bundle fraud/identity-provenance additions, and supplies synthetic fixtures that decode through generated client types. Compatibility regression tests must still reject unapproved values, endpoints, properties, response statuses and operation removal.
- OTel instrumentation uses safe route templates and avoids identity-derived metric labels.
- A clean generation run produces no uncommitted changes.
- Import-boundary, lint, test, migration, conformance, and vulnerability checks pass in CI.
