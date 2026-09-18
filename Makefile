GO ?= go
PNPM ?= corepack pnpm
BIN_DIR ?= bin
VERSION ?= dev
COMMIT ?= unknown
BUILD_DATE ?= unknown
IDENQA_DATABASE_URL ?= postgres://postgres:postgres@127.0.0.1:5432/idenqa?sslmode=disable
DATABASE_TEST_URL ?= $(IDENQA_DATABASE_URL)
OPENAPI_SPEC := contracts/api/openapi/v1/openapi.yaml
OPENAPI_CONFIG := contracts/api/openapi/v1/oapi-codegen.yaml
OPENAPI_RULESET := contracts/api/openapi/v1/vacuum.yaml
OPENAPI_WARN_IGNORE := contracts/api/openapi/v1/lifecycle-alpha-warnings.txt
S3_ADAPTER := ./adapters/objectstore/s3
S3_DISTRIBUTION := ./distributions/s3

BUILDINFO_PACKAGE := github.com/Mujhtech/idenqa/internal/buildinfo
LDFLAGS := -X $(BUILDINFO_PACKAGE).version=$(VERSION) -X $(BUILDINFO_PACKAGE).commit=$(COMMIT) -X $(BUILDINFO_PACKAGE).date=$(BUILD_DATE)

.PHONY: build build-s3-api contract-breaking contract-lint db-down db-up fmt fmt-check generate generate-check integration integration-s3 lint migrate mod-check package-check proto-breaking sdk-conformance test verify version vuln

build:
	mkdir -p $(BIN_DIR)
	mkdir -p $(BIN_DIR)/distributions/s3
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/api ./cmd/api
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/worker ./cmd/worker
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/adapter-runner ./cmd/adapter-runner
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/model-runner ./cmd/model-runner
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/idenqa ./cmd/idenqa
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/distributions/s3/api $(S3_DISTRIBUTION)/cmd/api
	$(PNPM) build

build-s3-api:
	mkdir -p $(BIN_DIR)/distributions/s3
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o $(BIN_DIR)/distributions/s3/api $(S3_DISTRIBUTION)/cmd/api

fmt:
	$(GO) tool golangci-lint fmt
	$(GO) tool golangci-lint fmt $(S3_ADAPTER)/...
	$(GO) tool golangci-lint fmt $(S3_DISTRIBUTION)/...
	$(PNPM) format

fmt-check:
	$(GO) tool golangci-lint fmt --diff
	$(GO) tool golangci-lint fmt --diff $(S3_ADAPTER)/...
	$(GO) tool golangci-lint fmt --diff $(S3_DISTRIBUTION)/...
	$(PNPM) format:check

generate:
	$(GO) tool buf generate
	$(GO) tool sqlc generate
	$(GO) tool oapi-codegen -config $(OPENAPI_CONFIG) $(OPENAPI_SPEC)
	$(PNPM) generate

generate-check: generate
	git diff --exit-code -- internal/platform/postgres/sqlgen internal/gen/openapi/v1 internal/gen/proto
	$(PNPM) generate:check

contract-lint:
	$(GO) tool buf lint
	$(GO) tool vacuum lint --no-update-check --no-banner --no-style --silent --min-score 100 --fail-severity warn --ruleset $(OPENAPI_RULESET) $(OPENAPI_SPEC)

contract-breaking:
	@test -n "$(OPENAPI_BASE)" || (echo "OPENAPI_BASE must name the base OpenAPI document" >&2; exit 2)
	$(GO) tool oasdiff breaking --lang en --warn-ignore $(OPENAPI_WARN_IGNORE) --fail-on WARN -- $(OPENAPI_BASE) $(OPENAPI_SPEC)

proto-breaking:
	@test -n "$(PROTO_BASE)" || (echo "PROTO_BASE must name a Buf input containing the base contract" >&2; exit 2)
	$(GO) tool buf breaking --against "$(PROTO_BASE)"

lint:
	$(GO) tool golangci-lint run
	$(GO) tool golangci-lint run $(S3_ADAPTER)/...
	$(GO) tool golangci-lint run $(S3_DISTRIBUTION)/...
	$(PNPM) typecheck

mod-check:
	$(GO) mod tidy
	git diff --exit-code -- go.mod go.sum
	$(GO) mod verify
	$(GO) -C $(S3_ADAPTER) mod verify
	$(GO) -C $(S3_DISTRIBUTION) mod verify

test:
	GO=$(GO) node --test test/conformance/openapi-compatibility.test.mjs
	$(GO) test -race -shuffle=on ./...
	$(GO) test -race -shuffle=on $(S3_ADAPTER)/...
	$(GO) test -race -shuffle=on $(S3_DISTRIBUTION)/...
	$(PNPM) test

integration:
	DATABASE_TEST_URL="$(DATABASE_TEST_URL)" $(GO) test -race -shuffle=on -tags=integration ./test/integration/...
	DATABASE_TEST_URL="$(DATABASE_TEST_URL)" $(GO) test -race -shuffle=on -tags=integration ./internal/platform/task/headgate

integration-s3: build-s3-api
	DATABASE_TEST_URL="$(DATABASE_TEST_URL)" S3_TEST_ENABLED=true $(GO) test -race -tags=integration -run '^TestPublicEvidenceUploadFlowThroughS3Distribution$$' ./test/integration/...

sdk-conformance: build
	DATABASE_TEST_URL="$(DATABASE_TEST_URL)" ./test/conformance/typescript-sdk.sh

db-up:
	docker compose -f deploy/dev/compose.yaml up -d --wait postgres

db-down:
	docker compose -f deploy/dev/compose.yaml down

migrate: build
	IDENQA_DATABASE_URL="$(IDENQA_DATABASE_URL)" ./$(BIN_DIR)/idenqa migrate up
	IDENQA_DATABASE_URL="$(IDENQA_DATABASE_URL)" ./$(BIN_DIR)/idenqa migrate headgate up

vuln:
	$(GO) tool govulncheck ./...
	$(GO) tool govulncheck $(S3_ADAPTER)/...
	$(GO) tool govulncheck $(S3_DISTRIBUTION)/...
	$(PNPM) audit

package-check:
	$(PNPM) package:check

verify: fmt-check contract-lint generate-check mod-check test lint vuln build package-check

version: build
	./$(BIN_DIR)/idenqa version
