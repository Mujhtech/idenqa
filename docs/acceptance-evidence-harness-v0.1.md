# Acceptance evidence harness v0.1

**Status — 2 October 2026:** Implemented for provider and model production-acceptance records. No provider or model tuple is accepted by this implementation.

## 1. Boundary

`internal/acceptance` owns the Core-side record grammar, validation and canonical semantic digest. `idenqa acceptance validate` owns file input and the normalized receipt written for a downstream release gate. The harness records content-free references and aggregates; it does not collect evidence, execute a provider, sign a release, or confer acceptance.

The Cloud repository independently owns live-AWS, Console-human and aggregate release records. This keeps Cloud implementation and commercial release policy out of Core.

## 2. Closed record contract

The exact schema identifier is `idenqa.acceptance.provider.v1`. A record selects either the seven `PRV-*` provider gates or the nine `MOD-*` model gates from the [production-acceptance protocol](provider-model-production-acceptance-v0.1.md).

Every accepted record must bind:

- the exact adapter/model tuple, immutable artefact and configuration digests;
- an accountable UTC run and revalidation date;
- repository commit, signed build, SBOM and public-contract version;
- production account/environment, region, approved dataset, rights/consent, hardware, exclusions, thresholds, raw-report, failure and incident references;
- at least one aggregate metric with a population and unit;
- every mandatory check with a content-free evidence reference;
- residual risks with owners, controls, remediation and expiry; and
- product, provider/model, security, privacy/legal and operations approvals.

Allowed external references have the form `ref:<kind>:<opaque-id>`. URLs, filesystem paths, object keys, query strings and free-form bodies are not representable. Unknown JSON fields fail strict decoding. Consequently credentials, evidence bytes, provider payloads, biometric templates, subject or verification identifiers, extracted fields and raw logs have no record field. The referenced evidence system remains responsible for access control and retention.

An `accepted` or `accepted_with_expiring_restrictions` claim is invalid when a mandatory field, check, metric or approval is absent. Critical residual risk always blocks passing; High risk requires the expiring-restrictions decision. An `open` record may remain incomplete and never reports `passed: true`.

## 3. Operation

Start from [the open Dojah example](examples/provider-acceptance-record-v1.json), replace the zero digests and empty evidence only during an authorised live run, then validate it:

```sh
idenqa acceptance validate \
  --file docs/examples/provider-acceptance-record-v1.json
```

Use the CI/release form to require a complete accepted decision:

```sh
idenqa acceptance validate \
  --file <record.json> \
  --require-passed > provider-receipt.json
```

Output uses `idenqa.acceptance.receipt.v2` and contains record identity, kind, decision, pass state, canonical source-record `sha256:` digest, a `tuple_digest` derived from the exact public tuple and `credential_ownership` derived from the validated evidence. Tuple digest is SHA-256 of the UTF-8 compact JSON of `ProviderTuple` in its declared field order. `operator_managed` denotes a deployment/service operator holding the credential on a tenant's behalf; it is not a Cloud dependency or a provider-owned account. Cloud maps its Idenqa-managed mode to this portable value.

The receipt v2 change does not accept any production tuple or change provider/model semantics. Old v1 receipts lack scope metadata and must be regenerated from their original validated source records, not relabelled. Cloud release gates require a matching receipt for every claimed tuple/custody; public Core remains independently usable. Array ordering and JSON presentation do not change the digest; changed semantic meaning does.

## 4. Verification and remaining gates

Unit tests prove a valid accepted record, incomplete-pass rejection, pending-approval rejection, unknown/prohibited-field rejection, unsafe-reference rejection, stable order-independent digesting and changed-meaning sensitivity. Live credentials, an approved representative evidence set, independent review and every external sign-off remain unavailable and open.
