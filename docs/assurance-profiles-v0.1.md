# Assurance profiles and decision context v0.1

Status: implemented initial-core contract. Assurance profiles and their public APIs are part of Apache-2.0 Core and work without Console or Cloud.

## 1. Requested assurance

`internal/policy` owns typed assurance profiles. A profile has a tenant-local name, an explicit positive revision, independent dimension requirements, and exact capability mappings. Publication canonicalises its contents and returns a lowercase SHA-256 digest. Published revisions are immutable, including before their first use; changing meaning requires another revision. Republishing the same revision and canonical contents is a no-op. Different contents conflict.

There is no default production profile, universal assurance score, implicit strength ordering, or jurisdiction certification. A requirement's `property` is a descriptive tenant label. Its actual meaning is the declared dimension, capabilities, source count, age bound, and exact runner mappings. Labels do not create equivalence between evidence classes or frameworks.

Each requirement must pass. Capabilities within a requirement are alternatives, and `minimum_sources` requires that many independently rooted successful sources. Use separate dimensions to require both applicant binding and liveness. Each dimension occurs once in a profile.

A tenant selects a published profile for future sessions of a policy using an expected assignment version. Assignment uses the existing `policies:activate` permission. The initial version is zero when no assignment exists. Setting `selection` to `null` explicitly removes the requirement for future sessions.

Session creation pins the exact profile name, revision, and digest in the same transaction as the session, idempotency receipt, audit, and outbox intent. Assignment updates and session creation take compatible locks on the policy root. Updating or disabling an assignment never changes an existing session. Recapture children copy the parent's request, even if the future-session assignment has since changed. An absent selection means no typed assurance was requested; it does not mean the highest or lowest assurance level.

## 2. Capability meanings and achievement

The version-one capability catalog defines which signals and source kinds can contribute to each dimension. A mapping binds a catalog capability to an exact runner kind, runner identifier, package digest, configuration digest, and optional model threshold revision digest. Validation rejects unsupported meanings, such as mapping document quality to liveness.

| Dimension | Initial available support |
| --- | --- |
| Identity resolution | Authoritative-record observations and configured identity corroboration |
| Evidence validation | Document authenticity, document quality, authoritative-record observations, or configured identity corroboration, with distinct capability meanings |
| Applicant binding | One-to-one face matching with fresh, live selfie acquisition provenance |
| Source independence | Independently rooted authoritative-record or document-authenticity observations |
| Fraud resistance | Explicitly configured, source-aware fraud-control findings |
| Human oversight | Accepted review findings; distinct reviewer roots support a two-person requirement |
| Provenance | Accepted live-capture provenance |
| Freshness | Accepted evidence acquired in the active session |
| Liveness | Declared passive or active PAD observations plus the required selfie acquisition provenance |
| Integrity | Accepted capture-integrity provenance |

Capability advertisements and mappings are not successful observations. Achievement uses server-projected accepted evidence, persisted check observations and request envelopes, immutable identity/fraud receipts, and accepted review findings. Capture metadata cannot substitute for a provider/model PAD or matching result. Liveness and face-binding mappings inspect the actual selfie inputs; a live document does not upgrade an uploaded selfie.

File-upload acquisition cannot establish freshness or live capture. Active PAD additionally requires the declared active-liveness acquisition assurance. Local image quality measurements and an SDK's capability advertisement do not establish PAD. The current model registry is evaluation-only, so its observations and threshold mappings remain ineligible for typed achieved assurance. Synthetic attempts or legacy imported attempts without a persisted execution envelope also remain ineligible.

The catalog exposes owned derivation digests for review, identity, and fraud. Review mappings use runner `idenqa.review` and the published review derivation digest for both package and configuration; the accepted findings themselves bind the actual review case and finding references. Identity and fraud mappings use their published derivation digest and the exact configuration digest recorded in their receipt. Capture mappings use the acquisition method as runner identifier, the evidence-registry digest as package digest, and the pinned capture-profile digest as configuration digest. Profile mapping digests use bare 64-character hex, including when the source registry displays `sha256:<hex>`.

### 2.1 Freshness and independence

Evaluation uses an explicit snapshot time. A source must have been collected no later than that time and be strictly younger than `maximum_age_seconds`; the exact age boundary is expired. Known validity, freshness, and retention deadlines also bound identity-derived sources. Importing, deriving, reviewing, or correcting a record does not refresh its collection time. Identity corroboration uses the oldest applicable record time, not the time its receipt was produced.

Sources that share a root, including through a transitive connection, count once. Roots preserve check/request provenance, evidence/capture lineage, and declared upstream correlation groups. Unclassified external providers share a conservative root. Existing identity source bindings and trusted observation imports can supply declared upstream groups; multiple adapters alone do not prove independence. Where exact field-level inputs are unavailable, potential shared capture inputs remain conservatively correlated. Repeated transformations and duplicate imports do not create independent sources.

When the policy would otherwise complete as verified but the requested profile is not achieved, Core selects manual review with `assurance.not_achieved`. Negative or inconclusive policy conclusions remain possible. A review cannot substitute a weaker profile. Recapture contributes separately referenced child sources only under the same requested profile, and parent reevaluation checks their age again.

Fresh verified commits recheck the pinned request and assurance at the current commit time. Expiry between evaluation and commit fails closed with the existing stale-input error; an operator must resolve the failed work or initiate an appropriate new evaluation/recapture. A committed decision's exact replay remains available after expiry and never creates a new claim of current freshness.

## 3. Complete decision context

New production snapshots include an optional, versioned `context` alongside the existing canonical fact contract. It binds the exact applicable references available in the current Core:

- Current claims and identifiers supplied for the verification, consumed identity records and their ancestors, normalised facts, and immutable identity/fraud receipts and configurations.
- Evidence identities and content revisions, conservative lineage groups, check versions, attempt/request/result digests, and normalised signal/observation identities.
- Accepted review findings and, for recapture, the distinct child decision and snapshot.
- Processing authority and response, region, recipient/purpose/allowed-region context, configured policy-pack reference, and capture-profile revision/digest.
- Requested assurance profile, selected model threshold revision, provider package/configuration/credential version, model/runtime/preprocessing digests, and exact policy/evaluator versions.

The configured policy-pack reference and authority-context digest bind the current transfer-policy context. This is not a resolved jurisdiction-pack registry or a claim that a transfer is lawful; those decisions remain with the separately scoped jurisdiction and governance work. A resource that does not exist or did not participate is not invented. Conservative evidence dependencies and exact persisted provider/model grant references are distinguished; the manifest does not claim field-level lineage that a runner has not supplied.

The context contains references and bounded metadata, not raw evidence, subject values, provider payloads, credentials, object locations, or plaintext evidence integrity digests. Evidence references remain opaque. Export still requires `decisions:export`.

The ordinary decision report adds `typed_assurance`, with the requested reference, achieved flag, and per-dimension results and counts. It does not expose source identifiers. The legacy `assurance` string remains for compatibility; for a typed verified decision it names the achieved profile revision. It is not a substitute for `typed_assurance` on older or unprofiled decisions.

An absent context is omitted from canonical JSON. Existing snapshots, evaluations, decisions, and bundles keep their original bytes and digests and remain reproducible. New exports restore the context and recompute achievement through the same deterministic resolver. Review and correction copy paths preserve it. Changing a source, profile, lineage binding, or derived outcome without the corresponding canonical digest is rejected.

## 4. Public API and SDK

| Operation | Endpoint | Permission |
| --- | --- | --- |
| Discover capability meanings and core derivation digests | `GET /v1/assurance-capabilities` | `policies:read` |
| List published revisions | `GET /v1/assurance-profiles` | `policies:read` |
| Publish an immutable revision | `POST /v1/assurance-profiles` | `policies:write` |
| Validate and canonicalise a profile | `POST /v1/assurance-profiles/validate` | `policies:write` |
| Read one revision | `GET /v1/assurance-profiles/{name}/revisions/{revision}` | `policies:read` |
| Read the future-session assignment | `GET /v1/policies/{policyID}/assurance` | `policies:read` |
| Change the future-session assignment | `PUT /v1/policies/{policyID}/assurance` | `policies:activate` |
| Read a session's immutable request | `GET /v1/verifications/{verificationID}/assurance` | `policies:read` |

Publish and assignment operations require `Idempotency-Key`. Validation does not write. Lists use `after` and `limit` (1–100, default 50); follow `next_cursor` while present. Missing and cross-tenant resources share not-found behavior. All responses are non-cacheable. Application services check permissions independently of HTTP middleware.

`IdenqaClient.assurance` exposes all eight operations using the published contract. For example, after constructing and validating a profile against discovered capabilities:

```typescript
const published = await client.assurance.publishProfile(profile, {
  idempotencyKey: "publish-assurance-v1",
});
const current = await client.assurance.getPolicySelection(policyId);
await client.assurance.assignPolicy(
  policyId,
  { name: profile.name, revision: profile.revision, digest: published.data.digest! },
  current.data.version ?? 0,
  { idempotencyKey: "assign-assurance-v1" },
);
const requested = await client.assurance.getVerificationSelection(verificationId);
const decision = await client.decisions.get(decisionId);
console.log(requested.data.selection, decision.data.typedAssurance);
```

Publish/assignment writes preserve canonical idempotency, expected-version conflicts, atomic common audit/outbox effects, explicit tenant predicates, and forced PostgreSQL RLS. Migration 50 adds the assurance tables. Downgrade refuses to discard populated profiles or assignments. No background-work library or public SDK dependency is added.

## 5. Bounds and remaining decisions

Profiles allow at most 16 requirements and 128 mappings; each requirement names 1–32 capabilities and requires 1–32 independent sources. The current catalog has ten dimensions. Age bounds are 1 second through 365 days. Decision context has a 128 KiB canonical bound, at most 8,192 references and sources, and at most 128 roots per source; existing snapshot/bundle limits still apply. Overflow fails closed rather than silently omitting lineage. Larger graph planning remains a separate scaling decision.

Production biometric operating points, framework equivalence, country-specific assurance packs, richer capabilities, provider field-level manifests, and release acceptance remain unresolved. This implementation supplies the configuration, deterministic evaluation, immutable references, and public contracts; synthetic verification does not establish production model accuracy or certification.
