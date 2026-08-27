# Capture-profile contract v1

This directory defines the portable capture-profile and evidence-registry
documents used by Idenqa Core. Persistence and HTTP resources are deliberately
outside this contract and begin in later build bricks.

## Registry and extension rules

- A profile pins `schema_version`, registry `revision`, and the registry's
  `sha256:<lowercase hex>` canonical content digest.
- Idenqa-owned vocabulary uses `idenqa.<kind>.<name>`, where `<kind>` is
  `evidence`, `artefact`, `method`, `purpose`, `assurance`, or `constraint`.
- Extension vocabulary uses an owner-controlled namespace containing at least
  two segments, for example `com.example.method.secure_camera`.
- A registry revision is immutable. Changing the meaning of an existing entry
  requires a new name or revision and produces a new digest.
- A profile may reference only entries present in its pinned registry. Unknown
  entries fail validation; an SDK capability advertisement cannot register an
  entry or prove that assurance was achieved.
- Extensions cannot use or nest below the reserved `idenqa` namespace.
- Constraint values are deliberately bounded to strings, non-negative
  integers, booleans, and unique string lists. Richer values require a future
  schema version rather than arbitrary executable or ambiguous JSON.

## Requirement semantics

Requirements are ordered. Artefacts and required assurances are sets.
Acquisition-method order is significant: it is preference order for `any_of`
and execution order for `all_of`.

Every branch of `any_of` must independently produce the required artefacts and
establish the required acquisition assurance. For `all_of`, every method must
produce the artefacts and the methods' assurance capabilities are combined.
Fallbacks use the same validation rules and therefore cannot weaken required
assurance.

The built-in v1 registry contains synthetic definitions for document-front,
document-back, and selfie images acquired by file upload or live camera. File
upload establishes no freshness, live-capture, liveness, or capture-integrity
assurance. Live camera may establish acquisition freshness, live capture, and
capture-path integrity; a still-image camera method alone does not establish
passive or active liveness.

Canonical serialization uses UTF-8 JSON without insignificant whitespace.
Object fields follow the schema order. Set-valued artefacts, assurances,
constraint entries, fallback conditions, and string-list constraint values are
lexically sorted. Requirement, acquisition-method, and fallback order is
preserved because it is semantic. The content digest is SHA-256 over those
canonical bytes and is encoded as `sha256:<lowercase hex>`.
