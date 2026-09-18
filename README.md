# Idenqa (eye-DEN-kah)

Idenqa is an open-source identity-verification core. The repository is currently being built in small, reviewable bricks from the accepted architecture documents in [`docs`](docs/).

## Development commands

Go 1.27.1 is the selected Go toolchain. TypeScript work uses Node.js 22.18+ and the repository-pinned pnpm release through Corepack. From the repository root:

```sh
corepack pnpm install --frozen-lockfile
make db-up
make migrate
make verify
make build
./bin/idenqa version
./bin/api
```

With the API running, its health endpoints are available locally:

```sh
curl --fail --show-error http://127.0.0.1:8080/livez
curl --fail --show-error http://127.0.0.1:8080/startupz
curl --fail --show-error http://127.0.0.1:8080/readyz
```

Each returns `ok` while its condition is healthy. During graceful drain, readiness changes to HTTP 503 before the server shuts down.

### API configuration

The API accepts typed `IDENQA_*` process variables:

| Variable                                     | Default                | Purpose                                                                                                                              |
| -------------------------------------------- | ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `IDENQA_ENVIRONMENT`                         | `production`           | Runtime safety mode: `production`, `development`, or `test`                                                                          |
| `IDENQA_DATABASE_URL`                        | Required               | PostgreSQL URL; treated as secret configuration and never logged                                                                     |
| `IDENQA_DATABASE_ROLE`                       | Empty                  | Optional validated runtime role entered by each pooled connection; it must not own tenant tables or bypass RLS                       |
| `IDENQA_DATABASE_ADMIN_URL`                  | Empty                  | Separate privileged URL for migrations and audited CLI administration; do not provide it to API or worker deployments                |
| `IDENQA_DATABASE_MAX_CONNECTIONS`            | `20`                   | Maximum API PostgreSQL pool size                                                                                                     |
| `IDENQA_DATABASE_MIN_CONNECTIONS`            | `2`                    | Minimum API PostgreSQL pool size                                                                                                     |
| `IDENQA_DATABASE_MAX_LIFETIME`               | `1h`                   | Maximum lifetime of a pooled connection                                                                                              |
| `IDENQA_DATABASE_MAX_IDLE_TIME`              | `15m`                  | Maximum idle duration of a pooled connection                                                                                         |
| `IDENQA_DATABASE_CONNECT_TIMEOUT`            | `5s`                   | Startup and migration connection deadline                                                                                            |
| `IDENQA_DATABASE_MIGRATION_TIMEOUT`          | `5m`                   | Per-statement migration timeout                                                                                                      |
| `IDENQA_DATABASE_HEALTH_INTERVAL`            | `10s`                  | Runtime database readiness-check interval                                                                                            |
| `IDENQA_DATABASE_HEALTH_TIMEOUT`             | `2s`                   | Runtime database readiness-check deadline                                                                                            |
| `IDENQA_API_KEY_ACTIVE_PEPPER_VERSION`       | Empty                  | Version used to authenticate newly issued API keys; configure together with the pepper set                                           |
| `IDENQA_API_KEY_PEPPERS`                     | Empty                  | Comma-separated `version=unpadded-base64url` 32-byte HMAC peppers; values are redacted and retained while matching keys remain valid |
| `IDENQA_API_KEY_ALLOW_NO_EXPIRY`             | `false`                | Whether an explicit no-expiry issuance choice is permitted                                                                           |
| `IDENQA_API_KEY_MAXIMUM_LIFETIME`            | Empty                  | Optional maximum fixed API-key lifetime; empty leaves fixed expiries unbounded but still explicit                                    |
| `IDENQA_API_KEY_MAXIMUM_ROTATION_OVERLAP`    | Empty                  | Maximum predecessor/successor overlap; required when API-key issuance is composed                                                    |
| `IDENQA_HTTP_HOST`                           | `127.0.0.1`            | HTTP bind host or interface                                                                                                          |
| `IDENQA_HTTP_PORT`                           | `8080`                 | HTTP listen port                                                                                                                     |
| `IDENQA_HTTP_TLS_MODE`                       | `disabled`             | `disabled` for plain HTTP or `file` for direct TLS                                                                                   |
| `IDENQA_HTTP_TLS_CERT_FILE`                  | Empty                  | PEM certificate chain used when TLS mode is `file`                                                                                   |
| `IDENQA_HTTP_TLS_KEY_FILE`                   | Empty                  | PEM private key used when TLS mode is `file`                                                                                         |
| `IDENQA_HTTP_MAX_BODY_BYTES`                 | `1048576`              | Maximum request-body size before route-specific limits                                                                               |
| `IDENQA_HTTP_REQUEST_TIMEOUT`                | `10s`                  | Default HTTP request deadline                                                                                                        |
| `IDENQA_HTTP_CORS_ALLOWED_ORIGINS`           | Empty                  | Comma-separated exact browser origins; empty denies cross-origin requests                                                            |
| `IDENQA_EVIDENCE_UPLOAD_MAXIMUM_BYTES`       | `16777216`             | Deployment evidence-upload ceiling; validated from 1 MiB through 64 MiB and tenant profiles may only narrow it                       |
| `IDENQA_EVIDENCE_UPLOAD_INTENT_LIFETIME`     | `15m`                  | Upload-intent lifetime; validated from 5 through 60 minutes                                                                          |
| `IDENQA_EVIDENCE_UPLOAD_ATTEMPT_TIMEOUT`     | `10m`                  | Whole-body attempt deadline; validated from 1 through 15 minutes                                                                     |
| `IDENQA_EVIDENCE_UPLOAD_ALLOWED_MEDIA_TYPES` | `image/jpeg,image/png` | Deployment media allow-list; v1 permits canonical JPEG and PNG values and tenant profiles may only narrow it                         |
| `IDENQA_EVIDENCE_LOCAL_DIRECTORY`            | Empty                  | Existing root directory for the root API's local immutable ciphertext store; configure together with the local keyring file          |
| `IDENQA_EVIDENCE_LOCAL_KEYRING_FILE`         | Empty                  | Existing mounted local KEK keyring; configure together with the local evidence directory                                             |
| `IDENQA_EVIDENCE_PROTECTION_CLEANUP_TIMEOUT` | `5s`                   | Bounded cancellation-independent deadline for exact staged-ciphertext compensation                                                   |
| `IDENQA_SHUTDOWN_TIMEOUT`                    | `10s`                  | Graceful-shutdown deadline                                                                                                           |
| `IDENQA_LOG_LEVEL`                           | `info`                 | `debug`, `info`, `warn`, or `error` logging threshold                                                                                |
| `IDENQA_LOG_FORMAT`                          | `json`                 | Structured `json` or `text` output                                                                                                   |

Production startup reads only the process environment. For explicit local or test loading, copy [`.env.example`](.env.example), edit it, and run:

```sh
./bin/api --env-file .env
```

Existing process variables take precedence over values in that file. Unknown `IDENQA_*` variables are rejected to catch configuration mistakes, and secret-like structured log attributes are redacted.

The `worker` additionally accepts validated reconciliation controls. Defaults
are `IDENQA_WORKER_RECONCILIATION_SWEEP_INTERVAL=1m`,
`IDENQA_WORKER_RECONCILIATION_BATCH_SIZE=100`,
`IDENQA_WORKER_RECONCILIATION_ITEM_LEASE=2m`, and
`IDENQA_WORKER_PROGRESS_POLL_INTERVAL=1s`. Its PostgreSQL pool must provide at
least two connections because one connection is dedicated to the optional
notification hint while durable queries remain the correctness path.

The core `api` enables evidence-upload routes only when both local evidence paths are configured. It then owns the filesystem ciphertext store and mounted keyring lifecycle. Production S3 deployments use the independently versioned `github.com/Mujhtech/idenqa/distributions/s3` composition module, which injects the same owned object and key ports without adding cloud SDKs to the root module.

Create the local keyring once before starting an evidence-enabled API:

```sh
idenqa evidence-key init --keyring-file /run/secrets/idenqa/evidence-keyring.json
```

The parent directory must already exist and be writable by the setup operator. Initialization creates an owner-only file atomically and refuses to replace any existing target. The command prints only the active key version; it never prints key material, the keyring identifier, or the configured path. Store or mount the resulting file as a secret, configure the same path through `IDENQA_EVIDENCE_LOCAL_KEYRING_FILE`, and back it up according to the deployment recovery policy. Losing every retained KEK version makes its evidence permanently unreadable. CLI rotation is intentionally not included yet because rotation cadence, fleet rewrap completion, recovery validation, and retirement approval remain unresolved operational policy.

### Public HTTP contract

The OpenAPI 3.1 source for v1 is [`contracts/api/openapi/v1/openapi.yaml`](contracts/api/openapi/v1/openapi.yaml). It currently describes the protected tenant walking resource and the reusable problem, pagination, idempotency, conditional-mutation, request-correlation, rate-limit, and deprecation conventions. Generated Go code under `internal/gen/openapi/v1` is a transport contract, not a domain model or an SDK implementation.

```sh
make contract-lint
make generate
make generate-check
make contract-breaking OPENAPI_BASE=/path/to/base-openapi.yaml
make proto-breaking PROTO_BASE='.git#branch=main'
```

Vacuum and Buf linting plus reproducible OpenAPI and Protobuf generation run as part of `make verify`. Pull requests compare the proposed OpenAPI and runner Protobuf documents to the base revision with oasdiff and Buf. Shared synthetic payloads live under `contracts/api/openapi/v1/fixtures` for handler and SDK conformance tests. Behaviour that OpenAPI cannot fully express is documented in [`contracts/api/openapi/v1/conventions.md`](contracts/api/openapi/v1/conventions.md).

### TypeScript SDK

The open-source TypeScript SDK lives in [`sdk/typescript`](sdk/typescript/) and is published as `@idenqa/sdk`. It provides a zero-runtime-dependency tenant client for capture-profile, verification, notice, and processing-authority operations; a capture-token client for retrieving immutable session requirements, displaying the exact authority notice, recording acknowledgement, consent, or refusal, issuing requirement-bound upload intents, and sending raw JPEG/PNG `Blob` evidence directly to ingress; and a separate outcome-token client restricted to the subject-safe outcome projection. Its public API uses `Promise`, `AbortSignal`, Fetch, stable errors, request IDs, and explicit idempotency keys; it does not require Effect.

The open-source Web capture package lives under [`capture/web`](capture/web/) and is published as `@idenqa/capture`. Its renderer-independent planner applies immutable session requirements to explicit host capabilities while preserving tenant `any_of`, `all_of`, artefact, and reason-specific fallback policy. The explicitly registered Lit Web Component renders that plan with semantic, keyboard-operable controls; its programmatic capture flow retrieves and correlates the immutable session and exact notice, records acknowledgement, consent, or refusal through the public SDK, and withholds capture methods until the response permits collection. Capture and outcome tokens enter only through the programmatic trusted-bootstrap boundary and never enter attributes, rendered state, events, URLs, storage, or logs; the outcome token is used only for the read-only subject projection. Vite and Playwright are development-only fixture and browser-test tools, tsdown remains the library builder, and Effect is not a dependency.

```sh
corepack pnpm generate:check
corepack pnpm typecheck
corepack pnpm test
corepack pnpm build
corepack pnpm package:check
```

The canonical OpenAPI document generates private committed types under `sdk/typescript/src/generated`. Those generated symbols are deliberately absent from the published API. See the [SDK README](sdk/typescript/README.md) for the walking-slice example. With an isolated PostgreSQL database available, `make sdk-conformance DATABASE_TEST_URL='postgres://...'` proves profile publication, idempotent verification creation, tenant retrieval, and capture bootstrap against the running core.

### Capture-profile contract

The portable v1 profile and evidence-registry schemas live under [`contracts/capture-profile/v1`](contracts/capture-profile/v1/). Profiles keep evidence types separate from acquisition methods, support `any_of` choices and `all_of` requirements, pin an immutable registry revision and digest, and validate namespaced extensions fail closed. The companion contract document defines canonical serialization, assurance-preserving fallbacks, and why SDK capability advertisements guide selection without proving assurance.

The initial registry supports document-front, document-back, and selfie images through file upload or live camera. Uploads cannot establish freshness, live-capture, passive-liveness, or active-liveness assurance. The mobile acquisition-plan schema and synthetic launch fixture live under [`contracts/capture/acquisition/v1`](contracts/capture/acquisition/v1/); local quality measurements and challenge transcripts remain capture metadata rather than biometric assurance.

### Native SDKs and provider adapters

The open-source [Swift SDK](sdk/swift/) and [Kotlin SDK](sdk/kotlin/) provide native session, transport, secure bootstrap, camera, and bounded acquisition foundations. Run `swift test` in `sdk/swift` and `./gradlew :idenqa:test` in `sdk/kotlin`; CI runs both on their supported host platforms.

The public provider contract and conformance harness are under [`contracts/provider/v1`](contracts/provider/v1/) and [`conformance/provider`](conformance/provider/). The reviewed pre-release Dojah and Smile ID implementations live under [`adapters/providers`](adapters/providers/) and require tenant-owned credentials, purpose-bound input/evidence resolvers, and isolated runner composition. Their manifests are catalogue records, not production entitlement or certification. See the [provider runbook](docs/runbooks/provider-operations.md) and [external-beta checklist](docs/releases/external-beta-checklist.md) before enabling real processing.

### PostgreSQL and migrations

The local PostgreSQL 18.4 harness uses the official pinned container image:

```sh
make db-up
make migrate
make integration
make integration-s3
make db-down
```

`make integration-s3` builds and starts the independently versioned S3 API
distribution, then proves the complete public evidence-upload flow against
PostgreSQL and an owned loopback, filesystem-backed S3 protocol fixture. The
production process still exercises the real AWS SDK adapter, SigV4 request
signing, immutable conditional writes, and encrypted object persistence; the
fixture keeps a third-party S3 emulator out of the repository and the AWS SDK
dependency graph out of the root module.

If port 5432 is already occupied, select another local port consistently:

```sh
IDENQA_POSTGRES_PORT=55433 make db-up
IDENQA_DATABASE_URL='postgres://idenqa:idenqa_dev@127.0.0.1:55433/idenqa?sslmode=disable' make migrate
DATABASE_TEST_URL='postgres://idenqa:idenqa_dev@127.0.0.1:55433/idenqa?sslmode=disable' make integration
DATABASE_TEST_URL='postgres://idenqa:idenqa_dev@127.0.0.1:55433/idenqa?sslmode=disable' make integration-s3
```

Migrations are reviewed SQL files embedded in `idenqa`. The API never changes the schema during startup. Operators apply and inspect migrations explicitly:

```sh
idenqa migrate preflight
idenqa migrate up
idenqa migrate version
```

Migration metadata is fixed at `public.schema_migrations`, independent of the connection role's PostgreSQL search path. `idenqa migrate down --confirm` rolls back exactly one version and is rejected unless `IDENQA_ENVIRONMENT` is explicitly `development` or `test`. The API verifies that the database is at the exact schema version required by its binary before becoming ready, so its restricted runtime database role requires `SELECT` on `public.schema_migrations` in addition to its narrow application schema and table grants. Runtime database failures make readiness fail without changing liveness; readiness recovers after PostgreSQL does.

### Tenant administration and isolation

Tenant-owned access is fail-closed and protected twice: repository operations require an explicit tenant scope, and `idenqa.tenants` uses forced PostgreSQL row-level security. The runtime database principal must have the required schema/table grants while neither owning tenant tables nor holding `BYPASSRLS`. Role creation and credential distribution remain deployment responsibilities; migrations deliberately do not create login roles or passwords.

Administrative operations require a separate `IDENQA_DATABASE_ADMIN_URL` whose principal is a superuser or explicitly provisioned `BYPASSRLS` role. Do not make this variable available to API or worker processes. Every successful operation requires an actor assertion and reason and writes an audit row atomically:

```sh
idenqa tenant create --actor local-operator --reason 'bootstrap local integration'
idenqa tenant inspect --id ten_01M... --actor local-operator --reason 'inspect local tenant'
idenqa tenant disable --id ten_01M... --version 1 --actor local-operator --reason 'disable local tenant'
```

The actor string is an operator assertion backed by possession of the administrative database credential. Tenant API-key authentication protects the public API, while authenticated human administration remains a later boundary.

### API-key administration

API-key CLI operations use the same separate administrative database URL and require an actor and reason. Creation and rotation require configured peppers and rotation policy. A fixed `--expires-at` value or explicit `--no-expiry` choice is mandatory:

```sh
idenqa api-key create --tenant ten_01M... --label backend \
  --scope tenant:read --scope 'verification_sessions:*' --expires-at 2027-08-27T00:00:00Z \
  --actor local-operator --reason 'create backend credential'

idenqa api-key list --tenant ten_01M... \
  --actor local-operator --reason 'inspect tenant credentials'

idenqa api-key rotate --tenant ten_01M... --id key_01M... --overlap 10m \
  --expires-at 2027-08-27T00:00:00Z --confirm \
  --actor local-operator --reason 'rotate backend credential'

idenqa api-key revoke --tenant ten_01M... --id key_01M... --version 1 --confirm \
  --actor local-operator --reason 'revoke compromised credential'
```

Create and rotate print the new `credential=` value exactly once after the database transaction commits. Store it immediately in an appropriate secret manager. List and revoke output is secret-free. Rotation and revocation are irreversible, require `--confirm`, and every successful create, list, rotate, or revoke appends an atomic audit record.

CORS is deny-by-default. Each permitted capture-page or SDK browser origin must be listed exactly, including its scheme and port when non-default. Wildcards, paths, credentials, query strings, and fragments are rejected.

For direct TLS, set `IDENQA_HTTP_TLS_MODE=file` and provide both certificate and key files. The pair is loaded before the API becomes started or ready, TLS versions below 1.2 are rejected, and certificate paths are not logged. File-based certificates are loaded at startup; the initial implementation requires a graceful restart to rotate them. When a trusted reverse proxy terminates TLS, leave the mode disabled and configure transport-header trust separately when that support is introduced.

## Licence

Idenqa is licensed under the [Apache License 2.0](LICENSE). New dependencies are governed by the [dependency licence policy](docs/dependency-licence-policy.md). Report vulnerabilities through the private process in [SECURITY.md](SECURITY.md).
