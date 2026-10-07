# Idenqa

**Identity infrastructure you control.**

Idenqa (pronounced _eye-DEN-kah_) is an Apache-2.0 open-source identity-verification core. It collects identity evidence, runs provider and private-model checks, evaluates deterministic policy, routes exceptions for review, and produces explainable, reproducible decisions through stable APIs.

The core is designed to be self-hosted and provider-neutral. A tenant can keep control of its evidence, encryption keys, policies, provider relationships, models, retention rules, and audit history. The open-source SDKs and Capture Web package work without Idenqa Cloud or Console.

> [!IMPORTANT]
> Idenqa is pre-release. The repository contains substantial working slices and a complete synthetic self-hosted journey, but its architecture and contracts are still drafts and several production-acceptance gates remain open. Use synthetic data only unless you are deliberately working through the [external-beta checklist](docs/releases/external-beta-checklist.md). The included model fixtures do not establish biometric accuracy, and the Capture Web demo remains development infrastructure until its outstanding device and interaction acceptance is complete.

## What Idenqa provides

- Tenant-scoped subjects, capture profiles, verification sessions, evidence metadata, checks, decisions, review cases, and privacy workflows.
- Versioned capture requirements with `any_of` choices, `all_of` requirements, policy-approved fallbacks, and immutable per-session snapshots.
- Encrypted evidence storage behind local or S3-compatible object-store boundaries. Raw evidence bytes stay out of WebSocket messages, task payloads, ordinary logs, traces, and domain objects.
- Provider and model runner contracts that keep vendor SDKs and inference runtimes outside the domain core.
- Deterministic policy evaluation, immutable decision snapshots, offline decision reproduction, audit-chain verification, signed webhooks, and durable at-least-once work with idempotent effects.
- Open TypeScript, Go, Swift, and Kotlin SDKs plus a framework-neutral Web capture package.
- Self-hosted deployment examples, operational CLI tooling, conformance suites, and portable public contracts.

Idenqa deliberately keeps **what was collected**, **how it was collected**, and **what assurance that method can establish** separate. For example, uploading a selfie may provide a selfie image, but it cannot by itself prove freshness, live capture, or liveness.

Commercial Idenqa Cloud and Console products are separate consumers of the same public contracts. They are not required to compile, run, upgrade, recover, or export data from Core.

## How the core fits together

```mermaid
flowchart LR
    Tenant["Tenant backend"] --> API["Core API"]
    Subject["Subject"] --> Capture["Capture Web or SDK"]
    Capture --> API
    API --> State["PostgreSQL<br/>state and orchestration"]
    API --> Vault["Encrypted evidence vault"]
    State --> Worker["Worker"]
    Worker --> Runners["Provider and model runners"]
    Worker --> Policy["Deterministic policy"]
    Policy --> Outcome["Decision or manual review"]
    Outcome --> Webhook["Signed webhook"]
```

PostgreSQL is authoritative for durable state, idempotency, the inbox/outbox, replay, and coordination. [Headgate](https://github.com/mujhtech/headgate) runs background work through its PostgreSQL backend. Evidence lives in an encrypted local or S3-compatible object store; Redis is not a baseline dependency.

Capture completion and verification completion are different events. Capture Web gathers the authorised evidence and reports capture progress; only Core's authoritative workflow—using checks, policy, and any required review—authors a verification outcome.

## Try the self-hosted stack

The cleanest local path needs only Docker with Compose. From the repository root:

```sh
deploy/self-hosted/smoke.sh
```

The smoke gate builds and starts Core without Cloud or Console, then proves 13 operational steps: migrations, tenant and API-key creation, policy activation, SDK/CLI access, a synthetic capture journey, worker execution, reproducible policy decisions, a verified signed webhook, audit verification, interruption recovery, retention and deletion, and a digest-verified tenant export.

After it passes, the stack remains available for inspection:

- API readiness: <http://127.0.0.1:8080/readyz>
- Hosted Capture Web fixture: <http://127.0.0.1:4173/hosted.html>
- OpenAPI 3.1 contract: [`contracts/api/openapi/v1/openapi.yaml`](contracts/api/openapi/v1/openapi.yaml)

Stop the stack without deleting its volumes:

```sh
docker compose \
  --project-directory deploy/self-hosted \
  -f deploy/self-hosted/compose.yaml down
```

The Compose defaults and generated credentials are for local development only. Copy [`deploy/self-hosted/.env.example`](deploy/self-hosted/.env.example) beside the Compose file when you need to override ports, image tags, or development settings. See the [backup and recovery runbook](docs/runbooks/backup-restore-and-recovery.md) before treating any deployment as durable.

## Develop from source

Required for the main Go and TypeScript workspace:

- Go 1.27.1
- Node.js 22.18 or newer
- Corepack with the repository-pinned pnpm release
- Docker with Compose for PostgreSQL integration tests

Install the TypeScript workspace and run the complete static, contract, generation, test, vulnerability, build, and package checks:

```sh
corepack pnpm install --frozen-lockfile
make verify
```

Run the PostgreSQL integration suite with the development database:

```sh
make db-up

IDENQA_DATABASE_URL='postgres://idenqa:idenqa_dev@127.0.0.1:5432/idenqa?sslmode=disable' \
  make migrate

DATABASE_TEST_URL='postgres://idenqa:idenqa_dev@127.0.0.1:5432/idenqa?sslmode=disable' \
  make integration

make db-down
```

`make build` produces the public `api`, `worker`, `idenqa`, `adapter-runner`, and `model-runner` binaries under `bin/`, plus the independently composed S3 API distribution. Explore operational commands with `./bin/idenqa --help`.

Useful focused commands:

```sh
make contract-lint       # lint OpenAPI and Protobuf contracts
make generate-check      # prove committed generated code is reproducible
make test                # Go race tests and TypeScript tests
DATABASE_TEST_URL='postgres://idenqa:idenqa_dev@127.0.0.1:5432/idenqa?sslmode=disable' \
  make integration-s3    # exercise public evidence upload through the S3 distribution
DATABASE_TEST_URL='postgres://idenqa:idenqa_dev@127.0.0.1:5432/idenqa?sslmode=disable' \
  make sdk-conformance   # run an SDK journey against a real local Core
```

Swift and Kotlin have their own package-level instructions in [`sdk/swift`](sdk/swift/README.md) and [`sdk/kotlin`](sdk/kotlin/README.md).

## Repository map

| Path                                    | Purpose                                                                                    |
| --------------------------------------- | ------------------------------------------------------------------------------------------ |
| [`cmd`](cmd/)                           | Thin process entry points for the API, worker, CLI, and isolated runners                   |
| [`internal`](internal/)                 | Bounded Go domain, application, transport, and platform packages                           |
| [`contracts`](contracts/)               | Public OpenAPI, Protobuf, capture, provider, model, policy, webhook, and related contracts |
| [`sdk`](sdk/)                           | Public TypeScript, Go, Swift, and Kotlin SDKs                                              |
| [`capture/web`](capture/web/)           | Open-source subject-facing Web capture package and development fixtures                    |
| [`adapters`](adapters/)                 | Provider, model, object-store, KMS, experience, and proposal adapters                      |
| [`distributions/s3`](distributions/s3/) | S3-composed API and worker distribution kept outside the root Go module                    |
| [`db`](db/)                             | Reviewed migrations and sqlc queries                                                       |
| [`deploy`](deploy/)                     | Development, observability, and self-hosted deployment assets                              |
| [`conformance`](conformance/)           | Provider, model, and policy conformance material                                           |
| [`test`](test/)                         | Cross-package integration and public-contract conformance suites                           |
| [`examples`](examples/)                 | Synthetic journey, runner, webhook, and audit examples                                     |
| [`docs`](docs/)                         | Architecture, build status, acceptance criteria, security material, and runbooks           |

The Go code uses modular hexagonal architecture with explicit constructor injection. Domain and application packages do not depend on HTTP routers, SQL drivers, task systems, telemetry SDKs, object-store SDKs, KMS SDKs, or cloud-provider SDKs.

## Public integration surfaces

- **HTTP API:** the canonical OpenAPI source is [`contracts/api/openapi/v1/openapi.yaml`](contracts/api/openapi/v1/openapi.yaml); behaviour that OpenAPI cannot fully express is in its [conventions](contracts/api/openapi/v1/conventions.md).
- **Capture profiles:** the portable schema and assurance-preserving rules are under [`contracts/capture-profile/v1`](contracts/capture-profile/v1/README.md).
- **Realtime capture:** a versioned WebSocket channel carries control and progress events; REST provides bootstrap, recovery, snapshots, and fallback; HTTP carries evidence bytes.
- **Provider and model runners:** versioned contracts and conformance material live under [`contracts/provider/v1`](contracts/provider/v1/README.md), [`contracts/model/v1`](contracts/model/v1/README.md), and [`contracts/runner`](contracts/runner/).
- **SDKs:** start with the [TypeScript SDK](sdk/typescript/README.md), [Go SDK](sdk/go/README.md), [Swift SDK](sdk/swift/README.md), or [Kotlin SDK](sdk/kotlin/README.md). Exact administration coverage is tracked in the [public SDK resource guide](docs/public-sdk-resources-v0.1.md).
- **Capture Web:** integration, theming, token-handling, liveness, document capture, and fixture guidance live in the [Capture Web README](capture/web/README.md).

## Configuration and deployment

Configuration is typed, fail-closed, and scoped to each process. Production processes read the environment; explicit local/test runs may opt into `--env-file`. Unknown `IDENQA_*` names are rejected, secret-like log attributes are redacted, and privileged migration or administration credentials must not be supplied to API or worker deployments.

Use these sources instead of copying a stale variable table from this README:

| Need                                             | Source                                                                                         |
| ------------------------------------------------ | ---------------------------------------------------------------------------------------------- |
| Direct API, worker, and CLI configuration        | [`.env.example`](.env.example)                                                                 |
| Packaged self-hosted stack and Compose overrides | [`deploy/self-hosted/.env.example`](deploy/self-hosted/.env.example)                           |
| Real provider-runner setup                       | [`docs/provider-runtime-v0.1.md`](docs/provider-runtime-v0.1.md)                               |
| Model-runner and evaluation setup                | [`docs/onnx-runtime-v0.1.md`](docs/onnx-runtime-v0.1.md)                                       |
| Backup, restore, and recovery                    | [`docs/runbooks/backup-restore-and-recovery.md`](docs/runbooks/backup-restore-and-recovery.md) |
| Provider operations                              | [`docs/runbooks/provider-operations.md`](docs/runbooks/provider-operations.md)                 |

## Project documents

The current sources of truth are:

1. [Repository structure and package decisions](docs/global-identity-core-repository-structure-and-packages-v0.1-draft.md) for layout, boundaries, dependencies, and unresolved implementation decisions.
2. [Technical architecture v0.6](docs/global-identity-core-technical-architecture-v0.6-draft.md) for the integrated product and system architecture.
3. [Build plan](docs/global-identity-core-build-plan-v0.1.md) for milestone status, decision gates, and completion evidence.
4. [Implementation gap audit](docs/global-identity-core-implementation-gap-audit-v0.1-draft.md) for the consolidated distinction between implemented work, missing acceptance evidence, and later scope.
5. [External-beta checklist](docs/releases/external-beta-checklist.md) and [threat model](docs/security/external-beta-threat-model.md) for release readiness.

These documents are drafts, and their **Selected**, **Proposed**, **Conditional**, and **TBD** labels are meaningful. The unversioned [`global-identity-core-technical-architecture.md`](docs/global-identity-core-technical-architecture.md) is the preserved historical v0.5 baseline, not the current architecture.

## Security and licence

Report vulnerabilities through the private process in [`SECURITY.md`](SECURITY.md). Do not open a public issue containing exploit details, secrets, personal data, identity evidence, or provider credentials.

Idenqa is licensed under the [Apache License 2.0](LICENSE). New dependencies are governed by the [dependency licence policy](docs/dependency-licence-policy.md).
