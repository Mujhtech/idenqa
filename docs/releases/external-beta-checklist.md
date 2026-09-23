# External-beta release checklist

No release may be labelled external beta until every mandatory item has an
owner, immutable evidence link, review date, and pass result. `N/A` requires a
written threat-based justification.

## Build and supply chain

- [ ] The tag is signed, protected, immutable, and matches the reviewed commit.
- [ ] `make verify` passes from a clean checkout with Go 1.27.1 and the pinned Node/pnpm toolchain.
- [ ] Generated OpenAPI, Protobuf, SQL, TypeScript, and lock files are unchanged.
- [ ] Root, S3 adapter, both S3 distribution binaries (`api` and `worker`), and SDK dependency graphs are verified.
- [ ] Reachable vulnerability, SAST, secret, and licence-policy gates pass.
- [ ] Release archives, checksums, SPDX SBOMs, and GitHub/Sigstore provenance are published.
- [ ] A second clean environment verifies checksums and provenance before install.

## Compatibility and recovery

- [ ] Current and previous supported API, event, provider, model, policy, and SDK versions pass compatibility tests.
- [ ] Mixed API/worker versions complete, release, or quarantine work without guessing.
- [ ] Expand-migrate-contract rehearsal passes at production-like scale with rollback criteria recorded.
- [ ] A new environment restores PostgreSQL, evidence, keys, and audit from backup and passes reconciliation.
- [ ] The 35-day backup/deletion boundary and tombstone replay are exercised.

## Capture and SDKs

- [ ] Plain HTML and React examples work against self-hosted Core without Console or Cloud.
- [ ] TypeScript, Swift, and Kotlin SDK conformance, interruption, cancellation, cleanup, and compatibility suites pass.
- [ ] Supported iOS and Android devices pass camera permission, rotation, backgrounding, low-memory, accessibility, and poor-network tests.
- [ ] Document front/back and live-selfie quality retry guidance is usable with screen readers and does not claim biometric assurance.
- [ ] Package metadata, minimum platform versions, changelog, migration guide, SBOM, and provenance are published.

## Providers and country packs

- [ ] Reviewed Smile ID and Dojah manifests match the enabled account catalogue and agreements.
- [ ] Official sandboxes or provider-approved fixtures cover success, no-record, invalid input, authentication, throttling, transient failure, callback/polling, reconciliation, and deletion.
- [ ] Nigeria, Ghana, Kenya, and South Africa each have a complete tested path for the declared initial documents and authorities.
- [ ] Controlled provider outage proves only policy-approved, semantically equivalent fallback.
- [ ] Tenant credential rotation, revocation, least privilege, regional routing, retention, and deletion are exercised.

## Resilience and performance

- [ ] Production-shaped load test meets the approved API, upload, WebSocket, worker, and webhook SLOs.
- [ ] A 24-hour minimum soak shows bounded memory, connections, goroutines, queues, database growth, and replay storage.
- [ ] Database, object store, worker, provider, telemetry, and network failures produce bounded safe diagnostics.
- [ ] Backpressure and overload reject safely without dropping authoritative intent.

## Security and operations

- [ ] The external-beta threat model has current owners and no unaccepted critical risk.
- [ ] Independent penetration testing has no unresolved critical findings; high findings have approved treatment and remediation verification.
- [ ] Cross-tenant, authorisation, upload, SSRF, callback, replay, request-smuggling, resource-exhaustion, and secret-leak tests pass.
- [ ] Alerts, dashboards, privacy-safe telemetry, backup/restore, provider outage, key compromise, and rollback runbooks are exercised.
- [ ] Security contact, coordinated disclosure process, supported-version policy, and patch SLA are published.

## Open-source usability

- [ ] A clean operator can deploy, migrate, create a tenant/key/policy, complete synthetic capture, inspect the decision/audit chain, simulate provider failure, recover, and export without undocumented database access.
- [ ] No Console, Cloud, proprietary endpoint, licence server, account, or phone-home dependency is required.
- [ ] Apache-2.0 notices and third-party attribution ship with every applicable artefact.
