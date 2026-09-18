# Idenqa Core Implementation Gap Audit v0.1 Draft

**Status:** Draft for review  
**Date:** 4 September 2026  
**Re-baselined:** 14 September 2026 — reconciled with the current build-plan statuses, generated OpenAPI, public TypeScript SDK, CLI, migrations, runtime composition, and Capture Web evidence. Historical implementation records retain the evidence and limitations recorded when each increment was completed.

**Compared architecture:** [`global-identity-core-technical-architecture-v0.6-draft.md`](global-identity-core-technical-architecture-v0.6-draft.md)  
**Repository decisions:** [`global-identity-core-repository-structure-and-packages-v0.1-draft.md`](global-identity-core-repository-structure-and-packages-v0.1-draft.md)  
**Build tracker:** [`global-identity-core-build-plan-v0.1.md`](global-identity-core-build-plan-v0.1.md)

This document records the implementation capabilities that are absent, partial, externally unproven, deliberately later, or deliberately deferred when the current repository is compared with the complete v0.6 technical architecture.

It is an audit snapshot, not a replacement architecture or build plan. It does not promote a Proposed, Conditional, or TBD item to Selected, and it does not change any brick status. The repository/package draft remains authoritative for package and dependency decisions, while the build plan remains authoritative for the scope and recorded evidence of individual bricks.

---

## 1. Audit interpretation

### 1.1 Gap classifications

| Classification                         | Meaning                                                                                                                                                                |
| -------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Implemented selected scope**         | The accepted repository-owned boundary is implemented; explicitly separate production, external-evidence, or later-scope work may remain                               |
| **Current-core gap**                   | Required by the open deterministic core or the v0.6 version-one boundary, but not implemented end to end                                                               |
| **Partial**                            | Domain, contract, adapter, persistence, test, or transport foundations exist, but the complete architecture capability is not runnable                                 |
| **Accepted later milestone**           | Accepted architecture capability planned after the initial deterministic core; absence is real but does not necessarily block version one                              |
| **Explicitly deferred or conditional** | The architecture, D-014, or another accepted decision intentionally postpones the capability                                                                           |
| **External evidence gap**              | Repository implementation exists, but provider, legal, device, security, resilience, or release evidence is still required                                             |
| **Alignment decision required**        | A narrower implementation decision and the broader v0.6 architecture describe different boundaries; the difference must be explicitly retained, scheduled, or resolved |

### 1.2 Static-audit limitation

This audit is based on repository and document inspection. It does not replace the exact toolchain, integration, provider-sandbox, device, penetration, load, soak, migration, disaster-recovery, or clean-room release evidence required by the build plan.

### 1.3 Important scope distinctions

- Non-deterministic AI is an **accepted architecture capability** scheduled for Milestone 6. The repository-owned proposal, guardrail, mode, registry, and bounded product slice is implemented and the build-plan milestone is **In review**. A real generative-model adapter and production evidence remain open; AI stays disabled by default and does not block version one while disabled.
- Tenant-isolated fraud analysis is implemented within sections 5.1/5.2. The cross-tenant fraud network remains explicitly deferred.
- Predictive model execution is separate from non-deterministic AI. Real predictive models are required for the selected biometric and document capabilities.
- Console, managed Cloud, Idenqa Pass, and reusable verification are later commercial capabilities and are not prerequisites for the open-source core.
- Flutter and React Native are required only when advertised as supported. They remain incremental open-source SDK family work.
- NFC, voice, KYB, AML, address, tax, phone, non-English localisation, additional countries, and unsupported resident or refugee documents are deferred by D-014.
- The more authoritative repository draft selects Go 1.27.1, Chi, Apache-2.0, and Headgate v0.1.10 even though section 43 of the v0.6 draft still labels some of them Proposed. They are not implementation decision gaps.

---

## 2. Overall finding

The repository contains strong foundations for tenancy, API-key and capture-token access, capture profiles, evidence encryption and upload, processing authority, WebSocket recovery, PostgreSQL-authoritative work, CEL policy evaluation, immutable decisions, assurance profiles and decision context, persistent identity, tenant-local fraud, review and recapture, audit verification, retention and deletion, provider and model contracts, provider/model runtime composition, AI proposal guardrails, SDK foundations, Capture Web, and vendor-neutral OpenTelemetry export.

The principal deficiency is production-complete composition and acceptance. The synthetic journey and several bounded review, provider, model, and AI paths are composed, but the following architecture journey is not yet demonstrated from a clean deployment with real selected providers and production-accepted models:

```text
subject and claims
    -> verification lifecycle
    -> capture and authority
    -> real provider or model execution
    -> immutable observations and facts
    -> assurance-aware policy decision
    -> manual review when necessary
    -> completed verification
    -> signed webhook
    -> retention, deletion, audit, and recovery
```

The current repository cannot yet run that complete journey using real selected providers and models from a clean self-hosted deployment.

### 2.1 Principal evidence anchors

| Audit area                      | Current implementation baseline                                                                                                                                                                                                                                                                                                | Principal remaining boundary                                                                                                                                                                         |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Verification lifecycle          | L-01 through L-03 compose the full state vocabulary, synthetic capture-to-decision delivery, cancellation, expiry, and recovery guards; O-03 composes nonterminal review routing, findings, linked recapture, credential recovery, and explicit re-evaluation.                                                                 | General resumption, operational-failure handling, complete consequential review follow-up, and recovery proof across every state.                                                                    |
| AI-native orchestration         | [`contracts/proposal/v1`](../contracts/proposal/v1) and [`internal/proposal`](../internal/proposal) implement proposal/command separation, deterministic guardrails, modes, registries, bounded products, public API, TypeScript SDK, and CLI. M-6 is **In review**.                                                           | Real generative-model adapter, external provenance, representative evaluation, deployment hardening, and independent guardrail review.                                                               |
| Predictive model execution      | The ONNX evaluation runner, immutable evaluation registry, threshold revisions, dataset comparison/drift tooling, and durable PAD/face-comparison composition are implemented.                                                                                                                                                 | Accepted data and weights, calibrated production thresholds, temporal liveness evidence, production deployment, and production promotion/rollback evidence.                                          |
| Tenant-local fraud              | [`internal/fraud`](../internal/fraud), migration 48, public API/SDK, and policy integration implement the selected tenant-local baseline.                                                                                                                                                                                      | Deployment-specific source acceptance and regional approval; cross-tenant intelligence remains explicitly deferred.                                                                                  |
| Subject and identity projection | [`internal/identity`](../internal/identity), migration 49, public API/SDK, and privacy/policy integration implement the initial core subject and identity model.                                                                                                                                                               | Real structured-provider ingestion, richer country normalisation, and larger incremental deletion planning.                                                                                          |
| Provider execution              | Dojah and Smile ID adapters are composed through bounded synchronous and asynchronous runtime paths with local-fixture recovery evidence.                                                                                                                                                                                      | Tenant-owned official-account runs, production secrets, provider health/callback/deletion operations, legal/regional approval, and equivalent fallback proof.                                        |
| Biometrics and documents        | Acquisition orchestration, ONNX preparation, evaluation-only PAD/face comparison, and provider document-analysis paths exist and remain fail-closed or inconclusive without accepted assurance.                                                                                                                                | Production PAD/face models and evaluation, complete temporal capture, OCR/MRZ/barcode and document classification/authenticity, plus derived-data deletion.                                          |
| Public API                      | The generated OpenAPI and TypeScript SDK include review, fraud, identity, assurance, proposal, cancellation, policy, and webhook administration in addition to the original capture/decision surfaces.                                                                                                                         | Resume, decision history/reconsideration, general evidence and consent administration, provider/model generated contracts, privacy/deletion status, policy simulation, and OAuth client credentials. |
| Webhooks                        | L-02 composes atomic completion delivery; H-01 adds public endpoint administration, inspection, rotation, and replay through API, TypeScript, and CLI.                                                                                                                                                                         | General event catalogue, subscriptions, versioned schemas/compatibility, scalable fanout, and clean-deployment proof.                                                                                |
| Capture experience              | Capture Web supplies the D-026 compact guided journey, active-liveness orchestration, arbitrary namespaced adapters, D-027's separately authenticated subject-safe outcome projection, nine live-Core journeys including authoritative expiry, and an appearance-only CSS-variable surface. E-04 and M-2 remain **In review**. | Obtain explicit advanced-interaction acceptance, then add the portable signed/versioned experience contract.                                                                                         |
| Self-hosted usability           | Individual binaries, migrations, runners, SDKs, Capture Web fixtures, and synthetic integration journeys exist.                                                                                                                                                                                                                | A clean packaged deployment still must compose the complete documented open-source operational contract; the development Compose file starts PostgreSQL only.                                        |
| External beta                   | X-03 and X-04 are **In review** with adapter, conformance, security/release workflow, SBOM, and provenance foundations.                                                                                                                                                                                                        | Provider/legal evidence, independent security review, load/soak, mixed-version and restore rehearsals, supported-device evidence, and a signed candidate.                                            |

---

## 3. AI-native orchestration

**Classification:** Implemented selected scope; build-plan M-6 **In review**; production and external evidence remain open

The v0.6 architecture distinguishes deterministic computation, predictive ML, and non-deterministic AI. `contracts/model/v1` remains the signal-only contract (`evidence refs -> scored signals -> deterministic policy`). `contracts/proposal/v1` now implements the separate `ProposalModel` (`redacted bounded context -> non-authoritative proposal -> guardrails/human approval -> deterministic AcceptedCommand`). The implementation preserves the required boundary and fail-closed defaults.

**Implemented — 10 September 2026 (M6 AI-01..AI-05):**

- `contracts/proposal/v1` — `ProposalModel` port, immutable `AgentProposal` envelope, bounded `Actions[]` (closed allow-list 8 kinds), `evidence_refs/signal_refs` refs only, `model_id/version/prompt_version/context_digest` pinning, `expires_at/supersedes`, `AcceptedCommand` separate, `Version` compatibility, `Digest/Canonical` and closed JSON validation. Raw evidence never in envelope/task/log/audit. See `contracts/proposal/v1/contract.go:1`, `validate.go:1`, `contract_test.go:1`.
- `internal/proposal/proposal.go:1` — `Proposal` aggregate, `isValidStatus/isTerminalStatus`, `Validate()`, `Advance()` graph `pending -> approved/rejected/expired/cancelled/superseded`, `Version` CAS, `id.Proposal/id.AcceptedCommand` (`ProposalPrefix prp_`, `AcceptedCommandPrefix acc_` in `internal/platform/id/id.go:1036`).
- `internal/proposal/guardrail.go:1` — 10 deterministic checks: schema, evidence/signal refs via `EvidenceChecker`, allow-list, tenant-policy (`modeAllows`), jurisdiction/residency + processing-authority via `RegionValidator`/`AuthorityChecker`, cost/rate via `CostLimiter`, raw-evidence/sensitive-context, human approval, deterministic command, audit linkage. `highRiskKinds` require human.
- `internal/proposal/mode.go:1` — `ModeConfig` versioned `disabled|assist|recommend|guardrailed_auto|human_required`, `AllowListVersion`, `AllowedKinds`, `CostDailyLimit`, `PromptID`, session-pinned at creation, fail-closed on unknown mode/kind, `InMemoryModeStore` with `expectedVersion` CAS.
- `internal/proposal/registry.go:1` — `PromptRecord` (with `Sensitive` classification)/`GenerativeModelRecord` immutable, `DigestPrompt()`, `ImpactAssessment`, `InMemoryRegistry` with `CreatePrompt/GetPrompt`, `CreateModel/GetModel`, `CreateImpact`.
- `internal/proposal/reference.go:1` — dependency-free deterministic `ReferenceModel` implementing `ProposalModel` and `ReferenceExecutor` implementing `CommandExecutor`; `Service.Propose` invokes the model, validates its `AgentProposal` output, and persists through the same guardrails. The reference model is wired into the API bootstrap, so the port is invocable in a running deployment; a real generative-model adapter remains an external gate.
- `internal/proposal/products.go:1` — `GeneratePolicyDraft` (sensitive-classified via `isSensitivePrompt`, 7d vs 24h expiry), `GeneratePolicyDiff`, `GenerateAdversarialScenarios`, `ProposeAccessibility/ExceptionPath`, `ProposeDocumentLayout`, `ProposeReviewCopilot/AdaptiveRoute` — all route through `Propose` (model-driven) and cover §3.4 review copilot, adaptive routing, NL drafts, diffs, adversarial, accessibility/exception, document layout. Policy-scoped products are anchored to `policy_id` (not a fake verification).
- `internal/proposal/service.go:1` — `Service` (manual DI, `clock.Clock`, `Repository/CommandStore/ModeStore/RegistryStore/ProposalModel/EvidenceChecker/AuthorityChecker/RegionValidator/CostLimiter/AuditRecorder`), `CreateProposal`/`Propose` (mode fail-closed, guardrail), `GetProposal`, `ApproveProposal` (human approval, creates `AcceptedCommandRecord` per action), `ExecuteCommand` (replay via stored command, `MarkExecuted` idempotent), `Reject/Cancel/Expire/Supersede`.
- `internal/proposal/postgres/store.go:1` + `mode_registry.go:1` + `guardrail.go:1` + `db/migrations/000051_proposal.up.sql:1`/`000052_prompt_sensitive.up.sql:1`/`000053_proposal_policy_anchor.up.sql:1` — durable `proposals` (verification- or policy-anchored), `accepted_commands`, `proposal_mode_configs`, `prompt_registry` (with `sensitive`), `generative_model_registry`, `impact_assessments` with `ENABLE/FORCE RLS`; postgres `ModeStore`, `RegistryStore`, `AuthorityChecker`, `RegionValidator`, `EvidenceChecker` adapters.
- `internal/proposal/memory.go:1`, `limiter.go:1`, `evidence.go:1` — in-memory `InMemoryProposalStore/CommandStore`, `InMemoryLimiter`, `InMemoryEvidenceChecker/AuthorityChecker/RegionValidator/AuditRecorder` for deterministic tests.
- `internal/transport/httpapi/proposal.go:1` + `internal/bootstrap/api/process.go:1` + `internal/transport/httpapi/apierror/error.go:1` — `ProposalRoutes` (`POST /v1/proposals`, `GET /v1/proposals/{proposalID}`, `POST /v1/proposals/{proposalID}/approve|reject|cancel`, `POST /v1/accepted-commands/{commandID}/execute`, `PUT/GET /v1/proposal-modes/{workflow}`, `POST/GET /v1/prompts`) with `proposals:read/write/approve/configure` + `prompts:read/write` in `internal/access/scope.go:1`, mapped to `400/404/409/403/429` via `apierror`.
- `internal/access/scope.go:1` — added `proposals:read/write/approve/configure`, `prompts:read/write` to `TenantRegistry`.
- `sdk/typescript/src/proposals.ts:1` + `client.ts:1` — dependency-free `ProposalsClient` (`create/get/approve/reject/cancel`, `putMode/getMode`, `createPrompt/getPrompt`) exposed as `IdenqaClient.proposals`.
- `internal/bootstrap/idenqa/proposal.go:1` — `idenqa proposal` command group (`create/get/approve/reject/cancel`, `mode get/put`, `prompt create/get`).

**Verification:** `contracts/proposal/v1` contract-compatibility + canonical round-trip, allow-list closed, duplicate-kind rejected; `internal/proposal` tests cover guardrail region/authority fail-closed, human-required approval + replay idempotent, expiry/supersession terminal conflict, prompt registry + impact assessment, and model-driven `Propose` through `ReferenceModel`. PostgreSQL integration tests (`TestProposalPersistenceIsolationAndReplay`, `TestProposalModeRegistryAndGuardrailPersistence`) prove RLS isolation, version CAS, mode/registry persistence, sensitive-prompt classification, and fail-closed guardrail checkers. `go vet`, `golangci-lint`, `go test -race`, `go test -short ./...`, TypeScript typecheck/tests, and `govulncheck` pass.

**Remaining production gates (not claimed by this code):** A real generative-model adapter behind `ProposalModel` (requires provider selection and provenance review), representative evaluation for production prompts/models, hardened OCI/kernel egress isolation, accelerator scheduling where required, live provider evidence for AI-assisted routing, and independent audit of guardrail coverage. The `disabled` default remains the version-one posture; M-6 remains **In review** until its applicable gates are evidenced.

### 3.1 Proposal contract — implemented

`ProposalModel`, `AgentProposal`, identifiers/lifecycle, bounded schemas, evidence/signal refs, pinning, expiry/cancellation/supersession/rejection, and `AcceptedCommand` are implemented as above.

### 3.2 Deterministic guardrails — implemented

Allow-lists, tenant-policy, jurisdiction/residency (via `RegionValidator`), processing-authority (via `AuthorityChecker`), cost/rate, raw-evidence/sensitive-context, human approval, deterministic execution, replay, and audit linkage are implemented. AI never independently verifies/rejects, grants evidence, etc., enforced by guardrails. Postgres adapters back the authority, region, and evidence-reference checks.

### 3.3 Automation modes — implemented

`disabled|assist|recommend|guardrailed_auto|human_required`, tenant/workflow versioned config, session pinning, and fail-closed are implemented as above.

### 3.4 AI products and operations — implemented (bounded proposal kinds)

Review copilot, adaptive-route, NL policy drafts, diffs, adversarial scenarios, accessibility/exception, document layout, prompt lifecycle (with sensitive classification persisted to `prompt_registry.sensitive`), generative-model registry, impact assessments, and evaluation/monitoring/cost/audit via `CostLimiter`+`AuditRecorder` are implemented as bounded proposal kinds routed through `Propose` (model-driven) with guardrails. Policy-scoped products are anchored to a real `policy_id`. A deterministic `ReferenceModel` and `ReferenceExecutor` exercise the `ProposalModel`/`CommandExecutor` ports; a real generative model remains an external gate. Full product UX outside the core remains later commercial work.

### 3.5 Required boundary — preserved

Two separate contracts are preserved:

```text
Signal model:
scoped evidence references -> scored signals -> deterministic policy

Proposal model:
redacted bounded context -> non-authoritative proposal
    -> deterministic guardrails or human approval
    -> recorded deterministic command
```

AI must never independently verify or reject a subject, grant evidence access, activate policy, select lawful basis, override consent, change retention, transfer evidence across regions, confirm a sanctions match, or change biometric thresholds — enforced by `internal/proposal/guardrail.go:1`.

---

## 4. Predictive models and MLOps

**Classification:** Current-core gap for selected biometric/document capabilities; otherwise Partial

The repository provides the model contract, gRPC transport and synthetic model, plus a native ONNX evaluation runner and durable Core integration. It does not contain an accepted production PAD model or production model lifecycle.

**Runtime decision — 7 September 2026:** ONNX Runtime is now **Selected** for initial local predictive inference, as recorded in [package section 6.9](global-identity-core-repository-structure-and-packages-v0.1-draft.md#69-model) and [architecture section 33.3](global-identity-core-technical-architecture-v0.6-draft.md#333-model-runtime). **Selected first capability:** PAD / liveness. The official Python 1.29.0 CPU reference, pinned loading/schema checks, private evidence redemption and persisted capture-to-policy/webhook workflow are implemented and tested. Candidate outputs are always inconclusive. A trained model and capture/preprocessing/quality/deployment acceptance remain TBD; see [runtime status and evidence](onnx-runtime-v0.1.md).

**Composition increment — 7 September 2026:** [Same-profile provider/model composition](composed-verification-runtime-v0.1.md) now joins document quality, PAD and face matching with atomic preparation, exact model dispatch, isolated gateway credentials and existing policy/webhook completion. Dynamic routing, intentional signal-overlap resolution and production model/provider acceptance remain open.

### 4.1 Missing runtime capabilities

- Hardened OCI deployment and kernel memory/CPU/PID/egress enforcement; the isolated process, reviewed runtime lock and bounded CPU reference exist.
- Production acceptance of the implemented pinned YuNet/contextual crop/resize path; real capture quality and provenance validation remain open. [Offline evaluation tooling and dataset intake](pad-evaluation-v0.1.md) now include local manifest import, bounded batch execution and baseline/candidate aggregate comparisons; there is no accepted representative local dataset.
- Supported-hardware and accelerator acceptance beyond the tested macOS arm64 CPU runtime.
- Model-specific reproducibility and quality evidence beyond the synthetic graph tolerance and candidate compatibility smoke test.
- Production face-comparison model and pair-dataset acceptance. The [evaluation-only matching integration](face-matching-runtime-v0.1.md) now supplies two-grant orchestration, bounded portrait extraction and native embedding/cosine execution; trained weights, model-specific alignment and matching thresholds remain open.
- Accepted production PAD model and, separately, active-liveness challenge/capture assurance.
- Document classification, OCR, quality, and manipulation models.
- Production-approved runtime selection from the implemented immutable evaluation registry; dynamic hot selection and runner provisioning remain absent.
- Resource and accelerator scheduling.
- Warm capacity and broader health supervision; per-dispatch bounded readiness checks now fence unavailable runners.
- Production failover when no authorised model is available; current evaluation paths fail closed or remain inconclusive.

### 4.2 Registry and governance — evaluation-only implementation complete; production acceptance open

[Evaluation registry v0.1](model-registry-v0.1.md) implements tenant model records, owner/licence/training/intended-use declarations, immutable manifest/configuration pins, declared regions and hardware class, independently versioned thresholds, evaluation-report digest references and audited activation/retirement/rollback history. The selected authority is one key with `models:activate`, expected-version checks and audit. Optional registry selections are verified inside attempt preparation. Production mode is rejected.

Remaining: verification of rights/provenance and representative evaluation reports; production approval and calibrated thresholds; live dynamic routing/provisioning and generated public registry contracts. Declared metadata and synthetic tests do not establish these acceptance gates.

### 4.3 Missing promotion and resilience

- Evaluation dataset governance.
- Production accuracy acceptance; experimental pinned report gates now recompute aggregate/group rates and reject coverage gaps.
- Demographic and device-class analysis.
- Privacy and impact assessment.
- Shadow execution.
- Canary promotion.
- Automatic technical rollback.
- Production quality rollback; explicit evaluation deployment rollback now appends audited history.
- Production drift ingestion/monitoring; the offline `check-drift` command now checks labelled aggregate population changes with explicit limits and pinned output reports.
- Reproducible inference record containing hardware or runtime class where relevant.

The worker supports the PR-01 Dojah and PR-02 Smile ID routes alongside the separate opt-in synthetic fixture. General multi-provider/model routing remains open; a mutually exclusive ONNX evaluation route now has real native model composition.

---

## 5. Tenant-isolated fraud and risk

**Classification:** Implemented selected scope — tenant-local sections 5.1 and 5.2 are closed; cross-tenant intelligence remains deferred.

### 5.1 Implemented signals

Device, identifier, exact trusted-template portrait and document-content reuse; live-capture content replay; observed verification velocity; IP/network patterns; provider disagreement and normalized inconsistency findings; identity-token inconsistency; capture duration; repeated failed liveness across distinct related journeys; and high-risk normalized model findings are implemented. Rules pin exact source/runner mappings and thresholds. Missing, untrusted, disabled or out-of-bound inputs remain inconclusive. This closure does not claim production biometric or external-intelligence acceptance.

### 5.2 Implemented graph and policy integration

Migration 48 and `internal/fraud` implement tenant/region token domains, evidence-backed relationships, source provenance, forced RLS, correlation-group and actual-lineage deduplication, immutable evaluation receipts, safe reason/summary projection and an attributed non-authoritative hypothesis boundary. The worker consumes fraud facts inside the authoritative policy transaction. Configuration uses the selected single `fraud:configure` key with expected-version checks, idempotency and common audit. Evidence deletion and hold-aware expiry remove links; hypothesis submission cannot change a decision or activate a policy.

Public API and TypeScript operations, runtime permissions, signal semantics, limits and verification evidence are documented in [tenant fraud and risk v0.1](tenant-fraud-risk-v0.1.md). Tenant-specific source acceptance/canonicalisation, thresholds and regional approval are deployment inputs. The initial configuration names one region and has bounded 2,000-row coverage. Similarity-based portrait matching and the cross-tenant fraud network remain explicitly deferred; no such capability is introduced through the tenant-local implementation.

---

## 6. Subject, identifier, claim, observation, and fact model

**Classification:** Implemented selected scope — initial core section complete on 9 September 2026

The user selected the full persistent identity model as one workstream, approved the narrow alpha v1 `identity` fact-source/receipt extension, and selected subject deletion to cover identity records plus all linked verification evidence. [Package section 6.3](global-identity-core-repository-structure-and-packages-v0.1-draft.md#63-identity) records the authoritative decision. D-019 remains the verification-local PII-free authority principal; new persistent subjects use independently generated IDs and explicit immutable links.

| Capability                                                                       | Implemented evidence                                                                                                                                                                                                                  |
| -------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Public subject creation/read/update/delete, external reference and lifecycle     | [Identity application](../internal/identity/service.go), [public routes](../internal/transport/httpapi/identity.go), [TypeScript client](../sdk/typescript/src/identity.ts) and versioned OpenAPI                                     |
| Non-global subject identity and tenant isolation                                 | Server-generated tenant/region subjects, explicit verification association, mandatory scope and forced RLS in [migration 49](../db/migrations/000049_identity.up.sql); no automatic identifier/reference merge                        |
| Encrypted structured claims, original/normalised values and national identifiers | Typed immutable observation/fact/claim/identifier metadata with separately encrypted values and per-subject wrapped keys in [identity persistence](../internal/identity/postgres/record.go)                                           |
| Keyed identifier lookup, masks and validity/verification metadata                | Domain-separated tenant/region HMAC, exact namespace/issuer/normalisation lookup and computed safe masks; reported verification state is explicitly tenant attested and actor attributed                                              |
| Confidence, validity, provenance, evidence and supersession                      | [Immutable record contracts](../internal/identity/contract.go), source inheritance, bounded transitive ancestry and current-source eligibility                                                                                        |
| Corrections and current-projection rebuilding                                    | Append-only successor chains and explicit rebuilding without changes to earlier decisions                                                                                                                                             |
| General observations and typed normalised facts                                  | Canonical string/boolean/integer/decimal/date/timestamp values; original values, units, confidence, validity/freshness and versioned exact/trim/ASCII-uppercase transformations                                                       |
| Correlation groups and independent-source enforcement                            | [Corroboration evaluator](../internal/identity/evaluation.go), shared ancestor/check/evidence/upstream roots, conservative unknown-provider grouping and immutable configuration/identity receipts in the existing policy transaction |
| Retention and deletion across the complete subject scope                         | Hold-aware value expiry; atomic linked-session stopping and exact evidence/identity targets; real object deletion; staged-upload cleanup guard; 35-day post-erasure backup interval and tombstone replay                              |

**Verification:** Go 1.26.6 full race tests and PostgreSQL/Headgate integration suites pass, alongside lint, contract compatibility, OpenAPI generation/lint, SDK tests, SDK public-HTTP smoke and package checks. Temporary synthetic checks exercised encryption, tenant isolation, correction/rebuilding, trusted provider imports, correlated-source rejection, authority withdrawal, held/unheld expiry, actual linked evidence-file erasure, backup completion and tombstone replay. They were removed after verification in line with the user's preference against new permanent tests. No new dependency was introduced; the vulnerability scan found no affected called/imported code.

**Scope limits:** This closes the general core model and its policy/privacy integration. Trusted imports currently expose existing boolean Core check outcomes; arbitrary structured tenant observations remain tenant attested. It does not claim production structured PII extraction, automatically verified national identifiers, real-provider/model accuracy, automatic source ingestion or global subject matching. Values require an explicit retention deadline capped at 30 days. Initial subject deletion caps 256 links and 255 evidence objects plus the identity target; larger incremental planning and richer country normalisation remain explicit follow-ups. [Operational guide](identity-model-v0.1.md).

---

## 7. Verification lifecycle and orchestration

**Classification:** Partial — primary deterministic, cancellation/expiry, and review/recapture paths are composed; remaining lifecycle branches are current-core gaps

L-01 through L-03 are complete for their recorded boundaries. They provide the complete state vocabulary, optimistic transition primitive, synthetic capture-to-processing-to-decision-to-signed-webhook journey, tenant and subject cancellation, automatic expiry, authority/session fencing, exact replay, and offline-deadline recovery. The create API deliberately returns an atomically activated `collecting` session; a separately persisted `created -> collecting` phase is not selected and is not counted as missing unless a future requirement introduces it.

O-03 has additionally composed automatic nonterminal routing to manual review, certified operator authority, findings and dual control, policy re-evaluation, linked recapture, progress-preserving credential replacement, child-outcome acknowledgement, explicit parent re-evaluation, correction successors, appeal lifecycle, queue/settings operations, controlled evidence display, public OpenAPI, and TypeScript SDK integration. One live-Core path now composes the original capture, queue claim, protected evidence display, linked child capture, child acknowledgement and terminal parent re-evaluation. O-03 is **In review** because recovery, correction and appeal branches, external certification and reviewer/subject acceptance are not finished.

Remaining lifecycle capabilities are:

- General session resumption outside the implemented fresh-page capture progress and review-recapture credential recovery paths.
- Explicit `awaiting_subject_input` orchestration outside linked review recapture.
- General `awaiting_external_result` callback/reconciliation beyond the implemented Smile ID status-only polling route.
- Operational-failure transition and recovery that cannot be represented as an identity outcome.
- Complete composed recovery, correction, appeal and escalation outcomes, including accepted reviewer/subject interaction and external certification.
- Current-decision and current-case projections on every applicable verification read, where required by the public lifecycle contract.
- Worker-loss and dependency-recovery proofs across every supported lifecycle state and externally completed provider operation.

Production runner selection, provider/model acceptance, and clean deployment remain separate from the lifecycle state-machine implementation.

---

## 8. Assurance and complete decision snapshots

**Classification:** Implemented selected scope — initial core complete; production calibration and external evidence remain separate.

### 8.1 Implemented assurance capabilities

Section 8 now has immutable tenant assurance profiles, future-session policy assignments and per-session pins, requested/achieved typed dimensions, exact capability/package/configuration/threshold mappings, deterministic freshness checks, and transitive source-correlation enforcement. Eight public API and TypeScript operations expose capability discovery, profile publication/validation/read/list, assignment, and session selection. Failed requested assurance prevents verified completion and routes the case to review.

### 8.2 Implemented decision references

Production snapshots bind applicable claims, identifiers, observations, normalised facts and ancestry; evidence revisions and lineage; checks, attempts and signals; accepted review findings; authority/response and region/transfer context; assurance profile and threshold revisions; provider/model/runtime/preprocessing/configuration references; and exact policy/evaluator versions. Review, correction, recapture, export and reproduction preserve the context. Existing canonical snapshots without context retain their original bytes.

The manifest binds the current configured policy-pack and authority context; a jurisdiction-pack registry and field-level provider lineage are separately scoped capabilities. Sources without sufficient provenance, synthetic attempts and evaluation-only models cannot establish achieved assurance. No production assurance defaults, biometric operating points or jurisdiction equivalence are selected by this completion.

**Evidence:** Temporary synthetic domain and real PostgreSQL/HTTP/SDK proofs cover canonical reproduction, freshness boundaries and stale commits, correlated and transitive roots, uploaded-selfie restrictions, immutable profile/session pins, future assignment and recapture, RLS, idempotency, rollback, complete identity ancestry and typed decision export. See [assurance profiles and decision context](assurance-profiles-v0.1.md) and build-plan AS-01.

---

## 9. Real provider runtime and provider operations

**Classification:** Current-core gap, Partial, and External evidence gap

Dojah and Smile ID adapter source, manifests, normalisation, conformance tests, routing logic, and provider-approved semantic boundaries exist. PR-01 now composes one Dojah document-analysis route into the running worker; PR-02 adds one asynchronous Smile ID document-and-selfie route with durable status-only recovery; official-account acceptance remains open.

### 9.1 Runtime composition progress and remaining gaps

**Implemented for the first route:** `adapter-runner`, TLS and separate workload credentials, worker client configuration, an explicit tenant/policy/profile route, atomic evidence-grant/request/task planning, private controlled evidence redemption, immutable dispatch receipts, normalized policy completion and signed-webhook retry. The [runtime guide](provider-runtime-v0.1.md) records local fixture evidence and remaining limits. Dojah's documented request body and explicit valid/invalid status mapping are corrected in adapter 0.1.1.

**PR-02 implemented:** Two accepted JPEG grants, opaque country/document-type references, bounded asynchronous RPCs, atomic submission ownership, immutable job reference, fenced status-only restart recovery, operational timeout and normalized policy/signed-webhook completion. Static-image liveness remains inconclusive. Local fixtures prove one submission/upload, two grant uses and restart completion.

**Remaining:**

- Hardened provider-runner deployment and external acceptance evidence.
- Tenant provider registration and configuration.
- Production secret-manager resolver.
- Dynamic credential rotation.
- General purpose-bound subject-input resolution beyond the Smile ID deployment-bound country/document-type references.
- Broader multi-operation grants and durable delivery recovery beyond the first document route.
- Provider health cache and selection input.
- Callback intake and broader provider reconciliation beyond the implemented Smile ID status-only polling.
- Provider-side deletion orchestration.

### 9.2 Missing resilience and cost controls

- Circuit breakers.
- Health-based routing from live health data.
- Degraded-mode visibility.
- Per-provider concurrency and rate control.
- Provider-side duplicate-charge reconciliation and budgets beyond the implemented one-initial-dispatch-per-attempt receipt.
- Cost attribution and budget limits.
- Safe fallback demonstration in the complete workflow.

### 9.3 External evidence still required

- Tenant-owned official sandbox or provider-approved fixture runs.
- Country and product capability confirmation.
- Callback operation where enabled.
- Provider retention and deletion exercises.
- Jurisdiction, recipient, purpose, and region approval.
- Controlled outage and equivalent-provider replacement demonstration.

X-03 must remain In review until this evidence is recorded.

---

## 10. Provider and model isolation security

**Classification:** Current-core hardening gap

The runner transport already provides TLS, rotating bearer authentication, deadlines, cancellation, message-size limits, schema validation, and OpenTelemetry propagation. Deployment and outbound isolation remain incomplete.

Missing capabilities include:

- Per-adapter egress allow-lists.
- DNS resolution pinning for every provider-controlled upload or callback destination.
- Private, loopback, link-local, and reserved-address rejection.
- Redirect rejection or bounded revalidation.
- DNS-rebinding protection.
- Read-only filesystem.
- CPU, memory, process, and file-descriptor limits.
- Per-adapter secret scope.
- Container or equivalent process isolation.
- Dynamic runner-credential reload.
- Runner supervision and termination on policy breach.

The composed Smile ID async route now validates partner/job-bound upload paths and uses reviewed-origin, DNS-pinned HTTPS with redirect rejection. Broader standalone adapter/deployment isolation and the operating-system controls above still require acceptance evidence.

---

## 11. Biometric subsystem

**Classification:** Current-core gap for the D-014 boundary

The Swift, Kotlin, and Web acquisition coordinators implement bounded live-camera acquisition, ordered prompts, deadline handling, and local quality checks. The ONNX evaluation path adds pinned face detection and contextual preparation; ML-02 adds evaluation-only portrait preparation, embedding execution, and one-to-one cosine comparison; ML-03 composes document, PAD, and face-comparison checks. These engineering paths explicitly remain inconclusive and do not establish production PAD, face-match, document-authenticity, MRZ, or barcode assurance.

Missing capabilities include:

- Production-accepted presentation-attack detection.
- Deepfake, replay, screenshot, and virtual-camera injection signals where technically possible.
- Model-specific face alignment and document-portrait selection accepted on representative data.
- Licensed production embedding weights and calibrated one-to-one thresholds.
- Production validation of the implemented separation between liveness and similarity signals.
- Low-quality-to-inconclusive handling in the full decision workflow.
- False-match and false-non-match evaluation.
- Failure-to-acquire and failure-to-enrol metrics.
- APCER and BPCER evaluation.
- Demographic, device, and camera-class evaluation.
- Independent retention for captures, reference portraits, embeddings, and caches.
- Embedding and model-cache deletion.

One-to-many identification remains outside version-one scope.

---

## 12. Document subsystem and country coverage

**Classification:** Current-core gap for selected D-014 capabilities

Capture Web already plans and independently completes required document front/back artefacts, supports upload or live-camera acquisition, and provides review/retake. The Dojah and Smile ID routes provide bounded provider document-analysis paths, and ML-03 composes document quality with evaluation-only biometric checks. This is capture and provider plumbing, not an implemented general document-understanding subsystem.

Missing capabilities include:

- Automatic document capture.
- Perspective correction and cropping.
- Image-level front/back correspondence and authenticity checks beyond capture-plan completeness.
- Document-type and issuing-country classification.
- OCR and structured field extraction.
- MRZ parsing and checksum validation.
- Barcode parsing.
- Expiry and age calculation.
- Cross-field consistency.
- Template and security-feature inspection.
- Manipulation and screenshot risk.
- Portrait extraction.
- Unknown-document provisional classification and guarded validation.
- Public document support-level response.

NFC and digital-credential trust-chain validation are explicitly deferred for the initial pack.

---

## 13. Document, jurisdiction, and assurance packs

**Classification:** Current-core gap and External evidence gap

Provider manifests are not a replacement for immutable country, document, jurisdiction, and assurance packs.

Missing capabilities include:

- Versioned Nigeria, Ghana, Kenya, and South Africa pack records.
- Country, authority, document type, known version, and validity metadata.
- Required sides and supported fields.
- Security checks.
- Barcode, MRZ, and future NFC support declarations.
- Model and parser version requirements.
- Evaluation coverage and known limitations.
- `fully_supported`, `structurally_supported`, `provider_only`, `best_effort`, and `unsupported` classifications.
- Authority, controller, processor, recipient, region, and transfer constraints.
- Legal-review metadata.
- Update, activation, deprecation, and retirement lifecycle.
- Default assurance profiles and mappings.

Every claimed country path still requires provider/account confirmation and legal/regional evidence.

---

## 14. Portable capture experience

**Classification:** Partial — Capture Web acquisition implemented; portable configuration remains a gap

Capture Web now provides the selected mobile-first guided Lit experience with one active task per screen, fixed built-in subject copy, exact notices, optional policy-bounded method choices, camera and upload paths, preview and retake or replacement, confirmation between multi-item steps, authority responses, realtime observation, recovery, processing, and authoritative outcome screens. The final accepted capture goes directly to processing instead of requiring a local finish action, and liveness follows the shorter preparation-to-guided-capture-to-processing branch. D-027's separately authenticated `GET /v1/capture/outcome` projection maps current workflow state and the immutable completed policy decision to a closed subject-safe vocabulary without exposing decision identifiers, policy reasons, assurance, provider/model details, evidence metadata, or subject data. The `idq_out_v1` bearer has its own `otk_` durable record, signing keyring, bounded post-session lifetime and revocation; it grants no capture authority and Capture Web keeps it in a dedicated public `OutcomeClient`. Its safe-default visual system covers responsive phone, tablet, and desktop layouts, light and dark modes, forced colours, visible keyboard focus, large text, reduced motion, right-to-left direction, and safe areas. A documented appearance-only `--idq-capture-*` custom-property surface supports host-local branding across the open Shadow DOM without changing notices, behaviour, evidence semantics, or assurance. Hosted and embedded pages use a server-side public-SDK bootstrap and complete real synthetic capture journeys against self-hosted Core while keeping the tenant API key outside the browser. Hosted upload, embedded upload, and active liveness progress through a real synthetic worker and immutable policy decision to the subject-safe verified screen. The same serial real-Core suite reaches action required through `request_input`, not verified and inconclusive through immutable completed decisions, subject cancellation through the public capture operation, operational failure through `fail_workflow`, and authoritative expiry through the worker before the still-live outcome credential reads the projection. The worker routing and expiry boundaries persist each exact lifecycle transition, audit event, outbox record and task effect atomically. D-026 is therefore proven across all nine selected outcome journeys without accepting an expired capture bearer or inferring expiry in the browser. Explicit advanced-interaction acceptance remains open. The public portable experience-configuration system described by v0.6 is also absent; CSS variables alone do not provide its immutable versioning, targeting, signing, revocation, or fallback contract.

The implemented safe default keeps capture completion, verification processing, verification completion, and tenant action distinct. Its reproducible conformance harness migrates an isolated Core and Headgate database, provisions a synthetic tenant and scoped credential, creates the journey through public contracts, and proves hosted upload, embedded upload, and active-liveness capture pages. After the required notice response, the first policy-ordered method is recommended directly; active liveness uses one preparation page and one start action before running all ordered prompts automatically, while **Use Another Method** retains approved alternatives. The active-liveness coordinator validates the public plan, presents ordered challenges under deadlines, requires host-supplied measurements for requested quality gates, and owns cancellation and camera cleanup. The generic programmatic adapter boundary accepts arbitrary namespaced acquisition methods while missing or ambiguous adapters fail closed. Adapter return, local prompt completion, and capability advertisement prove no assurance and cannot self-complete a step: the component refreshes authoritative Core progress and requires the exact requirement, evidence type, artefact, method, and fallback binding. The live synthetic path persists one representative captured frame through the ordinary evidence boundary; Core v1 does not yet persist the complete temporal frame set and the demonstration does not claim production liveness or PAD assurance.

Missing capabilities include:

- Public portable experience schema.
- Local schema validator.
- Stable experience ID and immutable version.
- Draft, approved, published, superseded, and revoked lifecycle.
- Structured tenant copy and locale catalogues.
- Mandatory regulatory, consent, safety, and accessibility copy.
- Tenant-copy and mandatory-copy version separation.
- Vetted asset storage and content digests.
- Verified support, privacy, terms, and custom-domain links.
- Workflow, country, application, origin, and SDK-version targeting.
- Manifest signing and digest validation.
- Tenant, environment, region, origin, bundle-ID, application-ID, and signing-identity binding.
- Session-pinned experience, locale, tenant-copy, and mandatory-copy versions.
- Signed accessible safe-default experience.
- Revocation, rollback, fallback, and kill-switch behaviour.
- Export and import without Console or Cloud.

The existing optional `rendered_experience_version` response field records only a caller-supplied version string; it does not implement the full experience contract.

E-04 and M-2 are **In review** in the build plan. The basic journey, responsive goldens, accessibility automation, streamlined one-start liveness flow, arbitrary-adapter fail-closed behaviour, three verified live Core-backed demonstrations, and real-Core action-required, not-verified, inconclusive, cancelled, failed, and expired demonstrations are implementation evidence. D-027 closes the post-expiry outcome-access contract; explicit acceptance of the advanced interaction is the remaining milestone gate. The separate portable experience-configuration gap remains open.

---

## 15. Capture SDK lifecycle and platform assurance

**Classification:** Current-core gap and Partial

Swift and Kotlin provide native bootstrap, secure token storage, proof-key binding, direct upload, realtime observation, one-shot camera capture, local quality assessment, and acquisition coordination.

Missing capabilities include:

- Complete `configure` contract.
- Session-oriented `start`.
- Durable `resume` after process death.
- Idempotent server-side `cancel`.
- Complete `clearLocalData`.
- Complete `getCapabilities`.
- Background transfer and recovery where allowed.
- Full notice and consent presentation.
- Complete document-front/back UI journey.
- Native active-liveness challenge UI and production temporal or video persistence; the Web challenge journey and representative-frame Core proof now exist.
- Actual PAD and face-match integration.
- MRZ and barcode processing.
- Secure temporary-file lifecycle.
- Sensitive-screen and application-switcher protection.
- Root, jailbreak, emulator, instrumentation, and integrity signals.
- Supported-device compatibility matrix.
- Platform-specific NFC, voice, video, and provider adapters conforming to the implemented Web boundary; unknown methods fail closed.
- Accessibility, orientation, permission-change, network-loss, backgrounding, interruption, cleanup, and cancellation conformance.

Flutter and React Native are not current gaps until advertised. Once advertised, they must remain thin native wrappers and must not transfer raw evidence through Dart or JavaScript bridges.

---

## 16. Manual review, correction, and appeals

**Classification:** Current-core gap and Partial

The Core now includes versioned operator administration, queue operations, controlled evidence display, independent arbitration, policy-authored correction successors, appeal lifecycle and public SDK/recapture integration. A live public-SDK/Core/Capture-Web fixture composes one reviewer-requested recapture through explicit parent completion. See the current implementation and runtime permissions in [manual review and linked recapture](manual-review-recapture-v0.1.md#6-review-operations-and-public-integration).

Remaining work includes:

- Composed progress-preserving recovery, correction, appeal and escalation outcome journeys.
- Explicit reviewer/subject visual and interaction acceptance of the live recapture fixture.
- External certification-issuer adapters; current certifications are explicitly tenant-attested.
- Advanced assignment automation beyond filtered queues, certified regional claiming and versioned priority/SLA management.
- Tenant subject notification and correction-request product surfaces; the Core API and capture handoff helper are available.
- Production acceptance of the implemented evidence-linked, non-authoritative review-copilot proposal path with a real generative model.

SSO remains deferred. Historical increments below describe their state at the time; section 6 of the linked guide records the current implementation. No new tests were written for the September 8 operations batch at the user's request.

**Implemented — 7 September 2026:** Review mutations now resolve stable operator identity and bounded permissions, certifications, regions and expiry from a server-managed assignment file at the application boundary. Transport bodies reject reviewer identity and certification assertions. Configuration removal/expiry denies subsequent actions, and the second reviewer must also hold the required certification. External certification validation and full operator identity administration remain gaps.

**Selected:** Recapture will create a new session linked to the review case, obtain fresh subject authorization and rerun its configured checks. Automatic routing is now implemented: canonical nonterminal receipts, unique immutable case origins and `processing -> manual_review` commit atomically under the task fence, with exact replay and current-authority checks. Case requirements use explicit exact-policy mappings. Accepted-finding re-evaluation is also implemented: pinned permitted findings, case-bound grant validation, dual-review consensus, durable requests, immutable policy input/output receipts and shared terminal completion. Nonterminal evaluations do not complete the session; conflicting findings escalate. Just-in-time grant issuance, escalation resolution and consequential follow-up actions remain unimplemented. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Linked recapture increment — 7 September 2026:** The user selected exact parent policy and capture-profile revisions for the child. `review` now authorizes and records linked creation after a persisted `request_input` evaluation; `verification/postgres` creates a fresh collecting session in the same transaction. Migration 41 enforces immutable tenant-scoped case/version uniqueness. Separate `reviews.recapture` idempotency and durable linkage restore the same child; policy loading preserves its parent revision while using fresh child observations. The internal route requires both review-write and session-create scopes plus current certified operator authority. Rollback, replay, fresh-child state, pinned inputs and transport guards have synthetic boundary tests. Generated public contracts, complete subject-facing handoff and correction/supersession remain pending; subsequent increments below implement Core credential recovery and explicit follow-up. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Selected child-outcome behavior and recovery increment — 7 September 2026:** The child's committed outcome is attached to its original case for authorized follow-up; it does not automatically re-evaluate the parent. A reference-only read projection follows immutable linkage to child completion and exposes current token/expiry metadata without bearer credentials. The initial expired-token renewal increment supported only sessions before any subject response or upload intent, with current reviewer authority, child/session locks, original deadline limits, irreversible old-token revocation, distinct idempotency and atomic audit. Creation/renewal responses include handoff expiry metadata. Synthetic tests cover renewal rollback/replay, stale-token denial and child-outcome visibility with unchanged parent state. Full subject-facing handoff, generated contracts and correction/supersession remained pending, so O-03 was still in progress at that point. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Selected acknowledgement increment — 7 September 2026:** The first follow-up action records an audited acknowledgement of the child's exact completed outcome, leaving parent state and decisions unchanged. Migration 42 binds an immutable receipt to tenant/case/version, exact child/decision, stable operator and API-key actor. The internal acknowledgement route requires review-write scope, current `reviews:resolve` authority and case certification; closed request bodies cannot assert reviewer identity. Receipt, audit and distinct idempotency commit atomically, and retries preserve original attribution. Status clears the follow-up flag for the acknowledged outcome. Synthetic tests cover denial, rollback, replay, immutability and absence of additional workflow effects. Correction/supersession remains pending; the following increment implements Core progress-preserving recovery. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Selected progress-preserving recovery — 8 September 2026:** Replacement credentials continue the same child with fresh subject authorization. Migration 44 records immutable credential lineage, retains accepted artefacts with original provenance and marks unfinished uploads abandoned. Recovery atomically revokes the old credential and fences the session; fresh authorization is required for upload and processing. REST and durable progress include explicitly retained artefacts, including repeated recovery. Original deadlines and reconciliation duties remain intact. This implements Core recovery; subject-facing visual and interaction acceptance remains pending. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Implemented explicit recapture follow-up — 8 September 2026:** A reviewer with current `reviews:resolve` authority may request parent policy re-evaluation after acknowledging the exact child decision. Migration 45 atomically records immutable acknowledgement-bound lineage, a new case version, audit, idempotency and durable evaluation intent. The existing fenced worker preserves the parent’s pinned policy and original fact timestamps and adds `review.recapture` from the child’s immutable outcome. Only policy may complete the parent; nonterminal results keep manual review. Child completion does not automatically schedule evaluation. Controlled evidence access, escalation/correction/supersession, generated public contracts and accepted subject-facing UI remain pending. See [manual review and linked recapture](manual-review-recapture-v0.1.md).

**Composed recapture proof — 15 September 2026:** A tenant fixture using the public TypeScript SDK now creates the original journey and review configuration while keeping its API key server-side. Live Chromium automation completes original capture, automatic manual-review routing, certified claim, protected evidence grant redemption, `request_input`, linked-child credential handoff, fresh child authorization and evidence, child completion, acknowledgement and explicit parent re-evaluation to a verified terminal decision. Immutable recapture lineage contributes a context-only `review.recapture.requested` fact to the child's pinned snapshot; it establishes no assurance. The serial self-hosted-Core matrix passes ten journeys. Recovery, correction and appeal composition, external certification and explicit reviewer/subject acceptance remain open, so O-03 is **In review**.

---

## 17. Webhooks and general event contracts

**Classification:** Implemented selected scope — catalogue, subscriptions and resumable fanout complete; clean-deployment proof remains under section 26

The repository includes durable webhook persistence, KMS-wrapped rotating secrets, retry exhaustion, replay lineage, SSRF-aware callback transport, and an independent Go verifier. L-02 now composes completion projection and signed delivery into the runnable worker. Its public API journey proves atomic decision/session/delivery effects, a durable 503 retry, worker restart, successful delivery, stable event identity, and independent signature verification. H-01 now exposes public endpoint administration, payload-free delivery/attempt inspection and exhausted-delivery replay through API, TypeScript and CLI. The broader event catalogue remains incomplete.

Resolved on 18 September 2026: endpoint event-type subscriptions (exact names or `*`, bounded to 64, default `verification.completed`); the versioned public event catalogue with per-type JSON schemas and canonical fixtures in `contracts/webhook/v1`; outbox-first emission from every owning feature for the selected catalogue (verification lifecycle, checks, decisions, evidence, consent, processing authority, subjects, deletion, review cases and appeals); and a fenced, resumable `webhook.fanout` task that pages subscribed endpoints in bounded batches, dedupes per endpoint and event, and continues from a persisted cursor — replacing the fail-closed 1024-endpoint guard.

Remaining:
- A multi-batch crash-resume fanout exercise at scale beyond one batch.
- The complete clean-deployment decision-to-delivery demonstration once the section 26 packaging gate exists.
- `verification.collecting` has no distinct persisted transition and `provider.degraded` has no provider-health owner yet; neither emits.

V-04 completed the application, persistence, task, transport, verifier, and deterministic proof boundary. L-02 adds the runnable completion and delivery composition. H-01 adds separately permissioned, atomically audited administration and replay, display-once secret delivery, overlap-safe rotation and signed tenant-bound inspection cursors. Receivers continue deduplicating the unchanged signed event ID across manual replay.

---

## 18. Public API and authentication

**Classification:** Current-core gap and Partial

The public OpenAPI now includes tenant inspection; capture profiles; verification creation/read/cancellation; decisions and bundles; notices and processing authority; capture session/progress/bootstrap/connections; evidence upload; webhook and policy administration; review cases, queue/settings, operators, evidence display, recapture, correction and appeals; tenant-local fraud; persistent subjects and identity records; assurance capabilities/profiles; and AI proposals, modes, commands, and prompts. The dependency-free TypeScript SDK exposes the corresponding review, fraud, identity, assurance, and proposal clients.

Missing or incomplete public resources include:

- General verification resume outside capture-progress recovery and review-recapture credential replacement.
- General decision history and reconsideration resources beyond review correction, appeal, and recapture operations.
- Tenant-facing evidence metadata and lifecycle inspection.
- General provider/model evidence-access grant creation, inspection, and revocation; review evidence grants are public and case-bound.
- Consent receipt inspection and revocation.
- Provider catalogue, capabilities, health, and configuration.
- Generated model-registry catalogue, capability, health, configuration, and rollback contracts; evaluation-only Core routes exist but are not in generated OpenAPI/SDK contracts.
- Public policy simulation, diff, and regression; P-01 implements listing, creation, validation, immutable versioning, source retrieval, activation, rollback, and activation-history inspection.
- Deletion-status retrieval.
- Direct privacy-request, restriction, objection, portability, and deletion administration.
- General webhook subscription and event-catalogue administration.
- OAuth client credentials as the alternative server-integration mode described by section 29.2.

Review, fraud, identity, assurance, proposal, policy, webhook, and cancellation surfaces are represented in the authoritative OpenAPI. Internal privacy operations and evaluation-model registry routes remain outside generated public contracts and cannot yet be treated as stable public administration APIs.

---

## 19. CLI and open-source operational contract

**Classification:** Current-core gap and Partial

The Cobra CLI provides tenant, API-key, migration, Headgate migration, evidence-key, policy-decision reproduction/verification, audit verification, recovery, background-work inspection/retry, public webhook administration/inspection/replay, public policy administration, and AI proposal/mode/prompt operations.

Missing capabilities include:

- Complete synthetic verification command.
- A packaged example policy and complete demonstration; public creation and activation commands are implemented.
- Policy simulation, diff and regression commands; P-01 implements create, validate, list/get, revision append/source/history, activate and rollback.
- Provider registration, validation, health, and failure simulation.
- Model registration, validation, health, and rollback.
- Review/operator/recapture administration matching the generated public API.
- Subject, identity, assurance-profile, and tenant-fraud administration matching the generated public API.
- Country, document, jurisdiction, and assurance-pack inspection.
- Retention-resolution inspection.
- Deletion-status inspection and safe retry.
- Tenant-owned data export.
- Tenant HMAC and production-key lifecycle operations.
- Accepted-command execution and complete generative-model/impact-assessment administration when AI is enabled.
- Clean-install preflight and end-to-end diagnostics.

---

## 20. Privacy, retention, and deletion

**Classification:** Partial

Implemented foundations include typed retention resolution, legal holds, observable deletion state, evidence ciphertext removal, backup-expiry waiting, deletion work, tombstones, and restore-aware recovery foundations.

Missing capabilities include:

- Data-subject access request and export.
- Portability.
- Correction request workflow.
- Processing restriction and objection handling.
- Processor and subprocessor inventory.
- Transfer and disclosure records.
- Provider-side deletion.
- Model cache and embedding deletion.
- OCR, search, and derived-index deletion.
- Webhook payload retention and deletion integration.
- External-delivery retention semantics.
- Production backup-store expiry enforcement.
- Complete deletion-status public API.
- Direct consent-receipt administration.
- Full deletion demonstration after a real provider and model journey.

---

## 21. Production KMS, HSM, and secrets

**Classification:** Current-core production gap and TBD

The repository provides an owned KMS boundary, local keyring, per-object streaming encryption, wrapped evidence keys, and evidence-key rewrapping.

Missing capabilities include:

- First production KMS or HSM adapter.
- Tenant-scoped HMAC key management for predictable identifiers.
- Provider secret-manager adapter.
- Model secret and configuration management.
- Dynamic secret and runner-credential reload.
- Fleet rewrap orchestration.
- Key retirement and verified destruction.
- Production key recovery ceremonies.
- Delegated support access.
- Break-glass access with approval, expiry, and audit.

The precise first production provider remains unresolved in the repository/package draft and must not be selected by this audit.

---

## 22. Regional deployment and data residency

**Classification:** Partial and Accepted later infrastructure work

The current implementation pins an immutable session region and carries region through capture, processing authority, evidence, policy input, deletion, and selected contracts.

Missing capabilities include:

- Multiple independently deployable regional data planes.
- Trusted region router or explicit regional API selection contract.
- Tenant allowed-region enforcement across deployments.
- Cross-region request rejection and redirection rules.
- Evidence-transfer workflow and approvals.
- Region-specific provider and model availability.
- Region-specific keys and backups.
- Regional capacity and health reporting.
- Regional failover subject to residency policy.
- First managed-region decision and operational evidence.

No implementation may infer region from IP address, locale, or device language or silently fall back across regions.

---

## 23. Observability

**Classification:** Partial

Vendor-neutral OpenTelemetry providers and selectable OTLP export over gRPC or HTTP/protobuf are implemented. Export is disabled by default and the selected TLS, mTLS, header, sampling, batching, retry, and metric-reader constraints are represented.

Missing end-to-end domain coverage includes:

- Workflow duration.
- Verification start, completion, outcome, and operational-failure rates.
- Capture drop-off by step.
- Evidence recapture rate.
- Time to decision.
- Review duration.
- Webhook delivery latency.
- Provider availability, callback delay, cost, and normalised outcomes.
- Model score distributions, thresholds, cohort performance, and drift.
- Deletion backlog and backup-expiry age.
- Regional capacity.
- Provider and model degraded-mode visibility.
- Production dashboards and alert rules.

Metrics and traces must continue to exclude raw evidence, personal claims, credentials, and high-cardinality subject identifiers.

---

## 24. Reliability and disaster recovery

**Classification:** Partial and External evidence gap

Implemented foundations include PostgreSQL-authoritative state, Headgate work, idempotency, attempt fencing, inbox-style result deduplication, outbox, reconciliation, recovery inspection, backup-manifest verification, audit verification, and one recorded PostgreSQL restore exercise.

Missing capabilities and evidence include:

- Complete lifecycle recovery across all verification states.
- Provider circuit breakers and live health routing.
- Model failover and rollback.
- Production provider callback and external-success reconciliation exercises.
- Provider credential restoration.
- Production KMS and key recovery.
- Complete evidence/object-store restoration.
- Regional failover.
- Measured API, webhook, recovery, RPO, and RTO objectives.
- Production-shaped load and 24-hour soak tests.
- Mixed-version deployment tests.
- Scaled expand-migrate-contract rehearsals.
- Incident exercises.

---

## 25. Security and secure development lifecycle

**Classification:** Partial and External evidence gap

Implemented foundations include API-key and capture-token authentication, application permission checks, forced PostgreSQL RLS, evidence encryption, security headers, bounded request handling, dependency/vulnerability automation, CodeQL, SBOM generation, artifact provenance configuration, a threat model, disclosure policy, and release/runbook foundations.

Missing or incomplete capabilities include:

- Distributed HTTP rate limiting under the no-Redis baseline.
- OAuth client credentials.
- Operator SSO.
- SCIM for the later commercial operator surface.
- Delegated support access.
- Break-glass workflow.
- Reviewer identity derived from authenticated operator claims.
- Malware scanning.
- Sandboxed document parsing.
- Provider and model egress allow-lists.
- Explicit secret-scanning gate.
- Container scanning and signed container images when containers ship.
- Fully enforced dependency-licence allow-list and review workflow.
- Independent penetration test.
- Production incident exercises.
- Clean-room provenance verification.

---

## 26. Self-hosted deployment and clean usability

**Classification:** Current-core gap

The development Compose file starts PostgreSQL only. A clean environment cannot yet execute the complete open-source operational contract.

The missing deployment path must compose:

- PostgreSQL.
- API.
- Worker.
- Local or S3-compatible object storage.
- Local development key provider.
- Provider and model runners.
- Mock provider and synthetic model.
- Safe-default Capture Web hosting with a real Core-backed guided journey; development fixtures are insufficient.
- Migrations and Headgate migrations.
- Example tenant, capture profile, policy, and webhook receiver.
- Health and readiness.
- Failure and recovery demonstration.

The clean usability gate must demonstrate:

1. Start the core without Cloud or Console.
2. Run migrations.
3. Create a tenant and scoped credential.
4. Activate an example policy.
5. Install or use a supported SDK.
6. Complete a synthetic capture journey.
7. Execute checks.
8. Produce and reproduce a decision.
9. Deliver and independently verify a signed webhook.
10. Inspect the audit chain.
11. Simulate provider failure and recovery.
12. Run retention and deletion.
13. Export tenant-owned data.

---

## 27. Release and external-beta evidence

**Classification:** External evidence gap

X-03 and X-04 remain In review. Required evidence includes:

- Official provider sandbox or provider-approved fixture results.
- Provider country, product, callback, retention, and deletion confirmation.
- Jurisdiction and regional review.
- Signed release candidate.
- Clean-room build and provenance verification.
- Independent penetration testing with no unresolved critical findings.
- Production-shaped load and 24-hour soak.
- Supported-device matrix.
- Accessibility and interruption testing.
- Mixed-version deployment and scaled migration rehearsal.
- Backup, evidence, key, audit, and credential restoration evidence.
- Incident exercises.
- Immutable evidence links and owners in the release checklist.

---

## 28. Later commercial and ecosystem capabilities

**Classification:** Accepted later milestones or Explicitly deferred

The following capabilities are absent but do not block the open-source-first deterministic core:

### 28.1 Console and managed Cloud

- Commercial operator dashboard.
- Verification explorer and reviewer workspace.
- Policy and provider management experiences.
- Commercial RBAC, SSO, SCIM, and dual-control experiences.
- Managed capture-experience editor and asset pipeline.
- Managed approvals, publishing, targeting, analytics, and rollback.
- Custom domains and managed mobile application registration.
- Managed Cloud, Connect Your Core, and BYOC control planes.
- Billing, licensing, entitlements, support, and fleet operations.

These surfaces must consume documented public core contracts and cannot become prerequisites for compiling, operating, recovering, or exporting from the core.

### 28.2 Idenqa Pass and reusable verification

- Regional reusable-profile vault.
- Passkeys, device binding, and recovery assurance.
- Pairwise tenant subject mappings.
- Reuse request, grant, presentation, disclosure, and revocation lifecycle.
- Selective disclosure and recipient-bound proofs.
- Component freshness, status, revocation, and delta checks.
- User disclosure history, correction, suspension, and deletion.
- Cloud-to-self-hosted signed presentations.

This is a later proprietary Cloud network capability and not a core v1 requirement.

### 28.3 Ecosystem

- Signed public adapter registry.
- Additional adapter tooling and conformance profiles.
- Additional native capabilities and framework wrappers.
- Additional regional data planes.
- Enterprise deployment references.
- External issuer, wallet, credential, trust-registry, and status interoperability.

---

## 29. Milestone alignment assessment

This table compares the broader v0.6 milestone outcomes with the current repository. It does not change the narrower build-plan brick statuses.

| v0.6 milestone                                | Architecture-wide assessment | Principal remaining work                                                                                                                                                                                                                                                        |
| --------------------------------------------- | ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Milestone 0 — Foundations                     | **Partial**                  | General event contracts, remaining public administration parity, and clean-deployment foundation acceptance.                                                                                                                                                                    |
| Milestone 1 — Deterministic identity core     | **Partial**                  | General resumption/operational-failure branches, complete review/reconsideration acceptance, and the clean self-hosted journey. Persistent identity, assurance context, synthetic completion, cancellation/expiry, policy administration, and signed delivery are implemented.  |
| Milestone 2 — Evidence and capture            | **Partial / In review**      | D-026 interaction completion and acceptance, portable experience schema, complete native SDK lifecycle, and production document/biometric integration.                                                                                                                          |
| Milestone 3 — Biometrics                      | **In progress / Partial**    | Production-accepted PAD and face models, representative datasets, calibrated thresholds, temporal liveness evidence, hardware/deployment acceptance, and production promotion/rollback. The evaluation runner, registry, dataset tooling, and composed engineering paths exist. |
| Milestone 4 — Real providers and global packs | **In review / Partial**      | Immutable packs, tenant-owned official-account evidence, live health/callback/deletion operations, equivalent fallback, and regional/legal acceptance. Dojah and Smile ID runtime paths are composed with local fixtures.                                                       |
| Milestone 5 — Operations and hardening        | **Partial / In review**      | External operator/certification trust, production KMS/egress/rate limiting, clean deployment, penetration test, load/soak, mixed-version and restoration rehearsals, SLO evidence, and signed candidate. Review queue and tenant-attested operator foundations exist.           |
| Milestone 6 — AI-native orchestration         | **In review**                | Real generative-model adapter, external provenance, representative evaluation, hardened deployment, and independent guardrail review. Proposal, guardrail, mode, registry, public API/SDK/CLI, and bounded product foundations are implemented.                                 |
| Milestone 7 — Reusable Verification Cloud     | **Later**                    | Entire commercial Pass and reusable-verification capability.                                                                                                                                                                                                                    |
| Milestone 8 — Ecosystem                       | **Later**                    | Registry, additional adapters/regions/wrappers, enterprise deployment, and credential interoperability.                                                                                                                                                                         |

The build plan's M-0, M-1, M-3, and M-4 completion records remain valid for their explicitly bounded bricks and proofs; M-2 remains **In review**. The architecture-wide assessment is broader and identifies capabilities those bricks intentionally did not include.

---

## 30. Version-one acceptance gaps

The following v0.6 version-one acceptance outcomes are not yet demonstrated end to end:

- Complete Core operation from a clean deployment without managed Cloud.
- Complete documented SDK, CLI, and capture operation without undocumented endpoints.
- Two real provider adapters operating through the composed runtime with external evidence.
- Provider replacement in the complete workflow without customer API or evidence-meaning changes.
- Real model replacement without workflow-definition changes.
- Production decision reproduction covering accepted real evidence, lineage, signals, models, providers, runtimes, preprocessing, thresholds, authority, assurance, and policy; AS-01 implements the general snapshot and replay contract.
- Complete resumption, external-wait, and operational-failure lifecycle handling while preserving workflow-state separation from identity outcome.
- Accepted end-to-end manual review, recovery, reconsideration, correction, and appeal journeys with least privilege and authenticated reviewer authority; one live recapture/re-evaluation path is demonstrated, but O-03 remains **In review**.
- Production provider/model evidence-grant processing with external systems; local composed routes already prove the owned mechanics.
- Deletion of all selected derived assets and external/provider copies.
- Automatic recovery across the complete workflow lifecycle.
- External-success reconciliation with a real provider.
- Complete capture SDK interruption, resumption, cancellation, cleanup, compatibility, and security conformance.
- Complete open-source subject journey through the portable experience schema and safe default theme.
- Experience, locale, tenant-copy, and mandatory-copy session pinning.
- Signed accessible experience fallback.
- Production model evaluation, calibrated promotion, monitoring, and rollback; evaluation-only registry and offline gates are implemented.
- Full backup restoration covering evidence, keys, credentials, and audit.
- Mixed-version and expand-migrate-contract evidence.
- Clean open-source usability gate.
- Independent penetration test with no unresolved critical findings.

The AI acceptance criterion is conditional: every **enabled** AI action must be a recorded proposal approved by guardrails. AI may remain disabled for version one. The proposal and guardrail foundations are implemented, but M-6 cannot complete without the production and external gates recorded in section 3.

---

## 31. Recommended sequencing

This sequence lists only unresolved work as of 15 September 2026. L-01 through L-03, H-01, P-01, I-01, AS-01, the selected tenant-local fraud baseline, and the repository-owned AI-01 through AI-05 slice are implementation baselines rather than future tasks. Their production or external gates remain listed in the owning sections.

### 31.1 Close active product and workflow review gates

1. Finish D-026 acceptance: retain the nine passing real-Core journeys, including authoritative expiry through D-027's separate outcome credential; record responsive browser evidence; and obtain explicit advanced-interaction acceptance. Continue rejecting expired capture bearers and browser-inferred lifecycle state. Optional choice screens remain bounded by the immutable session or future experience contract; any country choice that changes policy or profile must occur before session creation. Then close E-04 and M-2 if their exit proof passes.
2. Complete O-03's remaining review gates: retain the passing review-to-recapture-to-explicit-follow-up path; add composed progress-preserving recovery, correction/appeal/escalation outcomes, consequential-action acceptance, and the external operator-certification boundary. Obtain reviewer/subject visual acceptance. Do not reopen already implemented routing, queue, evidence-display, arbitration, correction-successor, or public-contract work.

### 31.2 Close the deterministic self-hosted core

3. Add general session resumption, external-wait and operational-failure orchestration, plus worker-loss recovery proofs across every supported lifecycle state.
4. **Resolved 18 September 2026:** the general event catalogue, endpoint subscriptions, versioned schemas/fixtures and bounded resumable fanout are implemented; only the multi-batch crash-resume exercise and the section 26 clean-deployment proof remain.
5. Close public contract and CLI parity for the still-internal or uncovered operations identified in sections 18 and 19, prioritising privacy/deletion status, evaluation-model registry, evidence/consent administration, and operational diagnostics.
6. Package API, worker, object storage, runners, Capture Web, migrations, example tenant/profile/policy/webhook receiver, and failure recovery into the clean self-hosted usability gate in section 26.

### 31.3 Complete the selected first-adopter identity path

7. Implement immutable Nigeria, Ghana, Kenya, and South Africa country/document/jurisdiction packs and their assurance mappings before advertising country support.
8. Implement the general document subsystem required by those packs, including classification, OCR, MRZ/barcode, image correction, field consistency, portrait extraction, manipulation risk, support-level projection, and derived-data deletion.
9. Complete production PAD and face-comparison acceptance: reviewed genuine/attack and genuine/impostor data, licensed weights, preprocessing/capture provenance, calibrated thresholds, temporal active-liveness evidence, cohort/device analysis, and hardened deployment. Existing ONNX, registry, evaluation, and composed workflow mechanics remain the starting point.
10. Run Dojah and Smile ID through tenant-owned official accounts with controlled evidence access, live health/callback or polling, retention/deletion exercises, legal/regional approval, and an exact-semantics outage/fallback demonstration. Existing local-fixture PR-01/PR-02 composition remains the starting point.
11. Complete Swift and Kotlin session lifecycle, durable resume, cancellation, cleanup, native liveness/document UI, platform security, accessibility, interruption, and supported-device conformance.
12. Implement the portable versioned capture-experience schema, validation, lifecycle, copy/asset separation, targeting, signing, session pins, revocation/rollback, accessible fallback, and export/import. CSS custom properties remain appearance-only.

### 31.4 Close production and release gates

13. Select and implement the first production KMS/secret-management path, tenant HMAC lifecycle, provider/model egress isolation, distributed rate limiting, authenticated operator identity, and complete derived/external deletion.
14. Complete regional/provider certification, independent penetration testing, production-shaped load and 24-hour soak, mixed-version and scaled migration rehearsals, complete restoration, SLO measurement, supported-device evidence, and incident exercises.
15. Produce and independently verify the signed external-beta candidate with immutable evidence links and owners.

### 31.5 Keep later boundaries explicit

16. If AI is enabled beyond the deterministic reference path, add a selected real generative-model adapter and complete the external provenance, evaluation, deployment, and independent guardrail gates in section 3. Do not treat this as a version-one prerequisite while AI remains disabled.
17. Keep cross-tenant fraud intelligence, Console/Cloud, Idenqa Pass, reusable verification, registry/ecosystem expansion, and explicitly deferred D-014 capabilities in their named later boundaries unless the user selects a new scope.

---

## 32. Completion rule for this audit

A gap may be removed from this document only when one of the following is recorded:

1. The capability is implemented and its applicable automated, integration, security, conformance, and operational evidence passes.
2. A more authoritative accepted decision explicitly narrows or removes the capability.
3. The capability is moved to an explicitly named later milestone or deferred boundary without being represented as already available.

When a gap changes status, update the narrowest authoritative architecture or repository decision first, update the build plan when implementation sequencing or evidence changes, and then update this audit snapshot.
