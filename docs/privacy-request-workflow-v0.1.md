# Privacy Request Workflow v0.1

**Status:** Draft

This document is the narrowest authority for the data-subject privacy-request
workflow introduced for gap-audit §20. Where it conflicts with a broader
architecture or repository/package draft, the broader document governs the
system boundary and this document governs this workflow. Items labelled
**Selected** are agreed direction. Items labelled **Proposed**, **Conditional**,
**TBD**, or **Gated** are not yet accepted commitments.

## 1. Purpose and scope

The workflow records, decides, executes, and audits data-subject requests for
access, portability, correction, restriction, objection, and erasure. It does
not replace deletion, export, identity, or review mechanisms; it delegates to
them. The tenant remains the controller and the only principal that may decide
or execute a request through the tenant API key.

## 2. Selected request types

| Type | Selected meaning |
| --- | --- |
| `access` | Subject-scoped export bundle of tenant-held records for the subject. |
| `portability` | The same machinery with the structured profile subset. |
| `correction` | Identity correction successor for subject-attested data, or a decision-level correction routed through the existing review correction intake. |
| `restriction` | Active subject-scoped restriction blocking new captures, processing starts, and new consent until lifted. In-flight external operations and completed decisions are untouched. |
| `objection` | Purpose-scoped record consumed as policy input for new processing only. |
| `erasure` | On approval, creates the existing deletion request with subject, identity, and evidence targets. Legal holds override. |

## 3. Selected states and transitions

```
requested -> in_review -> approved | partially_approved | denied -> executing -> completed | failed
requested -> withdrawn | expired
in_review -> withdrawn | expired
failed -> executing
```

- Terminal states are `denied`, `completed`, `withdrawn`, and `expired`. They
  are immutable, including for the tenant that approved them.
- Every consequential transition uses optimistic expected-version concurrency.
- Replay of the same decision or the same execution is idempotent and must not
  add events, decisions, or audit records. A conflicting replay fails.
- Transitions are audited with bounded reason codes. Approve, deny, withdraw,
  expiry, and lift each use a closed vocabulary.
- The default expiry is 30 days from request creation. A deployment may
  configure the default and maximum; a request may carry an explicit expiry
  within the maximum.

## 4. Selected channels and permissions

| Channel | Credential | Capability |
| --- | --- | --- |
| Tenant API | Tenant API key | Full request administration through `privacy_requests:read`, `privacy_requests:write`, and `privacy_requests:approve`. |
| Subject outcome | D-027 outcome credential | May create and read only its own request status through a closed subject-safe projection. It cannot approve, deny, withdraw, or trigger execution. |

New tenant permissions:

| Permission | Selected capability |
| --- | --- |
| `privacy_requests:read` | Read requests, restrictions, disclosures, and processor inventory. |
| `privacy_requests:write` | Create requests, withdraw undecided requests, record disclosures, and administer processor inventory. |
| `privacy_requests:approve` | Approve, partially approve, deny, execute, and lift restrictions. |

The subject-safe projection contains only request type, subject-safe status,
and timestamps. It never contains request identifiers, reason codes, actor
references, tenant state, effect references, verification identifiers, or any
other subject's data. Internal states collapse into
`received`, `in_review`, `in_progress`, `completed`, `closed`, `withdrawn`, and
`expired`.

## 5. Selected effects and delegation

| Request type | Owning mechanism | Recorded effect |
| --- | --- | --- |
| `access` | `internal/tenantexport` subject stream | `subject_export` with canonical digest and byte count |
| `portability` | `internal/tenantexport` structured selection | `subject_export` with canonical digest and byte count |
| `correction` (record) | Identity record successor | `correction` with successor reference |
| `correction` (decision) | Review correction intake | `correction` with intake reference |
| `restriction` | Privacy restriction aggregate | `restriction` with restriction reference |
| `objection` | Privacy restriction aggregate, purpose scope | `objection` with restriction reference |
| `erasure` | Identity deletion mechanism | `deletion_request` with deletion identifier |

The execution dispatcher calls these mechanisms through narrow ports. It never
duplicates target planning, export composition, record succession, correction
intake, or deletion execution.

Subject export bundles are canonical NDJSON with a SHA-256 digest over the
header and record lines. They exclude raw evidence bytes, object locations,
ciphertext checksums, key references, provider payloads, and decrypted identity
values. Decision records are the existing byte-canonical bundles.

## 6. Selected data model (migration 66)

| Table | Shape |
| --- | --- |
| `privacy_requests` | Current optimistic workflow row. Identity and instruction immutable; terminal states immutable. |
| `privacy_request_events` | Append-only per-transition events with reason, actor digest, and digest. |
| `privacy_request_decisions` | Immutable decision statements with outcome, reason, actor, and version. |
| `privacy_restrictions` | Subject-blocking and purpose-scoped records. Lifted state immutable. |
| `privacy_disclosures` | Immutable reference-only transfer and disclosure records. |
| `processor_inventory` | Current versioned processor, subprocessor, and recipient entry. |
| `processor_inventory_revisions` | Append-only inventory revisions. |

Every table has forced row-level security with the standard tenant scope
policy. Consequential records use database immutability triggers in addition to
application checks. Common audit integration records a reference-only audit
event per transition with the request version.

## 7. API surface

Tenant routes (relative to `/v1`):

| Method and path | Permission | Result |
| --- | --- | --- |
| `POST /privacy-requests` | `privacy_requests:write` | `202` request summary |
| `GET /privacy-requests` | `privacy_requests:read` | Bounded page |
| `GET /privacy-requests/{requestID}` | `privacy_requests:read` | Full request status with decisions |
| `POST /privacy-requests/{requestID}/approve` | `privacy_requests:approve` | Decision recorded |
| `POST /privacy-requests/{requestID}/deny` | `privacy_requests:approve` | Denial recorded |
| `POST /privacy-requests/{requestID}/withdraw` | `privacy_requests:write` | Withdrawal recorded |
| `POST /privacy-requests/{requestID}/execute` | `privacy_requests:approve` | Effect dispatched |
| `GET /privacy-restrictions` | `privacy_requests:read` | Bounded page |
| `POST /privacy-restrictions/{restrictionID}/lift` | `privacy_requests:approve` | Lift recorded |
| `GET/POST /privacy-disclosures` | `privacy_requests:read` / `write` | Disclosure records |
| `GET/POST /privacy-processors`, `GET/PUT /privacy-processors/{processorID}` | `privacy_requests:read` / `write` | Versioned inventory |

Subject-safe routes:

| Method and path | Credential | Result |
| --- | --- | --- |
| `POST /capture/privacy-requests` | Outcome credential | `202` closed projection |
| `GET /capture/privacy-requests` | Outcome credential | Closed projection page |

The public TypeScript `client.privacy` facade and typed Go client cover request
creation/inspection/decisions/withdrawal/execution, restriction inspection/lifting,
disclosures and processor inventory, plus deletion and retention reads. See
[public SDK resources](public-sdk-resources-v0.1.md) for exact methods, retry and
expected-version semantics, and the passing synthetic integration evidence.
This does not close the production execution gates in sections 10 and 11.

## 8. CLI surface

```
idenqa privacy request create|list|get|approve|deny|withdraw|execute
idenqa privacy restriction lift
idenqa privacy disclosure create|list
idenqa privacy processor put|list
```

## 9. Observability

- `idenqa.privacy.request.transitions` counter with bounded `type`, `from`,
  `to`, and `region` labels.
- `idenqa.privacy.request.age` gauge with bounded `type`, `state`, and
  `overdue` labels, recorded at review, execution, and terminal transitions.
- Alert rules ship in `deploy/observability/alerts.yaml`:
  overdue requests and repeated execution failures.

Identifiers, reason codes, and actor references must never become metric
labels. Receivers are injected at the application boundary and collapse
unknown values through the observability allow-lists.

## 10. Gated capabilities

The following capabilities are explicitly gated and must not be claimed by this
workflow:

- **Provider-side deletion.** Deleting provider-held copies requires a
  provider adapter contract and per-provider reconciliation.
- **Backup-store expiry.** Production backup expiry enforcement remains a
  platform concern; this workflow records the existing deletion backup
  boundary only.
- **Model caches and embeddings.** Derived model artefacts and embeddings do
  not yet have an exact owned deletion target.
- **External-delivery retention.** Copies of delivered webhook payloads held
  outside Core are not addressed by this workflow.

Additional gated behaviour:

- Tenant-channel correction without a linked verification cannot create an
  identity successor and returns an unavailable effect.
- Restriction enforcement covers processing authorization and the privacy
  gate. Capture bootstrap for a subject that is not yet linked to a persistent
  subject cannot evaluate a subject-scoped restriction.
- Delete-on-execute is at-least-once across process crashes. Effects must
  remain idempotent per request identifier.

## 11. Open items

- TBD: whether subject-channel requests should support withdrawal and whether
  the outcome projection should expose a coarse received age.
- TBD: correction payload shape for structured (non-string) identity values.
- TBD: retention interaction between restriction records and subject erasure.
- TBD: disclosure reference format beyond the bounded digest currently used.
- TBD: scheduled expiry ownership (worker task versus operator command) and
  batching limits across tenants.
- TBD: provider-side deletion, external model-workload cache teardown, and
  external-delivery retention contracts referenced in section 10. Model-contract
  v1.1 forbids declared persistence or non-zero retention of derived model data.
