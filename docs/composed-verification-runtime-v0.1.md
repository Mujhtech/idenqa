# Composed verification runtime v0.1

**Status — 7 September 2026:** Repository-owned composition of document-provider, PAD and face-matching checks implemented. Model outputs remain evaluation-only. No trained-model quality, live-provider entitlement or production identity assurance is accepted by this slice.

## 1. Configuration and boundaries

API and worker continue to read `IDENQA_MODEL_RUNTIME_FILE`. Its existing single `ModelRuntime` object remains supported. To configure multiple models, place the complete existing runtime objects inside a top-level `models` array. Each entry retains its manifest, tenant/policy/profile binding, configuration reference, runner address/TLS paths and credential paths. See [PAD runtime](onnx-runtime-v0.1.md) and [matching runtime](face-matching-runtime-v0.1.md) for their respective settings.

A bundle contains one to eight entries, is capped at 512 KiB, rejects unknown fields and cannot mix top-level single-route fields with an array. Duplicate complete configuration references are rejected. Each model must have a **distinct gateway credential**; the API rejects ambiguous credentials at startup. A model credential selects only its own configured route, which then verifies the exact saved request and evidence grant. The HTTP endpoint remains `/internal/v1/model-evidence`; there is no arbitrary model selector in its body.

`IDENQA_PROVIDER_RUNTIME_FILE` may now be set alongside the model file. The current provider composition remains one reviewed provider route. The initial combined fixture uses Dojah document analysis (`idenqa.signal.document_quality`), ONNX PAD (`idenqa.signal.passive_pad`) and ONNX matching (`idenqa.signal.face_match_1to1`). Document quality does not establish document authenticity.

Worker startup requires every component to have the **same tenant, policy ID and immutable profile digest**. It rejects duplicate check names and overlapping normalized output signals. Thus Smile ID's face-match signal cannot be combined with the local matching route without a future explicit signal-resolution design. Unsupported model configurations fail instead of falling back to another executor. Synthetic processing remains mutually exclusive with real runtime configuration.

This is one explicitly configured verification route, not dynamic multi-tenant routing, a workflow DAG, provider fallback or a general model-version catalogue. No public contracts, database migrations or dependencies change. Runtime selection and composition stay in bootstrap; model/provider implementations remain behind their existing owned boundaries.

## 2. Planning, execution and policy

Capture discovery retains the exact tenant/policy/profile filter. After capture is ready and processing authority permits the work, the existing processing transaction creates **all** planned checks, attempts, immutable model/provider requests, scoped grants and task intents. A missing/ambiguous evidence selection or failure in any later preparation rolls back the entire plan. There is no partially dispatched subset from a failed planning transaction.

Checks execute independently through Headgate and their existing durable executors. Model dispatch uses the exact saved configuration reference; request loaders retain current-authority and immutable-envelope checks. Each component has its own grants, even when two checks consume the same selfie or document asset. Workload credentials cannot redeem another model's attempt. Existing per-check deadlines, cancellation, fencing, ambiguous-execution reconciliation and retry semantics remain in force.

Policy evaluation uses the existing normalized facts and versioned tenant policy. This slice adds no default approval threshold or decision rule. Missing, failed or inconclusive signals do not automatically become satisfied evidence. Session completion continues to require all planned checks to be terminal and a policy decision that authorizes completion. Existing audit and signed-webhook delivery/recovery apply to the combined decision. An operational model failure is not proof of a negative identity match.

The combined fixture's policy explicitly consumes document quality plus **inconclusive** PAD/matching signals to exercise completion and webhook delivery. It is a synthetic orchestration fixture, not a production identity-approval policy. Real policies must define their own handling of unavailable evidence, inconclusive outcomes and review requirements.

## 3. Verification and remaining work

Unit tests cover closed/empty/oversized/duplicate bundles, mixed legacy/bundle fields, incompatible route bindings, duplicate checks/signals, exact preparation selection and rejection of unknown model configurations. The public capture fixture runs the document adapter and both native ONNX models in one workflow, with two captured assets, three checks and four purpose-bound grants. It checks cross-model credential denial, immutable receipts and signed-webhook recovery after worker restart. A late injected model-request failure also proves rollback of the earlier provider/model requests, checks and grants; removing the injected failure permits clean recovery. Synthetic images and embedding graphs establish mechanics only.

Production acceptance still requires real model/provider evaluation and rights, documented model-specific thresholds and capture assurance, supported document checks, hardened runtime isolation and operational testing. Dynamic route administration, deliberate resolution of overlapping signals, dependency-ordered checks and policy-approved fallback remain outside this reference composition.

**Local verification — 7 September 2026:** Root Go race tests, the full restricted-role PostgreSQL/Headgate suites, the focused late-failure/recovery journey, production lint and API/worker/model-runner builds pass. Integration lint retains the two pre-existing findings in `privacy_review_test.go` and `policy_catalog_test.go`. Vulnerability scanning reports no affected called/imported code and three advisories in required modules outside the call paths. No remote CI, live-provider or representative biometric evaluation is claimed.
