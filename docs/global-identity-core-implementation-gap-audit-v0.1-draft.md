# Idenqa Core Implementation Gap Audit v0.1 Draft

**Status:** Draft for review  
**Date:** 4 September 2026  
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
| **Current-core gap**                   | Required by the open deterministic core or the v0.6 version-one boundary, but not implemented end to end                                                               |
| **Partial**                            | Domain, contract, adapter, persistence, test, or transport foundations exist, but the complete architecture capability is not runnable                                 |
| **Accepted later milestone**           | Accepted architecture capability planned after the initial deterministic core; absence is real but does not necessarily block version one                              |
| **Explicitly deferred or conditional** | The architecture, D-014, or another accepted decision intentionally postpones the capability                                                                           |
| **External evidence gap**              | Repository implementation exists, but provider, legal, device, security, resilience, or release evidence is still required                                             |
| **Alignment decision required**        | A narrower implementation decision and the broader v0.6 architecture describe different boundaries; the difference must be explicitly retained, scheduled, or resolved |

### 1.2 Static-audit limitation

This audit is based on repository and document inspection. It does not replace the exact toolchain, integration, provider-sandbox, device, penetration, load, soak, migration, disaster-recovery, or clean-room release evidence required by the build plan.

### 1.3 Important scope distinctions

- Non-deterministic AI is an **accepted architecture capability** scheduled for Milestone 6. It is not explicitly deferred. It is absent from the implementation but does not block version one while AI remains disabled.
- Tenant-isolated fraud analysis is accepted and absent. Only the cross-tenant fraud network is explicitly deferred.
- Predictive model execution is separate from non-deterministic AI. Real predictive models are required for the selected biometric and document capabilities.
- Console, managed Cloud, Idenqa Pass, and reusable verification are later commercial capabilities and are not prerequisites for the open-source core.
- Flutter and React Native are required only when advertised as supported. They remain incremental open-source SDK family work.
- NFC, voice, KYB, AML, address, tax, phone, non-English localisation, additional countries, and unsupported resident or refugee documents are deferred by D-014.
- The more authoritative repository draft selects Go, Chi, Apache-2.0, and Headgate even though section 43 of the v0.6 draft still labels some of them Proposed. They are not implementation decision gaps.

---

## 2. Overall finding

The repository contains strong foundations for tenancy, API-key and capture-token access, capture profiles, evidence encryption and upload, processing authority, WebSocket recovery, PostgreSQL-authoritative work, CEL policy evaluation, immutable decisions, audit verification, retention and deletion, provider and model contracts, real-provider adapter source, SDK foundations, and vendor-neutral OpenTelemetry export.

The principal deficiency is composition. Many capabilities are proven independently but do not yet form the complete architecture journey:

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

| Audit area                      | Architecture or decision source                                                                                                                                                                                  | Current implementation evidence                                                                                                                                                                                                        |
| ------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Verification lifecycle          | [Verification lifecycle](global-identity-core-technical-architecture-v0.6-draft.md#12-verification-lifecycle)                                                                                                    | [`SessionState` currently exposes only `collecting`](../internal/verification/session.go); [database constraint permits only `collecting`](../db/migrations/000006_verification_session.up.sql)                                        |
| AI-native orchestration         | [AI architecture](global-identity-core-technical-architecture-v0.6-draft.md#18-ai-architecture); [Milestone 6](global-identity-core-technical-architecture-v0.6-draft.md#milestone-6--ai-native-orchestration)   | [Model v1 is a signal-execution contract and has no proposal contract](../contracts/model/v1/contract.go)                                                                                                                              |
| Predictive model execution      | [Model abstraction](global-identity-core-technical-architecture-v0.6-draft.md#17-model-abstraction); [model governance](global-identity-core-technical-architecture-v0.6-draft.md#36-model-governance-and-mlops) | [Runner transport exists](../internal/transport/runner/model.go); [worker composes a synthetic model](../internal/bootstrap/worker/process.go)                                                                                         |
| Tenant-local fraud              | [Fraud and risk subsystem](global-identity-core-technical-architecture-v0.6-draft.md#21-fraud-and-risk-subsystem)                                                                                                | No owned fraud package, contract, persistence, or runtime exists under `internal` or `contracts`                                                                                                                                       |
| Subject and identity projection | [Primary entities](global-identity-core-technical-architecture-v0.6-draft.md#112-primary-entities)                                                                                                               | [Verification-local subject persistence](../db/migrations/000007_authority.up.sql); D-019 records the narrower selected boundary in the [build plan](global-identity-core-build-plan-v0.1.md#3-just-in-time-decision-gates)            |
| Provider execution              | [Provider abstraction](global-identity-core-technical-architecture-v0.6-draft.md#16-provider-abstraction)                                                                                                        | [Dojah](../adapters/providers/dojah/adapter.go) and [Smile ID](../adapters/providers/smileid/adapter.go) exist; [worker composition remains synthetic](../internal/bootstrap/worker/process.go)                                        |
| Biometrics and documents        | [Biometric subsystem](global-identity-core-technical-architecture-v0.6-draft.md#19-biometric-subsystem); [document subsystem](global-identity-core-technical-architecture-v0.6-draft.md#20-document-subsystem)   | [Swift](../sdk/swift/README.md) and [Kotlin](../sdk/kotlin/README.md) explicitly state that acquisition metadata does not establish PAD, face-match, document-authenticity, MRZ, or barcode assurance                                  |
| Public API                      | [Core endpoint outline](global-identity-core-technical-architecture-v0.6-draft.md#293-core-endpoints)                                                                                                            | [Current authoritative OpenAPI](../contracts/api/openapi/v1/openapi.yaml); [review routes](../internal/transport/httpapi/review.go) and [privacy routes](../internal/transport/httpapi/privacy.go) are not represented there           |
| Webhooks                        | [Webhook architecture](global-identity-core-technical-architecture-v0.6-draft.md#30-webhooks)                                                                                                                    | [Application manager](../internal/delivery/service.go), [task handler](../internal/delivery/task/handler.go), and [proof](../internal/delivery/e2e_test.go) exist; public administration and running-process composition remain absent |
| Capture experience              | [Experience-configuration contract](global-identity-core-technical-architecture-v0.6-draft.md#3111-experience-configuration-contract)                                                                            | [Capture Web](../capture/web/src/capture-element.ts) uses its built-in renderer and copy; no portable experience-schema package or immutable experience aggregate exists                                                               |
| Self-hosted usability           | [Open-source operational contract](global-identity-core-technical-architecture-v0.6-draft.md#336-open-source-operational-contract)                                                                               | [Development Compose](../deploy/dev/compose.yaml) starts PostgreSQL only                                                                                                                                                               |
| External beta                   | [Version-one acceptance criteria](global-identity-core-technical-architecture-v0.6-draft.md#41-version-one-acceptance-criteria)                                                                                  | [X-03](global-identity-core-build-plan-v0.1.md#x-03-real-providers-and-global-packs) and [X-04](global-identity-core-build-plan-v0.1.md#x-04-external-beta-hardening-and-release) remain In review                                     |

---

## 3. AI-native orchestration

**Classification:** Accepted later milestone — not implemented

The v0.6 architecture distinguishes deterministic computation, predictive ML, and non-deterministic AI. The current public model contract implements only signal-oriented execution. No proposal-oriented contract or orchestration exists.

### 3.1 Missing proposal contract

- `ProposalModel` port.
- Immutable `AgentProposal` representation.
- Proposal identifiers and lifecycle.
- Bounded action and argument schemas.
- Evidence and signal reference validation.
- Model, model-version, prompt-version, and context-digest pinning.
- Proposal expiry, cancellation, supersession, and rejection records.
- Accepted-command representation separate from the original model response.

### 3.2 Missing deterministic guardrails

- Tool and action allow-lists.
- Tenant-policy validation.
- Jurisdiction and residency validation.
- Processing-authority validation.
- Cost and rate-limit validation.
- Raw-evidence and sensitive-context restrictions.
- Human approval where required.
- Deterministic execution of an accepted command.
- Replay from the stored accepted command without asking the model again.
- Audit linkage between proposal, validation, approval, command, effect, and result.

### 3.3 Missing automation modes

- `disabled`.
- `assist`.
- `recommend`.
- `guardrailed_auto`.
- `human_required`.
- Tenant and workflow configuration for the selected mode.
- Fail-closed behaviour when the mode or action is unsupported.

### 3.4 Missing AI products and operations

- Review copilot.
- Adaptive-route proposals.
- Natural-language policy drafts.
- Human-readable policy diff generation.
- AI-generated adversarial policy scenarios.
- Accessibility and exception-path proposals.
- Unfamiliar-document layout proposals.
- Prompt registry and prompt lifecycle.
- Generative-model registry.
- AI impact assessments.
- Sensitive prompt and response classification, retention, and deletion.
- AI-specific evaluation, monitoring, cost, and audit records.

### 3.5 Required boundary

The implementation must preserve two separate contracts:

```text
Signal model:
scoped evidence references -> scored signals -> deterministic policy

Proposal model:
redacted bounded context -> non-authoritative proposal
    -> deterministic guardrails or human approval
    -> recorded deterministic command
```

AI must never independently verify or reject a subject, grant evidence access, activate policy, select lawful basis, override consent, change retention, transfer evidence across regions, confirm a sanctions match, or change biometric thresholds.

---

## 4. Predictive models and MLOps

**Classification:** Current-core gap for selected biometric/document capabilities; otherwise Partial

The repository provides a model contract, gRPC transport, validation, conformance tests, and a synthetic model. It does not contain a real model implementation or production model lifecycle.

### 4.1 Missing runtime capabilities

- Runnable model-runner process and deployment.
- Real face-comparison model adapter.
- Real PAD or liveness model adapter.
- Document classification, OCR, quality, and manipulation models.
- Model configuration resolution.
- Evidence-grant redemption within the isolated model boundary.
- Resource and accelerator scheduling.
- Health supervision and warm capacity.
- Safe inconclusive behaviour when no authorised model is available.

### 4.2 Missing registry and governance

- Model registry records.
- Owner and licence metadata.
- Training-data provenance summary.
- Intended and prohibited uses.
- Model, runtime, preprocessing, and output-schema digests.
- Approved regions.
- Threshold sets.
- Evaluation reports.
- Activation, retirement, and rollback records.

### 4.3 Missing promotion and resilience

- Evaluation dataset governance.
- Accuracy gates.
- Demographic and device-class analysis.
- Privacy and impact assessment.
- Shadow execution.
- Canary promotion.
- Automatic technical rollback.
- Manual quality rollback.
- Drift monitoring.
- Reproducible inference record containing hardware or runtime class where relevant.

The worker currently composes synthetic provider and model executors rather than runner clients.

---

## 5. Tenant-isolated fraud and risk

**Classification:** Accepted architecture capability — not implemented

The tenant-local fraud subsystem is absent.

### 5.1 Missing signals

- Device reuse.
- Identifier reuse.
- Portrait reuse within a tenant.
- Document reuse.
- Capture replay.
- Verification velocity.
- IP and network anomalies.
- Provider inconsistency.
- Identity-attribute inconsistency.
- Unusual session timing.
- Repeated failed liveness.
- High-risk model findings.

### 5.2 Missing graph and policy integration

- Tenant-scoped tokenised graph nodes.
- Evidence-backed graph relationships.
- Device, identifier, document, portrait, address, and provider-event tokens.
- Correlation and independence semantics.
- Fraud hypothesis proposal boundary.
- Deterministic policy consumption of evidence-backed fraud signals.
- Safe customer-facing reason handling that does not disclose detection controls.

The cross-tenant fraud network remains explicitly deferred and must not be introduced through the tenant-local implementation.

---

## 6. Subject, identifier, claim, observation, and fact model

**Classification:** Alignment decision required and Partial

D-019 deliberately selected a verification-local, PII-free subject for the implemented processing-authority slice. The broader v0.6 architecture defines a durable tenant-scoped subject and identity projection. The implementation does not provide the broader aggregate.

Missing capabilities include:

- Public subject creation, retrieval, update, and deletion.
- External tenant subject reference.
- Subject lifecycle status.
- Pairwise or otherwise non-globally-linkable subject identity guarantees.
- Encrypted structured claims.
- Encrypted original and normalised claim values.
- National identifier encryption.
- Tenant-scoped keyed-HMAC identifier tokens.
- Masked identifier display.
- Identifier validity and verification state.
- Claim confidence, validity, provenance, evidence references, and supersession.
- Correction records and current-projection rebuilding.
- General immutable observations outside the implemented check-observation shape.
- Typed normalised facts with validity, freshness, units, confidence, and transformations.
- Correlation and lineage groups across derived evidence.
- Independent-source enforcement across correlated transformations.

A founder decision is required on whether the persistent subject projection is a later core milestone or whether the v0.6 subject/API sections should be narrowed to the verification-local model.

---

## 7. Verification lifecycle and orchestration

**Classification:** Current-core gap

The implemented session aggregate and database constraint recognise only `collecting`. The v0.6 lifecycle additionally requires `created`, `awaiting_input`, `processing`, `awaiting_external`, `manual_review`, `completed`, `cancelled`, `expired`, and `failed`.

Missing capabilities include:

- Valid transition table and transition invariants.
- Transition persistence with optimistic concurrency.
- Created-to-collecting activation.
- Capture-complete-to-processing transition.
- Awaiting-subject-input and resume flow.
- Awaiting-provider or authority callback flow.
- Automatic manual-review transition.
- Decision-authored completion.
- Subject and tenant cancellation.
- Session expiration timers.
- Operational-failure transition that cannot become `not_verified`.
- Reconsideration and superseding-decision integration.
- Complete current-decision and case references on the verification aggregate.
- Recovery after worker loss across all lifecycle states.

This is the largest domain gap because it prevents the independently implemented capture, execution, policy, review, and delivery components from behaving as one authoritative workflow.

---

## 8. Assurance and complete decision snapshots

**Classification:** Current-core gap and Partial

The CEL policy contract, immutable snapshots, immutable decisions, lineage, and reproduction tooling are implemented foundations. The complete architecture snapshot and assurance model are not.

### 8.1 Missing assurance capabilities

- Versioned assurance profiles.
- Requested and achieved assurance.
- Assurance dimensions for provenance, freshness, liveness, integrity, independence, and other selected properties.
- Assurance-profile-to-threshold mappings.
- Freshness evaluation.
- Capability-to-assurance mapping.
- Prevention of correlated evidence satisfying independent-source requirements.
- Public assurance-profile discovery and validation.

### 8.2 Missing complete snapshot references

Every consequential decision must eventually bind the exact applicable:

- Claim and identifier references.
- Observation and normalised-fact references.
- Evidence and lineage groups.
- Check and attempt references.
- Signal references.
- Review findings.
- Consent and processing authority.
- Region and transfer-policy context.
- Assurance profile and threshold set.
- Provider, model, runtime, preprocessing, and configuration versions.
- Evaluator and policy versions.

The current direct fact projection is narrower than this complete graph.

---

## 9. Real provider runtime and provider operations

**Classification:** Current-core gap, Partial, and External evidence gap

Dojah and Smile ID adapter source, manifests, normalisation, conformance tests, routing logic, and provider-approved semantic boundaries exist. They are not composed into the running worker.

### 9.1 Missing runtime composition

- Provider-runner binary.
- Provider-runner process lifecycle and deployment.
- Worker-to-runner client configuration.
- Tenant provider registration and configuration.
- Production secret-manager resolver.
- Dynamic credential rotation.
- Purpose-bound structured-input resolution.
- Evidence-grant redemption inside the runner.
- Provider health cache and selection input.
- Automatic callback or polling reconciliation integration.
- Provider-side deletion orchestration.

### 9.2 Missing resilience and cost controls

- Circuit breakers.
- Health-based routing from live health data.
- Degraded-mode visibility.
- Per-provider concurrency and rate control.
- Duplicate-charge protection.
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

The Smile ID adapter's upload URL validation currently checks only HTTPS, a non-empty host, and absence of user information. It does not itself enforce the complete outbound policy.

---

## 11. Biometric subsystem

**Classification:** Current-core gap for the D-014 boundary

The Swift and Kotlin acquisition coordinators implement bounded live-camera acquisition, ordered prompts, deadline handling, and local quality checks. They explicitly do not claim PAD, face-match, document-authenticity, MRZ, or barcode assurance.

Missing capabilities include:

- Presentation-attack detection.
- Deepfake, replay, screenshot, and virtual-camera injection signals where technically possible.
- Face detection and alignment pipeline.
- Embedding creation.
- Reference portrait preparation.
- One-to-one similarity scoring.
- Pinned threshold evaluation.
- Strict separation of liveness and similarity signals.
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

Missing capabilities include:

- Automatic document capture.
- Perspective correction and cropping.
- Front/back completeness.
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

**Classification:** Current-core gap and Alignment decision required

Capture Web provides a functional Lit Web Component, fixed built-in copy, camera and upload paths, authority responses, realtime observation, and recovery. The public portable experience-configuration system described by v0.6 is absent.

Missing capabilities include:

- Public portable experience schema.
- Local schema validator.
- Stable experience ID and immutable version.
- Draft, approved, published, superseded, and revoked lifecycle.
- Semantic light and dark theme tokens.
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
- RTL, text expansion, reduced-motion, contrast, and screen-reader validation.

The existing optional `rendered_experience_version` response field records only a caller-supplied version string; it does not implement the full experience contract.

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
- Actual PAD and face-match integration.
- MRZ and barcode processing.
- Secure temporary-file lifecycle.
- Sensitive-screen and application-switcher protection.
- Root, jailbreak, emulator, instrumentation, and integrity signals.
- Supported-device compatibility matrix.
- Accessibility, orientation, permission-change, network-loss, backgrounding, interruption, cleanup, and cancellation conformance.

Flutter and React Native are not current gaps until advertised. Once advertised, they must remain thin native wrappers and must not transfer raw evidence through Dart or JavaScript bridges.

---

## 16. Manual review, correction, and appeals

**Classification:** Current-core gap and Partial

The repository contains review case, finding, dual-control, correction, and appeal domain and persistence foundations plus internal HTTP routes. The complete workflow and operator-security model are absent.

Missing capabilities include:

- Automatic case creation from `route_manual_review`.
- Queue listing, filtering, ordering, and pagination.
- Priority and SLA management.
- Region, language, reason, assurance, risk, and certification routing.
- Authenticated operator identity and SSO.
- Reviewer certification verification.
- Just-in-time evidence grants.
- Field and image redaction.
- Watermarked evidence display.
- Screenshot deterrence and session timeout.
- Review quality sampling.
- Escalation routes.
- Complete correction intake and subject-facing correction flow.
- New-evidence collection.
- Full appeal lifecycle: eligibility check, correction, new evidence, independent review, withdrawal, and expiry.
- Automatic policy re-evaluation after an accepted finding.
- Independence enforcement against the reviewer of the challenged decision.
- Review copilot with evidence-linked, non-authoritative suggestions.

The current review transport accepts reviewer identity and certifications from request JSON. Those attributes must ultimately come from authenticated operator identity and delegated authority rather than being trusted as caller assertions.

---

## 17. Webhooks and general event contracts

**Classification:** Partial and Current-core composition gap

The repository includes durable webhook persistence, KMS-wrapped rotating secrets, a delivery task, retry exhaustion, replay lineage, SSRF-aware callback transport, an independent Go verifier, and an in-process synthetic decision-to-signed-webhook proof. The broader runnable public flow is incomplete.

Missing capabilities include:

- Public webhook endpoint administration transport.
- Event subscription configuration.
- Automatic domain-outbox-to-webhook projection in the running processes.
- Registration of `webhook.deliver` in the running worker composition.
- Delivery inspection API and CLI.
- Manual replay API and CLI.
- Endpoint and secret rotation transport.
- Complete public event catalogue.
- Versioned domain-event and webhook JSON schemas.
- Event compatibility fixtures and evolution tests.
- Complete clean-deployment decision-to-delivery demonstration.

This does not invalidate the narrower V-04 completion record. V-04 explicitly completed the application, persistence, task, transport, verifier, and deterministic proof boundary while leaving public administration transport for later.

---

## 18. Public API and authentication

**Classification:** Current-core gap and Partial

The public OpenAPI includes tenant inspection, capture profiles, verification creation/read, decisions, notices, processing authority, capture session/progress/bootstrap/connections, and evidence upload.

Missing or incomplete public resources include:

- Subject CRUD.
- Verification cancel and resume.
- Decision history and reconsiderations.
- Evidence metadata.
- Evidence access-grant creation, inspection, and revocation.
- Consent receipt inspection and revocation.
- Provider catalogue, capabilities, health, and configuration.
- Model catalogue, capabilities, health, and configuration.
- Assurance-profile discovery.
- Policy listing, creation, validation, versioning, simulation, activation, rollback, and activation-history inspection.
- Deletion-status retrieval.
- Review queue listing and assignment.
- Webhook administration and replay.
- AI proposal inspection and approval when Milestone 6 begins.
- Safe fraud-signal inspection when the tenant-local risk subsystem exists.
- OAuth client credentials as the alternative server-integration mode described by section 29.2.

The implemented review and privacy routes are not represented in the authoritative OpenAPI contract and therefore cannot yet be treated as stable public administration APIs.

---

## 19. CLI and open-source operational contract

**Classification:** Current-core gap and Partial

The Cobra CLI provides tenant, API-key, migration, Headgate migration, evidence-key, policy-decision reproduction/verification, audit verification, recovery, and background-work inspection/retry operations.

Missing capabilities include:

- Complete synthetic verification command.
- Example-policy creation and activation.
- Policy create, validate, simulate, diff, activate, rollback, and regression commands.
- Provider registration, validation, health, and failure simulation.
- Model registration, validation, health, and rollback.
- Country, document, jurisdiction, and assurance-pack inspection.
- Webhook endpoint configuration, delivery inspection, and replay.
- Retention-resolution inspection.
- Deletion-status inspection and safe retry.
- Tenant-owned data export.
- Tenant HMAC and production-key lifecycle operations.
- AI proposal inspection and approval when enabled.
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
- Capture Web hosting.
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

| v0.6 milestone                                | Architecture-wide assessment | Principal remaining work                                                                                                                               |
| --------------------------------------------- | ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Milestone 0 — Foundations                     | **Partial**                  | Full subject/claim/identifier model, general event contracts, and complete observation/fact/lineage vocabulary                                         |
| Milestone 1 — Deterministic identity core     | **Partial**                  | Complete lifecycle, subject projection, runnable webhook composition, broader administration APIs/CLI, and complete synthetic clean-deployment journey |
| Milestone 2 — Evidence and capture            | **Partial**                  | Portable experience schema, complete SDK lifecycle, OCR/document adapter, complete decision/review/deletion journey                                    |
| Milestone 3 — Biometrics                      | **Not complete**             | Model registry, PAD, face comparison, thresholds, evaluation harness, governance, and rollback                                                         |
| Milestone 4 — Real providers and global packs | **In review / Partial**      | Runtime composition, immutable packs, official provider evidence, live health, deletion, and regional/legal evidence                                   |
| Milestone 5 — Operations and hardening        | **Partial / In review**      | Review queue, operator identity, penetration test, load/soak, mixed-version rehearsal, SLO evidence, and signed release candidate                      |
| Milestone 6 — AI-native orchestration         | **Not started**              | Proposal contract, guardrails, registries, review copilot, adaptive routing, policy compiler, and impact assessment                                    |
| Milestone 7 — Reusable Verification Cloud     | **Later**                    | Entire commercial Pass and reusable-verification capability                                                                                            |
| Milestone 8 — Ecosystem                       | **Later**                    | Registry, additional adapters/regions/wrappers, enterprise deployment, and credential interoperability                                                 |

The build plan's M-0 through M-4 completion records remain valid for their explicitly bounded bricks and proofs. The architecture-wide assessment is broader and identifies capabilities those bricks intentionally did not include.

---

## 30. Version-one acceptance gaps

The following v0.6 version-one acceptance outcomes are not yet demonstrated end to end:

- Complete Core operation from a clean deployment without managed Cloud.
- Complete documented SDK, CLI, and capture operation without undocumented endpoints.
- Two real provider adapters operating through the composed runtime with external evidence.
- Provider replacement in the complete workflow without customer API or evidence-meaning changes.
- Real model replacement without workflow-definition changes.
- Tenant-scoped national identifier encryption and HMAC tokenisation.
- Complete decision snapshot covering evidence, lineage, signals, models, providers, runtimes, preprocessing, thresholds, authority, and policy.
- Independent-source enforcement across correlated evidence transformations.
- Complete workflow-state separation from identity outcome in the persisted state machine.
- Complete inconclusive and manual-review routing.
- End-to-end manual review with least privilege and authenticated reviewer authority.
- End-to-end provider/model evidence-grant processing.
- Deletion of all selected derived assets and external/provider copies.
- Automatic recovery across the complete workflow lifecycle.
- External-success reconciliation with a real provider.
- Runtime-composed signed, replay-protected, idempotent webhook delivery.
- Complete capture SDK interruption, resumption, cancellation, cleanup, compatibility, and security conformance.
- Complete open-source subject journey through the portable experience schema and safe default theme.
- Experience, locale, tenant-copy, and mandatory-copy session pinning.
- Signed accessible experience fallback.
- Operational model evaluation and rollback.
- Full backup restoration covering evidence, keys, credentials, and audit.
- Mixed-version and expand-migrate-contract evidence.
- Clean open-source usability gate.
- Independent penetration test with no unresolved critical findings.

The AI acceptance criterion is conditional: every **enabled** AI action must be a recorded proposal approved by guardrails. AI may remain disabled for version one, but Milestone 6 cannot begin without the proposal and guardrail foundations described in section 3 of this audit.

---

## 31. Recommended sequencing

### 31.1 Close the deterministic end-to-end core

1. Implement the complete verification state machine and transitions.
2. Resolve the persistent-subject versus verification-local-subject alignment decision.
3. Complete assurance profiles and decision snapshot references.
4. Compose the existing webhook application/task boundary into API and worker processes.
5. Complete the missing public OpenAPI and CLI administration surfaces required for independent operation.
6. Build the clean synthetic self-hosted journey.

### 31.2 Complete the selected first-adopter path

7. Add runnable isolated provider and model runner processes.
8. Compose Dojah and Smile ID with tenant-owned credentials and controlled evidence access.
9. Implement immutable country, document, jurisdiction, and assurance packs.
10. Implement document OCR/MRZ/barcode and the selected biometric PAD/face-comparison path.
11. Integrate automatic manual-review and reconsideration orchestration.
12. Complete native SDK lifecycle, recovery, security, accessibility, and device testing.
13. Implement the portable capture experience schema, pinning, and safe fallback.

### 31.3 Close production and release gates

14. Add production KMS/secrets, deletion breadth, provider egress isolation, rate limiting, and operator identity.
15. Complete provider certification, legal/regional review, penetration testing, load/soak, migration, recovery, and incident evidence.
16. Produce and verify the signed external-beta candidate.

### 31.4 Begin accepted later intelligence work

17. Implement model registry and MLOps lifecycle.
18. Implement tenant-isolated fraud signals and graph.
19. Define the proposal-model and `AgentProposal` public or owned contracts.
20. Implement deterministic AI guardrails, accepted-command replay, and audit.
21. Add review copilot and adaptive routing in `assist` or `recommend` mode first.
22. Add the natural-language policy compiler only after policy diff, simulation, adversarial fixtures, approval, and rollback are operational.

---

## 32. Completion rule for this audit

A gap may be removed from this document only when one of the following is recorded:

1. The capability is implemented and its applicable automated, integration, security, conformance, and operational evidence passes.
2. A more authoritative accepted decision explicitly narrows or removes the capability.
3. The capability is moved to an explicitly named later milestone or deferred boundary without being represented as already available.

When a gap changes status, update the narrowest authoritative architecture or repository decision first, update the build plan when implementation sequencing or evidence changes, and then update this audit snapshot.
