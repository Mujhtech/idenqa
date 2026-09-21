# Portable capture experience v1 contract

`contracts/experience/v1` is the selected portable capture-experience contract.
One immutable document revision describes the tenant copy, the Core-owned
mandatory-copy reference, targeting rules, vetted assets, links, and safe theme
tokens that a capture client renders.

The contract is implemented twice so it can be validated locally:

- Go: `contracts/experience/v1` (`contract.go`, `validate.go`, `canonical.go`, `signature.go`).
- TypeScript: `sdk/typescript/src/experience.ts`, exported from `@idenqa/sdk`.

Both implementations agree byte-for-byte on canonical form:

1. object keys sorted by UTF-8 byte order;
2. no insignificant whitespace;
3. integers only, base-10, without leading zeros;
4. strings escaped only where JSON requires it (quote, backslash, control
   characters with short escapes where available, otherwise `\u00xx`).

`DigestDocument` is lowercase SHA-256 over those canonical bytes. A manifest is
`{document, digest, key_id, algorithm, signature}` where `signature` is an
Ed25519 signature over the identical canonical bytes. Clients verify digest,
key id, and signature; unknown keys, tampered documents, and non-canonical
envelopes fail closed.

## Bounds

| Bound | Value |
| --- | --- |
| Document bytes | 256 KiB |
| Locales | 16 |
| Copy entries per locale | 64 |
| Copy value bytes | 2048 |
| Assets | 16 |
| Asset bytes | 2 MiB |
| Custom links | 8 |
| Allowed origins | 16 |
| Targeting rules | 32 |

Tenant copy must never claim the reserved Core-owned namespaces
`regulatory.`, `consent.`, `safety.`, or `accessibility.`. Assets are closed to
`kind: image` and the MIME allow-list `image/png`, `image/jpeg`, `image/webp`,
and `image/avif`; SVG and every executable or script-capable content type is
rejected. Links and allowed origins must be HTTPS.

## Test vectors

`testdata/document.json` and `testdata/manifest.json` are shared conformance
vectors consumed by both language test suites. The fixed digest is
`4b2a9f0e945fc29804640eef71527f1da012bf8d20fa90c1d01eaccc54f59855`.
