# Native capture bootstrap v1

Native SDKs redeem the tenant-backend-issued single-use capture token at
`POST /v1/capture/native/bootstrap`. The token remains only in the
`Authorization` header. The request body contains the SDK capability
advertisement and the proof-key metadata below.

The SDK obtains the application identifier from the installed application,
generates a non-exportable hardware-backed P-256 signing key, and signs this
canonical byte sequence:

```text
idq-native-bootstrap\0v1\0
<application-id>\0POST\0/v1/capture/native/bootstrap\0
<created-unix-seconds>\0<sha256-token-hex>\0<sha256-capability-tuple-hex>
```

The request sends `application_id`, `proof_key` (base64url of the platform
public-key encoding), `proof_created_at`, `proof_algorithm` equal to
`ES256`, `proof_format` equal to `der`, `proof`, optional attestation, and the
capability advertisement. The Core verifies the proof and configured native
application identity, atomically binds the single-use token to the proof-key
digest on first successful redemption, and rejects replay or changed-key
redemption without disclosing which check failed.

The capability tuple is `platform\0sdk-version\0implemented\0available`.
Each method list is sorted by code point and joined with U+001F before hashing.
Capability tokens are restricted to ASCII letters, digits, `.`, `_`, and `-`.

Platform attestation is an optional adapter. It strengthens application or
device integrity policy but does not replace proof-of-possession and must not
silently exclude a subject when the tenant profile permits a fallback.

The fixtures in this directory are the shared conformance source for both
native SDKs. Raw evidence and private keys never appear in this contract.
