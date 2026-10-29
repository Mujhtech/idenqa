# Optional operational admission v1

This public contract lets an independent issuer distribute signed operational ceilings to a Core installation. It contains no price, evidence, identity result or individual operation identifier. Core works independently with this optional boundary disabled. A snapshot never grants application permission, processing authority or identity assurance.

## 1. Trust and signed meaning

`Snapshot` binds one Core tenant, deployment and region; revision and immutable identifier; issuer; issuance, effectiveness and expiry; status; capability versions; limits; and exact workload rules. There are no implicit quota, grace or validity defaults. The operator separately pins Ed25519 public keys and an explicit maximum interval from issuance to expiry.

Sign `Snapshot.SigningBytes()`: the UTF-8 schema identifier `idenqa-core.admission-snapshot.v1`, one NUL byte, then Go `encoding/json` output for the declared struct field order with `Proof` omitted. Map keys use the encoder's deterministic ordering. Limits sort by `kind:key`, rules by operation, and budgets by `kind:key`; arrays must be unique. Wire durations are integer nanoseconds. Normalise issuer times to UTC before signing. Other implementations must prove conformance against these public signing bytes.

`active` and explicitly signed `grace` permit new work within validity. Other statuses refuse it. A newer expired or revoked revision cannot fall back to an older active revision. Future revisions do not hide current effective policy. Revision or ID reuse with changed meaning conflicts; exact old delivery acknowledges its installation without rolling back policy.

## 2. Rules and reservations

`VerificationOperation(profile, revision, policy)` names exactly `verifications.create/<profile>/<revision>/<policy>`. `RecaptureOperation` uses the separate `verifications.recapture/` prefix and requires an explicit rule for the actual child profile, revision and policy. Neither operation has a wildcard fallback or can borrow the other's grant. Core checks the actual immutable session configuration, lineage and region before reserving budgets. Dependent rules use `DependentOperation(workflow, ResourceOperation(kind, selectors...))`, binding each exact resource selection to the accepted workflow. Provider selectors are provider ID, adapter version, package digest and check; model selectors are model registration ID, model version, model digest and evaluation; evidence selectors are evidence type, artefact, acquisition method, MIME type and retention class; review selectors are certification and oversight. A different workflow cannot lend its dependent grants.

All budgets reserve atomically with source state, idempotency, audit and outbox. Each configured key retains its window meaning across revisions, including temporary omission. Changing a period requires a distinct key and cannot hide consumed quantities. Zero maximum explicitly denies admission.

| Kind | Required lifetime | Release condition |
| --- | --- | --- |
| Rate, usage | `window` | Never refunded by closure or replay; a new UTC fixed-duration window has its own counter |
| Concurrency, capacity | `terminal` | Authoritative completion, cancellation, expiry, failure or physical deletion |
| Count, storage | `delete` | Physical deletion of the owning resource |

Windows use `now.UTC().Truncate(window)` (Go's absolute fixed-duration boundary) to match the existing issuer quota contract; their persisted key is that start's Unix nanoseconds. This is an operational duration window, not an invented calendar billing interval.

Storage quantities reserve configured budgets. They do not measure actual evidence bytes or establish a commercial meter. Session count/storage reservations remain held while the session exists. Migration 94 connects provider/model request, upload-intent and review-case adapters to the same reservation mechanism. Evidence preflight compares the complete supported ciphertext bound, including its 40-byte header and per-segment authentication tags, with the signed per-object storage budget. A retry requires durable proof that all preceding object attempts were deleted. Started attempts without that proof retain their reservation for reconciliation; elapsed time alone cannot establish physical absence. Native encryption boundary tests verify the size calculation. Commercial byte meters and retention periods remain separate decisions.

Accepted create replay resolves its original idempotency result before new admission. Subject continuation, workers, recovery and privacy obligations do not consult a newly expired or suspended commercial policy. Independent authority, lifecycle, origin, residency and fleet safety controls still apply.

## 3. Local implementation and recovery

Migration 93 owns forced-RLS snapshots, immutable window definitions, operation/reservation history, indexed counters and append-only events. Runtime roles can consume policy and mutate scoped reservations/counters; they cannot install or rewrite snapshots. The separately privileged protected local socket exposes `POST /local/v1/admission/snapshots/install` and the separately scoped metadata read `POST /local/v1/admission/summary`, with bounded closed JSON and independent signature verification. External distribution belongs to the consumer; the companion Cloud implementation verifies and durably delivers the public contract. Summary responses contain only revision/status/validity, observation time and at most 64 configured limit/counter values. Missing policy returns unavailable; a dirty counter reports no available capacity. No operation, source or receipt identity is exposed.

`IDENQA_ADMISSION_RUNTIME_FILE` names a closed JSON file with `deploymentId`, `region`, `maximumLifetime` and `issuers` (issuer ID to standard-base64 public key). All values are explicit. The API and private bridge require the pinned region to match their Core region. Mount matching trust in both processes and apply the current runtime permission manifest after migration.

The effective head is monotonic and the future schedule is bounded to 64 revisions. Admission consults this bounded schedule and indexed counters rather than scanning snapshot or operation history. An urgent effective revocation can replace a full future schedule. Session creation uses its existing source transaction and a tenant advisory fence. Source rows precede that fence in lock order. No remote call or cross-database compensation is involved. A crash before commit rolls back source and reservations; lost acknowledgement resolves by exact replay. Authoritative closure releases the corresponding lifetimes in that same commit, without guessed timers for uncertain work.

Counter drift marks a projection dirty while allowing authoritative closure to proceed. New work receives `ADMISSION_UNAVAILABLE` until repair. An operator can rebuild one indexed counter from durable reservations using a non-bypass runtime connection:

```sh
idenqa migrate admission-counter-rebuild --tenant TENANT --kind KIND --key KEY --window WINDOW_START_UNIX_NANO --actor OPERATOR --reason-reference CHANGE
```

The command has a 30-second deadline. Its immutable event records the exact counter, window, before/after values, prior dirty state, operator and opaque reason reference. It neither releases reservations nor changes source state. Retention/archive policy remains separately gated. The development down migration refuses populated history.

## 4. Acceptance and remaining scope

The real PostgreSQL session journey uses actual runtime grants and proves missing-policy rollback, exact delivery/create replay, current/future revision selection, changed replay denial, atomic multi-budget refusal, runtime installation denial, scope plus RLS, authoritative closure under injected drift, audited counter repair, no terminal refund of count/storage/rate/usage, accepted continuation after revocation, concurrent one-slot admission and expiry without fallback. A later idempotency failure rolls back successful admission; a repair audit failure rolls back the projection repair. Public codes distinguish temporary rate/concurrency pressure, configured ceiling, usage interval, regional capacity, platform safety, missing capability and unavailable policy. No retry interval is guessed; platform safety remains an independent consumer-owned decision.

The companion Cloud durable delivery and real Core provider/Capture/webhook continuation journey passes locally, including actual quota-summary protocol conformance. PostgreSQL component checks cover all four dependent resource kinds, rollback, contention, replay and source-bound release. Exact deployed identities/readiness, production load, backup byte budgets, archive/legal-hold policy and commercial meter acceptance remain separate gates. These checks select no commercial quantities, grace, freshness or retention periods.
