# Backup, restore, and recovery runbook

**Status:** Selected operational contract for the open-source core. D-016 is resolved.

## Recovery set

One region-pinned recovery set contains:

- a PostgreSQL logical or physical backup compatible with the manifest schema;
- encrypted evidence objects for that same point and region;
- the wrapped-key configuration required to unwrap retained objects;
- audit records, public checkpoint keys, and checkpoints;
- a version-1 manifest containing exact byte lengths and SHA-256 digests.

Raw evidence, plaintext content keys, API credentials, capture tokens, and webhook secrets must not appear in the manifest, command output, ordinary logs, traces, or diagnostics. Backup copies expire after 35 days. Legal hold does not silently extend a backup: the deployment must create and record an explicitly authorised held recovery set.

## Backup procedure

1. Select one region and record the current database migration version.
2. Take a database-consistent snapshot and a matching object inventory. Quiescence is optional only when the database snapshot records the exact object/outbox reconciliation boundary.
3. Export the wrapped-key configuration and audit verification material. Never export plaintext content keys.
4. Calculate exact byte lengths and SHA-256 digests and create the bounded version-1 manifest.
5. Restore the set into an isolated clean environment and run `idenqa recovery verify --manifest-file <manifest.json> --backup-root <root>`.
6. Run migration preflight, object reconciliation, audit verification, deletion-tombstone replay, and failed-work inspection before permitting evidence access.
7. Record the test result, recovery-point age, recovery duration, region, schema version, and reference-only failure class.

## Restore order

1. Create an empty PostgreSQL service and empty regional object namespace.
2. Restore PostgreSQL without starting API or worker processes.
3. Restore encrypted objects and wrapped-key configuration into the same region.
4. Verify the manifest and audit chain.
5. Replay retained deletion tombstones; remove or crypto-shred every restored target covered by a tombstone.
6. Reconcile database references, object inventory, outbox, inbox, and Headgate work. Stale leases are not reused.
7. Start one worker with outbound provider and webhook delivery disabled, inspect failed work, and complete reconciliation.
8. Enable API reads, then workers, then outbound side effects. Replays must retain their original idempotency and event identifiers.

Evidence must remain inaccessible if region, manifest integrity, migration compatibility, audit verification, key availability, or tombstone replay fails.

The open operational surfaces are:

- `idenqa recovery reconcile --env-file <path>` for payload-free structural counts and restore readiness;
- `idenqa work list --state <state> --limit <n> --env-file <path>` for bounded payload-free Headgate inspection;
- `idenqa work retry --task-id <id> --confirm --env-file <path>` for one explicitly confirmed terminal-work retry.

The reconciliation report does not itself mutate or drain durable work. A `ready` result means no expired reconciliation lease, completed-deletion tombstone gap, or audit-head mismatch was found. Pending outbox, evidence, verification, or deletion work must still be drained under its original identifiers before outbound side effects are enabled.

## Recorded clean-restore proof

On 2026-09-04, schema 27 was migrated and seeded in an isolated PostgreSQL 17 source, dumped in custom format, restored into a separate empty PostgreSQL 17 database, and checked with migration preflight. The sentinel tenant matched exactly and `idenqa recovery reconcile` returned `ready=true` with zero pending work, expired leases, tombstone gaps, or audit-head gaps. Both temporary databases and the dump were deleted after the proof.

## Safe diagnostics and targets

Initial self-hosted operational targets are proposed until D-016 selects exporter details:

- recovery-point objective: at most 24 hours;
- recovery-time objective: at most 4 hours for the documented reference deployment;
- backup verification: every backup set before it is accepted, plus a clean restore rehearsal at least monthly;
- API availability target: 99.9% excluding declared maintenance;
- actionable alerts: backup age, failed verification, restore failure, object/reference drift, task backlog age, task retry exhaustion, database saturation, regional storage failure, webhook exhaustion, realtime reconnect rate, and audit checkpoint age.

Rate limits and backpressure must reject or defer bounded work before database, object-store, worker, or realtime resource limits are exhausted. Diagnostics expose identifiers, counts, durations, regions, versions, and stable failure classes only.

## Failure exercises

The release rehearsal interrupts each dependency separately: PostgreSQL, object storage, key provider, worker, realtime connection, and outbound webhook endpoint. Each exercise proves bounded timeouts, cancellation, idempotent retry, fencing, no cross-region fallback, no evidence leakage, actionable diagnostics, and successful reconciliation after recovery.
