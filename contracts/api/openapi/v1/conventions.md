# Idenqa Core HTTP API v1 conventions

This document explains the reusable rules encoded by `openapi.yaml`. The OpenAPI document is authoritative for wire shapes; this document is authoritative for behaviour that OpenAPI cannot express completely.

## Versioning and compatibility

- The URI major version (`/v1`) is the compatibility boundary.
- Additive endpoints, optional request fields, response fields, and enum values may be introduced within v1 only where clients are required to tolerate them.
- Removing or renaming operations or fields, narrowing accepted input, widening required input, or changing established semantics requires a new URI major version unless preserving the old behaviour would retain a security or correctness defect.
- Public OpenAPI changes must pass Vacuum linting, reproducible generation, and oasdiff against the pull request base contract.
- Generated transport types and clients are committed. They do not become domain models and do not own authentication or authorisation.

## JSON and problems

- JSON request objects reject unknown fields unless an operation explicitly documents an extension object.
- Successful single-resource responses contain the resource directly.
- Collections use `{ "data": [...], "page": { ... } }`; `data` is always an array and never `null`.
- Errors use `application/problem+json` with the required Idenqa `code` and server-issued `request_id` extensions to RFC 9457 problem details.
- Internal error text and cross-tenant resource existence are never disclosed.

## Pagination

- Collections use opaque cursor pagination with `cursor` and `limit` query parameters.
- The default limit is 25 and the maximum is 100 unless an operation documents a lower bound.
- Ordering uses an authoritative persisted field followed by the resource identifier as a stable tie-breaker.
- Cursors are integrity protected, tenant- and query-bound, and may expire. Clients must store and replay them unchanged.
- `page.has_more` is required. `page.next_cursor` is present only when another page is available.

## Idempotency

- Consequential `POST` operations require exactly one `Idempotency-Key` encoded as an RFC 9651 String.
- The key is scoped to the authenticated tenant and operation. The server binds it to a canonical request fingerprint.
- Repeating the same key and fingerprint returns the original completed result. A concurrent duplicate is reported as a conflict. Reusing a key with a different fingerprint is `IDEMPOTENCY_CONFLICT`.
- Idempotency records are durable PostgreSQL state. The retention period is operation-specific and must be documented before the operation is published.
- Keys are not secrets, but clients must not embed credentials, subject data, or evidence data in them.
- `POST /v1/capture/connections` is the deliberate exception: replaying a stored response would replay display-once secret material. A retry issues a fresh independently bounded ticket instead of accepting an idempotency key.

## Realtime connection bootstrap

Native clients first redeem their tenant-backend-issued token once through
`POST /v1/capture/native/bootstrap`. The request follows the public native
bootstrap v1 proof contract and binds the token to a configured application
identity and hardware-backed P-256 proof-key digest. Arbitrary identity headers
are never accepted as authentication.

- Browser clients authenticate `POST /v1/capture/connections` with the capture token. The server infers tenant and verification authority from that principal and binds the issued ticket to exactly one configured `Origin` value.
- Missing, duplicated, malformed, or disallowed origins fail before ticket issuance. CORS response policy is not a substitute for this application binding.
- The public WebSocket endpoint and regional data-plane identity are trusted deployment configuration. They are never derived from `Host`, forwarding headers, browser locale, device language, or IP address.
- The successful response is `Cache-Control: no-store`. Its `websocket_url` contains a single-use secret and must not enter application, proxy, access, analytics, or tracing logs.
- This operation has no request body and no idempotency key. Requesting another ticket is the safe recovery path after an ambiguous response, connection failure, or expiry.

## Conditional mutation

- Mutable resources return a strong `ETag` derived from their authoritative optimistic version.
- Mutating an existing resource requires exactly one `If-Match` value previously returned for that resource.
- A missing precondition is `PRECONDITION_REQUIRED`; a stale or mismatched entity tag is `PRECONDITION_FAILED`.
- A successful mutation returns the new `ETag`. Wildcard `If-Match` is not accepted unless an operation explicitly documents it.

## Evidence uploads

- Capture clients first create a requirement-bound intent with `POST /v1/evidence-uploads`, then send one complete raw JPEG or PNG body to `PUT /v1/evidence-uploads/{uploadID}`. Both operations use the session-bound capture token.
- The upload request requires a positive `Content-Length`, one exact canonical `Content-Type`, one canonical RFC 9530 SHA-256 `Content-Digest`, and one strong `If-Match`. Encoded, ranged, chunked, trailer-based, parameterised, duplicate, or multi-digest variants are rejected before an attempt is claimed.
- An interrupted attempt restarts from byte zero with the intent's latest ETag. A concurrent repeat of the currently claimed version conflicts. A completed repeat may use either the precondition used by the accepted attempt or the terminal ETag and returns the existing result without reading or storing the repeated body.
- Integrity digests are request claims and restricted internal metadata. Upload resources expose neither the digest nor an object-storage location.

## Request, rate-limit, and lifecycle headers

- Every API response carries a newly server-issued `X-Request-ID`. Caller-supplied values are ignored and are not used as authority.
- Rate-aware responses may carry `RateLimit` and `RateLimit-Policy` structured fields. A rejected request also carries `Retry-After` when the server can provide a useful delay.
- These fields report enforcement; they do not select the distributed enforcement implementation, which remains a separate operational decision.
- Deprecation uses the RFC 9745 `Deprecation` structured date, an RFC 8594 `Sunset` date when retirement is planned, and a `Link` relation to migration documentation.
