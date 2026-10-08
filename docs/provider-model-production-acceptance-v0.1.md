# Provider and model production acceptance v0.1

**Status — 2 October 2026:** Protocol implemented; no provider, PAD model, face-matching model, quality model, or temporal-liveness model is accepted by this document. Dojah document analysis is the first candidate path. Official-account execution is blocked here because no live credential or approved evidence set is available.

The [acceptance evidence harness](acceptance-evidence-harness-v0.1.md) now enforces this protocol's closed provider/model gates, immutable build and tuple bindings, content-free references, residual-risk rules and cross-functional approvals. Its open example and automated tests are preparation only; they do not change any tuple's acceptance state.

## 1. Acceptance unit

Acceptance applies to one exact tuple:

`adapter/model + immutable version/digest + capability + country + document/evidence class + acquisition method + region + hardware/runtime class + threshold/configuration revision`

A pass for one tuple does not extend to another. Provider availability is an operational property, not a subject outcome. Capability advertisement is not proof of assurance.

## 2. Evidence package

Every package must identify the accountable reviewer, execution date, repository commit, signed build and SBOM, contract version, adapter/model and configuration digests, credential ownership mode, provider account/environment, permitted processing region, approved dataset/evidence-set reference, rights and consent basis, device/hardware bounds, sample exclusions, expected thresholds, raw report location, aggregate metrics, failures, incidents, and expiry or revalidation date.

The repository stores only content-free references and aggregates. Credentials, evidence bytes, provider payloads, biometric templates, subject identifiers and extracted document fields stay in their approved regional systems.

## 3. Dojah document-analysis candidate

The executable production configuration requires:

```json
{
  "adapter": "dojah",
  "environment": "production",
  "base_url": "https://api.dojah.io"
}
```

The runner accepts no other production origin. Live App ID and secret key must arrive through the tenant-owned secret boundary. Dojah's current official documentation states that production uses live keys and wallet-funded calls, and documents the analysis request plus response and error classes:

- <https://docs.dojah.io/api-reference/get-started/environments>
- <https://docs.dojah.io/api-reference/document-analysis/document-analysis>

Required official-account evidence:

1. Exact-key authentication succeeds without logging credentials.
2. Approved, consented or legally usable representative front-only and front/back samples cover every claimed country/document tuple.
3. Valid, invalid, unreadable, unsupported and ambiguous documents map to the owned normalized outcomes without retaining extracted fields or returned images.
4. `400`, `401`, `402`, `424`, `429`, timeout, DNS, TLS, redirect and malformed/oversized response paths produce operational failure or inconclusive state, never an adverse subject outcome by accident.
5. Rate limits, wallet monitoring, retry bounds, circuit breaking, credential rotation/revocation, provider retention/deletion, subprocessors and transfer regions are reviewed.
6. The complete public Capture → encrypted evidence → runner → normalized signal → policy → decision → webhook journey passes in the selected managed region.
7. Logs, traces, task payloads, global Cloud records and the evidence package contain no prohibited content.

Until all seven pass, Dojah remains a candidate, not a production-supported path.

## 4. Model gates

PAD, face matching, quality assessment and temporal liveness remain evaluation-only until each exact tuple has:

- approved model provenance, licence, redistribution/deployment rights and immutable artefact digest;
- an independent, representative evaluation set with documented rights, subject-disjoint splits, attack classes, capture conditions and hardware classes;
- predeclared thresholds and metrics appropriate to the capability, including confidence intervals and nonresponse/error accounting;
- spoof, replay, injection, morph, presentation, look-alike and quality-boundary coverage where applicable;
- fairness and subgroup analysis approved by the responsible privacy/product reviewers;
- latency, memory, concurrency, thermal and crash evidence on every claimed runtime class;
- degradation, rollback, threshold-change, drift-monitoring and revalidation procedures;
- an assurance mapping proving that the acquisition method can establish the claimed provenance, freshness, liveness or binding; and
- independent biometric/privacy/security review where law, contract or risk requires it.

Still-image quality or pose analysis cannot establish PAD. A temporal sequence cannot establish temporal liveness unless the accepted predictor consumes the complete ordered sequence. Uploaded files cannot establish live capture or freshness.

## 5. Decision record

The final record must state `Accepted`, `Accepted with expiring restrictions`, or `Rejected`; enumerate the exact accepted tuple; link every evidence item; identify residual risks and expiry; and carry product, model/provider, security, privacy/legal and operations approvals. Missing evidence fails closed. Synthetic fixtures, sandbox responses, schema compatibility and smoke inference are supporting evidence only.
