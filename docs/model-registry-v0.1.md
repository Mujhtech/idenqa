# Evaluation model registry v0.1

## 1. Selected scope and authority

**Selected — 8 September 2026:** One tenant API key with `models:activate`, optimistic version checks and audit may activate, retire or roll back an **evaluation-only** model deployment. `models:write` permits immutable model and threshold registration; `models:read` permits inspection. Application authorization precedes idempotent replay. Existing keys retain their original permission snapshots; adding a permission to the registry does not expand an issued key.

Model registration records owner, licence, training provenance, intended/prohibited uses, regions, hardware class, complete manifest and configuration pins. These are operator declarations, not independently verified rights or production acceptance. Production activation is unavailable: both model and threshold records require `evaluation_only: true`, and native inference continues to emit inconclusive signals.

## 2. Durable contracts and ownership

`internal/model` owns commands, validation and application authorization. Its PostgreSQL adapter owns migration 43, tenant-scoped roots, independently numbered immutable model/threshold revisions, immutable command history, atomic audit/outbox and idempotent receipts. No new dependencies are introduced.

Every successful command increments the registry root's `version`. Registration never implicitly activates a model. All mutations require the current `expected_version`; a stale command conflicts. Retry with the same key and meaning returns the original receipt, actor and timestamp. Activation binds a model revision, threshold revision and declared region. Rollback appends history and requires that exact pair/region to have been active before. Retirement clears the selection while retaining immutable lookup and history.

Thresholds include the exact model/runtime/preprocessing/output-schema provenance and configuration reference, score name/range/direction/cutoff, and an evaluation-report digest. Activation rejects mismatched configuration or provenance. Cutoffs must be finite and inside the declared range. The report digest records a reference; registration alone does not ingest or verify the report's contents.

The restricted runtime role requires:

```sql
GRANT SELECT, INSERT, UPDATE ON idenqa.model_registries TO your_runtime_role;
GRANT SELECT, INSERT ON idenqa.model_registry_revisions, idenqa.model_registry_history TO your_runtime_role;
```

All three tables use forced RLS in addition to explicit tenant predicates. Immutable tables reject updates/deletes, and downgrade refuses to discard populated history. Command state, revision, history, audit, outbox and replay receipt share one serializable transaction.

## 3. Core HTTP operations

These implemented Core routes are not yet represented in generated OpenAPI/SDK contracts. Bodies are closed, case-sensitive JSON: include every field of the indicated model/threshold/manifest structure. Missing, duplicate and unknown fields are rejected. Responses use `Cache-Control: no-store`; mutation requests require `Idempotency-Key`.

| Operation | Route | Permission | Body |
| --- | --- | --- | --- |
| Register model revision | `POST /v1/models/{name}/register` | `models:write` | `expected_version`, `reason`, `registration` |
| Register threshold revision | `POST /v1/models/{name}/threshold` | `models:write` | `expected_version`, `reason`, `thresholds` |
| Activate pair | `POST /v1/models/{name}/activate` | `models:activate` | `expected_version`, `reason`, `deployment` |
| Roll back pair | `POST /v1/models/{name}/rollback` | `models:activate` | Same as activation |
| Retire deployment | `POST /v1/models/{name}/retire` | `models:activate` | `expected_version`, `reason` |
| Current state | `GET /v1/models/{name}` | `models:read` | — |
| Exact revision | `GET /v1/models/{name}/revisions/{model\|threshold}/{revision}` | `models:read` | — |
| Command history | `GET /v1/models/{name}/history?before=0&limit=50` | `models:read` | — |

Names and reasons are lowercase tokens up to 64 characters. History is descending by version, with a limit of 1–100; use the last returned version as `before` for the next page. A deployment body contains `model_revision`, `threshold_revision` and `region`. Mutation responses contain the resulting state, any newly registered revision, original actor, operation, reason and replay status.

## 4. Worker and evaluation integration

An operator may add `binding.registry` to an existing mounted model runtime binding:

```json
{
  "name": "pad",
  "model_revision": 1,
  "threshold_revision": 1,
  "model_digest": "sha256:<registered-model-revision-digest>",
  "threshold_digest": "sha256:<registered-threshold-revision-digest>"
}
```

The digest fields identify registry metadata revisions, not just weight files. Preparation locks the registry root in the same transaction that creates grants and immutable requests, verifies the active pair/region and exact mounted manifest/configuration, and rejects retired or changed selections. The immutable model request row stores the exact registry selection and a foreign-key reference to its deployment-history version, separately from the runner envelope. Existing saved requests continue to use their original execution envelope; activation cannot rewrite them. Legacy mounted routes remain available without a registry pin. Dynamic hot selection, automatic runner provisioning and automatic fallback are not implemented by this pin check.

A current readiness probe precedes each new dispatch. Not-ready, degraded, stale, future-dated or failed health responses prevent inference and produce a safe unavailable result. Durable saved results replay without a new probe. This is per-dispatch supervision, not warm-pool management or kernel isolation.

Export an immutable threshold revision through the exact-revision route, then run:

```sh
model-runner --config runner.json --evaluate-dataset dataset.json --threshold-revision threshold.json
model-runner check-evaluation --report report.json --gate gate.json
model-runner check-drift --baseline baseline.json --current current.json --limits drift-limits.json
```

The first command verifies the exported revision digest, exact runtime/model/configuration, PAD `real_score` meaning and dataset cutoff. Set the dataset's `threshold_reference` to `threshold-sha256-` followed by the threshold revision's 64 hexadecimal digest characters. It never silently substitutes a cutoff. This optional pin supports the existing PAD evaluator; face-match pair evaluation remains separate work.

The gate document requires version/reference, dataset/configuration/threshold pins, minimum genuine/attack/subject counts, maximum APCER/BPCER and genuine/attack nonresponse rates, and whether known subject IDs are required. It recomputes rates from counts, checks applicable capture groups and reports explicit failed reasons with report/gate digests. Failure exits nonzero. Even a passing gate reports `production_accepted: false`; experimental limits are operator inputs and have no approved defaults.

`check-drift` compares labelled aggregate populations under identical model/configuration/threshold meaning, recomputes rates from counts and emits pinned reports with alerts for changes, insufficient populations or missing capture groups. Limits require a reference, minimum genuine/attack counts and a maximum absolute rate change. Alerts exit nonzero. This is an offline monitoring primitive; it does not ingest production observations, establish statistical significance or change deployment state.

## 5. Verification and remaining work

Registry integration tests exercise restricted-role tenant isolation, immutable revision lookup, exact replay, stale versions, rollback eligibility, retirement fencing and whole-transaction rollback after an injected outbox failure, competing activation commands, direct unscoped RLS reads and immutable attempt registry pins. Unit tests reject production mode, cross-runtime thresholds, undeclared regions, nonfinite/out-of-range cutoffs, tampered exported revisions and unavailable readiness. Gate tests cover fabricated rate fields, missing denominators, missing groups, subject gaps and pin mismatches.

**Verification — 8 September 2026:** Root race tests, scoped regression tests, full PostgreSQL/Headgate race tests, production lint, command builds and repeatable SQL generation pass. The vulnerability scan reports zero affected code paths (three uncalled required-module advisories). Native ONNX journey tests require `ONNX_TEST_PYTHON` and were not rerun in this environment.

The broader data-independent MLOps batch is **not yet complete**. Live shadow/canary orchestration, automated technical rollback, persisted drift monitoring, hardened OCI/kernel enforcement and generated registry contracts remain. Hardware deployment selection is pending; actual hardware validation cannot be replaced by a manifest declaration. Accepted models, representative datasets, calibrated production thresholds, rights/impact review and accuracy acceptance also remain open. Manual-review progress-preserving recovery is the next product workflow after the agreed ML batch.

## 6. Related documents

- [Repository and package authority](global-identity-core-repository-structure-and-packages-v0.1-draft.md#69-model)
- [ONNX runtime](onnx-runtime-v0.1.md)
- [PAD evaluation](pad-evaluation-v0.1.md)
- [Implementation gap](global-identity-core-implementation-gap-audit-v0.1-draft.md#4-predictive-models-and-mlops)
