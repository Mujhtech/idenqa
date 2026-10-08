# Tracing implementation v0.1

This guide describes implemented diagnostic tracing. Architecture and package decisions remain governed by the [repository/package draft](global-identity-core-repository-structure-and-packages-v0.1-draft.md).

## Span ownership and context flow

HTTP ingress uses the existing chi OTel middleware. Application operations use the SDK-free [observability tracing port](../internal/platform/observability/tracing.go), injected during composition through `WithTracer`. The [OTel adapter](../internal/platform/telemetry/tracing.go) starts a child span and returns the context passed to downstream operations. Static names follow `package.Type.Method`; arguments and identifiers never become span names or attributes. An omitted tracer preserves the caller's context and application behavior. Completion records failures and panics using fixed diagnostic messages, then preserves the original error or panic for its existing handling boundary.

API composition wires tracing into verification, capture-profile, authority, evidence-read, review, identity, policy, provider/model management, privacy, experience, proposal, support, key-custody, audit-read, and request-log services. Worker composition wires provider/model execution, privacy, key rewrap, and webhook sending. Query tracing is installed on the shared pgx pool, including connections opened before the process-owned tracer is injected. Query spans are named `postgres.query`; SQL, arguments, connection strings, and database error messages are excluded.

The [Headgate adapter](../internal/platform/task/headgate/tracing.go) creates `task.enqueue` producer spans for both normal and transactional enqueue. It persists only W3C `traceparent` and `tracestate`, excluding baggage. An active caller span takes precedence over older intent metadata. Without an active caller, a shared explicit intent parent is used; mixed-parent batches preserve each explicit intent's lineage.

Worker execution extracts that carrier and starts a live `task.execute` consumer span before calling the handler. Both transactional preparation and fenced commit receive its context. Each retry creates a distinct span under the stored producer parent. Registered task name, version, queue, and one-based attempt number are allowed span attributes; tenant, subject, task identifiers, payloads, and evidence are excluded. Completion events retain task metrics without synthesizing duplicate spans after execution. A task without a carrier starts a new trace and does not inherit an unrelated worker-process span.

Runner gRPC clients and servers receive process-owned providers and W3C propagators. Runner HTTP calls have `http.client` spans that finish when the response body is consumed or closed. Only calls to the explicitly trusted Core evidence gateway propagate trace headers. Calls to external providers and tenants receive no added diagnostic headers. HTTP spans exclude URLs, headers, request/response bodies, and original error text. The underlying destination restrictions and connection cleanup remain in place. Webhook sending has a `delivery.send` span covering the sender operation.

Context-aware application logs include `trace_id` and `span_id`, including unsampled valid context. Use `InfoContext`, `WarnContext`, or `ErrorContext` with the current operation context to retain correlation.

## Export configuration

API, worker, adapter-runner, and model-runner use the existing `IDENQA_TELEMETRY_*` configuration. Export remains disabled by default; enabling spans does not configure a collector automatically. Isolated runners load only telemetry environment settings and do not require API database configuration.

For a local collector exposing an OTLP/gRPC listener on loopback:

```dotenv
IDENQA_TELEMETRY_PROTOCOL=grpc
IDENQA_TELEMETRY_ENDPOINT=127.0.0.1:4317
IDENQA_TELEMETRY_INSECURE=true
IDENQA_TELEMETRY_TRACE_SAMPLE_RATIO=1
```

Apply these settings to each process being inspected. Non-loopback collectors require TLS. Sampling remains parent-based: setting the root ratio to `1` does not override an incoming unsampled parent. Production sampling, authentication, CA/mTLS configuration, batching, and shutdown retain their existing bounds.

## Verification and limits

The tests assert HTTP → real application service → enqueue → worker → downstream parent relationships; active-context precedence; baggage exclusion; transactional enqueue; independent retry span IDs; preparation and commit context; parent sampling; cancellation; error/panic preservation and redaction; SQL/parameter exclusion; response-body span lifetime; internal-only HTTP propagation; and log correlation. Transactional commit propagation also has a PostgreSQL-backed integration test.

Tracing remains diagnostic. It does not change task identity, authorization, idempotency, retry, lease, fencing, or transaction semantics. Historical jobs without stored trace context cannot be retroactively connected to their original HTTP request. Additional operation spans in helpers and integrations can use the same injected port as needed.

Validation completed for this change: the full Go race suite and PostgreSQL-backed Headgate integration suite pass; the established application-service packages and tracing adapters pass lint; configured formatting checks pass; and `govulncheck ./...` reports no reachable vulnerabilities. Full repository lint still reports unrelated working-tree findings in other existing code and tests.

## Decisions still required

An age-based span-link policy for delayed or replayed work remains **TBD**. No age threshold is selected by this implementation; current retries preserve stored producer parentage.

## Files changed

This task changes the following Go files and adds this implementation guide. Files that already contained other working-tree changes retain those changes.

| Boundary | Files |
| --- | --- |
| `internal/access` | [tenant_reader.go](../internal/access/tenant_reader.go) |
| `internal/audit` | [read.go](../internal/audit/read.go) |
| `internal/authority` | [service.go](../internal/authority/service.go), [upload_acceptance_service.go](../internal/authority/upload_acceptance_service.go), [upload_service.go](../internal/authority/upload_service.go) |
| `internal/bootstrap/adapterrunner` | [process.go](../internal/bootstrap/adapterrunner/process.go) |
| `internal/bootstrap/api` | [model.go](../internal/bootstrap/api/model.go), [process.go](../internal/bootstrap/api/process.go), [provider.go](../internal/bootstrap/api/provider.go), [provider_callback.go](../internal/bootstrap/api/provider_callback.go), [webhook.go](../internal/bootstrap/api/webhook.go) |
| `internal/bootstrap/modelrunner` | [process.go](../internal/bootstrap/modelrunner/process.go) |
| `internal/bootstrap/worker` | [model.go](../internal/bootstrap/worker/model.go), [process.go](../internal/bootstrap/worker/process.go), [provider.go](../internal/bootstrap/worker/provider.go), [tracing.go](../internal/bootstrap/worker/tracing.go) |
| `internal/config` | [telemetry.go](../internal/config/telemetry.go), [telemetry_test.go](../internal/config/telemetry_test.go) |
| `internal/delivery` | [management.go](../internal/delivery/management.go), [service.go](../internal/delivery/service.go) |
| `internal/evidence` | [access.go](../internal/evidence/access.go), [orphan_discovery.go](../internal/evidence/orphan_discovery.go), [progress.go](../internal/evidence/progress.go), [reconciliation_service.go](../internal/evidence/reconciliation_service.go) |
| `internal/experience` | [service.go](../internal/experience/service.go) |
| `internal/fraud` | [service.go](../internal/fraud/service.go) |
| `internal/identity` | [service.go](../internal/identity/service.go) |
| `internal/keycustody` | [destruction.go](../internal/keycustody/destruction.go), [key.go](../internal/keycustody/key.go), [recovery.go](../internal/keycustody/recovery.go) |
| `internal/keyrewrap` | [keyrewrap.go](../internal/keyrewrap/keyrewrap.go) |
| `internal/managedtenant` | [service.go](../internal/managedtenant/service.go) |
| `internal/model` | [execution.go](../internal/model/execution.go), [management.go](../internal/model/management.go), [validation.go](../internal/model/validation.go) |
| `internal/platform/logging` | [logging.go](../internal/platform/logging/logging.go), [logging_test.go](../internal/platform/logging/logging_test.go) |
| `internal/platform/observability` | [tracing.go](../internal/platform/observability/tracing.go) |
| `internal/platform/postgres` | [pool.go](../internal/platform/postgres/pool.go), [tracing.go](../internal/platform/postgres/tracing.go), [tracing_test.go](../internal/platform/postgres/tracing_test.go) |
| `internal/platform/task/headgate` | [adapter.go](../internal/platform/task/headgate/adapter.go), [runtime.go](../internal/platform/task/headgate/runtime.go), [runtime_integration_test.go](../internal/platform/task/headgate/runtime_integration_test.go), [telemetry.go](../internal/platform/task/headgate/telemetry.go), [telemetry_test.go](../internal/platform/task/headgate/telemetry_test.go), [tracing.go](../internal/platform/task/headgate/tracing.go), [tracing_test.go](../internal/platform/task/headgate/tracing_test.go) |
| `internal/platform/telemetry` | [http.go](../internal/platform/telemetry/http.go), [http_test.go](../internal/platform/telemetry/http_test.go), [runner.go](../internal/platform/telemetry/runner.go), [telemetry.go](../internal/platform/telemetry/telemetry.go), [tracing.go](../internal/platform/telemetry/tracing.go), [tracing_test.go](../internal/platform/telemetry/tracing_test.go) |
| `internal/policy` | [assurance_management.go](../internal/policy/assurance_management.go), [management.go](../internal/policy/management.go), [reader.go](../internal/policy/reader.go) |
| `internal/privacy` | [request_service.go](../internal/privacy/request_service.go), [service.go](../internal/privacy/service.go) |
| `internal/proposal` | [products.go](../internal/proposal/products.go), [service.go](../internal/proposal/service.go) |
| `internal/provider` | [async.go](../internal/provider/async.go), [callback.go](../internal/provider/callback.go), [execution.go](../internal/provider/execution.go), [health.go](../internal/provider/health.go), [management.go](../internal/provider/management.go) |
| `internal/realtime` | [cleanup.go](../internal/realtime/cleanup.go), [command.go](../internal/realtime/command.go), [replay_cleanup.go](../internal/realtime/replay_cleanup.go), [ticket_service.go](../internal/realtime/ticket_service.go) |
| `internal/requestlog` | [requestlog.go](../internal/requestlog/requestlog.go) |
| `internal/review` | [evidence.go](../internal/review/evidence.go), [followup.go](../internal/review/followup.go), [management.go](../internal/review/management.go), [recapture.go](../internal/review/recapture.go), [recapture_acknowledgement.go](../internal/review/recapture_acknowledgement.go), [recapture_reevaluation.go](../internal/review/recapture_reevaluation.go), [recapture_renewal.go](../internal/review/recapture_renewal.go), [recapture_status.go](../internal/review/recapture_status.go), [service.go](../internal/review/service.go) |
| `internal/reviewbrowser` | [service.go](../internal/reviewbrowser/service.go) |
| `internal/support` | [support.go](../internal/support/support.go) |
| `internal/verification` | [cancellation.go](../internal/verification/cancellation.go), [capture_outcome.go](../internal/verification/capture_outcome.go), [document_selection_service.go](../internal/verification/document_selection_service.go), [execution_service.go](../internal/verification/execution_service.go), [history.go](../internal/verification/history.go), [inspection.go](../internal/verification/inspection.go), [native_bootstrap.go](../internal/verification/native_bootstrap.go), [service.go](../internal/verification/service.go), [session_service.go](../internal/verification/session_service.go), [session_service_test.go](../internal/verification/session_service_test.go), [signal_read.go](../internal/verification/signal_read.go), [timeline.go](../internal/verification/timeline.go) |
