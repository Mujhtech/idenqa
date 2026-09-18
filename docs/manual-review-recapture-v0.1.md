# Manual review and linked recapture v0.1

**Status:** Draft; Core review operations, corrections/appeals, controlled evidence and public integration are implemented, and one live review-to-recapture-to-parent-completion journey is demonstrated. Recovery/correction/appeal composition, external certification and visual/interaction acceptance remain outstanding; see sections 6–8.

## 1. Selected recapture direction

**Selected — 7 September 2026:** The initial recapture workflow creates a new verification session linked to the review case. It does not add capture rounds inside the original verification. The child collects fresh subject authorization and reruns its configured checks. The original session, evidence, findings and decisions retain their immutable history.

`internal/review` owns the reasoned request and case lineage. `internal/verification` owns child-session creation, capture, authorization, check execution and completion. `internal/policy` owns evaluation and decision authorship. Neither a reviewer finding nor a new child decision directly rewrites the original decision.

The linkage must be tenant-scoped and auditable, with expected case versions, one durable result per idempotent request and atomic child creation/linkage. A replay must return the same child. Old subject responses, capture tokens, grants and evidence cannot authorize or satisfy the new session. A failed creation transaction must leave neither an orphan child nor a consumed request.

## 2. Implemented reviewer authority prerequisite

The API resolves durable operator assignments first, with `IDENQA_REVIEW_AUTHORITY_FILE` as a bootstrap fallback only when no database assignment exists. Both bind an authenticated API-key record ID to a stable operator ID. The key still needs the existing transport scope (`reviews:write` or `appeals:write`). The application service separately resolves the operator's permissions, certifications, region and validity window for each reviewer action.

Claim, finding, correction and appeal assignment/resolution bodies no longer accept `reviewer_id` or `certifications`; closed JSON decoding rejects those fields. Findings recheck certification, including the second reviewer. Assignments across key rotation must retain the same operator ID so an additional credential does not create a second independent person. The API key remains the audit actor and findings retain the resolved operator ID.

The bounded, closed bootstrap file is reread when fallback applies. Missing or invalid authority fails closed. Protect file writes as privileged deployment configuration. Durable assignments are rechecked under transaction locks for consequential actions; revocation waits for already-authorized transactions and denies subsequent actions. A revoked database row never falls back to the file. Section 6 documents dynamic administration.

Example (replace every identifier and validity interval with deployment-owned values):

```json
{
  "assignments": [
    {
      "tenant_id": "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
      "api_key_id": "key_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
      "operator_id": "reviewer-1",
      "permissions": ["reviews:claim", "reviews:find"],
      "certifications": ["document.level2"],
      "regions": ["tenant.region.ng"],
      "not_before": "2026-09-07T00:00:00Z",
      "expires_at": "2026-09-08T00:00:00Z"
    }
  ]
}
```

This is a server-managed assignment mechanism. It does not verify a professional credential with an external issuer, prove which human possesses a shared API key, or implement SSO. Deployments must use individually assigned credentials and maintain operator identity consistently. SSO remains deferred.

## 3. Implemented automatic routing

The worker now evaluates both terminal and nonterminal policy results. A `route_manual_review` result uses the existing canonical snapshot/evaluation tables and an immutable `policy_routing_receipts` record. Its `request_id` is the existing authorship reservation, not an authored decision. Terminal `Decision` validation remains unchanged.

Migration 39 permits a case origin of exactly one challenged decision or routing request. A unique tenant/request constraint prevents duplicate cases; origin, region, certification and oversight cannot be rewritten. The receipt references its lifecycle event through a deferred foreign key. A new routing effect locks the session, verifies its reservation, policy and region, rechecks current processing authority and exact subject response, and requires terminal checks. Snapshot, evaluation, receipt, case, `processing -> manual_review`, reference-only audit and outbox commit in the same serializable Headgate effect transaction. No terminal decision or completion webhook is produced.

Replay reads saved provenance before consulting current policy. It verifies the exact request, canonical digests, case and original transition. An already committed route remains replayable after authority expiry or configuration removal, without new effects. The parent session's reserved decision ID stays immutable for future terminal authorship; accepted-finding re-evaluation uses the separate `review.evaluate` task identity.

The worker consults versioned database review settings for the exact policy and falls back to `IDENQA_REVIEW_ROUTING_FILE`, read at startup. The file contains case requirements for exact tenant/policy revision/digest combinations; file-routed cases need explicit settings adoption before evidence display:

```json
{
  "rules": [
    {
      "tenant_id": "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
      "policy_id": "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
      "revision": 1,
      "policy_digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "required_certificate": "document.level2",
      "oversight": "dual",
      "permitted_findings": [
        {"resolution": "satisfy", "reason_code": "document_reviewed"}
      ]
    }
  ]
}
```

Replace fixture IDs/digest with the actual immutable revision. No rule means no case creation: the author task is quarantined without partial workflow effects. Configure the mapping before activation; after repairing a missing mapping, restart the worker and use the existing failed-work recovery process. This operational mapping supplies case requirements only; it does not settle the complete public review-policy schema or authorize a finding to become a decision.

Provision restricted runtime roles with `SELECT, INSERT` on `idenqa.policy_routing_receipts`, alongside their existing policy, review, lifecycle, audit and outbox grants. Apply migration 39 before starting these binaries. Downgrade refuses routed cases rather than deleting their lineage; the older region constraint can also prevent downgrade when newer region values exist.

Case reads expose `routing_request_id` for routed cases and omit `challenged_decision_id`. Corrections and appeals requiring an existing challenged decision reject these pre-decision cases. These internal review routes still lack generated public OpenAPI/SDK contracts.

## 4. Implemented accepted-finding re-evaluation

The exact-policy routing configuration now pins an immutable `permitted_findings` allowlist of resolution/reason pairs to each case (maximum 64). Empty or absent lists deny routed findings, including existing cases created without a list. Changing deployment configuration does not rewrite an existing case. Single-review acceptance requires one permitted finding; dual review requires two independent reviewers agreeing on resolution. Disagreement records `escalated` and schedules no evaluation; subsequent escalation handling remains TBD.

Finding persistence rechecks current processing authority and the latest subject response, plus grant expiry/revocation and available, verified evidence. Grants must match tenant, verification, authority, response, region, stable reviewer binding and the case reference `review.<lowercase-case-id>`. Section 6.1 supplies just-in-time grants and controlled display; older integration fixtures retain explicit synthetic display receipts.

A resolved finding and its immutable `review_evaluation_requests` record commit atomically with the case version and audit. The worker discovers pending requests after restart and schedules `review.evaluate` v1 using case ID/version as its stable key. Under the Headgate transaction fence, it reconstructs the accepted case and original routing snapshot, checks current authority and persists an immutable evaluation receipt. The original policy revision, evaluator, authority, response and facts remain pinned. The added `review.resolution` fact maps `satisfy` to `satisfied`, `not_satisfy` to `not_satisfied`, and `request_input` to `inconclusive`; its source is a canonical accepted-case digest. Existing fact freshness limits still apply. Policies must explicitly consume this fact to change their result.

Only a terminal policy result authors the reserved machine decision and invokes the shared atomic session-completion and signed-webhook delivery path. Manual-review completion requires persisted review provenance. Nonterminal results retain their receipt and leave the session in manual review without completion effects; linked recapture creation is implemented in section 5. Exact replay restores saved results, including after authority withdrawal, without repeating evaluation or delivery effects.

Apply migration 40 before starting these binaries. Restricted runtime roles additionally need `SELECT, INSERT` on `idenqa.review_evaluation_requests` and `idenqa.review_evaluations`, and `EXECUTE` on `idenqa.list_ready_review_evaluations(timestamptz, integer)`. Both tables use forced tenant RLS and immutable records. Downgrade refuses persisted requests rather than deleting accepted-finding lineage.

## 5. Implemented linked recapture creation

**Selected — 7 September 2026:** The child reuses the parent's exact capture-profile snapshot and policy revision, even if current activation changes. It starts a fresh collecting journey, with a new capture credential and decision reservation. It inherits no evidence, check results, authority, notice binding or subject response. Fresh child authorization and capture must precede its normal configured-check execution. Retained parent configuration does not refresh old evidence or convey old assurance.

The internal `POST /v1/review-cases/{caseID}/recaptures` route accepts only `expected_version` with an `Idempotency-Key`. The API key requires both `reviews:write` and `verification_sessions:create`; the application also requires current server-resolved `reviews:find` authority and the case certification in its region. An accepted, resolved routed case must have a persisted policy evaluation selecting `request_input`. A finding alone cannot create a child. Deployment session/capture lifetimes apply; callers cannot override policy, profile or region.

Migration 41 introduces immutable, forced-RLS `review_recaptures` lineage. In one serializable transaction, the adapter checks current parent processing authority and exact case version, creates the child configuration/credential/audit/outbox, inserts the unique case-version link and completes `reviews.recapture` idempotency. Failure rolls back all effects. Durable case-version uniqueness returns the same child and credential even under a different idempotency key. Replay still requires current operator authorization; it does not renew an expired capture credential. The response supplies `verification_id`, `case_id`, `capture_token_id`, `capture_token_expires_at`, `verification_expires_at` and the explicitly delivered `capture_token` with `Cache-Control: no-store`, allowing a tenant-controlled handoff to the existing capture flow.

The policy input loader obtains linked-child policy pins from the canonical persisted parent evaluation instead of the current active revision. Child observations and processing authority remain its own. It also projects `review.recapture.requested` from immutable linkage so a pinned policy can distinguish a linked child from an original journey. This reference-only context fact does not prove identity, evidence quality, freshness, liveness or any other assurance; the child's own accepted evidence and completed checks must establish its outcome. Restricted roles need `SELECT, INSERT` on `idenqa.review_recaptures`; downgrade refuses retained linkage. Automatic subject messaging and correction/supersession product surfaces remain pending. The parent stays in manual review and no original decision is overwritten.

### 5.1. Progress-preserving credential recovery

**Selected — 8 September 2026:** A replacement credential continues the same child session, retains accepted evidence and requires fresh subject authorization before continuing. Pending uploads are abandoned and must be restarted with new upload IDs. Original evidence, token, response, acquisition and assurance provenance remain immutable.

`POST /v1/review-cases/{caseID}/recaptures/renew` accepts `expected_version` and `expected_capture_token_id` with an `Idempotency-Key`. It requires the same transport permissions and current certified reviewer authority as linked creation. Renewal permits only an expired, unrevoked latest token on an unexpired collecting child. Current child authority, if declared, must remain active and valid; a withdrawn/declined latest response prevents renewal. New mutations check the current case version; exact committed replay restores its original replacement credential after current reviewer authorization.

Under the child-session lock, renewal revokes the expected credential, creates a replacement capped at the original child deadline, fences the child-session row and appends immutable migration-44 recovery lineage. Each accepted upload visible to the previous credential receives an explicit retained binding for the replacement. Each previous-token issued/uploading intent receives an abandoned binding. These records, credential replacement, review audit and idempotency commit atomically. Failed transactions leave no partial recovery. Repeated recovery copies only explicitly retained accepted progress, without duplicate artefacts or changes to source provenance.

Abandonment is a durable recovery disposition, not a rewrite of the upload's original state or deadline. The revoked credential cannot claim or accept its unfinished work. Existing object-reconciliation records and expiry/lease cleanup remain responsible for unretained objects; recovery does not delete accepted objects or erase cleanup obligations. New attempts require fresh upload IDs.

Authority snapshots suppress a previous credential's response after recovery. New upload issuance and acceptance require a response from the current credential; processing also rejects the prior response while waiting for fresh authorization. After a fresh response, processing can use old accepted evidence only through its explicit retained recovery binding, under the same current authority/subject/purpose/region checks. Evidence ages and assurance claims are not refreshed. Subject-facing authority responses consequently require the existing capture client to present authorization again.

REST progress and durable capture-progress publication include current-token accepted uploads plus explicitly retained uploads. Recovery metadata itself exposes no raw evidence. Old signed claims remain immutable and old-token database authentication fails. New subject-response commits recheck token/session binding, lifetime and revocation under the shared session lock, preventing a response authenticated before renewal from committing afterward. Creation replies still reference their original credential; tenants retain renewal keys for exact retry.

Apply migration 44 and grant the restricted runtime role `SELECT, INSERT` on `idenqa.capture_recoveries` and `idenqa.capture_recovery_uploads`. Both use forced RLS and immutable rows. Downgrade refuses retained lineage. This implements Core recovery semantics; complete visual/interaction acceptance of the subject-facing recapture journey remains pending.

### 5.2. Child outcomes for authorized follow-up

**Selected — 7 September 2026:** Attach the child's own committed outcome to the original review case for an authorized follow-up. Do not automatically re-evaluate the parent.

`GET /v1/review-cases/{caseID}/recaptures` requires `reviews:read` and returns reference-only linked child status, current capture-token ID/expiry, verification expiry, and the committed child decision/outcome when available. Responses use `Cache-Control: no-store` and contain no bearer credential. The projection follows immutable case linkage to the child's atomically committed completed-decision pointer; it does not invent another decision or mutate parent history. Completed children expose `follow_up_required: true` until the exact child outcome is acknowledged. Status then exposes `acknowledged_at`, `acknowledged_by` and `follow_up_required: false`. This tracks acknowledgement only; it does not mark parent verification or consequential case resolution complete. Reading status leaves the parent in manual review, and existing child completion delivery remains unchanged. Explicit re-evaluation is a separate action in section 5.4.

The tenant can use the creation/renewal credential with the existing capture integration and use status metadata to choose recovery or reviewer follow-up. The composed development fixture now demonstrates a tenant-controlled handoff into a live child Capture Web journey. No automatic subject communication or accepted subject-facing visual journey is claimed.

### 5.3. Audited child-outcome acknowledgement

**Selected — 7 September 2026:** The first reviewer follow-up action is an audited acknowledgement of the child's exact outcome. It leaves the parent in its existing state and does not request policy re-evaluation.

`POST /v1/review-cases/{caseID}/recaptures/acknowledgements` accepts `expected_version` and `decision_id` with an `Idempotency-Key`. The API key needs `reviews:write`; the application resolves current operator authority and requires `reviews:resolve`, the case certification and region eligibility on every call, including replay. Caller-provided operator identity, certifications and free-form outcome substitutions are rejected by the closed request schema. This reference-only acknowledgement does not access evidence or require renewed subject processing authority.

Migration 42 stores one immutable acknowledgement per tenant/case/version. Composite foreign keys bind the exact recapture child and its decision. A serializable transaction verifies the current case version and committed child decision, then inserts the receipt, attributed audit and `reviews.recapture.acknowledge` idempotency result together. A repeated request, including another idempotency key, restores the original operator, API-key actor and timestamp. A different decision or stale case version fails. Acknowledgement does not alter case versions, parent state, decisions, policy evaluation requests, tasks or webhooks.

Restricted runtime roles need `SELECT, INSERT` on `idenqa.review_recapture_acknowledgements`. Downgrade refuses persisted acknowledgements. This action means that an authorized operator has acknowledged the referenced child outcome; it is not an identity finding, approval, supersession or proof of a completed evidence review.

### 5.4. Explicit parent policy re-evaluation

**Selected — 8 September 2026:** After acknowledging the exact completed child outcome, a currently authorized reviewer may explicitly request parent policy re-evaluation. Child completion and acknowledgement alone do not schedule this action. Only the parent's pinned policy may resolve it.

`POST /v1/review-cases/{caseID}/recaptures/reevaluations` accepts `expected_version` and the acknowledged child `decision_id` with an `Idempotency-Key`. It requires API-key `reviews:write` plus current server-resolved `reviews:resolve`, region eligibility and the case certification, including on retries. The response is `202` with the source and new case versions, child decision reference, original actor/reviewer and request timestamp. Caller-selected outcomes and operator identities are rejected.

Migration 45 binds one immutable request to the exact acknowledgement and next case version. In one serializable transaction, the adapter checks the resolved case/version, prior evaluation and current parent processing authority, advances the case version and records the request, audit, durable `review_evaluation_requests` intent and separate `reviews.recapture.reevaluate` idempotency result. Any failure rolls back all effects. Durable uniqueness returns the original attribution under a new retry key; changing the expected child decision fails.

The existing `review.evaluate` worker discovers the new version after restart. It reconstructs the request, acknowledgement, immutable child decision and previous parent evaluation snapshot under the transaction fence. The parent's original policy/evaluator, authority references and original facts—including their observation timestamps—remain unchanged. A separate `review.recapture` fact maps the child's `verified`, `not_verified` or `inconclusive` outcome to `satisfied`, `not_satisfied` or `inconclusive`. Its digest binds the complete attributed request, acknowledgement, child decision and previous snapshot. Its observation time is the child's evaluation time; requesting review cannot refresh old evidence. An original routing fact using the reserved `review.recapture` name is rejected rather than overwritten. A later recapture replaces only the current recapture fact in a new snapshot; all prior snapshots and receipts remain immutable.

A policy that does not consume the child outcome need not change its result. Nonterminal results retain the parent in manual review. Terminal results use the original reserved parent decision and shared atomic completion/outbox/delivery path. Replays restore the saved evaluation without duplicating completion. This does not permit reviewers to directly assert a verification outcome or supersede an existing completed parent decision.

Restricted runtime roles additionally need `SELECT, INSERT` on `idenqa.review_recapture_evaluation_requests`. Forced tenant RLS, immutable records and downgrade refusal preserve request history. Generated public review contracts, controlled evidence display and subject-facing acceptance remain separate work.

## 6. Review operations and public integration

**Selected and implemented — 8 September 2026:** One tenant key with `reviews:admin` manages versioned operator assignments, tenant-attested certifications and review configuration. Revoked database assignments are tombstones: they never fall back to the deployment assignment file. Current operator permissions, certificate, region and expiry are checked inside consequential transactions. Stable operator IDs must survive key rotation. External certificate issuers remain a separate verification-adapter integration.

`PUT/GET /v1/review-operators/{keyID}` and `PUT/GET /v1/review-policies/{policyID}/revisions/{revision}` expose configuration and version. Mutations require `Idempotency-Key`; writes carry `expected_version` and `configuration`. A policy configuration pins tenant, exact policy revision/digest, required certificate, oversight, permitted findings, explicit evidence display purposes/redaction rectangles, initial priority/SLA, queue labels, sampling percentage, appeal window and escalation certificate. Existing cases retain immutable settings. `PUT /v1/review-cases/{caseID}/settings` adopts matching settings once for an existing routed case using the settings document directly.

`GET /v1/review-cases` supports bounded pagination and state, region, reviewer, language, reason, assurance, risk, certificate, sampled and overdue filters. Ordering is priority descending, due time ascending and stable case ID; signed cursors bind tenant and all filters. `PUT /v1/review-cases/{caseID}/queue` versions priority, deadline, labels and quality-sampling selection independently of case findings. Labels guide queue selection; certificate and region authorization remain mandatory. Quality sampling marks cases for inspection without changing decisions.

### 6.1. Controlled reviewer evidence

After claiming a case, request `GET /v1/review-cases/{caseID}/evidence?expected_version=N` for display-eligible artefact references. Extracted personal fields, storage locations and encryption metadata are omitted. `POST /v1/review-cases/{caseID}/evidence-grants` requires the exact case version, evidence ID and idempotency key. Display requires an explicit pinned rule for that requirement; missing rules deny access. A grant is bound to tenant, case/version, stable reviewer, evidence and current subject authority; it lasts five minutes and permits one redemption.

`POST /v1/review-evidence-grants/{grantID}/content` rechecks authority before returning a redacted, burned-watermark PNG with no-store headers. The original bytes remain within the evidence adapter and trusted display receiver. JPEG/PNG inputs are bounded to 10 MiB, 8192 pixels per side and 12 megapixels. Normalized rectangular masks cover their full requested area. Unsupported media are denied. Successful display redemption is required before the grant may support a finding. Revocation, stale versions, subject withdrawal and expired grants deny subsequent access.

The TypeScript `createReviewViewer` helper accepts redacted PNG bytes through an authenticated tenant-backend callback, clears them on timeout, tab hiding or window blur, and revokes object URLs. Copy/context-menu suppression and watermarks deter copying; browsers cannot guarantee screenshot prevention. Broad tenant API keys must stay on the backend.

### 6.2. Escalation, correction and appeal

An independent supervisor with the pinned escalation certification may call `POST /v1/review-cases/{caseID}/arbitrations`. The immutable arbitration records a permitted resolution and reason. The supervisor cannot have submitted either finding or contributed to the challenged decision. Routed cases use the existing durable evaluation worker and add `review.arbitration`; only the pinned policy decides the outcome.

`POST /v1/decisions/{decisionID}/review-cases` opens one correction case for the latest immutable decision within the configured appeal window. Independent reviewers collect findings using controlled display. `POST /v1/review-cases/{caseID}/corrections/evaluate` evaluates those findings or independent arbitration under the original policy. It adds `review.correction` and, when acknowledged fresh evidence exists, `review.correction.recapture`. A correction chain retains all prior decisions and uses the original facts without rewriting their timestamps. Terminal policy results append a successor and an atomic `verification.decision.corrected` outbox/delivery intent; nonterminal results preserve the challenged decision. The original completed-session pointer remains historical; consumers follow successor lineage. Ordinary processing workers cannot reopen completed sessions: explicit reconsideration checks current subject authority and latest decision while permitting the original capture deadline to have elapsed.

New evidence uses the existing linked-session creation, recovery and acknowledgement routes after a persisted policy request for input. Child policy/profile revisions stay pinned and fresh subject authorization remains mandatory. Completion attaches the child outcome; it does not automatically alter the parent.

Appeal intake uses `POST /v1/review-cases/{caseID}/appeals` with an idempotency key and configured deadline. `GET /v1/appeals/{appealID}` exposes state, deadline, reason and successor. Assignment and resolution require current independent, certified appeal authority; current case contributors and original-decision contributors are excluded. More-input retains assignment in `awaiting_input`. Overturn can reference only a policy-authored correction successor for the same verification and challenged decision. The requesting key may withdraw with the exact version. The worker expires overdue active appeals. Immutable transition snapshots retain reasons through withdrawal and expiry.

### 6.3. SDK and subject handoff

The public OpenAPI contract and `IdenqaClient.reviews` cover queue, case, findings, controlled evidence, administration, arbitration, corrections, appeals and recapture operations. Types derive from published contracts. JSON field names in this review client match the wire schema. Tenant applications authenticate the subject before returning a capture credential.

Capture Web exports `createRecaptureHandoff(element, options, obtain)`. The callback obtains credentials from the tenant backend; the helper never stores them in URLs, history or browser storage. Recovery requires the same linked child ID, cancels prior capture activity and validates the Core session ID before rendering. The existing Core snapshot restores accepted progress and requires fresh authorization. Automatic email/SMS messaging is not part of this helper.

### 6.4. Migration and runtime permissions

Apply migrations 46 and 47 before starting these binaries. Restricted roles need `SELECT, INSERT, UPDATE` on `review_operator_assignments`, `review_policy_settings` and `review_case_operations`; `SELECT, INSERT` on `review_administration_history`, `review_case_settings`, `review_evidence_access`, `review_arbitrations`, `review_correction_evaluations`, `review_correction_intakes` and `review_appeal_history`; and worker `EXECUTE` on `idenqa.list_expired_review_appeals(timestamptz,integer)`. Existing evidence, audit, policy, outbox and delivery grants still apply. All tables have forced tenant RLS; historical records reject update/delete. Downgrade refuses retained history. Existing duplicate active appeals must be reconciled before migration 47's unique active-appeal index can succeed; migration never silently discards them.

## 7. Acceptance still required

The selected Core workflow and integration helpers above are implemented. External certification-issuer integration, a complete tenant subject-notification surface, advanced queue assignment automation and production acceptance of the non-authoritative review copilot remain separate work. SSO remains deferred.

The live composed fixture demonstrates one least-privilege path through queue claim, protected evidence redemption, `request_input`, tenant-controlled child handoff, fresh child authorization and capture, child outcome acknowledgement, explicit parent re-evaluation and parent completion. It is development and conformance evidence, not accepted product-demo evidence. O-03 remains **In review** until reviewer/subject visual and interaction acceptance and composed recovery, correction and appeal outcomes are demonstrated. External certification remains an integration boundary rather than a claim established by the tenant-attested fixture.

Acceptance must still demonstrate progress-preserving recovery in the composed surface; independent arbitration/correction and appeal branches through their consequential outcomes; revoked display denial in the live journey; final signed delivery where applicable; and usable reviewer/subject surfaces in a composed deployment.

## 8. Verification evidence

**Review operations batch — 8 September 2026:** Existing root Go race tests and the final full PostgreSQL/Headgate race regression pass. Scoped production lint reports zero issues; public binaries build; SQL and OpenAPI generation are repeatable; the public contract lint score is 100/100. SDK and Capture Web typechecks/builds pass; existing SDK tests report 35 passed and one skipped, and Capture Web unit tests report 39 passed. Vulnerability scanning reports no affected imported packages or called code; three uncalled required-module advisories remain. Markdown structure, fences, relative links and whitespace checks pass.

No new tests were added. Existing scope expectations, restricted-role grants and synthetic display receipts were adapted. The prior 15-second expiry-worker wait was shorter than its selected 30-second reconciliation backoff: diagnostics captured a PostgreSQL serialization conflict followed by a correctly scheduled retry. The existing wait now includes initial backoff and jitter; production retry policy was not changed. These checks do not establish new composed-journey or visual acceptance coverage.

**Recovery increment — 8 September 2026:** Restricted-role integration tests accept one artefact, roll back and retry renewal, verify immutable source provenance, deny old-token acceptance, append fresh authorization, accept the remaining artefact and validate combined processing authority. Repeated recovery preserves both accepted artefacts without duplication and requires authorization again. Unit tests reject another credential's response at upload issuance and omit it from the subject-facing authority snapshot. The recovery-only full root and PostgreSQL/Headgate race suites and scoped lint passed; this is not subject-facing visual acceptance.


Focused Go race tests cover the review domain/service, configuration and API boundary. They exercise tenant/region isolation, validity boundaries, assignment removal and malformed replacement, stable identity on key rotation, rejection of caller assertions, stale claims and second-reviewer certification. Restricted-role PostgreSQL review tests verify persistence, concurrency and atomic audit. Routing tests cover rollback after the complete effect, canonical/request replay, tenant isolation, immutable receipt/origin, revoked authority, expiry, missing configuration and wrong reservation; reconstructed adapters replay after authority expiry and configuration removal. Task tests prove saved routing bypasses evaluation and never invokes terminal completion, while terminal results still use the completion path. These routing-boundary fixtures seed terminal checks; they do not claim a complete public capture-to-review product journey. The root race suite and full PostgreSQL/Headgate integration suites pass. SQL generation is reproducible, public binaries build, and scoped lint reports no issues. Vulnerability scanning found no affected imported packages or called code (three uncalled required-module advisories remain). Accepted-finding tests additionally cover permitted reasons, dual-review disagreement, stale versions, changed input digests, missing completion provenance, revoked/wrong-case grants, authority withdrawal, atomic completion rollback, nonterminal persistence and exact replay. The task tests cover stable scheduling and restore-before-evaluate behavior. Linked-creation tests cover outbox-failure rollback, exact profile/policy pins, fresh child state, stale-version denial, immutable lineage and replay with the same or a different key. Policy-loader tests ensure active-policy lookup is bypassed for valid recapture pins. HTTP tests reject missing scopes and caller profile injection. Renewal tests cover expiry limits, complete transaction rollback, old-token revocation, exact replay, stale-token denial and rejection of an old-token response prepared before renewal. Status tests verify current credential metadata and child outcome visibility without modifying parent state; the outcome fixture seeds a committed child decision and does not establish full child execution. HTTP tests verify reference-only status without bearer disclosure. Acknowledgement tests cover incomplete child outcomes, wrong decisions, stale versions, atomic receipt/audit/idempotency rollback, original-attribution replay, immutable records and unchanged parent/workflow effects. HTTP/application tests cover caller identity rejection, revoked reviewer authority and clearing the follow-up flag after acknowledgement. The acknowledgement increment passes root race tests, scoped lint, public binary builds and reproducible SQL generation. An initial full integration run timed out in the offline expiry-worker test; the isolated test and a subsequent full PostgreSQL/Headgate race run passed. At that increment these were synthetic boundary tests and a full subject-facing recapture and consequential follow-up journey was not claimed; the 15 September composed increment below supersedes that limitation for one path.

**Explicit follow-up increment — 8 September 2026:** Synthetic PostgreSQL tests cover missing acknowledgement, wrong child decision, complete request rollback, next-version discovery, replay with original attribution, unchanged original facts and timestamps, changed-digest denial, terminal-completion rollback, nonterminal persistence and terminal completion/replay. HTTP/application tests reject injected reviewer identity and recheck revoked operator authority on retries. Combined root race tests, full PostgreSQL/Headgate race tests, scoped production lint, public binary builds, reproducible SQL generation and vulnerability scanning pass. A final focused PostgreSQL race regression also passes for original-fact collision rejection. No affected imported packages or called vulnerability paths were found; three uncalled required-module advisories remain. The child decision is seeded in this boundary fixture; these tests do not establish a complete live child execution or accepted reviewer/subject UI.

**Composed recapture increment — 15 September 2026:** A public-SDK-driven tenant fixture now creates and configures the original verification, lets an authorized operator claim the routed case, redeems a single-use protected evidence grant, records `request_input`, creates the linked child and hands its credentials to Capture Web without placing bearers in the URL. The child supplies fresh authorization, uploads new evidence, reaches its own verified decision, is acknowledged by the operator and triggers an explicit parent re-evaluation to the parent's terminal verified decision. The policy snapshot projects the immutable `review.recapture.requested` context fact for linked children; it is not an assurance assertion. The fixture's API key remains server-side and reviewer actions retain resolved assignment, permission, certification and region checks.

The serial real-Core Chromium suite passes all ten journeys, including this composed path and the existing hosted, embedded, active-liveness and terminal-outcome matrix. Focused Go regressions cover PostgreSQL-microsecond ordering across recapture creation, upload issuance, preflight and acceptance; policy tests cover bounded static fact-presence guards; and TypeScript tests cover RFC 9651 quoting of review idempotency keys. The captured completion screen is reviewable evidence, but explicit user-facing acceptance has not yet been recorded. Correction, appeal and recovery branches are not composed by this fixture, and no external certification issuer is integrated; O-03 therefore moves to **In review**, not complete.

## 9. Related documents

- [Repository and package authority](global-identity-core-repository-structure-and-packages-v0.1-draft.md)
- [Integrated architecture](global-identity-core-technical-architecture-v0.6-draft.md)
- [Implementation gap audit](global-identity-core-implementation-gap-audit-v0.1-draft.md)
