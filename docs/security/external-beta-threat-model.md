# External-beta threat model

**Status:** Reviewed implementation baseline; independent penetration-test
results are still required before a sensitive production beta.

**Scope:** Idenqa Core, public contracts, capture Web, Swift/Kotlin/TypeScript/Go
SDKs, PostgreSQL, evidence storage, Headgate workers, provider/model runners,
Smile ID and Dojah adapters, webhooks, migrations, backup and recovery.
Console and managed Cloud are outside this open-source beta boundary.

## Security objectives

1. A tenant cannot observe or affect another tenant's identities, evidence,
   policy, provider credentials, work, decisions, audit, or recovery state.
2. Raw evidence and structured identity values cross only an authorised,
   purpose-bound path and never enter ordinary logs, traces, tasks, WebSocket
   messages, audit payloads, or provider-control envelopes.
3. Capture method and provider output cannot be promoted to an assurance they
   did not establish.
4. At-least-once work and external retries cannot create a second semantic
   effect or overwrite a newer fenced result.
5. A release consumer can verify source, checksum, SBOM, and build provenance,
   and can recover without Console, Cloud, or proprietary access.

## Trust zones and sensitive flows

| Zone              | Trusted for                                                  | Not trusted for                                        |
| ----------------- | ------------------------------------------------------------ | ------------------------------------------------------ |
| Capture client    | Live acquisition and local advisory quality checks           | Establishing PAD, face match, or document authenticity |
| Public API        | Authentication, tenant/purpose checks, immutable intent      | Long-lived raw evidence handling                       |
| Evidence boundary | Bounded encrypted storage and purpose-bound redemption       | General provider or policy decisions                   |
| PostgreSQL        | Authoritative workflow, idempotency, audit, and coordination | Protecting plaintext without application/KMS controls  |
| Headgate worker   | Versioned, fenced orchestration                              | Carrying PII or raw evidence in task payloads          |
| Provider runner   | Resolving one credential/input/grant and normalising output  | Tenant-wide secret or evidence access                  |
| External provider | Contracted processing for one declared purpose/region        | Becoming policy authority or changing evidence meaning |
| Release workflow  | Building immutable public artefacts and attestations         | Holding production tenant or provider credentials      |

## Reviewed threats and controls

| Threat                                          | Required control and verification                                                                                       |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------- |
| Cross-tenant object reference                   | Mandatory application tenant scope, forced RLS, indistinguishable denial, integration tests                             |
| Capture-token theft/replay                      | Short lifetime, application/proof-key binding, single redemption, generic denial                                        |
| Uploaded image represented as live              | Immutable acquisition method; assurance compatibility rejects impossible claims                                         |
| Synthetic/replayed liveness frames              | Ordered server-authored challenge transcript; provider PAD result remains separately required                           |
| Poor or malformed image                         | Bounded media/size/dimension checks, local retry guidance, provider quality signal                                      |
| Evidence exfiltration through control planes    | Reference-only contracts, bounded schemas, safe-shape tests, redacted telemetry                                         |
| Provider credential disclosure                  | Tenant-owned external secret references resolved only inside the runner; no secret values in manifests                  |
| Authority identifier disclosure                 | Provider contract v1.1 opaque input references; values resolved inside the runner                                       |
| Provider request forgery/tampering              | TLS, runner workload authentication, Smile ID HMAC request/response verification                                        |
| Provider outage substituted with weaker meaning | Explicitly approved fallback with exact check/country/evidence/assurance/authority/recipient/region/purpose equivalence |
| Duplicate external execution                    | Stable attempt/idempotency identity, Smile ID deterministic job ID and status reconciliation, fenced local commit       |
| Malicious/oversized provider response           | Context deadline, bounded response body, closed stable result and failure vocabularies                                  |
| Callback spoof/replay                           | Provider authenticity verification before lookup; exact attempt mapping and inbox deduplication                         |
| Queue lease loss or stale worker                | Headgate lease plus Idenqa effect fence; old worker cannot commit                                                       |
| Audit mutation/reordering                       | Tenant hash chain, checkpoints, export verification, reference-only payloads                                            |
| Backup rollback resurrects deleted data         | Deletion tombstones, replay before access, 35-day backup boundary, restore reconciliation                               |
| Dependency or build compromise                  | Locked modules, vulnerability and licence gates, SBOM, checksums, signed provenance, immutable tags                     |
| Unreviewed schema downgrade                     | Same-major/supported-minor compatibility, generated-contract diff gates, fail-closed unknown security meaning           |

## Abuse and privacy cases

- Repeated capture attempts are rate- and session-bounded and cannot turn a
  capture token into general upload access.
- A tenant cannot use the adapter runner for an undeclared provider operation;
  the attempt pins its capability and configuration schema.
- Provider free-form messages, confidence values, and undocumented fields do
  not drive policy. Only closed normalised signals do.
- The initial country catalogue is not a claim that all documents or residents
  are supported. Unsupported paths require an accessible fallback or human
  handling without falsely returning `not_verified`.
- Biometric and authority processing requires a current processing authority,
  recipient, region, purpose, provider agreement, and retention/deletion terms.

## Open external-assurance gates

The following cannot be satisfied by repository code alone and block a
production-beta declaration:

1. An independent penetration test of the deployed topology with no unresolved
   critical finding and verified remediation of high findings.
2. Provider sandbox/production certification using tenant-owned Smile ID and
   Dojah accounts, representative consented/synthetic evidence, and provider
   confirmation of callback, reconciliation, retention, and deletion behaviour.
3. Jurisdictional and cross-border review for every enabled tenant/provider
   route.
4. Production-shaped load and soak results, including database, storage,
   worker, WebSocket, webhook, and provider degradation.
5. A documented incident exercise for credential compromise, evidence breach,
   provider outage, signing-key compromise, and destructive migration rollback.
