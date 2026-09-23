# Public SDK administration resources v0.1

**Implementation date:** 22 September 2026. This increment covers 30 published
operations in the resource groups below. It does not claim complete SDK coverage
of every Core endpoint, production provider/model acceptance, or milestone closure.

## 1. Public surface

| Resource | TypeScript tenant client | Go client methods |
| --- | --- | --- |
| Decision history and reconsideration | `decisions.history`, `decisions.reconsider` | `ListVerificationDecisions`, `CreateVerificationReconsideration` |
| Evidence metadata and lifecycle | `evidence.get`, `evidence.lifecycle` | `GetEvidence`, `ListEvidenceLifecycle` |
| Processing grants | `evidence.createGrant`, `evidence.getGrant`, `evidence.revokeGrant` | `CreateEvidenceAccessGrant`, `GetEvidenceAccessGrant`, `RevokeEvidenceAccessGrant` |
| Consent inspection and withdrawal | `consents.get`, `consents.revoke` | `GetConsentReceipt`, `RevokeConsentReceipt` |
| Impact assessments | `proposals.createImpactAssessment`, `proposals.getImpactAssessment`, `proposals.listImpactAssessments` | `CreateProposalImpactAssessment`, `GetProposalImpactAssessment`, `ListProposalImpactAssessments` |
| Privacy requests | `privacy.createRequest`, `privacy.getRequest`, `privacy.listRequests`, `privacy.approveRequest`, `privacy.denyRequest`, `privacy.withdrawRequest`, `privacy.executeRequest` | `CreatePrivacyRequest`, `GetPrivacyRequest`, `ListPrivacyRequests`, `ApprovePrivacyRequest`, `DenyPrivacyRequest`, `WithdrawPrivacyRequest`, `ExecutePrivacyRequest` |
| Processing restrictions | `privacy.listRestrictions`, `privacy.liftRestriction` | `ListPrivacyRestrictions`, `LiftPrivacyRestriction` |
| Disclosures | `privacy.createDisclosure`, `privacy.listDisclosures` | `CreatePrivacyDisclosure`, `ListPrivacyDisclosures` |
| Processor inventory | `privacy.createProcessor`, `privacy.getProcessor`, `privacy.listProcessors`, `privacy.updateProcessor` | `CreatePrivacyProcessor`, `GetPrivacyProcessor`, `ListPrivacyProcessors`, `UpdatePrivacyProcessor` |
| Deletion and retention reads | `privacy.listDeletions`, `privacy.getDeletion`, `privacy.retention` | `ListDeletions`, `GetDeletionStatus`, `GetRetentionResolution` |

The clients call the existing [public OpenAPI contract](../contracts/api/openapi/v1/openapi.yaml).
They do not introduce model-health semantics, OAuth credentials, or new server routes.
Older deletion-run and legal-hold write routes are not described by that contract
and are not represented as covered by this increment.

## 2. Compatibility and authority

- The Go resource types are generated directly from public OpenAPI using
  [the scoped configuration](../sdk/go/oapi-codegen.yaml). The standalone SDK has
  no external dependencies and imports no Core `internal` packages.
- TypeScript adds handwritten resource methods and named public types. Generated
  contract symbols remain an implementation detail; no Effect runtime is required.
- Resource documents retain public snake_case names, except decision-history
  reports, which reuse the existing TypeScript camelCase report mapping.
- Both SDKs preserve cancellation, request IDs, ETags and Location metadata.
  Go exposes stable problem codes through `APIError.Problem.Code` and accepts
  additive response fields while bounding response bodies to 1 MiB.
- Calls perform one request. Grant issue/revoke, consent withdrawal and
  reconsideration require caller-owned idempotency keys. Privacy transitions use
  their documented expected-version and replay semantics. Clients must not infer
  retry-key support for privacy or impact-assessment creation.
- Tenant clients cannot create subject consent. Withdrawal appends a refusal;
  it cannot rewrite the original receipt. Reconsideration opens an independent
  correction workflow and remains subject to operator authority.
- Evidence methods expose safe metadata and purpose-bound grants, never raw
  evidence bytes, storage locations or redemption credentials.
- Pagination retains the server's cursor meaning. Clients must keep filters and
  limits unchanged when following signed privacy cursors. Decision history uses
  an exclusive decision ID; impact history uses an exclusive RFC3339 timestamp.

## 3. Verification and remaining evidence

The focused SDK tests cover all 30 request paths, verbs and mutation-header
contracts, query encoding, response metadata, invalid input, cancellation, stable
errors and forward-compatible Go decoding. The Go client also refuses redirects
and absolute request destinations that could leak tenant credentials.

[The public integration proof](../test/integration/sdk_administration_test.go)
extends the existing isolated PostgreSQL/real-HTTP evidence journey and runs the
published TypeScript bundle against the same resources. On 22 September the focused
`TestPublicEvidenceUploadFlowThroughRunnableLocalAPI` passed, including a separate
race-enabled run, against local PostgreSQL 16.8 with a restricted runtime role.
This proves the synthetic journey,
not qualification of the selected PostgreSQL 18.4 distribution. It covers grant
issue/revoke replay and conflicts, privacy restriction execution/lifting,
processor optimistic concurrency, signed privacy pagination, immutable decision
history, operator-authority rejection and append-only consent withdrawal.

Package verification passed: TypeScript typecheck, 77 tests (one separate
environment-gated smoke test skipped), build, publication checks and generation
drift; Go SDK race tests, lint, vet, module verification and vulnerability scan.
The scan reported no vulnerabilities. Root integration compilation passed;
whole-integration-package lint still reports pre-existing findings outside this
increment and is not claimed clean.

The live proof required correcting test-role grants for pack reads, temporal-frame
writes and lifecycle-audit reads. It also exposed two existing blockers, corrected
in place: migration 76 referenced a nonexistent tenant-scope function, and worker
route admission omitted transaction-local tenant scope. A separate startup-error
cleanup hang (pool closure before listener shutdown) remains outside this SDK scope.
Privacy regions currently use the persistence format such as `ng`, not dotted
evidence-region references; the API can return 500 for the latter, so server-side
validation alignment remains follow-up work.

Reproduce the live proof with a configured local `DATABASE_TEST_URL`:

```sh
go test -race -tags=integration ./test/integration -run '^TestPublicEvidenceUploadFlowThroughRunnableLocalAPI$' -count=1 -timeout=120s
```

Repository verification now includes the Go SDK in generation, formatting, lint,
tests, module verification and vulnerability checks. The SDK guides document the
new methods and retry semantics: [Go](../sdk/go/README.md) and
[TypeScript](../sdk/typescript/README.md).

Full server-side rollback/failure-injection coverage, successful independent-review
acceptance and production privacy execution remain separate evidence boundaries.

## 4. Remaining work

- **SDK coverage:** broader typed Go endpoint parity beyond the 49 methods covered here and deeper provider/model adapter conformance helpers. The 19 registry-administration methods in §5 are implemented; they are not adapter execution helpers. Older deletion-run and legal-hold writes still need published contracts before SDK exposure.
- **Acceptance evidence:** successful independent-review authorization and production privacy execution; comprehensive server rollback/failure-injection coverage; qualification on the selected PostgreSQL 18.4 distribution. The passing synthetic proof is not a substitute for these checks.
- **Server follow-ups found during verification:** startup-error listener/pool cleanup ordering and privacy-region validation alignment. These are not missing SDK methods.
- **TBD contracts:** public model-health semantics and OAuth client-credential trust/lifecycle. API keys remain selected; this increment makes no new architecture decision.

Status is synchronized with the [repository/package boundary](global-identity-core-repository-structure-and-packages-v0.1-draft.md#141-sdks),
[build plan](global-identity-core-build-plan-v0.1.md#current-position), and
[gap audit](global-identity-core-implementation-gap-audit-v0.1-draft.md#18-public-api-and-authentication).

## 5. Go provider/model administration extension

On 22 September 2026, the Go SDK adds 19 methods over existing published contracts,
bringing its typed administration surface to 49 operations. This extension does
not change the TypeScript facade, adapter contracts, or server behavior.

| Resource | Go client methods |
| --- | --- |
| Model reads | `GetModel`, `GetModelRevision`, `ListModelHistory` |
| Model commands | `RegisterModel`, `SetModelThreshold`, `ActivateModel`, `RollbackModel`, `RetireModel`, `ValidateModel` |
| Provider reads | `ListProviderRegistrations`, `GetProviderRegistration`, `GetProviderRegistrationHealth` |
| Provider commands | `CreateProviderRegistration`, `UpdateProviderRegistration`, `ValidateProviderRegistration`, `EnableProviderRegistration`, `DisableProviderRegistration`, `RotateProviderRegistrationCredential`, `SimulateProviderFailure` |

Model commands remain evaluation-only. Provider creation and the five model
mutation commands require caller-owned idempotency keys. Provider replacement,
enable/disable and credential rotation retain their published body-level
`expected_version` fields; the client does not invent retry-key support.
Credential rotation carries secret-manager references, not credential values.
Model history uses an exclusive numeric registry-version cursor; provider lists
retain opaque cursor pagination.

The [extension tests](../sdk/go/registry_test.go) cover all 19 paths and verbs,
request-body presence, exact retry-key headers, query encoding, response metadata,
invalid names/revisions/cursors, credential-reference and expected-version
preservation, and stable conflict errors. Valid model names containing adjacent
dots are accepted while parent-path traversal remains rejected.

Verification for this extension: Go SDK race tests, scoped lint and vet pass;
the vulnerability scan reports no vulnerabilities.
These are request-contract/package checks, not a new live provider/model
integration or production-acceptance proof. The §3 live proof remains evidence
for the original 30-operation increment only.
