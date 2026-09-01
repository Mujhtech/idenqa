# Idenqa Go SDK

The Go SDK is dependency-light and uses an injected `net/http.Client`. It
includes the public API client and the v1 webhook verifier.

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
