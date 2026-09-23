# syntax=docker/dockerfile:1.7
#
# Self-hosted Idenqa Core images.
#
# Targets:
#   core          api, worker, api-s3, worker-s3, adapter-runner, model-runner,
#                 idenqa, and the deployment fixtures
#   capture-web   Capture Web demo bootstrap served by Vite
#   model-runner  isolated ONNX model runner with the hash-locked Python
#                 runtime and the repository's evaluation fixture
#
# Build from the repository root:
#   docker build -f deploy/self-hosted/Dockerfile -t idenqa-core:dev .

ARG GO_TOOLCHAIN=1.27.1
ARG NODE_VERSION=22.23.2
ARG PYTHON_VERSION=3.13

FROM golang:${GO_TOOLCHAIN}-bookworm@sha256:69a7b9788769bec032d238959b61854e9ae87f57be9029ec04e9885fabf99195 AS build

ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_DATE=unknown
ENV CGO_ENABLED=0
ENV GOTOOLCHAIN=local
WORKDIR /src

COPY go.mod go.sum go.work go.work.sum ./
COPY adapters/kms/aws/go.mod adapters/kms/aws/go.sum adapters/kms/aws/
COPY adapters/objectstore/s3/go.mod adapters/objectstore/s3/go.sum adapters/objectstore/s3/
COPY distributions/s3/go.mod distributions/s3/
COPY sdk/go/go.mod sdk/go/
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download \
    && cd adapters/objectstore/s3 && go mod download

COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    LDFLAGS="-s -w -X github.com/Mujhtech/idenqa/internal/buildinfo.version=${VERSION} -X github.com/Mujhtech/idenqa/internal/buildinfo.commit=${COMMIT} -X github.com/Mujhtech/idenqa/internal/buildinfo.date=${BUILD_DATE}" \
    && mkdir -p /out \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/api ./cmd/api \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/worker ./cmd/worker \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/adapter-runner ./cmd/adapter-runner \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/model-runner ./cmd/model-runner \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/idenqa ./cmd/idenqa \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/webhook-receiver ./examples/webhook-receiver \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/audit-export ./examples/audit-export \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/adapter-runner-fixture ./examples/adapter-runner-fixture \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/model-runner-fixture ./examples/model-runner-fixture \
    && cd distributions/s3 \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/api-s3 ./cmd/api \
    && go build -trimpath -ldflags "$LDFLAGS" -o /out/worker-s3 ./cmd/worker

FROM debian:bookworm-slim@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251 AS core

RUN apt-get update \
    && apt-get install -y --no-install-recommends bash ca-certificates curl openssl \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 idenqa \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin idenqa \
    && mkdir -p /var/lib/idenqa/evidence /run/idenqa /state \
    && chown -R 10001:10001 /var/lib/idenqa /run/idenqa /state \
    # The CLI reads its optional dotenv path by default and fails closed when
    # the file is absent; provide an empty file at the process working
    # directory. Deployment settings arrive through the process environment.
    && touch /var/lib/idenqa/.env \
    && chown 10001:10001 /var/lib/idenqa/.env

COPY --from=build /out/ /usr/local/bin/
COPY deploy/self-hosted/entrypoint.sh /usr/local/bin/idenqa-entrypoint
COPY deploy/self-hosted/provision.sh /usr/local/bin/provision-self-hosted.sh
COPY examples/synthetic /opt/idenqa/examples/synthetic
RUN chmod 0755 /usr/local/bin/idenqa-entrypoint /usr/local/bin/provision-self-hosted.sh

USER 10001:10001
WORKDIR /var/lib/idenqa
ENTRYPOINT ["idenqa-entrypoint"]

FROM node:${NODE_VERSION}-bookworm-slim@sha256:48e4b67d85f87bd551df43704e24d252f56cc5f8e9718841aace50f19948f0f9 AS capture-web

ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
ENV NEXT_TELEMETRY_DISABLED=1
RUN corepack enable \
    && apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app

COPY package.json pnpm-lock.yaml pnpm-workspace.yaml .npmrc tsconfig.base.json ./
COPY sdk/typescript/package.json sdk/typescript/
COPY capture/web/package.json capture/web/
RUN --mount=type=cache,target=/app/.cache/pnpm corepack pnpm install --frozen-lockfile

COPY sdk/typescript ./sdk/typescript
COPY capture/web ./capture/web
RUN corepack pnpm --dir sdk/typescript build \
    && corepack pnpm --dir capture/web build \
    && mkdir -p capture/web/node_modules/.vite

RUN groupadd --gid 10001 idenqa \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin idenqa \
    && chown -R 10001:10001 /app

COPY deploy/self-hosted/capture-web-entrypoint.sh /usr/local/bin/capture-web-entrypoint
RUN chmod 0755 /usr/local/bin/capture-web-entrypoint

USER 10001:10001
WORKDIR /app/capture/web
ENTRYPOINT ["capture-web-entrypoint"]

FROM python:${PYTHON_VERSION}-slim-bookworm@sha256:2325bb286ec344af3e5898cc224b5844e2707ac6e26b1632516fd3edc84a5e26 AS model-runner

RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

COPY adapters/models/onnx/requirements.lock /tmp/requirements.lock
RUN pip install --no-cache-dir --require-hashes -r /tmp/requirements.lock \
    && rm /tmp/requirements.lock

COPY --from=build /out/model-runner /out/model-runner-fixture /out/idenqa /usr/local/bin/
COPY adapters/models/onnx/testdata/pad_fixture.onnx /opt/idenqa/models/pad_fixture.onnx
COPY deploy/self-hosted/model-runner-entrypoint.sh /usr/local/bin/model-runner-entrypoint
RUN chmod 0755 /usr/local/bin/model-runner-entrypoint \
    && groupadd --gid 10001 idenqa \
    && useradd --uid 10001 --gid 10001 --no-create-home --shell /usr/sbin/nologin idenqa \
    && mkdir -p /var/lib/idenqa /run/idenqa /state \
    && chown -R 10001:10001 /var/lib/idenqa /run/idenqa /state

USER 10001:10001
WORKDIR /var/lib/idenqa
ENTRYPOINT ["model-runner-entrypoint"]
