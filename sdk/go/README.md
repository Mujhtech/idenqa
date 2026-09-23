# Idenqa Go SDK

The Go SDK is dependency-light and uses an injected `net/http.Client`. It
includes the public API client and the v1 webhook verifier.

Typed administration methods cover decision history and reconsideration, safe
evidence metadata and lifecycle, evidence-access grants, consent inspection and
withdrawal, proposal impact assessments, privacy requests/restrictions/disclosures,
processor inventory, deletion status, retention resolution, provider registration
administration and the evaluation-only model registry. Types are generated
from published OpenAPI using `sdk/go/oapi-codegen.yaml` and import only the standard
library. Run `make generate` from the repository root to refresh them. Other API
resources can be called through `DoJSON`, but that is not typed SDK coverage.
The [public SDK resource guide](../../docs/public-sdk-resources-v0.1.md) records
the exact 49-operation Go scope, verification evidence and remaining work.

```go
client, err := idenqa.NewClient(baseURL, tenantAPIKey, &http.Client{Timeout: 10 * time.Second})
if err != nil {
    return err
}
history, err := client.ListVerificationDecisions(ctx, verificationID, idenqa.DecisionHistoryOptions{Limit: 20})
if err != nil {
    return err
}
// history.Data.NextBefore is the exclusive cursor for the next history page.
// history.RequestID, ETag, Location and StatusCode preserve HTTP metadata.
```

Every operation accepts a context and performs one request. Mutations whose
contracts require `Idempotency-Key` take `MutationOptions`; persist and reuse the
same key and input after an ambiguous failure. Privacy operations use their
documented expected versions and replay rules instead. Privacy-request, processor,
disclosure and impact-assessment creation have no idempotency-key contract; do not
blindly retry an ambiguous creation failure. `APIError` exposes the HTTP status,
request identifier and stable `Problem.Code`; its message excludes response details.
Responses tolerate additive fields and are bounded to 1 MiB. Redirects are refused.

Provider creation and model register/threshold/activate/rollback/retire commands
require `MutationOptions`. Provider replacement, enable/disable and credential
rotation use the published `expected_version` body fields instead. Credential
rotation accepts secret-manager references, never credential values. Model
activation remains evaluation-only; no production model or model-health contract
is introduced. `ModelHistoryOptions.Before` is an exclusive registry-version
cursor, with zero selecting the newest receipt.

These are tenant-backend operations. Consent withdrawal appends a refusal; it never
rewrites the original receipt. Subject consent creation remains capture-credential
bound. Evidence methods return safe metadata and grants, not evidence bytes.

Webhook consumers must verify the signature over the exact body bytes before
decoding JSON, reject timestamps outside their chosen window, and durably
deduplicate successful handling by `Idenqa-Event-ID`. Idenqa delivery is
at-least-once: a timeout or crash may cause the same event ID to arrive again.
During secret rotation, construct the verifier with the active secret followed
by the still-valid overlap secret; remove the old secret when the configured
overlap closes.

Provider, model, and policy engine implementors use the public conformance
packages at `conformance/provider`, `conformance/model`, and
`conformance/policy`. Those packages do not expose Idenqa internal types.
