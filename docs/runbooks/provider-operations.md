# Provider operations runbook

Smile ID and Dojah are optional tenant-credentialed adapters. Core remains
usable with synthetic or alternative conforming adapters.

## Enablement

Before enabling a route, record the tenant, environment, provider account,
credential version, country/product pack revision, processing authority,
recipient, purpose, processing region, retention, deletion process, callback or
polling mode, rate/cost ceiling, and approved fallback semantics. Sandbox
availability is not production entitlement.

Resolve credentials and structured authority identifiers only inside the
provider runner. Never place their values in environment dumps, CLI arguments,
tasks, traces, ordinary logs, or runner request messages. Provider HTTP clients
must have transport timeouts, trusted roots, bounded responses, and no automatic
redirect to an unapproved host.

## Health and outage

Adapter health is safe local readiness, not a subject-data probe. On an outage:

1. Stop new selection of the unhealthy registration.
2. Preserve existing attempt provenance and reconcile retry-safe work.
3. Select another registration only when policy approved and exact check,
   country, evidence, assurance, authority, recipient, region, and purpose are
   equivalent.
4. Otherwise return an operational unavailable/inconclusive path; never weaken
   the evidence meaning or convert dependency loss into `not_verified`.
5. Record start/end, affected attempts, provider notice, routing decision, and
   reconciliation outcome without PII.

## Credential rotation

Add a new externally stored credential version, validate it through the runner,
activate it for new attempts, allow the bounded old-attempt window to close,
revoke the previous credential, and audit reference versions only. Smile ID API
key rotation immediately invalidates the previous signing key, so do not switch
until the new version is verified.

## Reconciliation

Smile ID uses deterministic `partner_params.job_id` from the Idenqa attempt and
polls the signed job-status endpoint after prep/upload, including duplicate-job
recovery. Dojah operations in the reviewed public catalogue are synchronous;
an external timeout is retried only through the owned attempt policy and must
not be assumed unsuccessful when the request may have reached the provider.

Callbacks are authenticated before lookup, mapped to an exact tenant attempt,
deduplicated, and fenced against late overwrite. Conflicting or unknown results
are protected diagnostics and require explicit reconciliation.

## Deletion and offboarding

The reviewed public catalogues do not provide a common per-job deletion API.
Production enablement therefore requires a contractual deletion process and a
tested support/account workflow for each tenant/provider. Offboarding blocks
new work, reconciles outstanding attempts, performs provider deletion or
expiry, revokes credentials/callbacks, retains the minimum reference-only proof,
and confirms that Core evidence deletion continues independently.
