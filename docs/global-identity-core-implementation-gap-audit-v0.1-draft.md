# Idenqa Core Implementation Gap Audit v0.1 Draft

**Status:** Draft for review
**Date:** 4 September 2026
**Re-baselined:** 14 September 2026 — reconciled with the current build-plan statuses, generated OpenAPI, public TypeScript SDK, CLI, migrations, runtime composition, and Capture Web evidence. Historical implementation records retain the evidence and limitations recorded when each increment was completed.
**Updated:** 22 September 2026 — synchronized the full gap review and added the consolidated remaining-work index in section 2.2. Corrected stale experience-pinning/fallback, evaluation-registry contract, document-choice acceptance-only and custody-migration claims. The 30-operation SDK increment retains its recorded passing evidence; production, external, device and human-acceptance gates remain open. No milestone status or architecture decision changed.

**Compared architecture:** [`global-identity-core-technical-architecture-v0.6-draft.md`](global-identity-core-technical-architecture-v0.6-draft.md)  
**Repository decisions:** [`global-identity-core-repository-structure-and-packages-v0.1-draft.md`](global-identity-core-repository-structure-and-packages-v0.1-draft.md)  
**Build tracker:** [`global-identity-core-build-plan-v0.1.md`](global-identity-core-build-plan-v0.1.md)

> **Remaining work:** [section 2.3](#23-remaining-work-checklist) highlights every
> recorded open boundary as an unchecked item, grouped by workstream. It includes
> implementation, acceptance, decisions and explicitly labelled later/conditional
> scope. Document-choice implementation is complete; real-camera liveness and
> explicit interaction acceptance remain open.

This document records the implementation capabilities that are absent, partial, externally unproven, deliberately later, or deliberately deferred when the current repository is compared with the complete v0.6 technical architecture.

It is an audit snapshot, not a replacement architecture or build plan. It does not promote a Proposed, Conditional, or TBD item to Selected, and it does not change any brick status. The repository/package draft remains authoritative for package and dependency decisions, while the build plan remains authoritative for the scope and recorded evidence of individual bricks.

---

**SDK evidence — 22 September 2026:** The handwritten TypeScript and typed Go follow-up is implemented for 30 published administration operations. Focused SDK checks and the real-HTTP/restricted-role PostgreSQL journey pass, including race detection. See [public SDK resources](public-sdk-resources-v0.1.md) for the exact scope, environment limits and remaining work.

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

- Non-deterministic AI is an **accepted architecture capability** scheduled for Milestone 6. The repository-owned proposal, guardrail, mode, registry, bounded product slice, provider-neutral generator and OpenAI-compatible/Anthropic protocol adapters are implemented; the build-plan milestone remains **In review**. Live provider/model evidence and production acceptance remain open; AI stays disabled by default and does not block version one while disabled.
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
| Verification lifecycle          | L-01 through L-03 compose the full state vocabulary, synthetic capture-to-decision delivery, cancellation, expiry, recovery guards, and a public expected-version/idempotent resume command; bounded provider semantic retries are scheduled atomically with the terminal attempt, the session enters `awaiting_external` while a durable asynchronous provider operation is pending, tenant reads project the current decision, review case, structured requested input and operational failure, and an authenticated provider callback route resolves an opaque attempt reference through the isolated runner for signature verification and a durable replay-identity receipt. O-03 composes nonterminal review routing, findings, linked recapture, credential recovery, explicit re-evaluation, behaviourally proven escalation arbitration, correction and appeal branches, composed real-HTTP journeys, and a fail-closed external certification adapter. | Live provider-account callback operation, third-party issuer trust integration, reviewer/subject visual acceptance, and production runner/provider acceptance. |
| AI-native orchestration         | [`contracts/proposal/v1`](../contracts/proposal/v1), [`internal/proposal`](../internal/proposal), and [`adapters/proposals`](../adapters/proposals) implement proposal/command separation, deterministic guardrails, modes, registries, bounded products, model-agnostic exact routing, OpenAI-compatible and Anthropic adapters, exact tenant registry/runtime binding, public audited activation/retirement/rollback, enforceable mode pins, all-attempt content-free usage receipts and aggregate reporting, public API, TypeScript SDK, and CLI. M-6 is **In review**. | Live provider/model provenance, representative evaluation, deployment hardening, provider-invoice reconciliation and unknown-spend handling where providers do not report failed-call usage, production monitoring/SLO evidence, and independent guardrail review. |
| Predictive model execution      | The ONNX evaluation runner, immutable evaluation registry, threshold revisions, dataset comparison/drift tooling, durable PAD/face-comparison/selfie-analysis composition, persisted execution graphs, exact fallback admission, correlated overlap handling and complete temporal-sequence model consumption are implemented. | Accepted data and weights, calibrated production thresholds, accepted active-liveness and occlusion/accessory models, production deployment, live hot provisioning, and production promotion/rollback evidence. |
| Tenant-local fraud              | [`internal/fraud`](../internal/fraud), migration 48, public API/SDK, and policy integration implement the selected tenant-local baseline.                                                                                                                                                                                      | Deployment-specific source acceptance and regional approval; cross-tenant intelligence remains explicitly deferred.                                                                                  |
| Subject and identity projection | [`internal/identity`](../internal/identity), migration 49, public API/SDK, and privacy/policy integration implement the initial core subject and identity model.                                                                                                                                                               | Real structured-provider ingestion, richer country normalisation, and larger incremental deletion planning.                                                                                          |
| Provider execution              | Dojah and Smile ID adapters are composed through bounded synchronous and asynchronous runtime paths with local-fixture recovery evidence; tenant provider registration and selection with secret-free configuration references, dispatch-evidence health reads, health-based registration routing, circuit breaking and a failure-classification preview are implemented.                                                                                                                                                                                      | Tenant-owned official-account runs, production credential custody evidence, provider-side deletion, legal/regional approval, and equivalent fallback proof.                                        |
| Biometrics and documents        | Acquisition orchestration, complete Web temporal-frame persistence, ONNX preparation, evaluation-only PAD/face comparison with portrait selection and five-landmark alignment, provider document-analysis paths, Capture Web auto-capture with perspective correction, and Core MRZ/barcode/field-consistency/classification with documented provider extraction exist and remain fail-closed or inconclusive without accepted assurance. | Production PAD/face models and evaluation, native/mobile temporal integration, template/security-feature and manipulation inspection, representative portrait/alignment acceptance, full OCR coverage and operational teardown proof for transient model caches. |
| Public API                      | The generated OpenAPI and internal TypeScript contract include review, fraud, identity, assurance, proposal, cancellation, resume, policy and webhook administration; privacy request administration, decision history/reconsideration, safe evidence metadata/lifecycle, idempotent evidence-access grants, append-only consent receipts, policy simulation/diff/regression, tenant provider administration, evaluation-model registry administration and proposal impact assessments are public. Tenant verification reads project the optional current decision and current review case. Handwritten TypeScript and typed Go clients now cover 30 administration operations with focused tests and a passing real-HTTP/PostgreSQL journey. | Broader Go resource parity beyond the additional 19 provider/model administration methods, deeper adapter conformance helpers, comprehensive server failure-injection evidence, independent-review acceptance, and model-health/OAuth contract decisions remain. |
| Webhooks                        | L-02 composes atomic completion delivery; H-01 adds public endpoint administration, inspection, rotation and replay; catalogue/subscriptions, exact `1.0` endpoint pins, resumable fanout with replacement-worker proof, list/SSE/local forwarding, hold-aware seven-day payload retention with 365-day tombstones, custom forward headers, endpoint-subscription discovery, a local reference-only projection, and `Last-Event-ID`-based durable history replay are implemented. | The fixture-backed packaged delivery proof passes; production-safe webhook networking and external release evidence remain. |
| Capture experience | Capture Web supplies the D-026 compact guided journey, measured-liveness implementation, profile/session-backed document choices, arbitrary namespaced adapters, D-027's subject-safe outcome projection, live-Core synthetic journeys, theming and the signed/versioned experience contract. E-04 and M-2 remain **In review**. | Validate measured liveness on real cameras/devices and obtain explicit document/advanced-liveness interaction acceptance; managed editing, DNS-verified custom domains and native rendering remain gated. |
| Self-hosted usability           | Individual binaries, migrations, runners, SDKs, Capture Web fixtures, and synthetic integration journeys exist; `deploy/self-hosted/` now packages the full stack (API, worker, runners, MinIO, Capture Web, webhook receiver, migrations, health), the optional S3 profile contains a paired API/worker composition, and `smoke.sh` passes the thirteen-step clean usability gate.                                                                                                                                               | Browser-driven Capture Web journey, real provider/model runner dialing, and live production S3 worker-deletion evidence remain external or section 4/9/20/24 gates.                                      |
| External beta                   | X-03 and X-04 are **In review** with adapter, conformance, security/release workflow, SBOM, and provenance foundations.                                                                                                                                                                                                        | Provider/legal evidence, independent security review, load/soak, mixed-version and restore rehearsals, supported-device evidence, and a signed candidate.                                            |

---

### 2.2 Consolidated remaining work — 22 September 2026

This index separates unfinished implementation from acceptance and later scope.
The owning sections below retain the detailed requirements and evidence limits;
no item here changes a Selected decision or a build-plan status.

**Implemented baselines, not remaining tasks:** the 30-operation public SDK
administration increment, profile/session-backed document-selection enforcement
and conformance, experience/session pinning, signed fallback, evaluation-registry
contracts and S3 worker-deletion composition. Their implementation evidence does
not close real-camera, human-interaction, live-provider/model or production-storage
acceptance. The [external-beta checklist](releases/external-beta-checklist.md)
requires owners, immutable evidence links, review dates and pass results before
release; existing automation alone does not satisfy those gates.

**Implementation and integration still required:**

- **Capture and review (§§14–16):** native notice/consent, document and liveness UI, temporal integration, background transfer and UI-test coverage; real certification-issuer key discovery and status/revocation integration. Profile/session-backed Web document-choice enforcement and recovery are implemented. Advanced queue automation and tenant-notification product surfaces remain separate enhancements.
- **SDK and server follow-ups (§18):** broader typed Go parity beyond 49 methods and deeper provider/model adapter conformance helpers; published contracts before exposing older deletion-run/legal-hold writes; startup-error listener/pool cleanup ordering and privacy-region validation alignment.
- **Models and documents (§§4, 11–12):** accepted production PAD, face and applicable document/selfie models; full OCR, template/security-feature and manipulation inspection; production check-plan selection, live registry routing/provisioning, scheduling, warm capacity, shadow/canary promotion, rollback and drift monitoring. Evaluation-only diagnostics establish no production assurance.
- **Providers (§9):** broader subject-input resolution, multi-operation grants, durable delivery recovery/reconciliation, provider-side deletion, duplicate-charge reconciliation, attribution and budgets. Existing health, concurrency/rate controls, callback receipts and polling must not be counted as missing.
- **Identity and assurance (§§6, 8; repository decisions 26–27):** structured provider extractors and automatic ingestion, richer normalisation, larger incremental deletion planning, field-level provenance/capability extensions, larger graph planning and recovery for assurance that expires before commit. Production assurance mappings and fraud configuration still need approved deployment inputs.
- **Security, custody and privacy (§§10, 20–21, 25):** identity lookup custody integration; API/worker secret-backed runner credentials/trust with safe redial; runner egress/OS isolation; no-Redis distributed HTTP rate limiting; malware scanning, sandboxed parsing, explicit secret-scanning and dependency-licence gates; live production S3 deletion evidence plus external-copy/backup enforcement. The repository composition for S3 worker deletion is implemented.
- **Observability (§23):** capture-start/drop-off instrumentation, calibrated model/cohort/drift metrics, provider cost data and regional pending-work capacity.

**Acceptance and release evidence still required:**

- Real-camera measured-liveness accuracy, pose/blink and subject-relative left/right behaviour, calibrated thresholds and supported-device performance; explicit document capture/review and advanced-liveness interaction acceptance; reviewer/subject acceptance; native accessibility, interruption and integrity evidence (§§14–16). Engineering thresholds and synthetic outcomes do not establish accepted biometric assurance.
- Licensed weights and lawful representative datasets; calibrated operating points, demographic/device analysis, reproducibility, supported-hardware acceptance, equivalent fallback and transient-cache teardown (§§4, 11–12).
- Tenant-owned Dojah/Smile ID account runs, live callbacks and external-success recovery, retention/deletion and outage/replacement exercises; NG/GH/KE/ZA structural sources, provider confirmation, legal review and substantiated assurance mappings (§§9, 13).
- Live AWS IAM/rotation/rewrap/deletion/recovery exercises; complete production provider/model privacy execution and restoration of data, evidence, keys, credentials and audit (§§20–24).
- Browser-driven packaged deployment with real runners, production object storage and safe webhook networking; comprehensive failure-injection evidence and PostgreSQL 18.4 qualification of the recent SDK proof (§§18, 26).
- Measured SLO/RPO/RTO, load and 24-hour soak, mixed-version and scaled migration rehearsals, incident exercises, independent penetration testing, clean-room provenance verification and a signed candidate with immutable evidence links and owners (§§24–27).

**Decisions and non-v1 scope:**

- Public model-health and OAuth semantics, rate-limit strategy, future module/release boundaries, licence-review policy, credential evolution, per-operation retention/concurrency, large-media resumability, rotation/content-migration cadence and backup/external-delivery deletion boundaries remain in [repository section 17](global-identity-core-repository-structure-and-packages-v0.1-draft.md#17-decisions-still-required). Do not implement a TBD as an accepted contract.
- AI production gates apply when enabled: approved live routes, provenance, billing/unknown-spend reconciliation, representative evaluation, monitoring/budgets, hardened deployment and independent guardrail review (§3). AI remains disabled by default and is not a v1 prerequisite while disabled.
- Managed experience/asset authoring, DNS-verified custom domains, native experience rendering, multi-region infrastructure, Console/Cloud/BYOC, SSO/SCIM, Pass/reuse and ecosystem expansion retain their named later boundaries (§§14, 22, 28). Cross-tenant fraud, unadvertised framework wrappers and D-014 capabilities remain deferred or conditional, not current-core implementation tickets.

### 2.3 Remaining-work checklist

**Every unchecked item below is still open.** This is the detailed checklist for
the remaining boundaries recorded in this audit, including unresolved decisions
in repository section 17. Section references identify the detailed evidence and
scope. An unchecked acceptance item does not mean its underlying implementation
is missing. **Later**, **conditional** and **TBD** items retain those labels and
do not become version-one requirements or approved implementation work.

#### 2.3.1 Capture Web and native capture — §§14–15

- [ ] **Web liveness acceptance:** validate real-camera pose/blink accuracy, subject-relative left/right behaviour with mirrored previews, representative subjects, calibrated target/hold/quality thresholds, and supported-browser/device performance. Accept or replace the evaluated tracker/model; engineering defaults are not production calibration.
- [ ] **Product acceptance:** obtain explicit visual and interaction acceptance for document capture/review and measured liveness. Keep E-04/M-2 **In review** until the applicable exit proofs pass.
- [ ] **Native UI:** complete Swift/Kotlin notice and consent, document front/back and active-liveness journeys, including native temporal/video evidence persistence and production PAD/face-match integration.
- [ ] **Native operation:** background transfer/recovery where supported, MRZ/barcode result presentation, orientation/accessibility UI conformance, a native UI-test harness, and real-device permission/interruption/screen-protection evidence.
- [ ] **Platform assurance decision/evidence:** accepted root, jailbreak, emulator, instrumentation and integrity signals, plus a supported-device compatibility matrix.
- [ ] **Platform extensions:** remaining video/provider adapters and native rendering of resolved experience copy/locale. NFC/voice and framework wrappers retain their deferred/conditional scope below.

#### 2.3.2 Review and authenticated reviewer integration — §§7, 16, 25

- [ ] **Issuer integration:** real third-party certification key discovery, trust/status and revocation checks beyond the implemented deployment-key adapter.
- [ ] **Interaction acceptance:** reviewer and subject review/recapture/recovery/reconsideration/correction/appeal journeys with least privilege and accepted reviewer authority.
- [ ] **Later operator integration:** authenticated operator-claim identity beyond the selected tenant-attested boundary; SSO remains deferred rather than an initial-core prerequisite.
- [ ] **Operational enhancements:** advanced queue assignment automation, tenant subject-notification and correction-request product surfaces.
- [ ] **Conditional AI acceptance:** production review-copilot UX and evidence with an approved real generative model when enabled.

#### 2.3.3 Predictive models, biometrics and MLOps — §§4, 11

- [ ] **Models and data:** licensed production PAD and face-embedding weights, applicable document/selfie models, lawful representative genuine/attack and genuine/impostor datasets, dataset governance, rights/provenance verification, and privacy/impact assessment.
- [ ] **Capture/preprocessing:** production acceptance of face localisation/contextual transforms, document-portrait selection, five-landmark alignment, capture provenance and quality bounds; independently accepted active-liveness challenge-response assurance.
- [ ] **Biometric evaluation:** calibrated operating points; false-match/false-non-match, failure-to-acquire/enrol and APCER/BPCER results; demographic, device and camera-class analysis; validation of liveness/similarity separation.
- [ ] **Additional detection:** deepfake, replay, screenshot and virtual-camera injection signals where feasible; occlusion/accessory and semantic challenge-response classifiers require accepted models. Age, watchlist/public-figure and account-comparison capabilities require separate justification and lawful data before implementation.
- [ ] **Runtime acceptance:** hardened OCI/kernel CPU, memory, PID and egress controls; supported hardware/accelerators; model-specific reproducibility and inference records identifying hardware/runtime class.
- [ ] **Production administration:** approved production check-plan/route selection, public route administration, live registry routing and runner hot provisioning beyond mounted immutable evaluation routes.
- [ ] **Capacity/resilience:** resource/accelerator scheduling, warm capacity, broader supervision and dynamic route failover; operational acceptance of explicitly equivalent fallback sets.
- [ ] **Promotion:** shadow execution, canary promotion, automatic technical rollback, production quality rollback and live drift ingestion/monitoring beyond offline comparison and evaluation-only rollback.
- [ ] **Biometric privacy evidence:** independent production capture/reference-portrait retention approval and operational proof that process/model-cache teardown clears all transient copies. Persisted embeddings/crops/tensors remain prohibited by the current contract.

#### 2.3.4 Documents and country/assurance packs — §§12–13

- [ ] **Document inspection:** accepted template/security-feature, manipulation and screenshot inspection; full OCR/field coverage and representative portrait extraction/alignment acceptance.
- [ ] **Country substantiation:** official national-ID/driver-licence structural sources and tenant-owned provider/product/account confirmation for NG/GH/KE/ZA before advertising support.
- [ ] **Legal and assurance approval:** country/region review, substantiated purpose/recipient/transfer requirements, representative document evaluation and evidence-backed assurance mappings.

#### 2.3.5 Provider runtime and costs — §9

- [ ] **Integration breadth:** purpose-bound subject-input resolution beyond current deployment references, broader multi-operation grants, durable evidence-delivery recovery and reconciliation beyond the implemented routes.
- [ ] **Provider privacy:** provider-side deletion orchestration and live retention/deletion exercises after complete verification journeys.
- [ ] **Cost controls:** duplicate-charge reconciliation, cost attribution and budget limits; workflow-metadata retention/purge for immutable request/dispatch receipts remains a repository follow-up.
- [ ] **Official-account evidence:** Dojah/Smile ID sandbox or provider-approved runs, country/product confirmation, live callbacks/polling, external-success/local-failure recovery and approved recipient/purpose/region use.
- [ ] **Replacement evidence:** controlled outages and equivalent-provider fallback/replacement through the complete workflow without changing customer APIs or evidence meaning.

#### 2.3.6 Identity, fraud and assurance extensions — §§5–8

- [ ] **Identity ingestion:** production structured provider extractors, automatic ingestion and richer country-specific normalisation.
- [ ] **Identity scale:** incremental deletion planning beyond the initial atomic subject/link/evidence limits.
- [ ] **Fraud deployment inputs:** accepted canonicalisation namespaces, trusted-template sources, thresholds, network/provider/model mappings and regional operating approval. Similarity search, simultaneous regional active configurations and cross-tenant intelligence remain outside the initial baseline.
- [ ] **Assurance follow-ups:** approved production profiles and biometric operating points, jurisdiction/framework mappings, richer capability versions, field-level runner provenance/manifests, larger graph planning and recovery when assurance expires before commit.

#### 2.3.7 Public SDK/API/CLI follow-ups — §§18–19

- [ ] **SDK breadth:** broader typed Go endpoint parity and deeper provider/model adapter conformance helpers beyond the original 30-operation increment plus the 19-method Go provider/model administration extension (49 total).
- [ ] **Contract-first extensions:** publish the required contracts before exposing older deletion-run/legal-hold writes; do not infer a contract from internal endpoints.
- [ ] **Server follow-ups:** startup-error listener/pool cleanup ordering and privacy-region validation alignment recorded by the SDK follow-up review.
- [ ] **Acceptance:** comprehensive server failure injection, independent review and PostgreSQL 18.4 qualification of the recent public-SDK proof.
- [ ] **TBD — model health:** select resource freshness/failure semantics (live probe, supervised durable state or aggregate), then provide API/SDK/CLI inspection against that accepted contract.
- [ ] **TBD — OAuth:** issuer, token format, audience, tenant/scope mapping, issuance, rotation, revocation and overlap before implementing another server credential format; API keys remain the selected initial mechanism.

#### 2.3.8 Runner isolation and secure development — §§10, 25

- [ ] **Network isolation:** complete per-adapter egress allow-lists, destination DNS pinning, reserved/private-address rejection, redirect revalidation/rejection and DNS-rebinding protection beyond the accepted route-specific controls.
- [ ] **OS isolation:** read-only filesystems, CPU/memory/process/file-descriptor limits, per-adapter secret scope, container/process isolation, supervision and termination on policy breach.
- [ ] **HTTP protection:** select and implement distributed rate limiting while preserving the no-Redis baseline.
- [ ] **Evidence processing:** malware scanning and sandboxed document parsing.
- [ ] **Supply-chain gates:** explicit secret scanning and an enforced dependency-licence allow-list/review workflow.
- [ ] **Independent evidence:** penetration testing with no unresolved critical findings, production incident exercises and clean-room provenance verification.

#### 2.3.9 Custody, privacy and deletion — §§20–21

- [ ] **Custody integration:** migrate identity keyed lookup onto the custody catalogue with legacy/custody verification; assign its migration number when implemented.
- [ ] **Client credentials:** secret-provider-backed API/worker outbound runner credentials and trust material with safe redial/overlap; optional client mTLS remains a decision.
- [ ] **Live AWS evidence:** IAM failure modes, Secrets Manager rotation, material rewrap, key-deletion scheduling and production recovery ceremonies.
- [ ] **Operational decisions:** rotation cadence, content-algorithm migration trigger/rollout, backup deletion boundaries and externally delivered payload retention/deletion treatment.
- [ ] **End-to-end privacy:** real provider/model deletion, teardown of external model/OCR/index caches, production backup expiry enforcement and full restoration evidence. Core-owned deletion and event-payload expiry are already implemented.

#### 2.3.10 Observability — §23

- [ ] Capture-start instrumentation and drop-off by step.
- [ ] Production-calibrated model score, threshold, cohort and drift metrics.
- [ ] Provider cost attribution/budget data and regional pending-work capacity reporting.

#### 2.3.11 Deployment, reliability and external beta — §§17, 24, 26–27, 30

- [ ] **Packaged product journey:** browser-driven capture from a clean packaged deployment with real provider/model runner dialing, production object storage and production-safe webhook networking rather than the development network exception.
- [ ] **Production storage:** live evidence that the paired S3 API/worker composition deletes exact production objects safely, including scale and failure recovery.
- [ ] **Production contract proof:** documented SDK/CLI/capture operation for real provider/model/privacy paths, scoped external evidence-grant processing, model replacement without workflow changes and decision reproduction with accepted real lineage/provenance.
- [ ] **Full recovery:** real provider callbacks and external-success reconciliation, worker/runner loss, provider credentials, KMS keys, database, evidence/object storage and audit restoration across the complete lifecycle.
- [ ] **Reliability measurements:** API/webhook/recovery SLOs, RPO/RTO, production-shaped load and 24-hour soak.
- [ ] **Upgrade/operations evidence:** mixed-version deployment, scaled expand-migrate-contract rehearsals and incident exercises.
- [ ] **Release closure:** supported-device/accessibility/interruption evidence, independent security acceptance, clean-room build/provenance verification and a signed candidate with immutable evidence links, owners and review dates. X-03/X-04 remain **In review**.
- [ ] **Shared-checkout checks:** resolve or independently disposition the recorded repository-wide Go lint findings and OpenAPI warnings before claiming a clean release gate; focused passing checks do not close them.

#### 2.3.12 AI production gates — §3; conditional when enabled

- [ ] Approve exact live provider/model routes and provider terms/data-handling provenance.
- [ ] Reconcile estimated/reported usage with provider invoices and define unknown-spend handling for failed calls without reported usage.
- [ ] Representative prompt/model and AI-assisted-routing evaluation; production monitoring, budgets and SLO evidence; complete product UX.
- [ ] Hardened deployment/egress isolation and independent guardrail review; accelerator scheduling only if a future self-hosted route requires it. AI may remain disabled for version one.

#### 2.3.13 Remaining foundational decisions — repository §17

- [ ] Future independently published SDK/adapter/contract/conformance module paths and root-versus-independent release boundaries beyond the selected modules.
- [ ] Future task-type retention, operation-specific idempotency/replay/in-progress policies and transaction/locking/concurrency policies beyond already selected workflows.
- [ ] Durable WebSocket replay retention and deployment topology.
- [ ] Future capture-token/runner credential evolution beyond selected formats.
- [ ] Large-media byte-offset resumability and encryption-compatible staging.
- [ ] Whether Ozzo is needed beyond configuration validation, and whether optional/internal Effect adoption meets its conditional requirements. Neither is approval to add a dependency.

#### 2.3.14 Regional infrastructure — §22; accepted later boundary

- [ ] Independent regional data planes; trusted routing/explicit regional API selection; cross-deployment allowed-region enforcement and rejection/redirection rules.
- [ ] Approved evidence transfer, region-specific provider/model availability, keys and backups, capacity/health reporting and residency-safe failover.
- [ ] First managed-region decision and operational evidence; no region inference from IP, locale or device language and no silent cross-region fallback.

#### 2.3.15 Commercial and ecosystem scope — §28; later

- [ ] **Console/Cloud:** operator dashboard, verification explorer/reviewer workspace, policy/provider management, commercial RBAC/SSO/SCIM/dual control, managed experience and asset authoring, approvals/publishing/targeting/analytics/rollback, DNS-verified custom domains and mobile application registration.
- [ ] **Commercial operations:** Cloud/Connect Your Core/BYOC control planes, billing, licensing, entitlements, support and fleet operations.
- [ ] **Pass/reuse:** regional vault, passkeys/device binding/recovery, pairwise mappings, request/grant/presentation/disclosure/revocation lifecycle, recipient-bound selective proofs, freshness/status/delta checks, disclosure history/correction/suspension/deletion and signed Cloud-to-self-hosted presentations.
- [ ] **Ecosystem:** signed public adapter registry, additional tooling/conformance profiles, regions/native capabilities/framework wrappers, enterprise deployment references and issuer/wallet/credential/trust-registry/status interoperability.

#### 2.3.16 Explicitly deferred or conditional scope — §§1, 5, 11–15, 28

- [ ] **D-014 deferred:** NFC and digital-credential trust chains, voice, KYB, AML, address, tax, phone, non-English localisation, additional countries and unsupported resident/refugee documents.
- [ ] **Outside v1:** cross-tenant fraud and one-to-many identification; similarity-based portrait search is outside the selected tenant-local baseline.
- [ ] **Conditional wrappers:** Flutter and React Native only become support obligations when advertised; use thin native wrappers without raw evidence crossing Dart/JavaScript bridges.

## 3. AI-native orchestration

**Classification:** Implemented selected scope; build-plan M-6 **In review**; production acceptance and external evidence remain open

The v0.6 architecture distinguishes deterministic computation, predictive ML, and non-deterministic AI. `contracts/model/v1` remains the signal-only contract (`evidence refs -> scored signals -> deterministic policy`). `contracts/proposal/v1` now implements the separate `ProposalModel` (`redacted bounded context -> non-authoritative proposal -> guardrails/human approval -> deterministic AcceptedCommand`). The implementation preserves the required boundary and fail-closed defaults.

**Implemented — 10 September 2026 (M6 AI-01..AI-05):**

- `contracts/proposal/v1` — `ProposalModel` port, immutable `AgentProposal` envelope, bounded `Actions[]` (closed allow-list 8 kinds), `evidence_refs/signal_refs` refs only, `model_id/version/prompt_version/context_digest` pinning, `expires_at/supersedes`, `AcceptedCommand` separate, `Version` compatibility, `Digest/Canonical` and closed JSON validation. Raw evidence never in envelope/task/log/audit. See `contracts/proposal/v1/contract.go:1`, `validate.go:1`, `contract_test.go:1`.
- `internal/proposal/proposal.go:1` — `Proposal` aggregate, `isValidStatus/isTerminalStatus`, `Validate()`, `Advance()` graph `pending -> approved/rejected/expired/cancelled/superseded`, `Version` CAS, `id.Proposal/id.AcceptedCommand` (`ProposalPrefix prp_`, `AcceptedCommandPrefix acc_` in `internal/platform/id/id.go:1036`).
- `internal/proposal/guardrail.go:1` — 10 deterministic checks: schema, evidence/signal refs via `EvidenceChecker`, allow-list, tenant-policy (`modeAllows`), jurisdiction/residency + processing-authority via `RegionValidator`/`AuthorityChecker`, cost/rate via `CostLimiter`, raw-evidence/sensitive-context, human approval, deterministic command, audit linkage. `highRiskKinds` require human.
- `internal/proposal/mode.go:1` — `ModeConfig` versioned `disabled|assist|recommend|guardrailed_auto|human_required`, `AllowListVersion`, `AllowedKinds`, `CostDailyLimit`, `PromptID`, session-pinned at creation, fail-closed on unknown mode/kind, `InMemoryModeStore` with `expectedVersion` CAS.
- `internal/proposal/registry.go:1` — `PromptRecord` (with `Sensitive` classification)/`GenerativeModelRecord` immutable, `DigestPrompt()`, `ImpactAssessment`, `InMemoryRegistry` with `CreatePrompt/GetPrompt`, `CreateModel/GetModel`, `CreateImpact`.
- `internal/proposal/reference.go:1` — dependency-free deterministic `ReferenceModel` implementing `ProposalModel` and `ReferenceExecutor` implementing `CommandExecutor`; `Service.Propose` invokes the selected model, validates its `AgentProposal` output, and persists through the same guardrails. The reference model remains the no-runtime-file default.
- `internal/proposal/generative.go:1` + `adapters/proposals/openaicompatible/client.go:1` + `adapters/proposals/anthropic/client.go:1` — provider-neutral `Generator` port and exact `model_id`/`model_version`/`prompt_version` router, strict local output normalisation, a shared OpenAI-compatible chat-completions adapter, and a separate Anthropic Messages/tool-use adapter. Provider types do not cross the owned boundary; credentials are `secret://` references; bodies are bounded; egress is exact-origin pinned; model/prompt/provider fallback is prohibited. Mode, allow-list, evidence/signal-reference, authority, residency and generation-rate preflight occurs before outbound invocation.
- `internal/config/proposal.go:1` + `internal/bootstrap/api/proposal.go:1` — closed mounted runtime configuration composes one to 32 exact model routes and selects the deterministic reference model only when the runtime file is absent. Production rejects fixture transport. `POST /v1/proposals` invokes `Service.Propose`, so the selected model path is exercised before the existing guardrail and persistence pipeline.
- `internal/proposal/generation_binding.go:1` + `internal/proposal/usage.go:1` + `internal/proposal/postgres/usage.go:1` + migrations 72/73/74 — each deployed route binds exact tenant-owned immutable prompt/model registry IDs, versions and digests; the logical model, mounted instruction digest, current active lifecycle revision and mode pins must match before egress. Public CAS-protected activation, retirement and rollback append immutable history. Every provider attempt persists a tenant-scoped, content-free outcome receipt; aggregate reporting exposes outcomes, unreported usage, reported tokens and locally estimated micro-cost. Pre-binding, retired and stale routes cannot authorize generation.
- `internal/proposal/products.go:1` — `GeneratePolicyDraft` (sensitive-classified via `isSensitivePrompt`, 7d vs 24h expiry), `GeneratePolicyDiff`, `GenerateAdversarialScenarios`, `ProposeAccessibility/ExceptionPath`, `ProposeDocumentLayout`, `ProposeReviewCopilot/AdaptiveRoute` — all route through `Propose` (model-driven) and cover §3.4 review copilot, adaptive routing, NL drafts, diffs, adversarial, accessibility/exception, document layout. Policy-scoped products are anchored to `policy_id` (not a fake verification).
- `internal/proposal/service.go:1` — `Service` (manual DI, `clock.Clock`, `Repository/CommandStore/ModeStore/RegistryStore/ProposalModel/EvidenceChecker/AuthorityChecker/RegionValidator/CostLimiter/AuditRecorder`), `CreateProposal`/`Propose` (mode fail-closed, guardrail), `GetProposal`, `ApproveProposal` (human approval, creates `AcceptedCommandRecord` per action), `ExecuteCommand` (replay via stored command, `MarkExecuted` idempotent), `Reject/Cancel/Expire/Supersede`.
- `internal/proposal/postgres/store.go:1` + `mode_registry.go:1` + `guardrail.go:1` + `db/migrations/000051_proposal.up.sql:1`/`000052_prompt_sensitive.up.sql:1`/`000053_proposal_policy_anchor.up.sql:1` — durable `proposals` (verification- or policy-anchored), `accepted_commands`, `proposal_mode_configs`, `prompt_registry` (with `sensitive`), `generative_model_registry`, `impact_assessments` with `ENABLE/FORCE RLS`; postgres `ModeStore`, `RegistryStore`, `AuthorityChecker`, `RegionValidator`, `EvidenceChecker` adapters.
- `internal/proposal/memory.go:1`, `limiter.go:1`, `evidence.go:1` — in-memory `InMemoryProposalStore/CommandStore`, `InMemoryLimiter`, `InMemoryEvidenceChecker/AuthorityChecker/RegionValidator/AuditRecorder` for deterministic tests.
- `internal/transport/httpapi/proposal.go:1` + `internal/bootstrap/api/process.go:1` + `internal/transport/httpapi/apierror/error.go:1` — `ProposalRoutes` exposes proposal lifecycle, modes, prompts, model registration, workflow activation/history/retirement/rollback and bounded usage reporting with `proposals:read/write/approve/configure` + `prompts:read/write` in `internal/access/scope.go:1`, mapped to stable problem responses via `apierror`.
- `internal/access/scope.go:1` — added `proposals:read/write/approve/configure`, `prompts:read/write` to `TenantRegistry`.
- `sdk/typescript/src/proposals.ts:1` + `client.ts:1` — dependency-free `ProposalsClient` covers proposals, modes and exact pins, prompts, model records, activation lifecycle/history and usage reporting through `IdenqaClient.proposals`.
- `internal/bootstrap/idenqa/proposal.go:1` — `idenqa proposal` covers proposals, mode, prompt, model, activation and usage administration.

**Verification:** `contracts/proposal/v1` contract-compatibility + canonical round-trip, allow-list closed, duplicate-kind rejected; `internal/proposal` tests cover guardrail region/authority fail-closed, human-required approval + replay idempotent, expiry/supersession terminal conflict, prompt registry + impact assessment, and model-driven `Propose` through `ReferenceModel`. PostgreSQL integration tests (`TestProposalPersistenceIsolationAndReplay`, `TestProposalModeRegistryAndGuardrailPersistence`) prove RLS isolation, version CAS, mode/registry persistence, sensitive-prompt classification, and fail-closed guardrail checkers. `go vet`, `golangci-lint`, `go test -race`, `go test -short ./...`, TypeScript typecheck/tests, and `govulncheck` pass.

**Remaining production gates (not claimed by this code):** Select and approve exact live provider/model routes; record provider terms and data-handling provenance; reconcile local estimates and reported tokens with provider billing, including the unknown-spend policy for providers that do not report failed-call usage; run representative evaluation for production prompts/models; demonstrate production monitoring, budgets and SLOs; complete hardened OCI/kernel egress isolation; provide live-provider evidence for AI-assisted routing; and independently audit guardrail coverage. Accelerator scheduling applies only if a future self-hosted route requires it. The `disabled` default remains the version-one posture; M-6 remains **In review** until its applicable gates are evidenced.

### 3.1 Proposal contract — implemented

`ProposalModel`, `AgentProposal`, identifiers/lifecycle, bounded schemas, evidence/signal refs, pinning, expiry/cancellation/supersession/rejection, and `AcceptedCommand` are implemented as above.

### 3.2 Deterministic guardrails — implemented

Allow-lists, tenant-policy, jurisdiction/residency (via `RegionValidator`), processing-authority (via `AuthorityChecker`), cost/rate, raw-evidence/sensitive-context, human approval, deterministic execution, replay, and audit linkage are implemented. AI never independently verifies/rejects, grants evidence, etc., enforced by guardrails. Postgres adapters back the authority, region, and evidence-reference checks.

### 3.3 Automation modes — implemented

`disabled|assist|recommend|guardrailed_auto|human_required`, tenant/workflow versioned config, session pinning, and fail-closed are implemented as above.

### 3.4 AI products and operations — implemented (bounded proposal kinds)

Review copilot, adaptive-route, NL policy drafts, diffs, adversarial scenarios, accessibility/exception, document layout, prompt lifecycle (with sensitive classification persisted to `prompt_registry.sensitive`), generative-model registry, impact assessments, and guardrail cost/rate/audit controls are implemented as bounded proposal kinds routed through `Propose` (model-driven). Policy-scoped products are anchored to a real `policy_id`. The deterministic `ReferenceModel`, provider-neutral generator router, OpenAI-compatible adapter and Anthropic adapter exercise the `ProposalModel` boundary without granting model output authority. Durable all-attempt usage receipts and bounded aggregate reports now provide operational outcome/token/cost visibility without storing provider content. Representative live-model evaluation, provider-invoice reconciliation, production monitoring evidence and full product UX remain open.

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

**Composition increment — 21 September 2026:** [Same-profile provider/model composition](composed-verification-runtime-v0.1.md) joins document quality, PAD and face matching with atomic preparation, exact model dispatch, isolated gateway credentials and existing policy/webhook completion. Explicit check identities, priority, dependency edges, operational-failure fallback and correlation groups are validated, persisted and enforced at dispatch. Public route administration, registry-driven hot provisioning and production model/provider acceptance remain open.

**First-party expansion — 22 September 2026:** Idenqa-owned biometric models are the selected primary direction; provider adapters are optional for tenants that cannot self-host or require other capabilities. Exact evaluation selection and the detector-only `selfie_analysis` route are implemented. It consumes one selfie or every frame of a temporal sequence and emits separate inconclusive face-count, framing, pose, image-quality and temporal-integrity signals without exporting geometry. This closes the repository routing/execution gap for those diagnostics, not their production quality acceptance. No glasses, face-covering, watchlist/public-figure, age, account-comparison or semantic pose-repeat classifier is claimed without an accepted model and lawful representative data.

### 4.1 Missing runtime capabilities

- Hardened OCI deployment and kernel memory/CPU/PID/egress enforcement; the isolated process, reviewed runtime lock and bounded CPU reference exist.
- Production acceptance of the implemented pinned YuNet/contextual crop/resize path; real capture quality and provenance validation remain open. [Offline evaluation tooling and dataset intake](pad-evaluation-v0.1.md) now include local manifest import, bounded batch execution and baseline/candidate aggregate comparisons; there is no accepted representative local dataset.
- Supported-hardware and accelerator acceptance beyond the tested macOS arm64 CPU runtime.
- Model-specific reproducibility and quality evidence beyond the synthetic graph tolerance and candidate compatibility smoke test.
- Production face-comparison model and pair-dataset acceptance. The [evaluation-only matching integration](face-matching-runtime-v0.1.md) supplies two-grant orchestration, whole-image portrait selection, pinned five-landmark alignment and native embedding/cosine execution; trained weights, representative acceptance and matching thresholds remain open.
- Accepted production PAD model and, separately, active-liveness challenge/capture assurance.
- Accepted production selfie-analysis classifiers and thresholds. Current version-pinned face-count/framing/pose/image-quality/temporal diagnostics are evaluation-only; occlusion/accessory, semantic challenge-response, age and watchlist/account checks remain absent unless separately justified and implemented.
- Document classification, OCR, quality, and manipulation models.
- Production-approved runtime selection from the implemented immutable evaluation registry; live registry-driven hot selection and runner provisioning remain absent. File-mounted immutable routes now support runtime dependency/fallback admission.
- Resource and accelerator scheduling.
- Warm capacity and broader health supervision; per-dispatch bounded readiness checks now fence unavailable runners.
- Production acceptance of authorised equivalent-model fallback sets; repository execution fails over only through an explicitly pinned equivalent route and otherwise fails closed.

### 4.2 Registry and governance — evaluation-only implementation complete; production acceptance open

[Evaluation registry v0.1](model-registry-v0.1.md) implements tenant model records, owner/licence/training/intended-use declarations, immutable manifest/configuration pins, declared regions and hardware class, independently versioned thresholds, evaluation-report digest references and audited activation/retirement/rollback history. The selected authority is one key with `models:activate`, expected-version checks and audit. Optional registry selections are verified inside attempt preparation. Production mode is rejected.

Remaining: verification of rights/provenance and representative evaluation reports; production approval and calibrated thresholds; and live dynamic routing/provisioning. Public evaluation-registry contracts and generated types are implemented as recorded in section 18; they are not a remaining registry gap. Declared metadata and synthetic tests do not establish production acceptance.

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

The worker supports the PR-01 Dojah and PR-02 Smile ID routes, a bounded multi-model graph and the separate opt-in synthetic fixture. Provider configuration is optional for first-party biometric execution. Public dynamic multi-provider/model administration, registry-driven hot provisioning and production acceptance remain open; mounted immutable routes already support exact model capability selection plus dependency and fallback admission.

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

**Classification:** Implemented selected scope — every lifecycle mechanism is implemented and evidenced; linked live-account, third-party-issuer and human-acceptance gates remain in sections 9, 16, and 27

L-01 through L-03 are complete for their recorded boundaries. They provide the complete state vocabulary, optimistic transition primitive, synthetic capture-to-processing-to-decision-to-signed-webhook journey, tenant and subject cancellation, automatic expiry, authority/session fencing, exact replay, and offline-deadline recovery. The create API deliberately returns an atomically activated `collecting` session; a separately persisted `created -> collecting` phase is not selected and is not counted as missing unless a future requirement introduces it. The asynchronous provider route now projects the parent session into `awaiting_external` only while a durable external dispatch is pending and no local check remains runnable, and returns it to `processing` inside the fenced task transaction that accepts the authoritative result; execution-owned authority validation alone tolerates `awaiting_external`, while policy authorship, review routing and new capture effects remain strictly `processing`. Policy `request_input` routing now records a bounded structured input request and tenant reads project `requested_input` while the session is `awaiting_input`; a composed non-review proof drives request, action-required outcome, fresh-authorisation resume and completion. Authenticated callback intake resolves an opaque attempt reference through the isolated runner for signature verification and a replay-identity receipt, and operational failure projects a bounded class/code through the lifecycle record.

O-03 has composed automatic nonterminal routing to manual review, certified operator authority, findings and dual control, policy re-evaluation, linked recapture, progress-preserving credential replacement, child-outcome acknowledgement, explicit parent re-evaluation, correction successors, appeal lifecycle, queue/settings operations, controlled evidence display, public OpenAPI, and TypeScript SDK integration. Behavioural PostgreSQL proofs now cover escalation arbitration (including independence, certificate and non-escalated denials), correction intake with policy-authored successor lineage, immutable original decisions and the `decision.corrected` catalogue event, appeal assignment/resolution/withdrawal/expiry with successor receipts, review-evaluation and callback-runner replacement-worker consistency, external-success/local-failure reconciliation, and real-HTTP composed correction and appeal journeys. External certification is now an owned fail-closed Ed25519 assertion adapter over the existing authority configuration, keeping the tenant-attested path unchanged when no issuer is configured.

Remaining lifecycle-linked gates are external only:

- Live provider-account callback operation and official sandbox evidence; the transport, runner verification, receipt deduplication and Core ingress are implemented and proven with local fixtures.
- A real third-party certification issuer trust/status integration (remote key discovery, revocation checks); the deployment-key adapter and fail-closed boundary are implemented.
- Composed reviewer/subject visual and interaction acceptance (O-03/M-4), which is human evidence rather than an unbuilt mechanism.
- Production runner selection, provider/model acceptance, and clean deployment, which remain separate from the lifecycle state machine.

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
- General purpose-bound subject-input resolution beyond the Smile ID deployment-bound country/document-type references.
- Dojah adapter 0.1.2, scoped runner and worker preparation support required front/back sides from the immutable session profile and durable selected branch, with pre-redemption ambiguity checks, one-use grants and atomic rollback. Focused adapter and restricted-role PostgreSQL 16.8 race tests pass, including the full two-sided public capture-to-decision-to-webhook journey with distinct image bytes, worker replacement, replay rejection and raw-field non-persistence. Official-account evidence remains open; front-only selections remain front-only.
- Broader multi-operation grants and durable delivery recovery beyond the first document route.
- Broader provider reconciliation beyond the implemented status-only polling and verified callback-receipt paths.
- Provider-side deletion orchestration.

### 9.2 Missing resilience and cost controls

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
- Runner supervision and termination on policy breach.

The composed Smile ID async route now validates partner/job-bound upload paths and uses reviewed-origin, DNS-pinned HTTPS with redirect rejection. Runner-side `secret://` material and TLS identities dynamically reload with last-good fallback and overlap; API/worker outbound runner client credentials remain the separate section 21 gap. Broader standalone adapter/deployment isolation and the operating-system controls above still require acceptance evidence.

---

## 11. Biometric subsystem

**Classification:** Current-core gap for the D-014 boundary

The Swift, Kotlin, and Web acquisition coordinators implement bounded live-camera acquisition, ordered prompts, deadline handling, and local quality checks. The ONNX evaluation path adds pinned face detection and contextual preparation; ML-02 adds evaluation-only portrait preparation, embedding execution, and one-to-one cosine comparison; ML-03 composes document, PAD, and face-comparison checks. These engineering paths explicitly remain inconclusive and do not establish production PAD, face-match, document-authenticity, MRZ, or barcode assurance.

Missing capabilities include:

- Production-accepted presentation-attack detection.
- Deepfake, replay, screenshot, and virtual-camera injection signals where technically possible.
- Representative-data acceptance of the implemented model-specific face alignment and document-portrait selection.
- Licensed production embedding weights and calibrated one-to-one thresholds.
- Production validation of the implemented separation between liveness and similarity signals.
- Production calibration of quality bounds; the model contract and normalization path now require unacceptable quality to remain inconclusive.
- False-match and false-non-match evaluation.
- Failure-to-acquire and failure-to-enrol metrics.
- APCER and BPCER evaluation.
- Demographic, device, and camera-class evaluation.
- Independent production retention approval for captures and reference portraits. Model-contract v1 forbids persisted embeddings, extracted portraits, tensors and model-derived caches; those values remain transient in the current runtime.
- Operational proof that process/model-cache teardown clears every runtime copy; no embedding or extracted-portrait persistence exists in Core.

One-to-many identification remains outside version-one scope.

---

## 12. Document subsystem and country coverage

**Classification:** Implemented selected scope — Core document understanding, capture, provider extraction and public support-level inspection are implemented; template/manipulation inspection and representative acceptance of the evaluation-only portrait-selection path remain provider, model and pack gates

Capture Web plans and independently completes required document front/back artefacts, supports upload or live-camera acquisition, and provides review/retake. Document capture now includes automatic quad detection with an N-frame stability and sharpness gate, a manual shutter override, and perspective correction/cropping before the existing upload boundary, with dependency-free fallback to the uncorrected frame when detection fails. Core owns a dependency-free document-knowledge layer: TD1/TD2/TD3 MRZ parsing with per-field and composite checksums and century inference, decoded barcode payload parsing (image decoding stays provider-supplied), bounded field merging across provider, MRZ and barcode sources, cross-field and front/back consistency with expiry and age calculation, and provisional type/country classification. Provider extraction is mapped from primary documentation — Dojah `text_data`/`document_type` and Smile ID document-verification `id_fields` on the documented `clear` callback — into a bounded transient `DocumentObservation`; Core consumes it into derived `document_*` signals and drops the raw data before fingerprinting or persistence, with integration proofs that no MRZ, barcode or field value reaches result bodies, receipts, observations, outbox or realtime paths. Dojah/Smile status and action signals continue to carry authenticity and liveness meaning.

Remaining capabilities are gated, not unbuilt machinery:

Dojah-first follow-up, 22 September 2026: conflicting valid duplicate extracted
fields now suppress the transient observation instead of choosing the first value.
Mapped-field permutations and raw-field consumption tests cover this boundary.
Full OCR/official-account acceptance and structured identity ingestion remain open;
see the [delivery plan](identity-provider-document-delivery-plan-v0.1.md).

- Template, security-feature and manipulation/screenshot inspection require provider or production model evidence beyond the documented status and action signals.
- Portrait extraction requires the section 4 production face model and alignment acceptance.
- Complete OCR and field coverage beyond the documented provider fields awaits official-account response acceptance.
- The public document support-level response resolves from the active section 13 pack; national-ID/driver-licence levels await public structural sources.

NFC and digital-credential trust-chain validation are explicitly deferred for the initial pack.

---

## 13. Document, jurisdiction, and assurance packs

**Classification:** Implemented selected scope — the immutable pack registry and its public inspection surface are implemented; per-country provider, legal and evaluation acceptance remains external evidence

The repository now owns an immutable, versioned pack registry (`internal/pack`) with embedded canonical NG/GH/KE/ZA records, full ISO 3166-1 pairs, expected-version activation/deprecation/retirement with append-only history (migration 65), and a public read surface under `packs:read`: `GET /v1/packs`, `GET /v1/packs/{country}`, `GET /v1/packs/{country}/revisions/{revision}`, and `GET /v1/document-support?country=&type=` plus `idenqa pack list|country|document|support`.

Pack documents carry: document type, known versions, required sides mapped to existing evidence artefacts, supported fields drawn from Core's canonical field vocabulary, declared security checks, barcode/MRZ/NFC support declarations, model and parser requirement references, evaluation coverage with limitations, the `fully_supported|structurally_supported|provider_only|best_effort|unsupported` classification, requirement slots for authority/controller/processor/recipient/region/transfer (tenant-policy keys, never legal assertions), assurance mappings to existing built-in profiles or `not_mapped`, and legal-review state (`not_reviewed` by default, `reviewed` requires an opaque reference).

Only substantiated structure is claimed: passports are `structurally_supported` on ICAO 9303 TD3 MRZ (fields exactly match the implemented parser, required front side, parser reference and evidence codes), while national IDs and driver licences are `unsupported` with `public_structural_source_not_cited` rather than guessed. Security-feature inspection, production-accepted portrait extraction, authenticity, NFC, barcode decoding and assurance mappings are declared unavailable or unmapped with reasons. The evaluation-only face route's transient portrait selection does not change pack support. The §12 support-level response resolves from the active pack and marks unsupported documents provisional/inconclusive without changing assurance semantics.

Remaining work is external confirmation, not missing registry machinery:

- Per-country provider/account confirmation and national-ID/driver-licence structural sources.
- Legal/regional review that moves `legal_review` from `not_reviewed` and substantiates the requirement slots.
- Evaluation and assurance mappings once representative document data and provider evidence exist.

Every claimed country path still requires provider/account confirmation and legal/regional evidence.

---

## 14. Portable capture experience

**Classification:** Implemented selected scope — the portable experience contract, signed manifests, lifecycle, targeting, session pins and signed safe-default fallback are implemented; managed editing, custom-domain verification and native rendering remain gated

Capture Web now provides the selected mobile-first guided Lit experience with one active task per screen, fixed built-in subject copy, exact notices, optional policy-bounded method choices, camera and upload paths, preview and retake or replacement, confirmation between multi-item steps, authority responses, realtime observation, recovery, processing, and authoritative outcome screens. The final accepted capture goes directly to processing instead of requiring a local finish action, and liveness follows the shorter preparation-to-guided-capture-to-processing branch. D-027's separately authenticated `GET /v1/capture/outcome` projection maps current workflow state and the immutable completed policy decision to a closed subject-safe vocabulary without exposing decision identifiers, policy reasons, assurance, provider/model details, evidence metadata, or subject data. The `idq_out_v1` bearer has its own `otk_` durable record, signing keyring, bounded post-session lifetime and revocation; it grants no capture authority and Capture Web keeps it in a dedicated public `OutcomeClient`. Its safe-default visual system covers responsive phone, tablet, and desktop layouts, light and dark modes, forced colours, visible keyboard focus, large text, reduced motion, right-to-left direction, and safe areas. A documented appearance-only `--idq-capture-*` custom-property surface supports host-local branding across the open Shadow DOM without changing notices, behaviour, evidence semantics, or assurance. Hosted and embedded pages use a server-side public-SDK bootstrap and complete real synthetic capture journeys against self-hosted Core while keeping the tenant API key outside the browser. Hosted upload, embedded upload, and active liveness progress through a real synthetic worker and immutable policy decision to the subject-safe verified screen. The same serial real-Core suite reaches action required through `request_input`, not verified and inconclusive through immutable completed decisions, subject cancellation through the public capture operation, operational failure through `fail_workflow`, and authoritative expiry through the worker before the still-live outcome credential reads the projection. The worker routing and expiry boundaries persist each exact lifecycle transition, audit event, outbox record and task effect atomically. D-026 is therefore proven across all nine selected outcome journeys without accepting an expired capture bearer or inferring expiry in the browser. The portable experience-configuration system is now implemented: `contracts/experience/v1` defines the canonical schema with a Go/TS validator, signed Ed25519 manifests over a Core-managed keyring, draft/approved/published/superseded/revoked lifecycle with audited expected-version transitions and rollback, structured locale copy with non-overridable mandatory regulatory/consent/safety/accessibility keys, digest-verified vetted assets, validated HTTPS links and allowed origins, deterministic most-specific-wins targeting with fail-closed conflicts, versioned processor-free export/import, and a signed accessible safe-default fallback used on revocation, kill-switch or any resolution failure. Sessions pin experience, locale, tenant-copy and mandatory-copy versions at creation, preserve them across resume, and expose them through the capture experience and progress reads. See [portable experience v0.1](portable-experience-v0.1.md). Explicit advanced-interaction acceptance remains open.

The implemented safe default keeps capture completion, verification processing, verification completion, and tenant action distinct. Its reproducible conformance harness migrates an isolated Core and Headgate database, provisions a synthetic tenant and scoped credential, creates the journey through public contracts, and proves hosted upload, embedded upload, and active-liveness capture pages. After the required notice response, the first policy-ordered method is recommended directly; active liveness uses one preparation page and one start action before running all ordered prompts automatically, while **Use Another Method** retains approved alternatives. The active-liveness coordinator validates the public plan, presents ordered challenges under deadlines, requires host-supplied measurements for requested quality gates, and owns cancellation and camera cleanup. The generic programmatic adapter boundary accepts arbitrary namespaced acquisition methods while missing or ambiguous adapters fail closed. Adapter return, local prompt completion, and capability advertisement prove no assurance and cannot self-complete a step: the component refreshes authoritative Core progress and requires the exact requirement, evidence type, artefact, method, and fallback binding. Migration 76 and the public upload contract now persist every Web challenge frame with ordered challenge/time metadata and a content-digest chain; capture completion waits for the declared sequence, and temporal-capable model requests bind every frame grant. This implements temporal evidence transport and provenance but does not establish active-liveness or PAD assurance.


Missing capabilities are now gated or downstream work:

- Managed experience editor/publishing Console UI and DNS-verified custom domains; allowed origins and validated HTTPS links are implemented.
- Tenant asset-upload authoring endpoint; vetted references with verified digests and MIME/size bounds are implemented.
- Native SDK rendering of copy/locale beyond the resolved bootstrap document. Web palette contrast diagnostics are implemented; Core publication-time contrast enforcement and whole-page accessibility certification are outside that diagnostic's scope.

The existing optional `rendered_experience_version` response field still records a caller-supplied version string; the authoritative contract is now `contracts/experience/v1` with signed manifests and `docs/portable-experience-v0.1.md` as its guide.

E-04 and M-2 remain **In review**. The basic journey, responsive goldens, accessibility automation, streamlined one-start liveness flow, arbitrary-adapter fail-closed behaviour, three verified live Core-backed demonstrations, and real-Core action-required, not-verified, inconclusive, cancelled, failed, and expired demonstrations are implementation evidence. D-026's advanced-interaction acceptance remains the milestone gate; the portable experience contract itself is now implemented with session pinning and signed safe-default fallback.

### 14.1 Capture Web completion increment — 22 September 2026

The document path now uses a dedicated dark camera/review surface, side labels,
expandable help, manual or automatic capture, and explicit use/retake actions.
Profile-pinned, requirement-scoped document choices drive the instructions.
Core persists the selected branch separately from the immutable profile snapshot
and controls its required artefacts and acquisition bindings. The component
recovers both selection and accepted evidence from Core after reload, without a
host-supplied catalogue or selected ID. Selection is not an assurance claim. The
Web planner presents the front before the back even when Core returns the
canonical artefact set in lexical order.

Hosted and embedded document fixtures now create a real front/back session
through the server-side public SDK bootstrap. Their browser proof covers driver
license and national identity card, retake, a recreated component recovering the
selection and accepted front from Core, capture of the back, and an authoritative
synthetic verification outcome. A passport branch completes after its front
only, without changing snapshot bytes or waiving another branch's required back.
The conformance run also exposed a nil route-dependency slice being written as
SQL NULL; the persistence boundary now writes the required empty PostgreSQL array
for routes without predecessors.

`auditCaptureThemeContrast` checks resolved opaque-sRGB theme combinations using
unrounded WCAG 2.x ratios, including secondary text, button hover and focus rings.
Unsupported colours produce diagnostics. Browser checks cover actual light/dark
defaults, a failing host palette, document-review action contrast, and portable
theme precedence/reset. This is Web authoring/conformance tooling, not a claim
of whole-page accessibility or Core publication enforcement.

**Selected source — 22 September 2026:** the user selected the capture profile
and session, rather than signed experience, as authority for document types and
required sides. Package section 14.2 records this decision. Core must persist
the chosen pinned branch separately from immutable requirements, reject uploads
before selection and outside that branch, prevent switching once an upload
intent exists, and return the selection on recovery. The earlier host-only
`documentChoices` input is superseded. Migration 77, the capture-authenticated
`POST /v1/capture/document-selection` command, generated contracts, TypeScript
SDK and Capture Web implement this selection. The same effective-artefact
resolver gates upload issuance, transactional upload creation/acceptance,
completion counting and realtime commands. Original profiles without options
retain their canonical digests and fixed-artefact behaviour.

**Verification:** 110 Capture Web unit tests, 37 mocked browser tests and all 13
live self-hosted Core browser tests pass. The live suite includes all three
document types, hosted/embedded upload, active liveness, linked recapture and
every selected outcome. TypeScript SDK tests pass (78 tests; its separate
standalone SDK conformance test is environment-gated). Go unit/race and isolated
PostgreSQL integration tests cover branch enforcement, retry/version conflicts,
upload-history locking, credential revocation, recovery and snapshot stability.
Final migration-77 guards additionally enforce append-only selection audit and
reject selection writes after capture completion or outside collecting. Real
concurrent selection/upload transactions pass in both lock orderings, verified
through PostgreSQL blocking observations. All three document browser journeys
were rerun successfully against a fresh database after these final guards.
SDK/Web typechecks, builds, package checks, generation checks, Markdown structure
and links, and whitespace checks pass. Repository-wide lint is not a clean gate:
unrelated Go lint findings and OpenAPI warnings remain in the shared checkout.

**Measured-liveness correction — 22 September 2026:** the earlier loop advanced
after a delay and image-quality checks without checking the requested pose.
Its successful synthetic Core outcome was transport evidence only. The replacement
implements local landmark inference, neutral calibration, pose/blink validation,
contiguous holds, recovery and a measurement-driven ring. Real-camera accuracy,
subject-relative left/right behaviour, supported-device performance and explicit
interaction acceptance remain open. The initial thresholds are engineering
defaults, not accepted biometric calibration. See [implementation and setup](../capture/web/README.md#measured-liveness).

**Acceptance still required:** real-camera liveness validation and explicit visual and interaction acceptance of
document capture/review and advanced liveness. The request to finish the work is
not itself that acceptance. E-04 and M-2 remain **In review** until the user
accepts the demonstrated interaction. The document-choice authority gap is
implemented; automated conformance does not supply visual acceptance.
Managed Console editing, asset authoring,
DNS-verified custom domains and native rendering remain their separate gates.

---

## 15. Capture SDK lifecycle and platform assurance

**Classification:** Implemented selected scope for the lifecycle and hardening contract — Swift and Kotlin are at parity with Capture Web for configure/start/resume/cancel/clear/getCapabilities, secure temp files and screen protection; native UI journeys, integrity signals, device evidence and platform adapters remain gated

Swift and Kotlin provide native bootstrap, secure token storage, proof-key binding, direct upload, realtime observation, one-shot camera capture, local quality assessment, and acquisition coordination. Both SDKs now implement the complete bounded `configure` contract, session-oriented `start` from the native bootstrap credential with Capture-Web-parity plan semantics, durable Keychain/Keystore-backed `resume` after process death with idempotent re-attachment, idempotent server-side `cancel` with expected-version and reused idempotency keys, complete verifiable `clearLocalData`, honest assurance-free `getCapabilities` with fail-closed unknown methods, bounded app-private temporary-file lifecycle with deterministic cleanup, and iOS/Android sensitive-screen protection adapters. Accessibility/interruption/permission/network/backgrounding guidance with an always-reachable cancel is tested on both platforms. Evidence: 27 Swift tests, 27 Kotlin tests, iOS device-target compile, and `native-sdk.yml` unchanged.

Remaining capabilities are gated, not missing contract work:

**23 September 2026 native parity increment:** SwiftUI and Android View-based screens now compose Core notice/response and durable document-selection APIs with an SDK camera, side-specific guidance, Help, review/retake, recovery, cancellation and processing states. Capture requires a session-bound acquisition plan, enforces quality before review and upload, and executes measured active liveness: shared neutral calibration, directional target/tolerance/hold gates, two-sample blink sequencing, stale/invalid/lost-face rejection, segmented-ring progress and ordered digest-chained sequence submission. Apple Vision is the iOS tracker; MediaPipe Tasks Vision 1.0.0 with a digest-pinned packaged model is the Android tracker. Evidence: 36 Swift host tests, 38 iOS Simulator tests, 36 Kotlin unit tests, Android debug assembly and lint, and one on-device Android instrumented smoke test proving real JNI/model loading, zero faces on a blank frame and non-completion of the gate. Instrumented and simulator tests do not establish landmark accuracy, camera behaviour, PAD or visual acceptance: real-Core journeys, per-device calibration, host wiring, native host applications and touch-driven UI automation remain open. See [native implementation and device evidence](native-capture-evidence-v0.1.md).

- Complete and accept the now-implemented notice/consent, document front/back and measured-liveness surfaces; real-Core native journeys, per-device tracker calibration, native temporal/video persistence and PAD/face-match integration remain open. PAD and face matching still require the section 4 production models.
- Background transfer and recovery where the platforms allow, MRZ/barcode surfacing from the implemented Core parsers, and full orientation/accessibility UI conformance (no native touch-driven UI-test harness yet; tracker behaviour is emulator and synthetic evidence only, so camera, calibration and device evidence is still required).
- Root, jailbreak, emulator, instrumentation and integrity signals plus the supported-device compatibility matrix need a decision on acceptable signals and device evidence.
- Platform-specific NFC, voice, video and provider adapters conforming to the Web boundary; NFC remains deferred.

Flutter and React Native are not current gaps until advertised. Once advertised, they must remain thin native wrappers and must not transfer raw evidence through Dart or JavaScript bridges.

---

## 16. Manual review, correction, and appeals

**Classification:** Implemented selected review scope; external integration, product acceptance and later operational enhancements remain

The Core now includes versioned operator administration, queue operations, controlled evidence display, independent arbitration, policy-authored correction successors, appeal lifecycle and public SDK/recapture integration. A live public-SDK/Core/Capture-Web fixture composes one reviewer-requested recapture through explicit parent completion. See the current implementation and runtime permissions in [manual review and linked recapture](manual-review-recapture-v0.1.md#6-review-operations-and-public-integration).

Remaining work includes:

- Composed reviewer/subject visual and interaction acceptance of the live recapture and correction/appeal journeys; the real-HTTP composed journeys and replacement-worker recovery proofs are implemented.
- A real third-party certification-issuer trust/status integration (remote key discovery and revocation checks); the fail-closed Ed25519 deployment-key adapter over the existing authority configuration is implemented, and the tenant-attested path is preserved when no issuer is configured.
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

**Composed recapture proof — 15 September 2026; current status reconciled 20 September 2026:** A tenant fixture using the public TypeScript SDK creates the original journey and review configuration while keeping its API key server-side. Live Chromium automation completes original capture, automatic manual-review routing, certified claim, protected evidence grant redemption, `request_input`, linked-child credential handoff, fresh child authorization and evidence, child completion, acknowledgement and explicit parent re-evaluation to a verified terminal decision. Immutable recapture lineage contributes a context-only `review.recapture.requested` fact to the child's pinned snapshot; it establishes no assurance. Subsequent real-HTTP and replacement-worker proofs compose progress-preserving recovery, correction, appeal and escalation, and a fail-closed external-certification adapter is implemented. O-03 remains **In review** only for a real third-party issuer trust/status integration and reviewer/subject visual and interaction acceptance.

---

## 17. Webhooks and general event contracts

**Classification:** Implemented selected scope — catalogue, subscriptions, resumable fanout, developer event list/stream, retention and the fixture-backed clean-deployment proof are complete

The repository includes durable webhook persistence, KMS-wrapped rotating secrets, retry exhaustion, replay lineage, SSRF-aware callback transport, and an independent Go verifier. L-02 now composes completion projection and signed delivery into the runnable worker. Its public API journey proves atomic decision/session/delivery effects, a durable 503 retry, worker restart, successful delivery, stable event identity, and independent signature verification. H-01 now exposes public endpoint administration, payload-free delivery/attempt inspection and exhausted-delivery replay through API, TypeScript and CLI. The 19 September 2026 developer event feed adds an authorised read-only list and SSE stream over the same durable events, plus `idenqa webhook listen` local signed forwarding, without changing endpoint delivery or payload-free delivery/attempt inspection.

Resolved on 18 September 2026: endpoint event-type subscriptions (exact names or `*`, bounded to 64, default `verification.completed`); the versioned public event catalogue with per-type JSON schemas and canonical fixtures in `contracts/webhook/v1`; outbox-first emission from every owning feature for the selected catalogue (verification lifecycle, checks, decisions, evidence, consent, processing authority, subjects, deletion, review cases and appeals); and a fenced, resumable `webhook.fanout` task that pages subscribed endpoints in bounded batches, dedupes per endpoint and event, and continues from a persisted cursor — replacing the fail-closed 1024-endpoint guard.

**Developer event stream — implemented 19 September 2026:** Migration 58 adds the monotonic `stream_sequence` identity and unique `(tenant_id,stream_sequence)` index, and emission publishes a routing-only `idenqa_webhook_events_v1` notification that the API process multiplexes over one LISTEN connection. Catalogue bodies remain KMS-wrapped at emission and are unwrapped only at authorised boundaries. `GET /v1/webhook-events` pages decrypted canonical envelopes under `webhooks:read` with a signed opaque cursor; `GET /v1/webhook-events/stream` serves Server-Sent Events with `Last-Event-ID` sequence resume, heartbeat comments, an optional validated `event_types` filter, a 64-sequence commit-gap window, bounded duplicate suppression, and a durable polling fallback when the notification listener is unavailable. `idenqa webhook listen` consumes the stream, prints summaries or `--json` canonical envelopes, reconnects with bounded backoff and resume, and with `--forward-to` forwards exact bytes signed locally with the canonical v1 headers, using exactly one of `--secret-file` or a new owner-only `--secret-out`.

Completed 19 September 2026: exact endpoint schema-version `1.0` pinning; a 257-endpoint multi-batch replacement-worker crash/resume exercise; and migration 60's elected, bounded, legal-hold-aware retention maintenance. Encrypted event/delivery payloads and attempt diagnostics expire after seven days, expired events disappear from list/SSE, replay returns `410 Gone`, minimal reference tombstones remain 365 days, released holds resume expiry, and final purge is bounded.

**Local listener refinements — implemented 19 September 2026:** `idenqa webhook listen` adds validated repeatable `--forward-header` entries (reserved signature, content and transport headers rejected), `--load-from-webhooks-api` subscription discovery (pages `GET /v1/webhook-endpoints`, skips disabled endpoints, unions event types and collapses `*`), `--thin` local reference-only projection (canonical envelope metadata preserved; `data` reduced to `id`, `type` and `_id` reference keys; projected bytes signed with the canonical v1 headers), and `--backfill` (`Last-Event-ID: 0` replays retained durable history before live continuation, with reconnect and receiver deduplication unchanged).

Remaining limitations:
- The packaged decision-to-delivery proof uses a documented development-only internal-network exception; production webhook networking remains external-beta evidence under section 27.
- `verification.collecting` is emitted on transitions into `collecting` (the resume path); creation inserts the activated `collecting` session without a distinct transition. `provider.degraded` is now emitted once per transition into `degraded`/`not_ready` from the provider health and circuit-breaker layer (migration 68).

V-04 completed the application, persistence, task, transport, verifier, and deterministic proof boundary. L-02 adds the runnable completion and delivery composition. H-01 adds separately permissioned, atomically audited administration and replay, display-once secret delivery, overlap-safe rotation and signed tenant-bound inspection cursors. Receivers continue deduplicating the unchanged signed event ID across manual replay.

---

## 18. Public API and authentication

**Classification:** Implemented selected API/generated-contract and 30-operation public SDK scope; broader parity, independent-review/production acceptance and two contract decisions remain

The public OpenAPI now includes tenant inspection; capture profiles; verification creation/read/cancellation; decisions and bundles; notices and processing authority; capture session/progress/bootstrap/connections; evidence upload; webhook and policy administration; webhook-event listing and streaming; review cases, queue/settings, operators, evidence display, recapture, correction and appeals; tenant-local fraud; persistent subjects and identity records; assurance capabilities/profiles; privacy deletion status/listing and retention resolutions under `deletions:read`; policy simulation, revision diff and scenario regression under `policies:read`; tenant provider registration and selection contracts under `providers:read`/`providers:write` (secret-free configuration references, health from owned dispatch evidence, failure-classification preview); evaluation-only model registry catalogue, validation and rollback contracts under `models:read`/`models:write`/`models:activate`; and AI proposals, modes, commands, and prompts. Tenant verification reads project the optional `current_decision` (decision leaf id, outcome, directive, decided_at) and `current_case` (case id, state, version) only when the caller holds `decisions:read`/`reviews:read`; capture-token reads remain subject-safe and exclude both. The dependency-free handwritten TypeScript facade exposes the capture/session walking slice and the review, fraud, identity, assurance, policy, webhook and proposal clients; generated TypeScript contract symbols remain internal.

Implemented 21 September 2026: decision history and verification-bound reconsideration now use immutable decision lineage and the existing independent correction workflow; reconsideration never rewrites a decision. Safe tenant evidence metadata and append-only lifecycle inspection exclude object identity, envelope material, digests and bytes. General evidence-access grant creation and revocation use atomic idempotency receipts, live authority re-evaluation and purpose/runner/use/expiry bounds; inspection excludes redemption details. Consent creation remains capture-token and exact-notice bound, while tenant inspection and withdrawal append a new refusal receipt without mutating the original consent. Privacy administration writes were already implemented and are no longer a parity gap. Proposal impact-assessment create/get/list is also represented in the public contract. OpenAPI, generated Go/TypeScript contracts, route composition and CLI commands are present. The 22 September [public SDK resource increment](public-sdk-resources-v0.1.md) adds handwritten TypeScript methods, dependency-free typed Go clients and focused request-contract tests for 30 operations across these resources and privacy administration. The linked guide records real-HTTP/PostgreSQL evidence separately from unit and package checks; broader endpoint parity and successful independent-review acceptance are not implied.

Two contract decisions remain rather than hidden implementation tasks:

- **TBD — model health:** choose whether the public resource is a live runner probe, durable supervised state, or an aggregate of registry/runtime evidence. The evaluation catalogue and provider health must not be mislabeled as model health.
- **TBD — OAuth client credentials:** decide issuer ownership, token format, audience/scope mapping, rotation, revocation and overlap before adding a second server credential format. Tenant API keys remain the selected initial mechanism.

---

## 19. CLI and open-source operational contract

**Classification:** Implemented selected CLI scope; model-health inspection remains blocked on its contract decision

The Cobra CLI provides tenant, API-key, migration, Headgate migration, evidence-key, policy-decision reproduction/verification, audit verification, recovery, background-work inspection/retry, public webhook administration/inspection/replay and live webhook event listing/streaming with local signed forwarding, public policy administration with simulation, revision diff and scenario regression, AI proposal/mode/prompt operations and accepted-command execution, review case/appeal/correction/arbitration/recapture administration, subject/identity administration, assurance profile and verification-assurance operations, tenant-fraud configuration/proposal/receipt operations, privacy deletion-status/retry/retention inspection, tenant-owned streaming data export with digest verification, tenant provider registration and selection with secret-free configuration references, evaluation-only model registry administration (register/validate/threshold/activate/rollback/retire), immutable jurisdiction/document pack inspection and support-level resolution, a complete public-API synthetic verification journey with packaged example profile/policy fixtures and demo script, and clean-install preflight and diagnostics through `idenqa doctor`.

Remaining boundaries include:

- Model health inspection, pending the public-resource semantics above; registration, validation, activation, rollback and retirement commands are implemented against the evaluation-only registry.
- Live production-key operational evidence remains under section 21; tenant HMAC custody, rotation, retirement, destruction and recovery commands are implemented.

Decision history/reconsideration, safe evidence metadata/lifecycle, evidence-access grant administration, consent inspection/withdrawal and proposal impact-assessment commands now have CLI coverage. Generative-model administration and accepted-command execution were already implemented.

---

## 20. Privacy, retention, and deletion

**Classification:** Implemented selected scope — the privacy-request workflow, subject export, restrictions, disclosures and processor inventory are implemented; provider-side deletion and production backup-store enforcement remain external infrastructure gates

Implemented foundations include typed retention resolution, legal holds, observable deletion state, evidence ciphertext removal, backup-expiry waiting, deletion work, tombstones, and restore-aware recovery foundations. Migration 66 and the [privacy request workflow](privacy-request-workflow-v0.1.md) now add the tenant-mediated request lifecycle (`access`, `portability`, `correction`, `restriction`, `objection`, `erasure`) with expected-version transitions, expiry, audited reason codes, subject-safe creation/status through the outcome credential, subject-scoped access/portability export with digest verification (extending the tenant export), identity correction successors and review correction routing, active restrictions that block new capture/processing/consent until lifted, purpose-scoped objections, erasure execution through the existing hold-aware deletion path, immutable disclosure records, a versioned processor/subprocessor inventory, public API and CLI surfaces, and request age/transition observability with alert rules.

Remaining capabilities are external or infrastructure gates:

- Provider-side deletion orchestration and the full live deletion demonstration after a real provider and model journey.
- Operational teardown proof for external model caches and OCR/derived indexes. Model-contract v1.1 forbids Core/model adapters from declaring persisted derived model data or non-zero derived retention, and Core holds no embeddings, crops or face coordinates today beyond decision-derived records that immutable decision semantics retain.
- Production backup-store expiry enforcement and complete restoration evidence.
- External-delivery retention semantics and deletion treatment for webhook copies held by receivers; Core-owned event/delivery/attempt payload expiry with legal holds is implemented.

Direct consent-receipt inspection and append-only withdrawal are implemented through the public API and CLI; capture-bound creation remains tied to the exact active notice and capture principal.

---

## 21. Production KMS, HSM, and secrets

**Classification:** Implemented selected scope — AWS KMS and AWS Secrets Manager are the selected first production providers and the custody mechanisms are implemented; live AWS acceptance, the identity lookup migration and API/worker outbound runner client-credential rotation remain

The repository provides an owned KMS boundary, local keyring, per-object streaming encryption, wrapped evidence keys, and evidence-key rewrapping. The user selected AWS KMS and AWS Secrets Manager as the first production KMS/HSM and secret-manager providers; `adapters/kms/aws` implements the owned KMS and secret-resolver ports with fail-closed provider selection (`KMS_PROVIDER=local|aws`, `SECRETS_PROVIDER=file|aws`), and Core never resolves or stores provider secret values. Tenant-scoped HMAC key custody (migration 69) provides domain-separated immutable versions with create/rotate/disable/retire, expected-version checks, audit and backward verification of previously issued identifiers. Runners prime and dynamically reload `secret://` references with last-good fallback, credential overlap and TLS rotation, and tenant provider credential references are version-pinned per dispatch with an audited rotation operation. Migration 70 adds a fenced, resumable fleet rewrap duty covering evidence content, webhook events/deliveries/secrets, custody HMAC keys, identity lookup/subject values and fraud correlation — rewrap, verify, then update, with per-batch audit and bounded metrics. Verified destruction requires reference-free repeatable-read snapshots across those classes and records immutable receipts before scheduling AWS KMS key deletion; dual-control recovery ceremonies support `rewrap` and `migrate_epoch`. Delegated, time-bounded support access and approval-gated break-glass access with expiry and append-only use ledgers are implemented with API/CLI surfaces and regenerated contracts.

Remaining:

- Live AWS KMS/Secrets Manager acceptance evidence: real key-deletion scheduling, rotated-material rewraps and IAM failure modes.
- Migrating the identity keyed-identifier lookup onto the custody catalog end-to-end, including dual legacy/custody verification. Its migration number must be assigned when implemented; migration 71 already belongs to provider dispatch controls.
- Resolving API/worker outbound runner client credentials through the secret provider with redial/overlap.
- Production recovery-ceremony rehearsals and the operational rotation cadence, where content-algorithm migration rollout remains under decision 16.

The provider choice is already Selected in the authoritative repository/package draft. This audit records only the remaining live-acceptance and client-side integration gates; it does not reopen that decision.

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

**Classification:** Implemented selected scope — the bounded domain metric layer, dashboards and alerts are implemented; a few families remain gated on data that other sections must produce

Vendor-neutral OpenTelemetry providers and selectable OTLP export over gRPC or HTTP/protobuf are implemented. Export is disabled by default and the selected TLS, mTLS, header, sampling, batching, retry, and metric-reader constraints are represented. The repository now emits twenty bounded domain instruments through injected receivers at their owning application boundaries: verification transitions, starts, completions, operational failures, workflow duration and time to decision; capture step outcomes and recapture rate; review resolution outcomes and duration; webhook delivery attempts and latency; provider dispatch outcomes and callback adoption delay; model dispatch outcomes/duration and a readiness gauge; privacy deletion transitions, a bounded backlog sample and backup-expiry age. Every label is a named vocabulary with allow-list collapsing, and tests prove tenant/subject/evidence identifiers or raw values can never become labels. Versioned Prometheus alert rules and a Grafana dashboard whose queries match the emitted names ship in `deploy/observability/`.

Remaining families are gated on data other sections must produce:

- Capture drop-off by step needs a subject-journey start event; completion and recapture counters are the available proxies.
- Model score, threshold, cohort and drift metrics need the section 4 evaluation dataset and production calibration.
- Provider cost attribution and budgets depend on sections 9.2/9.3; live health routing, the bounded health gauge and degraded-mode visibility are implemented.
- Regional pending-work capacity needs region on work payloads; per-region session starts are implemented.

Metrics and traces must continue to exclude raw evidence, personal claims, credentials, and high-cardinality subject identifiers.

---

## 24. Reliability and disaster recovery

**Classification:** Partial and External evidence gap

Implemented foundations include PostgreSQL-authoritative state, Headgate work, idempotency, attempt fencing, inbox-style result deduplication, outbox, reconciliation, recovery inspection, backup-manifest verification, audit verification, and one recorded PostgreSQL restore exercise.

Missing capabilities and evidence include:

- Production recovery evidence across the complete real-provider/model verification lifecycle; the repository-owned lifecycle, callback/poll convergence and replacement-worker mechanisms are implemented.
- Dynamic model-route failover; evaluation-registry activation rollback is implemented.
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

Implemented foundations include API-key and capture-token authentication, application permission checks, forced PostgreSQL RLS, evidence encryption, security headers, bounded request handling, dependency/vulnerability automation, CodeQL, SBOM generation, artifact provenance configuration, a threat model, disclosure policy, release/runbook foundations, and a container publishing workflow that builds the self-hosted images, scans fixable HIGH/CRITICAL findings, attaches BuildKit provenance and SBOM attestations, and keyless-signs the pushed GHCR digests on release tags.

Missing or incomplete capabilities include:

- Distributed HTTP rate limiting under the no-Redis baseline.
- OAuth client credentials.
- Operator SSO remains deferred under repository decision 23; it is not an additional initial-core authentication requirement.
- SCIM for the later commercial operator surface.
- Reviewer identity derived from authenticated operator claims.
- Malware scanning.
- Sandboxed document parsing.
- Provider and model egress allow-lists.
- Explicit secret-scanning gate.
- Fully enforced dependency-licence allow-list and review workflow.
- Independent penetration test.
- Production incident exercises.
- Clean-room provenance verification.

---

## 26. Self-hosted deployment and clean usability

**Classification:** Implemented selected scope — the packaged stack and the thirteen-step gate pass with documented dev-mode fixture boundaries

`deploy/self-hosted/` now packages the documented open-source operational contract: a digest-pinned Compose stack (PostgreSQL, MinIO bucket init, one-shot provisioning and application/Headgate migrations, API, worker, adapter runner, Capture Web host, webhook receiver; optional `model-runner` and paired `api-s3`/`worker-s3` profiles), a multi-stage non-root Dockerfile for the core binaries plus a Capture Web image, an explicit `.env.example` contract, runtime secret sourcing, healthchecks and readiness, and mounted fixture configs for the runners.

`deploy/self-hosted/smoke.sh` implements the clean usability gate end to end and currently passes all thirteen steps: start without Cloud or Console; run migrations; create a tenant and scoped credential; activate the packaged example policy; use the CLI; complete the synthetic capture journey; execute checks; produce and reproduce a decision; deliver and independently verify a signed webhook through the example `sdk/go` receiver; inspect the audit chain through the portable export verifier; interrupt the worker and observe fenced recovery to completion; run retention and deletion; and export tenant-owned data with an independently recomputed digest. `.github/workflows/images.yml` publishes the `idenqa-core`, `idenqa-capture-web` and `idenqa-model-runner` images to GHCR on release tags with provenance, SBOM and keyless signatures after a Trivy gate, and `deploy/self-hosted/compose.published.yaml` runs the same stack from those published images.

Remaining limitations are dev-mode boundaries, not missing deployment machinery:

- The gate exercises the documented server-side Capture Web bootstrap rather than a browser-driven Playwright journey.
- Provider and model runners run in fixture mode because the root worker rejects mixing synthetic processing with real provider/model runtime files; real runner dialing belongs to the provider and model acceptance gates in sections 9 and 4.
- The default gate uses mounted local evidence storage. The optional paired `api-s3`/`worker-s3` services now compose one S3 namespace through the provider-neutral API ingress and worker exact-deletion ports; live S3 worker deletion and production-scale operational evidence remain external validation work under sections 20, 24, and 27.
- Webhook delivery uses a documented dev-only internal network exception so the shipped SSRF-aware callback transport accepts the pinned local receiver; production delivery remains §27 evidence.

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
| Milestone 0 — Foundations                     | **Partial**                  | Remaining public model-health semantics, broader SDK parity and acceptance evidence, production pack substantiation and production-key integration. The 30-operation public SDK administration increment passes focused and real-HTTP/PostgreSQL tests. Provider dispatch-evidence health snapshots, health-based routing and the shared circuit breaker are implemented. Endpoint schema pinning, payload/attempt retention, multi-batch replacement-worker fanout, public API/generated-contract/CLI administration parity, and the packaged self-hosted stack with a passing thirteen-step usability gate are implemented. |
| Milestone 1 — Deterministic identity core     | **Partial**                  | Live provider-account callback operation, third-party certification-issuer trust integration, reviewer/subject acceptance and production-representative deployment remain. The fixture-backed self-hosted gate, general resume, bounded semantic retries, `awaiting_external` execution projection, structured subject-input orchestration, callback intake with replay-identity deduplication, operational-failure projection, dependency/worker-loss recovery proofs, current decision/case reads, persistent identity, assurance context, synthetic completion, cancellation/expiry, policy administration, signed delivery and composed review escalation/correction/appeal proofs are implemented. |
| Milestone 2 — Evidence and capture | **Partial / In review** | Measured-liveness real-camera/device validation, D-026 document/advanced-liveness interaction acceptance, native UI journeys/device evidence and production document/biometric models. Profile/session-backed document choices, portable experience, native SDK lifecycle and document-knowledge layers are implemented. |
| Milestone 3 — Biometrics                      | **In progress / Partial**    | Production-accepted PAD and face models, representative datasets, calibrated thresholds, temporal liveness evidence, hardware/deployment acceptance, and production promotion/rollback. The evaluation runner, registry, dataset tooling, and composed engineering paths exist. |
| Milestone 4 — Real providers and global packs | **In review / Partial**      | Tenant-owned official-account evidence, live health/callback/deletion operations, equivalent fallback, regional/legal acceptance, and the national-ID/driver-licence structural sources and legal review for the implemented pack registry. Dojah and Smile ID runtime paths are composed with local fixtures.                                                       |
| Milestone 5 — Operations and hardening        | **Partial / In review**      | Third-party certification-issuer trust/status integration, production KMS/egress/rate limiting, production-representative deployment, penetration test, load/soak, mixed-version and restoration rehearsals, SLO evidence, and signed candidate. The fixture-backed self-hosted gate, review queue, fail-closed certification adapter and tenant-attested operator foundations exist. |
| Milestone 6 — AI-native orchestration         | **In review**                | Provider-neutral generation, OpenAI-compatible and Anthropic adapters, exact tenant registry/runtime binding, public audited activation/retirement/rollback, enforceable mode pins, and all-attempt usage/cost reporting are implemented. Live provider/model provenance, provider-invoice and unknown-spend reconciliation, representative evaluation, production monitoring evidence, hardened deployment, and independent guardrail review remain. Proposal, guardrail, mode, registry, public API/SDK/CLI, and bounded product foundations are implemented. |
| Milestone 7 — Reusable Verification Cloud     | **Later**                    | Entire commercial Pass and reusable-verification capability.                                                                                                                                                                                                                    |
| Milestone 8 — Ecosystem                       | **Later**                    | Registry, additional adapters/regions/wrappers, enterprise deployment, and credential interoperability.                                                                                                                                                                         |

The build plan's M-0, M-1 and M-3 completion records remain valid for their explicitly bounded bricks and proofs. M-2 and M-4 remain **In review**, and M-5 remains **In progress**. The architecture-wide assessment is broader and identifies capabilities those bricks intentionally did not include.

---

## 30. Version-one acceptance gaps

The following v0.6 version-one acceptance outcomes are not yet demonstrated end to end:

- Production-representative Core operation from a clean deployment without managed Cloud, using browser-driven Capture Web, real provider/model runners, production object storage and production-safe webhook networking; the fixture-backed thirteen-step clean-usability gate passes.
- Complete documented SDK, CLI and capture operation for the production provider/model and privacy-administration paths without undocumented endpoints; the packaged synthetic journey and current administration surfaces pass their recorded gate.
- Two real provider adapters operating through the composed runtime with external evidence.
- Provider replacement in the complete workflow without customer API or evidence-meaning changes.
- Real model replacement without workflow-definition changes.
- Production decision reproduction covering accepted real evidence, lineage, signals, models, providers, runtimes, preprocessing, thresholds, authority, assurance, and policy; AS-01 implements the general snapshot and replay contract.
- Authenticated callback intake, operational-failure projection, callback/poll first-terminal convergence, external-success/local-failure reconciliation and worker/runner-loss recovery are implemented and proven at the owned boundary; live provider-account callback operation and external-success reconciliation with a real provider remain undemonstrated.
- Accepted end-to-end manual review, recovery, reconsideration, correction, and appeal journeys with least privilege and authenticated reviewer authority; real-HTTP composed correction and appeal journeys, behavioural escalation/worker-restart proofs, and the fail-closed certification adapter exist, but third-party issuer trust integration and composed reviewer/subject acceptance remain and O-03 remains **In review**.
- Production provider/model evidence-grant processing with external systems; local composed routes already prove the owned mechanics.
- Deletion of all selected derived assets and external/provider copies.
- Production recovery across the complete workflow lifecycle, including external-success reconciliation with a real provider; the owned callback/poll, worker-replacement and local-failure mechanics are already proven with fixtures.
- Complete native UI, supported-device, interruption, compatibility and security acceptance; the shared configure/start/resume/cancel/cleanup/capability lifecycle is implemented.
- Validate the measured-liveness implementation on real cameras and supported devices, then obtain explicit document capture/review and advanced-liveness interaction acceptance. Profile/session-backed document choices, experience, locale, tenant-copy and mandatory-copy session pinning and signed accessible fallback are implemented; native rendering and managed authoring remain separate boundaries.
- Production model evaluation, calibrated promotion, monitoring, and rollback; evaluation-only registry and offline gates are implemented.
- Full backup restoration covering evidence, keys, credentials, and audit.
- Mixed-version and expand-migrate-contract evidence.
- Production closure of the clean open-source usability gate beyond its passing fixture-backed baseline: browser-driven packaged capture, real runner dialing, production-safe webhook networking and live evidence that the composed S3 worker deletes exact production objects safely.
- Independent penetration test with no unresolved critical findings.

The AI acceptance criterion is conditional: every **enabled** AI action must be a recorded proposal approved by guardrails. AI may remain disabled for version one. The proposal and guardrail foundations are implemented, but M-6 cannot complete without the production and external gates recorded in section 3.

---

## 31. Recommended sequencing

This sequence is reconciled through 22 September 2026. Completed entries identify foundations to retain, not work to implement again. L-01 through L-03, H-01, P-01, I-01, AS-01, the selected tenant-local fraud baseline, the 30-operation SDK administration increment and the repository-owned AI-01 through AI-05 slice are implementation baselines. Their production or external gates remain listed in the owning sections. This is a recommended sequence, not approval to start parallel work or change milestone status.

### 31.1 Close active product and workflow review gates

1. Validate measured liveness on real cameras and supported devices and obtain explicit document capture/review and advanced-liveness interaction acceptance. The profile/session-backed document-choice contract and conformance in section 14.1 are implemented: durable expected-version/idempotent selection, upload branch enforcement, upload-history locking and authoritative recovery preserve immutable snapshot bytes. Retain the recorded Core journeys and responsive/accessibility evidence, including D-027's separate outcome credential; synthetic pose injection proves transport, not real-camera accuracy. Country choices that change policy or profile occur before session creation; signed experience controls presentation, not document requirements. Close E-04 and M-2 only when all applicable exit proofs and acceptance pass.
2. Complete O-03's remaining review gates: retain the passing review-to-recapture-to-explicit-follow-up, progress-preserving recovery, correction/appeal/escalation, consequential-action and fail-closed certification-adapter proofs. Integrate a real third-party certification issuer with key discovery and revocation/status checks, and obtain reviewer/subject visual and interaction acceptance. Do not reopen the implemented routing, queue, evidence-display, recovery, arbitration, correction-successor, appeal, certification-adapter or public-contract work.

### 31.2 Close the deterministic self-hosted core

3. Every lifecycle mechanism is implemented and evidenced: resumption, bounded semantic retries, `awaiting_external`, structured `awaiting_input` requests, callback intake with replay-identity deduplication, operational-failure projection, decision/case reads, and dependency/worker-loss recovery proofs. Related external gates remain — live provider-account callback operation, real third-party certification-issuer trust/status integration and reviewer/subject acceptance.
4. **Implemented selected runtime scope 22 September 2026:** the general event catalogue, endpoint subscriptions, exact schema pins, bounded resumable fanout with replacement-worker proof, authorised list/SSE, local signed forwarding, hold-aware payload/attempt retention, and S3 worker exact-deletion composition are complete. The section 26 packaged stack and thirteen-step local-storage gate pass; live production S3 deletion and real-runner evidence remain.
5. **Implemented selected resource scope 22 September 2026:** privacy administration, decision history/reconsideration, evidence/consent administration and proposal impact assessments have public API/generated-contract/CLI coverage plus handwritten TypeScript and typed Go clients for 30 published operations. See [public SDK resources](public-sdk-resources-v0.1.md) for exact verification evidence and remaining boundaries. A Go-only extension adds 19 provider/model administration methods with passing request-contract tests, race tests, lint and vet (49 typed Go operations total), without a new live provider/model proof. Broader Go endpoint parity, deeper adapter conformance helpers, successful independent-review acceptance and comprehensive server failure-injection evidence remain separate work. Remaining decisions are the exact model-health resource semantics and OAuth client-credential trust/lifecycle contract; do not scaffold either until selected.
6. **Implemented through 22 September 2026:** API, worker, adapter runner, Capture Web host, MinIO, migrations, provisioning, example tenant/profile/policy/webhook receiver, healthchecks and failure recovery are packaged under `deploy/self-hosted/` with a passing thirteen-step `smoke.sh` gate. The optional S3 profile packages matching `api-s3` and `worker-s3` services. Remaining: browser-driven capture evidence, real runner dialing and live production S3 worker-deletion evidence.

### 31.3 Complete the selected first-adopter identity path

7. Substantiate the implemented immutable Nigeria, Ghana, Kenya, and South Africa country/document/jurisdiction packs before advertising support: add official national-ID and driver-licence structural sources, provider/account confirmation, legal/regional review and evidence-backed assurance mappings.
8. The general document subsystem is implemented for Core-owned knowledge: automatic capture with perspective correction, MRZ/checksums, decoded-barcode parsing, documented provider extraction, field consistency, provisional classification, expiry/age, support-level resolution and the signed experience contract. Remaining are provider/model-gated template/security-feature and manipulation inspection, representative acceptance of transient portrait selection/alignment, complete OCR coverage, and operational teardown proof for model caches. Model-contract v1.1 forbids persisted derived model data.
9. Complete production PAD and face-comparison acceptance: reviewed genuine/attack and genuine/impostor data, licensed weights, preprocessing/capture provenance, calibrated thresholds, temporal active-liveness evidence, cohort/device analysis, and hardened deployment. Existing ONNX, registry, evaluation, and composed workflow mechanics remain the starting point.
10. Run Dojah and Smile ID through tenant-owned official accounts with controlled evidence access, live health/callback or polling, retention/deletion exercises, legal/regional approval, and an exact-semantics outage/fallback demonstration. Existing local-fixture PR-01/PR-02 composition remains the starting point.
11. Complete the initial Swift and Kotlin native notice/consent and document surfaces, integrate acquisition policy and measured active-liveness UI, then obtain real-Core journey, platform-security, accessibility, interruption and supported-device evidence. The shared configure/start/resume/cancel/cleanup/capability lifecycle is already implemented; see the native evidence record linked in §15.
12. **Implemented 20 September 2026:** the portable versioned capture-experience schema, validation, lifecycle, copy/mandatory separation, targeting, signing, session pins, revocation/rollback, signed accessible fallback and export/import are implemented with the pack registry. Remaining: managed editing/publishing UI, DNS-verified custom domains and native rendering. CSS custom properties remain appearance-only.

### 31.4 Close production and release gates

13. Live-accept the selected AWS KMS/Secrets Manager path, migrate identity lookup to the custody catalogue, resolve API/worker outbound runner client credentials with redial/overlap, and rehearse rotation/recovery. Complete provider/model egress isolation, distributed HTTP rate limiting, authenticated operator identity and derived/external deletion.
14. Complete regional/provider certification, independent penetration testing, production-shaped load and 24-hour soak, mixed-version and scaled migration rehearsals, complete restoration, SLO measurement, supported-device evidence, and incident exercises.
15. Produce and independently verify the signed external-beta candidate with immutable evidence links and owners.

### 31.5 Keep later boundaries explicit

16. If AI is enabled beyond the deterministic reference path, bind approved registry records to exact live routes through the implemented provider-neutral OpenAI-compatible or Anthropic adapters, persist usage/cost receipts, and complete the external provenance, representative evaluation, deployment, and independent guardrail gates in section 3. Do not treat this as a version-one prerequisite while AI remains disabled.
17. Keep cross-tenant fraud intelligence, Console/Cloud, Idenqa Pass, reusable verification, registry/ecosystem expansion, and explicitly deferred D-014 capabilities in their named later boundaries unless the user selects a new scope.

---

## 32. Completion rule for this audit

A gap may be removed from this document only when one of the following is recorded:

1. The capability is implemented and its applicable automated, integration, security, conformance, and operational evidence passes.
2. A more authoritative accepted decision explicitly narrows or removes the capability.
3. The capability is moved to an explicitly named later milestone or deferred boundary without being represented as already available.

When a gap changes status, update the narrowest authoritative architecture or repository decision first, update the build plan when implementation sequencing or evidence changes, and then update this audit snapshot.
