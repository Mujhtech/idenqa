# Identity, provider and document gap closure v0.1

**Status:** Proposed delivery sequence; implementation workstream requested on
22 September 2026. This plan does not select unresolved data, trust, provider or
production-assurance contracts and does not close any milestone.

## 1. Scope and authority

Close repository-owned gaps in identity/fraud/assurance, provider operations and
document processing, then collect their separately required acceptance evidence.
The [repository/package draft](global-identity-core-repository-structure-and-packages-v0.1-draft.md)
remains authoritative. The [gap checklist](global-identity-core-implementation-gap-audit-v0.1-draft.md#23-remaining-work-checklist)
records the broader outstanding boundaries.

Preserve existing user changes. Do not reopen implemented identity persistence,
fraud correlation, assurance snapshots, document parsers, provider callback/poll
convergence, health routing, or country-pack administration.

## 2. Contract gate before the first implementation slice

The repository explicitly leaves provider-specific structured identity extraction
and automatic ingestion TBD. Current provider document fields are transient:
Core derives document signals and drops raw fields before result fingerprinting
and persistence. Completed-check imports currently carry boolean observations.
An ingestion implementation must not silently convert extracted text into either
persistent identity values or verified facts.

**Selected — 22 September 2026:** Dojah document analysis first. Smile ID document
verification remains the proposed subsequent path. This selection preserves raw
document fields as transient and does not select persistent identity ingestion.
The following ingestion policy still requires confirmation before implementing a
new persistent field path:

- Which exact fields may be retained and under which processing purpose and
  subject authority; no implicit full-response capture.
- Whether ingestion is an explicit command or a tenant-opt-in automatic rule.
- The immutable provider/check/evidence provenance binding, field mapping version,
  retention bounds and correction/deletion behavior.
- The distinction between provider observation, derived claim/fact and independently
  substantiated assurance. Provider extraction alone must not verify an identifier.

Recommended starting boundary: explicit, tenant-authorised ingestion with a closed
field allow-list and existing encrypted identity storage; defer automatic ingestion
until replay, withdrawal, correction and deletion are proven for that same contract.
This recommendation is **Proposed**, not Selected.

## 3. Delivery batches

### 3.1 Provider inputs and execution integrity

- Inventory each selected route's supported input, recipient, purpose and region.
- Extend subject-input resolution beyond deployment-bound references only against
  an approved purpose-bound input contract.
- Define each additional grant operation before expanding grant permissions.
- Extend durable delivery/reconciliation with explicit ambiguous-outcome handling;
  never turn a status-only recovery into an accidental second submission/upload.
- Add duplicate-charge reconciliation and attribution before enforcing budgets
  against unverified price or billing assumptions.
- Implement hold-aware request/dispatch metadata expiry against selected retention
  policy without losing recovery or audit references.

Acceptance: tenant/purpose isolation, revoked/expired authority rejection, bounded
input handling, restart and external-success/local-failure tests, exact grant-use
accounting, replay conflicts, and no raw values or secrets in diagnostics.

### 3.2 Identity ingestion, fraud and assurance

- Publish and implement the approved structured-ingestion contract from section 2.
- Preserve encrypted original/normalised values, exact source ancestry and
  non-expanding authority; reuse existing versioned transformations first.
- Add provider-specific and country-specific transformations only with documented
  semantics and fixtures; do not introduce fuzzy equivalence implicitly.
- Define resumable incremental identity deletion above existing atomic limits,
  including holds, fencing, replay and bounded progress.
- Extend assurance field provenance/capability versions and graph limits only
  through versioned contracts; specify safe recovery when assurance expires before
  commit.
- Obtain tenant-approved fraud namespaces, trusted sources, thresholds and signal
  mappings. Do not expand into cross-tenant or similarity-search scope.

Acceptance: immutable provenance, independent-source counting, withdrawal and
expiry exclusion, encrypted storage, correction lineage, idempotent imports,
tenant/region isolation, historical decision reproduction, and deletion recovery.

### 3.3 Documents and provider extraction coverage

- Inventory canonical fields against documented Dojah and Smile ID response shapes.
- Extend mappings only where primary documentation and accepted fixtures establish
  field meaning, dates, missing-value behavior and response status.
- Add malformed, conflicting, partial and unsupported-field tests; retain
  inconclusive outcomes where evidence is insufficient.
- Keep raw fields transient except through the separately approved identity path.
- Integrate template/security-feature, manipulation/screenshot and portrait models
  only after model/provider capability and provenance are accepted.
- Update country-pack support levels only after structural sources, official-account
  evidence and required legal/assurance review substantiate the claim.

Acceptance: parser and field-mapping conformance, front/back consistency,
no invented authenticity/liveness/freshness, bounded transient data, no raw-field
leakage into persisted runner results or telemetry, and representative OCR/portrait
evaluation separate from synthetic parser tests.

### 3.4 Provider deletion, cost and production proof

- Select provider-supported deletion mechanics and receipts before scheduling
  external deletion; record exceptions and confirmation deadlines explicitly.
- Exercise retention and deletion after a complete verification journey.
- Reconcile duplicate charges and unknown outcomes using official-account billing
  evidence; enforce approved budget policy without promising exactly-once delivery.
- Run controlled outages and semantically equivalent replacement through the
  complete workflow without changing customer APIs or evidence meaning.

Acceptance: live callbacks/polling, recovery, approved recipient/purpose/region use,
external-copy deletion evidence and provider-account confirmation. Local fixtures
cannot close these external gates.

## 4. Decisions and external inputs still required

- Approved structured identity field/ingestion contract; first provider is selected
  as Dojah document analysis.
- Purpose-bound input catalogue and additional grant operations.
- Provider deletion support, billing data, budget/unknown-cost policy and account
  access; no credentials belong in this document or ordinary logs.
- Country normalisation semantics, incremental-deletion contract and assurance
  extension/recovery semantics.
- Accepted production models, licensed representative datasets and operating points.
- Fraud deployment configuration, legal/regional approval and country-pack sources.

## 5. Completion evidence

### 5.1 First Dojah extraction increment — 22 September 2026

Implemented fail-closed handling of conflicting valid duplicate mapped fields,
preserving provider quality semantics while suppressing ambiguous extracted data.
Added permutation, identical/partial-value and Core-consumption leakage tests;
response-buffer cleanup now also covers oversized/failed reads. No new persistent
identity values, provider fields, assurance claims or dependencies are introduced.
This closes the duplicate-precedence defect, not the three-area workstream.
Detailed boundaries are recorded in [provider runtime](provider-runtime-v0.1.md).
Verification: race tests pass for the Dojah adapter, document packages and
verification package; scoped adapter lint, vet and language-server diagnostics
pass. No live provider-account or new PostgreSQL acceptance is claimed.

### 5.2 Explicit Dojah document-side grants — 22 September 2026

Adapter 0.1.2 implements one front plus optional explicitly granted back, rejects
ambiguous side sets before redemption and avoids partial submission on read
failure/cancellation. The manifest version/source pin changes; old in-flight
requests require their pinned runner. No new fields are persisted and no additional
assurance is advertised. Worker-side back-grant preparation and its composed
recovery proof remain open; the existing front-only route is preserved.
Verification: race tests pass for Dojah, provider execution and runner/worker
composition; scoped lint, vet and language-server diagnostics pass. The
vulnerability scan reports no vulnerabilities and the checked-in source pin
matches the documented hash recipe. These checks are not live provider evidence.

### 5.3 Session-bound Dojah grant preparation — 22 September 2026

Worker preparation now resolves the required sides from the immutable session
profile and durable selected branch, checks route digest and purpose, and prepares
one-use grants for exactly those sides inside the existing planning transaction.
Smile ID behavior is unchanged. Profile-union membership or the mere presence of
an uploaded back does not authorise its release.

Ten focused selection cases and restricted-role PostgreSQL 16.8 race tests pass,
covering passport/licence choices, unselected branches, exact grant bindings,
missing-back rollback and downstream rollback. Provider package race tests, scoped
lint, vet and diagnostics pass. This supersedes §5.2's worker-preparation gap.
The existing public front-only capture-to-decision-to-webhook journey also passes
with race detection after aligning its fixture secret reference with the mounted
credential-file mode. No production credential behavior was relaxed.
A full two-sided public capture-to-webhook proof was subsequently completed in
§5.4. PostgreSQL 18.4 qualification and official-account evidence remain separate
acceptance work.

### 5.4 Composed two-sided journey and retry authority — 22 September 2026

The public two-sided journey now passes with distinct synthetic front/back bytes,
encrypted uploads, exactly two one-use grants, persisted request replay,
cross-tenant rejection, no persistent raw extraction fields, decision completion,
and signed webhook retry across worker replacement. Both completed grants reject
further redemption. This exposed and fixed a stale one-image-only scoped-runner
guard; adapter-side exact-side validation remains mandatory.

The failing path also exposed a separate guarded-commit defect: scheduling a retry
advanced the check timestamp into the future and incorrectly used that schedule
for authorization of the received result. The commit now uses the validated result
receipt's time. A deterministic restricted-role PostgreSQL regression fails before
the fix and passes afterward, including rejection of future-dated receipts and
idempotent replay. This is not permission to resubmit ambiguous provider work.

The broader provider/document integration suite passes with race detection on
PostgreSQL 16.8, covering registration, callbacks, polling, recovery and the public
Dojah/Smile journeys. Synthetic results are not official-account, production model,
country-support or legal acceptance evidence.

### 5.5 Remaining delivery gates

The following are not completed by §5.4 and must not be relabelled as external
acceptance alone:

| Boundary | Required before completion |
| --- | --- |
| Structured identity ingestion | Approve the §2 field/purpose/trigger/provenance/retention contract, then implement ingestion, correction, withdrawal and deletion tests. |
| Identity scale and assurance | Select incremental-deletion and assurance-extension/recovery contracts, then implement bounded durable planning and recovery. |
| Provider inputs and grants | Select additional purpose-bound input and operation contracts, then implement resolution and recovery without replaying unauthorized evidence. |
| Provider metadata expiry | Repository implementation remains open: hold-aware expiry must preserve immutable audit references and recovery dependencies under the selected retention policy. |
| Provider deletion and costs | Obtain supported deletion mechanics, account/billing evidence and approved budget policy; orchestration, reconciliation and enforcement remain implementation work. |
| Document inspection and broader mappings | Obtain documented field semantics, accepted models and licensed representative fixtures; implement and evaluate the supported mappings/models. |
| Fraud and country/assurance acceptance | Supply approved namespaces, sources, thresholds, country-pack substantiation and regional/legal mappings; synthetic fixtures cannot select them. |

No persistent extracted-field path or production support claim is selected by
the Dojah-first decision. These are explicit delivery dependencies, not a request
to choose a different workstream.

For each subsequent batch, record changed contracts, migration and compatibility
impact, focused unit/race/integration/conformance checks, failure-injection results,
and remaining external gates in the owning guide and gap audit. Verify formatting,
generation drift, lint and vulnerability checks as applicable. Preserve any
unrelated repository-wide failures as explicit limits rather than claiming a clean
release gate.

No batch is complete solely because a plan, interface, fixture or passing synthetic
test exists. The entire three-area workstream remains open until its implementation
and applicable independent production acceptance criteria are satisfied.
