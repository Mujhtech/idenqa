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

The project standardises on **Go 1.26.6**.

The root module and each Go submodule use:

```go.mod
go 1.26.6
toolchain go1.26.6
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
│   ├── verification/
│   ├── policy/
│   ├── provider/
│   ├── model/
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
│   └── models/
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

### 6.7 `policy`

Owns policy documents, type checking, compilation, evaluation inputs and outputs, reason codes, simulation, activation, versioning, and reproducibility contracts.

The policy package presents an Idenqa-owned API even when CEL is used internally. CEL types must not leak into other domain packages or public contracts.

The first V-03 foundation is **Implemented without resolving the CEL proposal**. `internal/policy` now owns closed requirement states, workflow directives, terminal outcomes, reference-only fact sources, immutable canonical snapshots, deterministic priority and conflict resolution, and reproducible terminal decisions. `pol_` and `dec_` are Idenqa-owned sortable identifiers; the underlying ULID remains hidden by `internal/platform/id`. Evaluation takes an explicit UTC time, cannot obtain providers, models, storage, networks, filesystems, or clocks through its type surface, and rejects stale facts, unknown states, duplicate or conflicting bindings, incompatible versions, and digest mismatches. Raw evidence, signals, credentials, object locations, and arbitrary maps are not representable in its fact contract.

The second V-03 foundation is also **Implemented without resolving the CEL proposal**. Strict restoration accepts only the closed canonical snapshot, evaluation, and decision representations within owned byte limits, rebuilds them through the original constructors and resolution rules, and requires exact canonical bytes plus SHA-256 digests. Migration 19 stores snapshots, evaluations, and decisions separately as immutable tenant-owned records. Composite foreign keys bind the chain to an existing verification and permit decisions to reference only terminal evaluations. Forced row-level security, append-only triggers, one-root and one-successor uniqueness, explicit predecessor locking, exact replay, and leaf-based latest lookup preserve a linear tenant-scoped lineage. The PostgreSQL adapter is confined to `internal/policy/postgres`; SQL and pgx types do not enter the domain contract.

The third V-03 foundation is **Implemented without resolving the CEL proposal**. `internal/policy` owns a versioned, bounded, closed portable decision bundle containing the exact canonical snapshot, evaluation, and decision plus their digests and a digest of the complete bundle payload. Restoration re-enters the original snapshot constructor, deterministic resolver, terminal-authorisation path, and exact canonical-byte checks without a database, provider, model, network, task runtime, or expression engine. The safe reproduction report contains only pinned identifiers, versions, digests, directive, outcome, assurance, actor, lineage, timestamps, and counts; it omits facts, requirement details, reason codes, evidence references, and canonical inputs.

The Cobra `idenqa policy decision reproduce` command performs an exact migration preflight and tenant-scoped runtime-role repository lookup before writing a summary, JSON report, or byte-exact bundle. `idenqa policy decision verify` consumes a bounded canonical file or standard input and performs the same reproduction offline. The embedded payload digest detects accidental or unexplained changes but is not a digital signature or an external trust anchor.

The fourth V-03 foundation is **Implemented without resolving the CEL proposal**. `internal/policy.Reader` rechecks tenant application authority above the repository and separates bounded report reads from richer portable exports through `decisions:read` and `decisions:export`. The API exposes exact and latest reproduced summaries plus explicit byte-canonical bundle export with strong digest ETags, private revalidation, generic tenant-invisible 404s, and no capture-token route. The PostgreSQL adapter remains behind the owned reader. OpenAPI defines the complete closed canonical bundle shape, and generated Go contracts plus the dependency-free TypeScript `IdenqaClient.decisions` facade consume only published HTTP meaning. The SDK keeps portable snake-case document fields and exact canonical text together so callers can store or verify the original export without lossy re-encoding.

The fifth V-03 foundation was **Implemented before resolving D-012**. `internal/policy.Author` owns the CEL-neutral machine-decision application seam over three narrow consumer-owned capabilities: exact decision persistence, authoritative input loading, and bounded evaluation. A replay request pins decision and verification identities, optional predecessor, evaluation time, and decision time. Existing durable meaning is reproduced before reuse; changed meaning conflicts. Fresh input re-enters canonical snapshot construction, evaluator output re-enters deterministic resolution, and only a terminal authorised evaluation can create a machine-authored decision. A conflicting append is accepted as a concurrent replay only when the complete canonical decision digest matches. The author reads and reproduces the durable result before returning it, propagates cancellation unchanged, and cannot represent raw evidence or engine-specific values.

The sixth V-03 foundation **Resolves D-012 and is implemented**. `contracts/policy/v1` defines a dependency-free, bounded canonical JSON document with exact policy identity and revision, uniquely named boolean rules, explicit owned result state, directive, priority, exact contributing-fact provenance, reason codes, and a canonical SHA-256 digest. JSON is the v1 identity format; YAML may later be an authoring format only if it compiles to the same canonical JSON. Unknown and duplicate fields, trailing values, unsupported schema versions, invalid vocabularies, ambiguous ordering, and oversized documents fail closed.

`internal/policy/cel` confines `github.com/google/cel-go` v0.31.0 and implements the existing owned evaluator port. CEL sees only a freshly constructed `facts: map<string,string>` and `region: string`. Macros, comprehensions, functions, receiver calls, dynamic indexing, field selection, object or collection construction, arithmetic, conditionals, native Go structs, clocks and I/O are excluded by parser limits plus a checked-AST allow-list. Every fact index is a static validated key and must exactly match the rule's declared contributing facts. Expressions are boolean, at most 2,048 bytes and 64 AST nodes, with depth 32 and runtime cost 1,000. Compiled programs are immutable and concurrently reusable. The evaluator pins the CEL release, subset and limits in its implementation digest and requires the snapshot's policy and evaluator references to match before evaluation.

The seventh V-03 foundation **is implemented**. `internal/policy` now owns immutable canonical revisions, exact evaluator references, a compiler-before-write catalog service, and monotonic actor-attributed activations. Migration `000020_policy_catalog` stores tenant-scoped policy roots, append-only revisions and append-only activation history. Registration retries must match canonical bytes, evaluator identity and creation time exactly. Active-pointer changes use an explicit expected activation version, advance it by exactly one and append previous revision, new revision, actor and UTC time atomically. Forced row-level security, foreign keys and append-only triggers provide database defence in depth.

The same activation primitive may select any registered immutable revision, including an older one. It does not itself decide whether that action is an operational rollback or whether approval, step-up authentication or dual control is required; those remain application and administration policy.

The eighth V-03 foundation **is implemented**. `internal/policy/cel.Resolver` implements the existing owned evaluator port by loading and compiling the exact immutable revision pinned in each snapshot; it never follows the mutable active pointer during evaluation. Its explicit-capacity LRU is bounded to at most 4,096 compiled entries, keys tenant, policy, revision, public schema and digest plus evaluator identity, deduplicates one cold key, allows waiters to cancel independently, retries after a cancelled owner, starts no goroutines and does not retain failures. Durable reference, canonical digest and evaluator identity are verified before reuse.

`internal/policy.ActiveInputLoader` combines one atomically supplied `AuthoritativeState` with the catalog's active immutable revision. The projection contains only selected policy, authority and acknowledgement references, explicit region and bounded owned facts; it cannot contain raw evidence or provider payloads. For v1 the tenant explicitly selects a policy when creating a verification session, and that policy identifier is an immutable part of the session snapshot. The active revision is resolved and pinned when the decision snapshot is authored. Existing upgraded sessions without a trustworthy assignment fail closed rather than receiving an invented assignment.

The ninth V-03 foundation originally deferred assignment and fact semantics. The selected v1 completion contract is: every new verification session immutably pins the tenant-selected policy identifier; each validated namespaced normalised signal maps directly to the same public policy fact key; and `satisfied`, `not_satisfied`, and `inconclusive` map one-to-one. `unavailable` and `prohibited` are never inferred from those signal outcomes; they are derived only from authoritative terminal workflow or authority state with exact provenance. Duplicate projected fact keys fail closed. The projection remains reference-only and never loads raw evidence or provider payloads.

The tenth V-03 foundation originally deferred its production trigger. The selected v1 trigger enqueues `policy.author` only after capture completion and after every required verification check is terminal. The durable intent uses a stable decision identity and readiness timestamp so rediscovery is an exact idempotent replay. `internal/policy.Builder`, the decision store, and the transactionally fenced handler retain their existing deterministic and at-least-once semantics.

The eleventh V-03 foundation **was implemented before selecting the public policy-testkit package split**. `internal/policy.Simulator` accepts exact canonical policy bytes plus explicit synthetic tenant, verification, authority, subject-response, region, evaluation-time and reference-only fact inputs. It compiles through a narrow owned port without registering or activating the revision, constructs the same immutable snapshot used by production, evaluates through the same owned evaluator boundary, and re-enters deterministic resolution for terminal and non-terminal directives. It has no repository, clock, provider, model, task, network or filesystem capability and cannot author an authoritative decision or workflow effect. `internal/policy/cel.Compiler` implements the simulation compiler port without leaking CEL types. A separately versioned, bounded simulation bundle closes over canonical policy, snapshot and evaluation bytes plus their nested digests and a complete payload digest. Restoration strictly rejects malformed, unknown, duplicate, trailing, non-canonical, oversized, cross-identity or changed-digest meaning and re-enters snapshot and evaluation restoration without CEL or PostgreSQL. Its safe report contains only identifiers, versions, digests, selected directive, optional terminal outcome and assurance, counts, explicit evaluation time and reproduction status; it omits facts, source references, reason codes, expressions and canonical inputs. This internal foundation did not determine the later selection of the root-module `conformance/policy` package.

The twelfth V-03 foundation **was implemented before selecting the public policy-testkit package split**. `internal/policy.ScenarioSuite` runs at most 256 uniquely named synthetic examples through the existing simulator in canonical name order. Each expectation is expressed as owned requirement results and optional assurance, then resolved against the scenario's immutable snapshot through the same production resolver; an expectation mismatch is report data so one run retains every mismatch, while malformed input, invalid expected meaning, cancellation or execution failure aborts the run. Results include the self-checking actual simulation bundle, exact expected evaluation digest and safe actual report. The suite report is input-order independent, carries a digest over its safe canonical payload and excludes facts, provenance, expressions, requirement details, reasons and canonical policy bytes. The runner is sequential, starts no goroutines, owns returned slices and remains an internal foundation rather than a substitute for the now-selected public `conformance/policy` API.

The thirteenth V-03 foundation **is implemented without selecting a policy administration surface**. `internal/policy.CatalogInspector` consumes narrow metadata-only revision and activation-history ports and returns immutable newest-first pages of at most 100 items. Zero means the newest internal boundary; subsequent pages use exclusive numeric revision or activation-version boundaries, while public cursor encoding remains unresolved. The PostgreSQL adapter selects revision identity, evaluator identity, actor, previous revision and UTC timestamps without loading canonical policy bytes, forces tenant scope in a read-only transaction, validates every durable field, and rejects oversized, unordered, cross-policy or malformed repository results. Existing migration 20 primary keys support these reads, so no schema change or new mutable capability is introduced. Empty cross-tenant pages preserve non-disclosure. This inspection seam cannot register or activate a policy and does not define HTTP, CLI, permissions, approval, rollback or Console behaviour.

The public policy test-kit split is now **Selected and implemented** as `conformance/policy` in the root Go module. It depends only on `contracts/policy/v1` and exposes a narrow engine interface plus bounded portable cases. The suite runs each case twice, compares exact ordered results, verifies defensive input ownership and cancellation, and rejects deliberately non-conforming implementations. It does not import `internal/policy`, expose CEL types, register or activate policies, or author authoritative decisions. Policy administration, activation approval, rollback authority, public cursors, and their transport surfaces remain unresolved separately.

### 6.8 `provider`

Owns provider capabilities, configuration validation, selection, execution requests, stable results, stable error classification, and provider lifecycle metadata.

External provider implementations live under `adapters/providers`, not inside this domain package.

The dependency-free public v1 adapter vocabulary lives under `contracts/provider/v1`. It uses same-major, supported-minor compatibility and structurally permits only pinned provenance, capability and restriction snapshots, secret references, scoped evidence-grant redemptions, bounded stable results, redacted failures, and safe health. Version 1.1 adds at most 32 uniquely named opaque structured-input references. Every name must be declared by the pinned capability, input-only checks require no dummy evidence grant, and a v1.0 request cannot smuggle the additive field. The runner resolves values only inside the isolated adapter boundary; identifier values never appear in the public attempt envelope, task payload, ordinary telemetry, or adapter manifest. The public conformance harness lives under `conformance/provider`; it does not import `internal` packages.

D-014 selects Smile ID and Dojah as the first real adapters for mobile-first regulated fintech onboarding of adult individuals in Nigeria, Ghana, Kenya, and South Africa. Each adapter lives under `adapters/providers`, runs through the isolated provider-runner contract, uses tenant-owned secret references, and publishes an exact versioned country/capability/restriction manifest. Neither provider name enters domain policy as a universal primary. Routing and fallback operate on owned capabilities and may substitute a provider only when the processing authority, recipient, region, evidence semantics, and assurance remain compatible. Sandbox support is not evidence of production entitlement; production startup fails closed without the tenant's provider agreement, credentials, and compatible regional configuration.

The pre-release implementations are `adapters/providers/dojah` and `adapters/providers/smileid`. They consume only Idenqa-owned secret, structured-input, evidence, HTTP and clock/wait ports. Dojah maps the reviewed synchronous HTTP operations; Smile ID maps signed preparation, bounded package upload, signed job-status polling and duplicate-job reconciliation. Provider HTTP shapes stop at the adapter. Their checked-in manifests are catalogue review records, not claims of tenant entitlement or provider certification. Whether these adapters remain in the root release or become independently versioned modules remains unresolved under section 17.

The shared initial capability baseline is live selfie, liveness/PAD, one-to-one face comparison, national identity document, passport and driving-licence capture, document quality, and MRZ/barcode where declared. Authority-backed v1 identifiers are Nigeria NIN/VNIN with BVN only in an explicitly banking-compatible profile, Ghana Card, Kenya National ID or passport, and South Africa National ID. English is the initial catalogue language. NFC, voice, KYB, AML, address, tax, phone, additional countries, non-English localisation, and documents without a tested pack remain outside D-014.

### 6.9 `model`

Owns model manifests, capabilities, input/output contracts, version selection, evaluation metadata, and stable errors.

External model implementations live under `adapters/models` and execute through the model-runner boundary where isolation is required.

The dependency-free public v1 model vocabulary lives under `contracts/model/v1`. It pins model, runtime, preprocessing, configuration, output-schema, capability, and resource restrictions while accepting evidence only through scoped grant redemptions. The public conformance harness lives under `conformance/model`; runner transport and process isolation remain separate from the model contract.

### 6.10 `review`

Owns review cases, assignments, findings, reason codes, resolution, appeals, and review audit events. It does not own a commercial reviewer dashboard.

### 6.11 `privacy`

Owns retention classification, deletion requests, erasure progress, backup/tombstone semantics, exports, and privacy-operation proofs.

### 6.12 `audit`

Owns audit event semantics, append-only records, integrity checkpoints, verification, and authorised export.

The O-01 v1 audit format is **Selected and implemented**. Each tenant has a monotonic sequence whose closed canonical reference-only records are linked with SHA-256; Ed25519 checkpoints sign an exact sequence and chain head under an immutable key ID. Migration 24 stores serialised tenant heads, forced-RLS append-only records, public-key history, and forced-RLS append-only checkpoints. `internal/audit/postgres` owns persistence and repeatable-read export, while the application boundary rechecks `audit:export`. The dependency-free verifier and `idenqa audit verify` consume a bounded portable export and public-key file without opening PostgreSQL or trusting the running core. This detects gaps, deletion, reordering, mutation, wrong keys, and invalid signatures; it does not claim protection from compromised signing-key custody or deletion of every independently controlled export.

### 6.13 `delivery`

Owns tenant webhook endpoints, signing-key references, delivery intents, attempts, retry state, endpoint disablement, replay authorisation, and safe delivery diagnostics.

`delivery` owns the durable customer-facing operation. `transport/callback` performs HTTP delivery and callback receipt. Transport failures never become identity outcomes without an explicit workflow rule.

The V-04 delivery contract is **Selected and implemented**. Migration 23 persists forced-RLS endpoints, append-only KMS-wrapped signing-secret versions, deliveries with explicit replay lineage, and append-only attempts. `webhooks:configure` and `webhooks:replay` are separate application permissions. The reference-only `webhook.deliver` v1 Headgate task uses the `delivery` queue, eight bounded attempts, one-second-to-one-hour backoff with deterministic jitter, and 30-day successful metadata retention. The callback adapter requires HTTPS and TLS 1.2+, pins approved public DNS answers for each attempt, refuses redirects and unsafe addresses, and bounds response handling. The public Go SDK verifies the exact signed body under active-plus-overlap secrets and documents timestamp-window checks and durable event-ID deduplication.

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
- `platform/id`, `platform/clock`, `platform/health`, and `platform/httpclient` provide narrow testable process primitives.
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

HTTP clients use `Idempotency-Key`. Realtime clients use a stable `command_id`. Provider requests use a stable attempt ID and provider idempotency key. Webhook and callback consumers deduplicate by signed event or delivery ID.

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

The owned task contract retains each task's maximum attempts, initial backoff, maximum backoff, and jitter. Headgate v0.1.2 enforces the maximum-attempt budget but does not invoke its declared `Config.RetryPolicy`. The Idenqa Store decorator therefore reconstructs the policy from each claimed durable envelope and passes the exact owned delay through Headgate's supported Ack override. The delay uses the task identity and one-based attempt, preserving deterministic-driver parity across worker restarts. Deployment-wide pgx base and cap remain a fallback for non-Idenqa retries and crash recovery rather than replacing task policy.

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

The selected rewrap execution primitive operates on one exact tenant-scoped evidence aggregate and expected version. It unwraps through the recorded provider identity, purpose, and authenticated context; wraps through the provider's active key; immediately unwraps the target and constant-time verifies that the keyset is unchanged; clears plaintext copies; and only then persists. PostgreSQL locks the expected aggregate version and atomically changes only provider-neutral wrapped-key metadata, aggregate version, and update time. The same transaction appends the normal aggregate event plus immutable principal, effective tenant actor, reason, and old/new key identities without storing either wrapped-key value. Ciphertext, object identity, content revision, integrity, lifecycle, and quarantine state remain unchanged, including when the asset is already quarantined. The open-source Cobra CLI exposes this primitive as `idenqa evidence-key rewrap` with an exact tenant, evidence ID, version, explicit confirmation, and complete attribution. Fleet discovery, batching, retry scheduling, rotation cadence, first production KMS/HSM adapter, and content-algorithm migration rollout remain unresolved under decision 16.

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

The canonical root module path is **Selected** as `github.com/Mujhtech/idenqa`, matching the public repository. The S3 adapter module path is **Selected** as `github.com/Mujhtech/idenqa/adapters/objectstore/s3`. Future independently released SDK and other adapter module paths remain launch decisions and must match their published repository or submodule paths. Local `replace` directives must not be committed as a substitute for correctly versioned releases; `go.work` is used for local composition.

During pre-release development, `go.work` resolves the S3 adapter's import of the root-owned object-store contract and the S3 distribution's imports of both modules. Because no root or adapter version exists yet, these independent modules cannot record or tidy valid inter-module requirements without inventing unpublished versions. Before either module's first independent release, its `go.mod` must require the first compatible published dependencies, then pass standalone `go mod tidy`, `go mod verify`, tests, lint, and vulnerability checks with `GOWORK=off`. This is a release gate, not permission to commit a local `replace`.

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

The root `api` keeps a cloud-SDK-free local composition selected for development and simple self-hosting. Evidence ingress is enabled only when `IDENQA_EVIDENCE_LOCAL_DIRECTORY` and `IDENQA_EVIDENCE_LOCAL_KEYRING_FILE` are both set to existing resources; partial pairs and surrounding whitespace fail validation. `IDENQA_EVIDENCE_PROTECTION_CLEANUP_TIMEOUT` defaults to five seconds and bounds cancellation-independent exact-object compensation. Provider distributions inject the same owned object and key ports without setting a local storage directory. The selected production composition module is `github.com/Mujhtech/idenqa/distributions/s3`: it retains evidence ingress in the public `api`, composes the independently versioned S3 adapter plus Tink and the mounted local keyring, and keeps AWS dependencies outside the root module. Its strict combined environment schema reuses `IDENQA_EVIDENCE_LOCAL_KEYRING_FILE` and adds required `IDENQA_S3_BUCKET` and `IDENQA_S3_REGION`; optional prefix, endpoint, path-style, explicit plain-HTTP opt-in, and a 30-second bounded ambiguous-upload cleanup timeout remain distribution-owned settings. AWS credentials and custom certificate authorities use the SDK's standard external chain. A dedicated `evidence` process remains a later deployment option only when demonstrated isolation or scaling needs justify the extra service boundary.

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

| Package                                              | Status       | Scope                                                                |
| ---------------------------------------------------- | ------------ | -------------------------------------------------------------------- |
| `github.com/mujhtech/headgate/go`                    | **Selected** | Headgate v0.1.2 Go task definitions, enqueueing, runners, and hooks   |
| `github.com/mujhtech/headgate/go/driver/headgatepgx` | **Selected** | Headgate v0.1.2 PostgreSQL backend and transactional pgx integration  |
| `github.com/mujhtech/headgate/go/headgatemigrate`    | **Selected** | Headgate v0.1.2 migration-only engine used by the explicit CLI path   |

Headgate v0.1.2 is the selected production background-work system. Idenqa uses its PostgreSQL backend; Headgate's MySQL and Redis backends, Rust SDK, workflow module, UI, and optional integrations are not selected merely by choosing the core library. River and Asynq are not part of the Idenqa dependency set.

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

| Package                    | Status       | Scope                                                                             |
| -------------------------- | ------------ | --------------------------------------------------------------------------------- |
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

General cryptographic operations use the Go standard library. Tink Go v2.8.0 provides the reviewed Streaming AEAD implementation for large evidence objects, initially using `AES256_GCM_HKDF_1MB`, but its types never cross `platform/crypto`. The v2.8.0 intake is Apache-2.0, compatible with the selected Go 1.26.6 toolchain, checksum-locked, and includes the upstream correction for silent truncation in the streaming reader. Provider-specific KMS and HSM SDKs remain isolated in adapter modules.

F-03 uses `github.com/oklog/ulid/v2` v2.1.2 behind Idenqa-owned prefixed value types and an injected generator. Domain packages expose resource-specific ID types rather than the library type. Production generation uses cryptographic, concurrency-safe monotonic entropy; clocks and entropy remain injectable for deterministic tests. ULID timestamps are not authoritative business timestamps, and pagination uses an authoritative ordering field plus the identifier as a stable tie-breaker.

### 11.7 CLI

| Package                          | Status       | Scope                                                                                          |
| -------------------------------- | ------------ | ---------------------------------------------------------------------------------------------- |
| `github.com/spf13/cobra` v1.10.2 | **Selected** | Shared command trees, flags, help, version output, and shell completion for public Go binaries |

`internal/cli` constructs the shared root conventions while each `internal/bootstrap/*` package owns its process-specific command tree and composition. Every execution receives a fresh tree, uses `RunE` for cancellable operational commands, writes only through Cobra's command output streams, and preserves exit code `2` for invalid usage separately from exit code `1` for operational failure. Production execution lets Cobra read process arguments; explicit argument injection exists only for tests and embedded callers. Viper is not selected. Configuration is loaded explicitly through `envconfig`, with `.env` support limited to `godotenv` at the bootstrap boundary.

The initial self-hosted CLI covers migrations and preflight checks, tenant and API-key management, health diagnostics, webhook replay, failed-work inspection and recovery, retention execution, authorised export, and deletion workflows. These capabilities are not reserved for Console or Cloud.

### 11.8 Observability

| Package                                                                       | Status                        | Scope                                            |
| ----------------------------------------------------------------------------- | ----------------------------- | ------------------------------------------------ |
| `log/slog`                                                                    | **Selected standard library** | Structured application logging                   |
| `go.opentelemetry.io/otel` module family                                      | **Selected**                  | Traces, metrics, propagation, SDK, and exporters |
| `github.com/riandyrn/otelchi`                                                 | **Selected**                  | Chi route instrumentation                        |
| `go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc` v0.71.0 | **Selected**                  | gRPC runner client/server instrumentation        |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc` v1.46.0     | **Selected**                  | OTLP/gRPC trace export                            |
| `go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp` v1.46.0     | **Selected**                  | OTLP HTTP/protobuf trace export                   |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc` v1.46.0   | **Selected**                  | OTLP/gRPC metric export                           |
| `go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp` v1.46.0   | **Selected**                  | OTLP HTTP/protobuf metric export                  |

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

| Package                                      | Status               | Scope                                                                         |
| -------------------------------------------- | -------------------- | ----------------------------------------------------------------------------- |
| `google.golang.org/grpc` v1.83.2             | **Selected**         | Adapter-runner and model-runner RPC transport                                 |
| `google.golang.org/protobuf` v1.36.12        | **Selected**         | Versioned runner messages and generated Go types                              |
| `buf.build/go/protovalidate` v1.3.0          | **Selected runtime** | Generated-message boundary validation                                         |
| Buf CLI v1.72.0                              | **Selected tool**    | Protobuf linting, breaking-change checks, and reproducible generation         |
| `protoc-gen-go` v1.36.12                     | **Selected tool**    | Reproducible Go message binding generation                                    |
| `protoc-gen-go-grpc` v1.6.2                  | **Selected tool**    | Reproducible Go gRPC binding generation                                       |

gRPC and Protobuf are used for isolated runner contracts, not as a requirement for the public customer API. HTTP remains the primary public API transport.

The reviewed runner intake found active upstream maintenance, a Go-version requirement compatible with Go 1.26.6, no direct OSV advisories for the exact selected versions, Apache-2.0 licensing for gRPC, Buf, Protovalidate, `protoc-gen-go-grpc`, and `otelgrpc`, and BSD-3-Clause licensing for Protobuf Go. The full reachability scan finds no vulnerable imported packages or called symbols. It separately reports GO-2026-5932 against the unmaintained `golang.org/x/crypto/openpgp` package in a required module, but Idenqa does not import that package and the advisory has no fixed release; the module remains monitored rather than being misreported as a reachable runner vulnerability. Protovalidate's current canonical Go module is `buf.build/go/protovalidate`; the retired GitHub import path must not be reintroduced. Generated Go bindings are committed under `internal/gen/proto/runner/v1`, while the authoritative source is the typed v1 schema under `contracts/runner`.

The v1 runner transport uses a per-runner bearer credential with the display form `idq_wrk_v1_<secret>`, where `secret` is 32 cryptographically random bytes encoded as unpadded Base64URL. It is accepted only as exactly one `Authorization: Bearer` gRPC metadata value over server-authenticated TLS 1.2 or newer. The client requires transport security before sending it; the server retains only SHA-256 digests in its authentication set after startup. One current and one previous credential may overlap for rotation. Missing, malformed, unknown, and removed credentials are non-disclosing; credential material must never enter Protobuf messages, URLs, logs, traces, metrics, audit payloads, or adapter contracts. Each remote runner has an explicit certificate-authority trust root and expected server name. Client mTLS, dynamic credential reload, and external secret-manager loading may be added behind owned adapters later and are not required for the initial self-hosted runner.

Every runner RPC requires a caller deadline within a process-configured maximum capped at ten minutes. gRPC context cancellation reaches the adapter. The v1 wire ceiling is 320 KiB and the existing public result ceiling remains 256 KiB, enforced on both service and client adapters. Compression is not enabled. `otelgrpc` uses process-owned providers and W3C propagation; runner payload, credential, tenant, subject, verification, evidence, and grant values are never telemetry attributes or baggage.

### 11.10 Object storage and cloud adapters

The root core module defines object-storage, KMS, and secret-provider ports. Cloud SDKs are confined to optional adapter modules.

| Package/module                                     | Status       | Scope                                                                           |
| -------------------------------------------------- | ------------ | ------------------------------------------------------------------------------- |
| `github.com/aws/aws-sdk-go-v2` v1.45.1             | **Selected** | AWS configuration and shared types inside the optional S3 adapter               |
| `github.com/aws/aws-sdk-go-v2/config` v1.33.1      | **Selected** | External credential, region, and HTTP-transport configuration                   |
| `github.com/aws/aws-sdk-go-v2/service/s3` v1.109.1 | **Selected** | S3-compatible immutable ciphertext PUT, exact GET/DELETE, and bounded inventory |

The independently versioned `adapters/objectstore/s3` module implements the owned exact-version object-store boundary. It streams bounded ciphertext without whole-object buffering, creates a random physical version under the logical tenant-scoped key, uses `If-None-Match: *`, records the ciphertext size and SHA-256 digest, verifies both while reading, and deletes only the exact physical version. An ambiguous PUT attempts bounded cancellation-independent cleanup; if cleanup cannot be confirmed, the caller receives the exact owned reference for durable reconciliation. A failed create precondition never deletes another writer's object.

The same owned boundary exposes a separate bounded, cursor-paginated inventory capability for residual orphan discovery. Inventory records contain the logical key, exact physical version, provider size, and modification time but no authenticated checksum, so they cannot be promoted into readable evidence objects or persisted evidence metadata. The core constructs a tenant evidence prefix, accepts only canonical Idenqa evidence keys and generated physical versions, waits beyond the maximum upload-attempt lease plus a provider/application clock-skew allowance, and asks PostgreSQL whether an evidence asset or active reconciliation record protects the exact key and version before deletion. Provider entries outside that shape are ignored. A failed scan, classification, or deletion replays the page; the object store remains the durable inventory. Both the local adapter and S3 adapter implement this capability; production scheduling enters through the selected Headgate adapter behind the owned `platform/task` boundary.

HTTPS is the default and uses the SDK's standard TLS-aware payload-signing behaviour. A deployment may explicitly allow an HTTP endpoint for a controlled development or private-network environment; because the body is deliberately non-seekable, that mode selects S3's supported `UNSIGNED-PAYLOAD` signing form. Application-layer authenticated encryption and digest verification still apply, but they do not make an untrusted network safe; production deployments should use HTTPS and an externally configured trusted certificate chain.

The reviewed dependency intake found Apache-2.0 licensing, active upstream maintenance, and Go 1.24 minimum declarations compatible with Go 1.26.6. The selected S3 v1.109.1 is newer than v1.97.3, which fixed Go vulnerability advisory `GO-2026-5764`. Credentials and custom certificate authorities remain external AWS SDK configuration rather than Idenqa secret fields. Provider errors and SDK types do not cross the owned core boundary.

No AWS, GCP, Azure, or proprietary provider SDK belongs in the root core module merely to satisfy an optional deployment.

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

| Native SDK candidate | Status | Scope |
| --- | --- | --- |
| Swift tools 6.2, Apple first-party frameworks, iOS 16+ | **Selected** | Foundation HTTP/WebSocket and cancellation, strict concurrency, Keychain, Secure Enclave P-256 proof, and AVFoundation capture with no third-party runtime dependency |
| Android Gradle Plugin 9.4.0 with built-in Kotlin 2.3.21, Gradle 9.6.0, JDK 17, API 26+ | **Selected** | Android library build; the wrapper records the official Gradle distribution checksum |
| `org.jetbrains.kotlinx:kotlinx-coroutines-core` 1.11.0 | **Selected** | Cancellable suspend operations and public `Flow` realtime observation |
| `com.squareup.okhttp3:okhttp` 5.3.0 | **Selected** | Android HTTPS and WebSocket transport behind SDK-owned interfaces |
| `com.squareup.moshi:moshi` 1.15.2 | **Selected** | Bounded Android JSON mapping without reflection or code generation |

Both native SDKs provide platform secure-token stores, generate non-exportable hardware-backed P-256 proof keys, sign the published native-bootstrap transcript, and expose optional platform-attestation provider boundaries. The server has a matching owned verifier port and rejects supplied attestation when no verifier is composed. The tenant backend delivers a single-use Idenqa capture token; first redemption binds that token atomically to an allow-listed application identifier and proof-key digest. Capability advertisements narrow method choice and never prove assurance. Native raw capture remains owned by these SDKs rather than a Flutter or React Native bridge.

SDKs depend only on published contracts. They never import or reproduce internal domain implementations.

The TypeScript workspace uses pnpm. `sdk/typescript` is published as `@idenqa/sdk` for Node.js 22+ and modern browsers. Strict TypeScript and tsdown produce ESM, CommonJS, declarations, and source maps; Vitest owns package tests. `openapi-typescript` generates committed internal contract types from the authoritative OpenAPI document, and CI rejects regeneration drift.

Generated contract symbols are implementation details and are not exported as the public SDK API. The handwritten, zero-runtime-dependency facade owns clients, stable errors, request-ID propagation, idempotency-key behaviour, `AbortSignal` cancellation, and the mapping from wire representations to public SDK types. Networking uses `globalThis.fetch` by default with explicit fetch injection for tests and compatible custom runtimes. This boundary allows the OpenAPI generator to change without silently changing customer code.

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

| Package                    | Status            | Capture-Web scope                                                    |
| -------------------------- | ----------------- | -------------------------------------------------------------------- |
| `lit` v3.3.3               | **Selected**      | Framework-neutral Web Component runtime                              |
| `vite` v8.2.2              | **Selected tool** | Development server and plain-HTML browser fixtures only              |
| `@playwright/test` v1.62.1 | **Selected tool** | Real Chromium, Firefox, and WebKit browser tests as coverage expands |

The reviewed intake found active upstream maintenance and BSD-3-Clause, MIT, and Apache-2.0 licensing respectively, all compatible with Idenqa's Apache-2.0 distribution. React, React DOM, and their type packages are actively maintained and MIT-licensed. The resolved production graph contains only Lit and its five BSD/MIT dependencies. Vite, Playwright, React, React DOM, and the React type packages remain development-only, the workspace supply-chain policy passes, and the npm audit reports no known vulnerabilities. Neither Vite nor React is used to build the published package, avoiding coupling the library format to a development fixture or framework host.

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

The following items remain deliberately unresolved:

1. Canonical published paths for future independently released SDK and adapter modules other than the selected S3 adapter; the root module is selected as `github.com/Mujhtech/idenqa` and the S3 module as `github.com/Mujhtech/idenqa/adapters/objectstore/s3`.
2. The retention duration of each future concrete task type other than the selected 30-day successful-metadata retention for `verification.execute` v1 and `verification.reconcile` v1. Mandatory bounded retention is selected, but future durations belong with the task definition introduced by its owning brick. Headgate installation identity, schema and migration composition, queue and rate-class catalogue, partition mapping, retry enforcement, fleet rate/burst defaults, per-tenant concurrency defaults, process controls, telemetry ownership, v0.1.2 release, and Go/PostgreSQL module paths are selected.
3. **Resolved:** D-016 selects per-process OTLP over either gRPC or HTTP/protobuf, disabled-by-default self-hosting, TLS except explicit loopback development, redacting authentication headers, optional private CA and mTLS, parent-based configurable trace sampling, bounded batching and retry, and periodic unsampled metrics.
4. Whether Ozzo is needed beyond configuration validation.
5. Which adapters other than the independently versioned S3 adapter ship in the root release versus independently versioned modules. The S3 distribution-composition module is selected as `github.com/Mujhtech/idenqa/distributions/s3` and keeps evidence ingress in `api`.
6. The public API client and webhook verifier are **Selected** in the dependency-light `sdk/go` module. Provider and model contracts and conformance suites are selected under `contracts/{provider,model}/v1` and `conformance/{provider,model}`. The public policy engine test kit is selected under `conformance/policy` and depends only on `contracts/policy/v1`. Future independently released module paths for these root-module packages remain unresolved under item 1.
7. Detailed dependency-licence allow-list and review process within the selected Apache-2.0 distribution policy.
8. Credential formats and lifecycle contracts beyond the selected initial tenant API key, v1 deterministic signed capture-token contract, and v1 runner transport credential. Dynamic runner-credential reload and external secret-manager loading, delegated access, support access, break-glass flows, and any future capture-token versions remain unresolved under `internal/access` and owned adapters.
9. Durable WebSocket event replay retention and deployment topology. D-010 selected the v1 catalogue, single-use query-ticket bootstrap and lifetime, connection-local sequence policy, version rules, and bounded R-01 limits.
10. Per-operation idempotency retention, response replay, and duplicate-in-progress behaviour beyond the selected capture-profile command policy.
11. Transaction isolation, row-locking, optimistic-version, and PostgreSQL advisory-lock policy for workflows beyond the selected capture-profile command policy.
12. Distributed rate-limit enforcement strategy that preserves the no-Redis baseline.
13. A future byte-offset resumable protocol and its encryption-compatible staging design for large video or other media. The E-03 v1 single-request limits, SHA-256 integrity, expiry, full-body retry, exact-object cleanup, and reconciliation contract are selected under D-009.
14. Whether a future optional `@idenqa/sdk-effect` adapter or internal Effect adoption meets the bundle budget, browser-compatibility, and stable-release requirements. Effect is excluded from the current public SDK and Web capture runtime.
15. The first production KMS/HSM adapter, operational rotation cadence, and the trigger and rollout policy for a content-algorithm migration. Tink Go v2.8.0, the local provider, provider-neutral adapter boundary, rewrap-first key rotation, and cryptographic-agility envelope are selected.
16. The deletion boundary for backups and externally delivered payloads. The v1 audit tamper-evidence format is selected as a canonical tenant-sequenced SHA-256 chain with Ed25519 checkpoints, immutable key IDs, bounded portable export, and offline verification.
17. **Resolved:** deployment defaults are raw and derived evidence 30 days, webhook payloads 7 days, workflow metadata 365 days, reference-only audit records and deletion tombstones 7 years, and backup expiry 35 days. Tenant policy may shorten retention; extension requires an explicit deployment cap. Legal holds override expiry. Processing and storage are immutably pinned to the session region with no silent cross-region fallback.
18. Policy activation authorisation and approval (including operational rollback treatment) and the policy administration surface remain unresolved. The v1 tenant-to-policy assignment, direct namespaced signal-to-fact mapping, one-to-one normalised outcomes, authoritative-only `unavailable`/`prohibited` derivation, and capture-complete plus required-checks-terminal `policy.author` trigger are **Selected**. Public cursors, permissions and administration remain unresolved.

---

## 18. Acceptance criteria

This structure is accepted when:

- The public core builds with Go 1.26.6 without a commercial repository.
- The core, SDKs, capture packages, contracts, conformance suites, and bundled examples carry Apache-2.0 licensing and pass the dependency-licence policy check.
- `api`, `worker`, `evidence`, `adapter-runner`, `model-runner`, and `idenqa` entry points contain only composition and process lifecycle code.
- Domain packages compile without HTTP router, SQL driver, task-library, telemetry SDK, or cloud SDK imports.
- The Go SDK compiles without access to root `internal` packages.
- The optional S3 adapter passes real AWS SDK conformance over HTTPS and explicitly enabled HTTP, streams without whole-object buffering, detects ciphertext modification, preserves exact versions, and leaves the root module free of cloud SDK dependencies.
- The Web capture package can be embedded in a non-React page and uses the public TypeScript SDK.
- PostgreSQL remains sufficient to recover durable workflow and job intent.
- The selected background library is replaceable through `platform/task` without changing domain APIs.
- A state transition, its outbox event, and resulting task intent commit atomically or not at all.
- Inbox deduplication and task fencing prevent duplicate delivery from producing duplicate domain effects.
- Retrying an idempotent operation with the same key and request fingerprint replays the recorded result; a different fingerprint conflicts; concurrent duplicates do not execute the operation twice.
- Authorisation is enforced by application services using an explicit access context, even when an operation is invoked outside HTTP.
- The API process fails before opening PostgreSQL when its active API-key pepper or pepper set is absent, and `GET /v1/tenant` derives its sole target from the verified access context while repeating `tenant:read` authorisation below HTTP.
- API-key CLI create and rotate reveal the new credential exactly once after an atomically audited commit; list and revoke remain secret-free; destructive operations require explicit confirmation; and stale revocation versions fail without a lifecycle or audit write.
- API-key scope tests cover exact grants, resource wildcards, action wildcards, full wildcards, invalid partial globs, registry snapshot non-expansion, and exclusion of non-tenant permissions.
- Tenant-owned repository operations fail without verified tenant scope, and isolation tests attempt cross-tenant reads, writes, identifier probes, jobs, events, and realtime subscriptions.
- PostgreSQL row-level security denies tenant-table access without the expected tenant scope; every privileged bypass path is explicitly authorised and audited.
- Canonical policy snapshots, evaluations, and decisions restore only through owned validators; durable records are append-only, tenant isolated, bound to an existing verification, restricted to one linear decision lineage, and reject changed-meaning replay.
- Canonical policy revisions compile before registration, replay only on exact immutable meaning, remain tenant isolated and append-only, and switch active pointers only through an expected-version compare-and-swap that atomically records previous revision, selected revision, actor, and UTC time.
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
- Capture clients can reconnect by cursor, replay retained events in order, and fall back to REST snapshot recovery after a replay gap.
- Reconciliation discovery exposes only bounded routing identifiers, every item re-enters tenant scope, and only one installation scheduler enqueues exact attempt work while application and Headgate fences reject stale owners.
- Capture-safe check progress is atomically projected from the outbox, remains correct without PostgreSQL notifications, and cannot expose outcomes, signals, reason codes, provider data, or evidence through Go, JSON Schema, SDK, or Capture Web contracts.
- Realtime tests cover connection-ticket consumption, command deduplication, heartbeat timeout, slow consumers, clean shutdown, and protocol-version rejection.
- Raw evidence bytes and reusable credentials never travel in WebSocket messages; uploads use dedicated authenticated HTTP endpoints.
- Rate limits, bounded queues, deadlines, retry budgets, and backpressure prevent an overloaded dependency or slow client from creating unbounded work.
- Readiness, liveness, startup, and drain behaviour are verified for every long-running process.
- The documented self-hosted administrative and recovery operations are usable through `idenqa` without Console or Cloud.
- HTTP errors expose stable codes and request IDs without leaking internal error strings.
- The versioned OpenAPI source lints at the repository threshold, regenerates committed Go transport types and the minimal client without drift, rejects breaking pull-request changes against the base contract, and supplies synthetic fixtures that decode through generated client types.
- OTel instrumentation uses safe route templates and avoids identity-derived metric labels.
- A clean generation run produces no uncommitted changes.
- Import-boundary, lint, test, migration, conformance, and vulnerability checks pass in CI.
