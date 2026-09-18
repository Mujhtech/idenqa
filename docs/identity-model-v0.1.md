# Persistent identity model v0.1

**Decision date:** 9 September 2026  
**Authority:** [Repository/package section 6.3](global-identity-core-repository-structure-and-packages-v0.1-draft.md#63-identity)  
**Public contract:** [Identity API conventions](../contracts/api/openapi/v1/conventions.md#persistent-subjects-and-identity-records)

## 1. Create and link a subject

Create a tenant subject with `POST /v1/subjects` and an optional `external_reference`. Core assigns an opaque ID and version 1. Link an existing same-tenant, same-region verification using `PUT /v1/subjects/{subjectID}/verifications/{verificationID}` with the subject's current `expected_version`. Linking increments its version. The processing-authority subject created for that verification keeps its original identity and immutable receipts.

External references are encrypted and non-unique. Explicit links do not prove that two captures belong to the same civil identity. Core neither merges subjects by identifier nor offers cross-tenant lookup.

## 2. Append identity records

An observation records a typed value and its provenance. For example, send this `record` with `expected_version` to the subject's records collection, replacing the verification ID and times with the actual current values:

```json
{
  "kind": "observation",
  "name": "person.national_identifier",
  "verification_id": "ver_01M11HEQG00000000000000000",
  "normalization": "identity.exact.v1",
  "source_name": "tenant.onboarding",
  "input_version": "tenant.onboarding.v1",
  "value": { "type": "string", "value": " SYNTHETIC123 " },
  "collected_at": "2026-09-09T12:00:00Z",
  "observed_at": "2026-09-09T12:00:00Z",
  "valid_from": "2026-09-09T12:00:00Z",
  "valid_until": "2026-09-10T12:00:00Z",
  "retain_until": "2026-09-10T12:00:00Z",
  "confidence_bps": 9000
}
```

This is always a tenant attestation. `source_name` is a label, not a trust grant. To import a trusted completed Core check, provide `origin_observation_id` instead of a value, source label, input version, confidence, unit or evidence list. Core supplies the boolean outcome, runner/check/attempt provenance and conservative shared-input lineage. Inconclusive imports cannot become satisfied identity evidence.

Derive a `fact` by supplying `source_record_ids` and a selected normalisation, omitting tenant value/source fields. `identity.exact.v1` preserves any supported scalar; `identity.trim.v1` trims string whitespace; `identity.ascii_upper.v1` also uppercases ASCII strings and rejects non-ASCII input. Facts with multiple source records require equal normalised values. Claims and identifiers each derive from one same-name fact. An identifier additionally supplies `identifier: { "namespace": "national.synthetic", "issuer": "authority.synthetic", "verification_state": "unverified" }`.

Derived records inherit transitive provenance and cannot extend source retention, validity or freshness, or increase confidence. Omitted confidence remains unknown; it is not inferred as certainty. Full original/normalised values are encrypted separately from immutable metadata. Numeric scalars are canonical strings, including signed 64-bit integers and bounded decimals. Namespace, issuer, unit and fact/source names use the documented closed name grammar and must contain no personal data.

## 3. Read, correct and evaluate

`GET /v1/subjects/{subjectID}/records` returns immutable metadata and computed identifier masks. Add `reveal=true` only when full values are needed; this requires the additional reveal scope and creates an audit event. `current=true` selects series heads. An expired or superseded ancestor can still make a current head ineligible for policy. Lists return a bounded page and, when applicable, `next_cursor`; preserve collection and filters on continuation.

A correction appends a same-kind/name/type/unit record with `supersedes` set to the current head. Identifier corrections also preserve namespace and issuer. Re-derive affected descendants explicitly. `POST /v1/subjects/{subjectID}/projection/rebuild` reconstructs heads from immutable supersession without rewriting an old snapshot or decision.

Identity configuration is versioned per tenant and region. A requirement names the fact, type, optional unit, permitted source classes, minimum confidence, validity/freshness constraints and minimum independent sources. Boolean requirements explicitly specify the expected boolean. Policy consumes a satisfied/not-satisfied/inconclusive requirement state and an immutable receipt digest; plaintext identity values never enter the CEL input or public decision bundle. Conflicts are not satisfied, while missing or unavailable coverage stays inconclusive.

Repeated tenant assertions count as one source. Related facts, checks, evidence content and provider upstream groups remain correlated across derivation and correction. Configure known provider upstream groups by exact runner ID and package digest; unknown providers share a conservative group. A later configuration can add correlation but cannot erase recorded source roots. Identifier `verification_state` is explicitly tenant reported and is not a trusted policy fact by itself.

## 4. Deploy and retain

Apply migration 49 with Go 1.27.1. Use the existing owned evidence key provider in API and worker composition. Keep its wrapping keys available for retained identity values; do not lose old KEK versions. No new dependency, queue backend or commercial service is required. An unavailable key provider fails protected operations closed. The worker uses the existing Headgate maintenance duty for bounded identity retention.

Provision these privileges on the restricted runtime role in addition to the existing verification/privacy/audit/outbox grants:

| Tables or function | Privileges |
| --- | --- |
| `identity_subjects` | SELECT, INSERT, UPDATE |
| `identity_keys`, `identity_current` | SELECT, INSERT, UPDATE, DELETE |
| `identity_record_values`, `identity_identifier_tokens` | SELECT, INSERT, DELETE |
| `identity_subject_verifications`, `identity_records`, `identity_record_edges`, `identity_record_evidence`, `identity_configurations`, `identity_receipts` | SELECT, INSERT |
| `idenqa.list_expired_identity_tenants(timestamptz,integer)` | EXECUTE |

All identity tables enforce tenant RLS even for their owner. The privileged expiry-discovery function exposes only bounded tenant IDs. Immutable record/configuration/link/receipt triggers reject mutation independently of role grants. Migration rollback refuses to discard a populated identity model.

Set an explicit per-record retention deadline no later than 30 days after record creation. Legal holds override erasure, while authority withdrawal and validity/freshness still prevent new decision use. `DELETE /v1/subjects/{subjectID}` requires its current version and returns a durable deletion ID. It atomically stops active linked verifications and covers identity values, lookup indexes and linked evidence. Initial requests cap 256 links and 255 evidence objects; over-limit planning rolls back without partial effects. Linked evidence in another region also rejects planning atomically rather than silently omitting it.

Subject or linked-verification holds block deletion. Backup waiting begins from actual erasure and lasts 35 days. Completion also waits for exact cleanup of staged upload objects that were not in the initial accepted-evidence target set. Only proof completion sets the subject to `deleted`. Restore procedures must replay durable tombstones before admitting workloads. Immutable reference-only provenance remains available; deleted plaintext does not become reconstructible from an old decision.

## 5. Validation scope

The temporary synthetic end-to-end check exercised the TypeScript SDK over authenticated HTTP, PostgreSQL forced RLS, encrypted original and normalised values, exact keyed lookup, correction and rebuilding, provider check imports, correlated-source rejection, authority withdrawal, held and unheld expiry, linked evidence-file erasure, backup completion and tombstone replay under the race detector. The temporary checks were removed after verification, retaining the user's preference against new permanent tests.

These checks establish core engineering behavior. Production structured-field extractors, automatic ingestion, country-specific normalisation, larger incremental deletion batches and real provider/data acceptance remain separate follow-up work. This work does not claim model accuracy, identity assurance from a tenant report, or acceptance of the Capture Web product journey.
