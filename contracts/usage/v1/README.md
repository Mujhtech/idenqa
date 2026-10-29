# Regional usage receipts v1

This public, content-free contract allows an independently built regional
consumer to observe durable Core events without a dependency on Core internals
or a commercial service. Core remains usable without such a consumer.

The implemented events are `provider_dispatch`, `provider_terminal`,
`model_dispatch`, `model_terminal` and `verification_completed`, each quantity
`1`. A dispatch observes durable acceptance; a terminal observes a durable
normalized result, including callback adoption. Completion observes the durable
verification transition. Each receipt commits in the owning state transaction.
They establish neither provider charging nor successful identity assurance.
Older events are not backfilled.

The receipt contains only `schema`, opaque deterministic `id`, `coreTenantId`,
`event`, applicable provider/model registration ID, adapter ID/version/package digest, check, UTC occurrence
time and integer quantity. It excludes attempt/verification/subject identifiers,
evidence references or bytes, input/result payloads and credentials. It stays
in the Core deployment's region; global commercial consumers receive separately
validated aggregate projections rather than receipts.

The private Core-local listener provides fixed POST routes:

- `/local/v1/usage/receipts/read`: `{ "coreTenantId": "ten_…", "consumerId": "64 lowercase hex", "routingDigest": "64 lowercase hex", "limit": 100 }`.
  The limit must be 1–1000; response is `{ "items": [Receipt] }`.
- `/local/v1/usage/receipts/ack`: the same tenant/consumer/routing fields plus
  `id` and `digest` (64 lowercase hex each).
  Response is `{ "acknowledged": true }` only after durable acknowledgement.
- `/local/v1/usage/receipts/coverage`: the tenant/consumer/routing fields.
  Returns `idenqa-core.usage-coverage.v1`, its exact binding, epoch, covered
  `from` and `through`. A zero `through` means pending receipts prevent coverage.

All writers and coverage reads serialize through one tenant transaction lock.
Database occurrence times advance strictly even if the wall clock moves back.
Coverage has a conservative activation floor; an empty poll does not prove
historical zero usage. Consumers must require the complete interval to fall
within authenticated coverage. Only one consumer and immutable route digest may
own acknowledgements. Legacy read/ack without ownership fields works only before
ownership is established and cannot claim coverage or take over delivered history.

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

Migration 88 supplies the regional outbox; migration 92 adds the source clock,
ownership and privileged handoff audit. Apply the exact runtime permission
manifest, including SELECT/INSERT/UPDATE on `usage_source_clocks`. Runtime
receives neither receipt DELETE nor handoff mutation privileges.

`idenqa migrate usage-handoff` uses the dedicated migration identity and exact
expected epoch/consumer/routing, new epoch/consumer/routing, actor and opaque
reason reference. Default handoff requires all receipts acknowledged. It returns
a new floor `B` and `previous` coverage through `B`; neither boundary is backdated.
Changing a producer/key requires this drained mode. Old read/ack is fenced.

For an upgrade introducing new event routes that the old collector cannot drain,
the explicit `--carry-pending` mode preserves every receipt and acknowledgement.
An already owned source must keep the same consumer. First validate a superset
of the old route meanings with the consumer's validator (Cloud provides
`metering source-expansion`); keep the same
producer/key and existing meter/unit/dimensions. This preserves exact replay after
a lost acknowledgement. The privileged CAS records pending count and set digest;
it grants no previous completeness when pending detail exists. New coverage starts
at `B` and remains unavailable until pending receipts drain. The earlier uncertain
period stays provisional. Arbitrary producer transfer with pending receipts is
refused; expand/drain under the old producer before rotating it.

Source retention/archive policy and deployed handoff evidence remain open. Local
tests exercise rollback, replay, scope, callback adoption, coverage ordering,
clock rollback and handoff authority. They do not establish production acceptance.
