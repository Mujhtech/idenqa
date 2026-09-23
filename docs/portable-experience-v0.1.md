# Portable capture experience v0.1

**Status:** Draft
**Scope:** Selected portable capture-experience design for the open-source core.
**Companion decisions:** Recorded here for the first time; this document does not
override the integrated architecture, repository/package draft, or build plan.

The portable experience configuration system makes capture presentation
tenant-configurable without weakening assurance, notice semantics, or
accessibility. It is usable with self-hosted Core alone: Console and Cloud are
not required to author, approve, publish, export, import, or resolve an
experience.

## Selected design

1. **Contract.** One closed, bounded canonical JSON document per immutable
   revision, with Go and TypeScript types, validators, canonical bytes, and
   SHA-256 digests (`contracts/experience/v1`, `sdk/typescript/src/experience.ts`).
2. **Lifecycle.** `draft -> approved -> published -> superseded | revoked`.
   Publication requires an explicit approval of the exact latest version.
   Rollback republishes a prior immutable approved revision. Revocation is the
   kill switch. Every transition is expected-version, state-idempotent, and
   audited.
3. **Copy.** Structured tenant copy catalogues keyed by locale plus Core-owned
   mandatory regulatory, consent, safety, and accessibility copy that tenant
   copy cannot override. Tenant-copy and mandatory-copy versions are independent
   and both pinned per session.
4. **Assets.** Vetted image references with size, MIME, content digest, and
   exact object-store version, verified through the owned object-store port
   before publication or import. Executable and script-capable content is
   rejected.
5. **Links.** Validated HTTPS support, privacy, and terms links, optional custom
   links, and allowed-origin declarations.
6. **Targeting.** Workflow, country, application id, origin, and SDK-version
   range. Most-specific published match wins deterministically; equally specific
   conflicts fail closed to the signed safe default.
7. **Manifest signing.** Ed25519 over canonical document bytes with a Core-owned
   key id derived from the deployment keyring. Clients verify digest, key id,
   and signature before rendering.
8. **Session pinning.** At session creation Core resolves and pins the
   experience id, immutable version, locale, tenant-copy version, and
   mandatory-copy version. Resume preserves the pin. The capture bootstrap
   resolution and capture progress projection expose the pinned values.
9. **Safe default.** Core ships a signed, accessible safe-default experience.
   Fallback order is pinned experience -> newest published match -> signed
   default. Revocation, kill switch, missing revisions, tampered digests, and
   resolution failure always fall back to the signed default, never to
   unbranded or unsigned tenant copy.
10. **Export/import.** Canonical signed manifests export and import without
    Console or Cloud. Import validates the closed document, recomputes the
    digest, verifies the signature against a trusted key, checks mandatory-copy
    and asset references, and creates or advances a draft.

## Contract inventory

| Item | Location |
| --- | --- |
| JSON Schema (closed, bounded) | `contracts/experience/v1/experience.schema.json` |
| Go types, validator, digest, signature | `contracts/experience/v1/{contract,validate,canonical,signature,errors}.go` |
| Shared conformance vectors | `contracts/experience/v1/testdata/{document,manifest,exported-manifest}.json` |
| TypeScript types and validator | `sdk/typescript/src/experience.ts` (exported from `@idenqa/sdk`) |
| SDK capture resolution client | `CaptureClient.getExperience` in `sdk/typescript/src/client.ts` |

Bounds: document 256 KiB, 16 locales, 64 copy entries per locale, 2 KiB copy
value, 16 assets, 2 MiB per asset, 8 custom links, 16 allowed origins, 32
targeting rules, 32 allowed MIME-bound image formats (`image/png`, `image/jpeg`,
`image/webp`, `image/avif`). Canonical form sorts object keys by UTF-8 byte
order, emits no insignificant whitespace, uses integers only, and escapes only
what JSON requires. Both languages must produce identical bytes; the shared
vector fixes the digest family.

Reserved mandatory-copy namespaces: `regulatory.`, `consent.`, `safety.`,
`accessibility.`. Tenant copy claiming them fails validation with
`experience v1: reserved mandatory copy key`.

## Lifecycle state inventory

| Layer | Value | Meaning |
| --- | --- | --- |
| Aggregate | `draft` | Latest revision is not approved. |
| Aggregate | `approved` | Latest revision is approved and awaiting publication. |
| Aggregate | `published` | The latest approved revision is live. |
| Aggregate | `revoked` | The kill switch cleared the live revision. |
| Revision | `draft`, `approved`, `published`, `superseded`, `revoked` | Immutable per-version content with a constrained state-transition allow-list. |

Only `draft -> approved`, `approved -> published`, `published -> superseded`,
`published -> revoked`, and `superseded -> published` revision transitions are
accepted by the database trigger. Document, digest, key id, signature, actor,
and creation time are immutable. Version state changes are the only permitted
revision update.

Expected-version preconditions use the aggregate `revision` counter. Repeating
an already-effective approve, publish, revoke, or rollback is an idempotent
no-op that appends no duplicate history. Editing an approved experience resets
`approved_version`, because approval binds to one exact version. A revoked
experience refuses edits until an explicit rollback republishes a prior
approved revision.

## Permission inventory

| Permission | Allows |
| --- | --- |
| `experiences:read` | List, get, export, and read the signed safe default. |
| `experiences:write` | Create drafts, save new draft revisions, and import manifests. |
| `experiences:publish` | Approve, publish, revoke, and roll back. |

## API inventory

| Method and path | Auth | Behaviour |
| --- | --- | --- |
| `GET /v1/experiences` | `experiences:read` | Cursor page of tenant experiences. |
| `POST /v1/experiences` | `experiences:write` | Create a draft with a generated `exp_` id and signed revision 1. |
| `GET /v1/experiences/{id}` | `experiences:read` | Aggregate state, optimistic revision, latest signed document. |
| `PUT /v1/experiences/{id}` | `experiences:write` | Save a new immutable draft revision with `expected_version`. |
| `POST /v1/experiences/{id}/approve` | `experiences:publish` | Explicit approval of the latest revision. |
| `POST /v1/experiences/{id}/publish` | `experiences:publish` | Publish the exactly approved revision; supersede the prior live one. |
| `POST /v1/experiences/{id}/revoke` | `experiences:publish` | Kill switch with a required bounded reason. |
| `POST /v1/experiences/{id}/rollback` | `experiences:publish` | Republishes a prior approved immutable version. |
| `GET /v1/experiences/{id}/export` | `experiences:read` | Signed canonical manifest. |
| `POST /v1/experiences/import` | `experiences:write` | Validate and import a signed manifest as a draft. |
| `GET /v1/experience-default` | `experiences:read` | Read the signed accessible safe default. |
| `GET /v1/capture/experience` | capture token | Resolve, pin, and return the signed resolution plus pinned versions. |

`GET /v1/capture/experience` accepts optional `workflow`, `country`,
`application_id`, `origin`, `sdk_version`, and `locale` query parameters used
only when no pin exists yet; the exact origin is never inferred from a header.
The response carries `Cache-Control: no-store`. The capture progress projection
(`GET /v1/capture/progress`) exposes the same pinned values as an optional
`experience` object when a pin exists.

## Persistence inventory (migration 67)

| Table | Purpose |
| --- | --- |
| `idenqa.experiences` | Tenant aggregate: state, optimistic revision, latest/approved/published version. |
| `idenqa.experience_revisions` | Immutable signed documents with constrained version-state transitions. |
| `idenqa.experience_targeting` | Append-only targeting projection for operational matching. |
| `idenqa.experience_events` | Append-only lifecycle history (create, update, approve, publish, revoke, rollback, import). |
| `idenqa.experience_session_pins` | Immutable per-session pin of experience, locale, tenant-copy, and mandatory-copy versions. |

All five tables have `ENABLE`/`FORCE ROW LEVEL SECURITY`, a `tenant_scope`
policy on `current_setting('idenqa.tenant_id')`, and `REVOKE ALL FROM PUBLIC`.
Consequential writes append the tenant audit chain inside the same transaction
through `internal/audit/postgres`. Revisions, targeting rows, events, and pins
reject mutation; revisions permit only the version-state allow-list.

## Fallback and pinning behaviour

Resolution is deterministic and fail-closed:

1. If a session pin exists, the pinned revision is served even after a newer
   publication. A superseded pinned revision stays renderable.
2. If the pinned experience is revoked, missing, or its revision state is
   revoked, or if any resolution step fails, the signed accessible safe default
   is served with `fallback: true`.
3. Without a pin, the newest published most-specific match is selected and
   pinned. An empty dimension never satisfies a rule that constrains it, so
   unproven targeting does not match.
4. Equally specific matches from different experiences return an ambiguity and
   fall back to the signed default.
5. Revocation and kill switch never fall back to another tenant experience.

The safe default is signed at service construction and verified before the
service accepts traffic; a deployment that cannot sign or verify it refuses to
start the experience service.

## Mandatory copy

The Core-owned catalogue ships as a reviewed embedded file
(`internal/experience/mandatory/default_v1.json`, version `mc-2026-09-01`)
covering processing notice, retention notice, explicit consent, safety
guidance, and accessibility support. Documents reference a catalogue version;
the resolver serves the entries with a canonical digest. New versions are
additive files. Unknown versions fail closed.

## Assets and links

Asset references are verified by reading the exact object-store version through
the owned object-store port and comparing size and SHA-256 digest. Publication
and import fail closed when verification is unavailable, including deployments
without an object store (`experience.DenyAssets`). Links and allowed origins
must be HTTPS, bounded, credential-free, and (for origins) pathless. The safe
default intentionally references no assets.

## Capture Web wiring

The hosted server-side bootstrap resolves `GET /v1/capture/experience` with the
session capture token and includes the signed resolution in the bootstrap
payload. `IdenqaCaptureElement.start` accepts that resolution and:

- selects the pinned locale for package-owned UI copy;
- overlays tenant copy only for a closed allow-list of `ui.*` keys, so notices,
  consent, safety, and assurance semantics are unchanged;
- applies safe theme tokens to `--idq-capture-*` variables only where the host
  has not already set them, preserving the existing appearance-only branding
  surface;
- exposes validated links, mandatory copy, fallback state, and the pin through
  the read-only `experience` getter.

`captureExperiencePresentation` and `applyCaptureExperienceTheme` are exported
from `@idenqa/capture`.

## CLI

`idenqa experience list | get | validate | publish | approve | revoke |
rollback | export | import | resolve`. Lifecycle commands and export/import use
the tenant API; `validate` and `resolve` are local, bounded, and require no
Core, Console, or Cloud. Export prints the signed manifest; import posts a
bounded manifest file as a draft.

## Implemented

- Closed contract, dual-language validator and digest, shared conformance
  vectors, Ed25519 manifest signing and verification.
- Domain aggregate, expected-version lifecycle with approval gate, idempotent
  transitions, rollback, kill switch, and append-only history.
- Targeting resolver, locale copy catalogue, mandatory-copy separation, asset
  and link validation, session pin resolution, export/import.
- Migration 67 with forced RLS, immutable revisions, append-only history,
  targeting projection, session pins, and atomic audit.
- Tenant API with `experiences:read|write|publish`, capture-token resolution,
  and the signed safe default; OpenAPI and generated Go/TypeScript clients.
- Session creation pins experience and copy versions; resume preserves pins;
  capture progress exposes them.
- Capture Web bootstrap wiring, presentation derivation, and host-first theme
  application.
- Capture Web opaque-sRGB theme contrast diagnostics for body/secondary text,
  button and hover text, and focus rings, with unrounded WCAG 2.x ratios and
  explicit unsupported-colour findings. Computed default light/dark palettes
  and host overrides are browser-tested. Experience colours are applied inside
  the shadow cascade so built-in defaults do not mask them and host branding
  continues to win; restart clears the prior experience stylesheet.
- CLI inspection and lifecycle commands.
- Unit, route, CLI, contract, TypeScript, and joined-DB integration tests,
  including a session pin and revocation-to-safe-default proof.

## Gated

- Managed visual editor and Console publishing UI (commercial consumer).
- DNS-verified custom domains beyond the allowed-origin declaration.
- Tenant asset upload route and moderation workflow. Asset verification is
  implemented; tenant-facing asset authoring is not.
- Core publication-time contrast enforcement and whole-page accessibility
  certification; the Web authoring/conformance palette audit is implemented.
- Native SDK rendering of the resolved document beyond locale and copy.

## Open items

- Safe-default support, privacy, and terms URLs are placeholders
  (`https://idenqa.dev/...`) until the deployable link set is confirmed.
- The allowed-origin list is enforced at resolution and documented, but the
  capture-connection ticket origin binding remains the authoritative browser
  origin control.
- Targeting disambiguation currently fails closed on equal specificity; an
  explicit tie-break, if ever selected, must be added to the contract and
  documented here rather than in the resolver alone.
- Rollback of a revoked revision is deliberately refused; if operational
  recovery needs republishing a revoked revision, that is a new contract
  decision.
- Mandatory-copy entries are served with a digest but are not separately
  signed; clients trust the Core TLS boundary for mandatory copy while the
  experience manifest remains independently verifiable.

## Verification evidence

- `go test ./contracts/experience/... ./internal/experience/...` — contract,
  lifecycle, targeting, fallback, session-pin, and export/import tests.
- `go test ./internal/transport/httpapi ./internal/verification ./internal/bootstrap/idenqa`
  — route, session-pin wiring, and CLI tests.
- `corepack pnpm --filter @idenqa/sdk test` — shared-vector digest and manifest
  verification.
- `corepack pnpm --filter @idenqa/capture test:unit` — presentation derivation.
- `DATABASE_TEST_URL=... go test -race -tags=integration -count=1 -run
  'TestPortableExperiencePinningAndRevocationFallback|TestPostgreSQLFoundation'
  ./test/integration/...` — migration pairing, forced RLS, session pinning, and
  revocation fallback against PostgreSQL.
