# Idenqa Webhook Event Contract v1

This package owns the public, versioned webhook event catalogue and the canonical
reference-only envelope selected on 18 September 2026.

## Envelope

Every event is canonical compact JSON with exactly these fields:

```json
{
  "id": "evt_01M11HEQG00000000000000000",
  "type": "evidence.ready",
  "schema_version": "1.0",
  "created_at": "2026-09-18T09:00:00Z",
  "tenant_id": "ten_01M11HEQG00000000000000000",
  "region": "eu-west",
  "data": {
    "evidence_id": "evd_01M11HEQG00000000000000000",
    "verification_id": "ver_01M11HEQG00000000000000000"
  }
}
```

`data` carries stable resource references plus safe summary fields — status,
outcome, revisions, reason codes, timestamps, assurance lists, masks and
counts. It is redacted at construction: raw evidence, biometric templates,
identity values, credentials, provider payloads, and free-form personal data
are never signed, sent, or stored, so retries and replays re-send exactly the
same redacted body. Receivers retrieve authoritative detail through the API.

## Versioning

`schema_version` is `major.minor`. Evolution is additive within a major version:
new optional data fields may appear; required fields, field meaning, and the
envelope shape cannot change. An incompatible change requires a new major
`schema_version` and a new fixture directory. Per-endpoint payload-version
pinning is not selected for v1.

## Layout

- `catalogue.go` — closed event types with required/optional data fields and
  subscription validation (exact names or the single `*` entry, maximum 64).
- `envelope.go` — canonical encode, parse, validate, and SHA-256 digest.
- `envelope.schema.json` — JSON Schema for the envelope.
- `events/<type>.schema.json` — closed JSON Schema per event type.
- `fixtures/<type>/<version>.json` — canonical compatibility fixture per event
  type and version.

`contract_test.go` proves the catalogue, schemas, and fixtures agree, that
fixtures are canonical, and that unknown types, missing/extra data fields,
malformed identifiers, and trailing content are rejected.
