# Regional usage receipts v1

This public, content-free contract allows an independently built regional
consumer to observe durable Core events without a dependency on Core internals
or a commercial service. Core remains usable without such a consumer.

The implemented event is `provider_dispatch`, quantity `1`: Core accepted an
initial dispatch claim. Its receipt commits in the same PostgreSQL transaction
as that claim. The initial asynchronous path uses the same claim store.
It does not establish external execution, terminal completion, billable usage,
provider cost or a model invocation. No historical dispatch is backfilled.

The receipt contains only `schema`, opaque deterministic `id`, `coreTenantId`,
`event`, `providerId`, adapter ID/version/package digest, check, UTC occurrence
time and integer quantity. It excludes attempt/verification/subject identifiers,
evidence references or bytes, input/result payloads and credentials. It stays
in the Core deployment's region; global commercial consumers receive separately
validated aggregate projections rather than receipts.

The private Core-local listener provides fixed POST routes:

- `/local/v1/usage/receipts/read`: `{ "coreTenantId": "ten_…", "limit": 100 }`.
  The limit must be 1–1000; response is `{ "items": [Receipt] }`.
- `/local/v1/usage/receipts/ack`: `{ "coreTenantId": "ten_…", "id": "64 lowercase hex", "digest": "64 lowercase hex" }`.
  Response is `{ "acknowledged": true }` only after durable acknowledgement.

This privileged local transport is not exposed on the tenant HTTP API. The
deployment operator must grant socket access only to the regional receipt
consumer. The transport neither invokes providers nor grants evidence or
tenant-administration authority. JSON is bounded and strict; unknown fields,
trailing documents and invalid tenant IDs are rejected.

The acknowledgement digest is SHA-256 of the UTF-8 JSON produced from the typed
`Receipt` in its declared field order, with the Go standard JSON encoding and
no trailing newline. Read and acknowledge use explicit tenant predicates plus
transaction-local forced RLS. Receipts are immutable; acknowledgement may only
set the delivery timestamp once. Consumers must commit idempotent acceptance
before acknowledging. Lost acknowledgements leave a pending source and require
replay-safe effects, rather than an exactly-once external-delivery promise.

Migration 88 supplies the regional outbox. A non-superuser, non-`BYPASSRLS`
runtime role requires scoped SELECT/INSERT and acknowledgement UPDATE grants;
it receives no DELETE grant. Source retention/archive policy and deployed
handoff evidence remain open. Local tests prove rollback, replay, scope,
immutability and acknowledgement; they do not establish production acceptance.
