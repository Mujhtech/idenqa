# Provider adapter contract v1

This package is the public, transport-independent provider adapter contract.
An attempt pins the adapter version and digest, contract version, advertised
capability, execution restrictions, provider registration, credential-reference
version, idempotency key, deadline, trace context, and scoped evidence-grant
redemptions.

Credentials, credential values, structured subject values, raw evidence,
object-store locations, tenant keys, and unrestricted provider payloads cannot
be represented. Version 1.1 adds named opaque input references so authority
identifiers can be resolved inside the isolated runner without placing those
values in tasks, traces, logs, or transport envelopes. A capability declares
the input names it accepts; an input-only authority lookup does not require a
dummy evidence grant. Results use
bounded stable signals or a redacted failure classification. Contract
compatibility requires the same major version; an implementation may consume
requests at or below its supported minor version.

The public conformance harness is in `conformance/provider`. The Protobuf/gRPC
and HTTP-Protobuf mappings share the authoritative schema under
`contracts/runner`; adapters remain transport independent.

## Built-in adapter identifiers

Go code that names an adapter shipped by Idenqa uses the canonical contract
constants rather than repeating provider-name string literals:

```go
providerv1.AdapterDojah
providerv1.AdapterSmileID
```

Their wire and configuration values are `dojah` and `smileid`, respectively.
They are convenience constants for the built-in adapters, not a closed enum.
Third-party adapters may continue to publish any identifier accepted by the v1
contract validation rules.
