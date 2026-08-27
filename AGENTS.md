# Idenqa Agent Guide

This file governs agent work in this repository. Read it before reviewing, editing, or implementing from the architecture documents.

## Current repository phase

Idenqa is in architecture and repository-design work. Do not scaffold an implementation, add dependencies, generate contracts, or create empty target directories unless the user explicitly asks for implementation.

The product is built open-source-core first. SDKs and basic hosted or embeddable capture pages are part of the open-source core. Console and managed Cloud are later commercial consumers of the same public contracts; they are not prerequisites for using the core.

## Document authority

Use the documents in this order when their scopes overlap:

1. The user's latest explicit decision, once recorded in the appropriate document.
2. `docs/global-identity-core-repository-structure-and-packages-v0.1-draft.md` for repository layout, package boundaries, foundational technology contracts, dependencies, SDKs, capture packages, and unresolved implementation decisions.
3. `docs/global-identity-core-technical-architecture-v0.6-draft.md` for the integrated product and system architecture.
4. `docs/global-identity-core-build-plan-v0.1.md` for implementation sequence, brick status, decision gates, and completion evidence. It may not override architecture or package decisions.
5. `docs/global-identity-core-architecture-amendments-draft.md` for the rationale and proposal history that produced v0.6.
6. `docs/global-identity-core-technical-architecture.md` as the historical v0.5 baseline.

The v0.5 file is preserved for comparison and must not be treated as the current architecture. Do not edit it unless the user explicitly requests a revision to that file. Do not append new decisions to the amendments document when they belong in the integrated or repository/package draft.

All current documents are drafts. Preserve the distinction between:

- **Selected**: explicitly agreed direction.
- **Proposed**: recommendation awaiting confirmation during implementation or review.
- **Conditional**: permitted only when a demonstrated requirement justifies it.
- **TBD**: capability or question remains unresolved.

Never promote a Proposed, Conditional, or TBD item to Selected without an explicit user decision. When documents conflict, identify the conflict and resolve it in the narrowest authoritative document; do not silently combine incompatible statements.

## Decisions that must be preserved

- Use Go 1.26.6 unless the user explicitly changes the toolchain decision.
- Use modular hexagonal architecture with manual constructor injection.
- Public binaries use simple names such as `api`, `worker`, and `idenqa`; commercial control-plane workloads may use qualified names separately.
- PostgreSQL is authoritative for durable state, orchestration, idempotency, inbox/outbox, replay, and coordination.
- Evidence uses S3-compatible object storage. Redis is not a mandatory baseline dependency.
- Active capture uses a versioned WebSocket control channel where bidirectional communication is needed. REST handles bootstrap, recovery, snapshots, and fallback; HTTP handles evidence upload; signed webhooks notify tenant backends.
- Capture requirements are tenant-configurable through versioned profiles. Evidence type, artefact, acquisition method, and assurance are distinct concepts.
- Capture profiles support `any_of` choices, `all_of` requirements, policy-approved fallbacks, and immutable per-session snapshots.
- An uploaded selfie or document cannot satisfy freshness, live-capture, active-liveness, NFC-provenance, or similar assurance that its acquisition method cannot establish.
- SDK capability advertisements guide method selection but are not proof of assurance.
- SDKs and capture pages remain open source and usable without Console or Cloud.
- The public TypeScript SDK must not require Effect. Effect may be evaluated internally or exposed through a separate optional adapter.
- Tenant-owned PostgreSQL tables use mandatory application tenant scoping plus row-level security as defence in depth.
- The open-source distribution uses Apache License 2.0.
- Headgate v0.1.2 is the selected background-work system. Its Go modules are `github.com/mujhtech/headgate/go` and `github.com/mujhtech/headgate/go/driver/headgatepgx`; use its PostgreSQL backend behind the owned `platform/task` boundary. Do not leak Headgate types into domain or public contracts, and do not substitute River, Asynq, Redis, or another queue as the baseline.

## Architecture and package boundaries

- Domain and application packages must not import HTTP routers, SQL drivers, task-library types, telemetry SDKs, object-store SDKs, KMS SDKs, or cloud-provider SDKs.
- Interfaces belong at the consuming boundary and should remain narrow.
- `cmd/*` contains only argument handling, composition, lifecycle, and process startup.
- Do not introduce root-level `service`, `repository`, `utils`, `helpers`, or `common` packages.
- Package names are lowercase and singular. Avoid package-name stuttering at call sites.
- Keep provider, model, storage, KMS, and commercial integrations behind owned ports and adapters.
- Public SDKs depend only on published contracts and must not import or reproduce root `internal` implementations.
- Raw evidence bytes do not belong in WebSocket messages, task payloads, ordinary logs, traces, audit payloads, or domain objects.
- Consequential state transitions must account for idempotency, optimistic concurrency, atomic outbox and task intent, inbox deduplication, retries, fencing, replay, and reconciliation.
- Authentication at a transport boundary does not replace application-level authorisation.

## Go development

Before any Go coding, review, debugging, troubleshooting, or setup task, load the `samber/cc-skills-golang@golang-how-to` skill first — it routes to whichever other Go skills the task needs.

Follow the repository/package draft for dependency status. Do not add a dependency merely because it is listed there. Before pinning or upgrading a dependency, verify its current release, licence, maintenance state, transitive graph, and known vulnerabilities using primary sources.

When Go code exists:

- keep entry points thin and constructors explicit;
- use `context.Context` for request and operation lifetime, never as a dependency container;
- wrap errors with operation context while preserving stable public error codes;
- use deterministic clocks and identifiers in tests;
- prefer table-driven, integration, conformance, race, and fuzz tests according to risk;
- run the relevant formatting, generation, test, lint, migration, and vulnerability checks before reporting completion.

## Editing architecture documents

When the user asks for review, explanation, or discussion, do not edit files unless they also ask for a change. When they ask to document an accepted decision:

1. Put it in the narrowest authoritative document.
2. State what is selected and what remains unresolved.
3. Update package ownership, contracts, dependency tables, unresolved decisions, and acceptance criteria when the decision affects them.
4. Check related documents for contradictions and report any that should be handled separately.
5. Preserve comparison files; create a new draft when the user asks to compare revisions.

Use Idenqa terminology consistently:

- **tenant** for the integrating organisation;
- **subject** for the person whose identity is being verified;
- **operator** or **reviewer** for an authorised human acting on a case;
- **evidence type** for what was collected;
- **artefact** for a required component such as document front or back;
- **acquisition method** for how evidence was obtained;
- **assurance** for what provenance, freshness, liveness, or integrity the method can establish;
- **capture completion** separately from **verification completion** and tenant action.

Avoid describing SDKs or capture pages as commercial Web surfaces. Avoid treating every file upload as equivalent evidence. Avoid promising exactly-once delivery across external systems; the architecture provides at-least-once execution with idempotent effects.

## Documentation validation

After editing Markdown:

- verify heading numbering and hierarchy;
- verify fenced blocks are balanced;
- search for merge markers and stale contradictory statements;
- verify relative links and referenced filenames;
- keep decision labels and RFC-style obligation words (`must`, `should`, `may`) accurate;
- ensure new unresolved details appear in the decisions-still-required section;
- ensure accepted invariants have testable acceptance criteria;
- report which files changed and link directly to them.

Do not rewrite large documents for style alone. Preserve the user's wording and existing decisions unless clarity or consistency requires a targeted change.
