# Idenqa S3 distribution

This module builds the public `api` and `worker` binaries with Idenqa's evidence
ingress and worker-owned exact-deletion flows backed by the same S3-compatible
immutable ciphertext store. The API uses a mounted local KEK keyring; the
worker uses that keyring for the core delivery/rewrap path while S3 deletion
itself needs only exact object references. Both processes reuse core-owned
ports and keep the AWS SDK dependency graph outside the root module.

## Configuration

The process accepts every core API `IDENQA_*` setting plus:

- `IDENQA_S3_BUCKET` and `IDENQA_S3_REGION` (required)
- `IDENQA_S3_PREFIX` (optional owned object-key prefix)
- `IDENQA_S3_ENDPOINT` (optional S3-compatible HTTPS endpoint)
- `IDENQA_S3_FORCE_PATH_STYLE` (default `false`)
- `IDENQA_S3_ALLOW_HTTP` (default `false`; required for an explicit HTTP endpoint)
- `IDENQA_S3_CLEANUP_TIMEOUT` (default `30s`, maximum `5m`)

`IDENQA_EVIDENCE_LOCAL_KEYRING_FILE` is required and must point to an existing
owner-only keyring created with `idenqa evidence-key init`. Do not set
`IDENQA_EVIDENCE_LOCAL_DIRECTORY`; this distribution injects S3 storage.

AWS credentials and custom certificate authorities use the AWS SDK's standard
external configuration chain. They are not accepted as Idenqa command flags or
embedded in this module's configuration type.

## Run from the workspace

```sh
go run ./distributions/s3/cmd/api --env-file ./idenqa.env
go run ./distributions/s3/cmd/worker --env-file ./idenqa.env
```

Run the API and worker with the same `IDENQA_S3_BUCKET`, `IDENQA_S3_PREFIX`,
region and endpoint. A mismatched worker cannot fulfil privacy deletion or
evidence-reconciliation work for objects accepted by the API.

## Self-hosted image

The packaged image exposes the pair as `api-s3` and `worker-s3`. Start the
named services so Compose does not also start the default local-storage pair:

```sh
docker compose --project-directory deploy/self-hosted \
  -f deploy/self-hosted/compose.yaml \
  --profile s3 up -d api-s3 worker-s3
```

Add other named services required by the installation, but never run `api-s3`
with the root-local `worker`, or `worker-s3` with the root-local `api`, against
the same database.

The workspace supplies the unreleased core and S3 adapter modules during
pre-release development. Before this distribution's first independent release,
its module must require compatible published versions and pass its standalone
module gates with `GOWORK=off`; no local `replace` directive is committed.

## Workspace live proof

With the development PostgreSQL instance running, execute:

```sh
make integration-s3
```

This builds and starts the distribution's API as a separate process and drives
the public capture-token flow through encrypted evidence acceptance. The
production process uses the real AWS SDK adapter. The test supplies an owned loopback,
filesystem-backed S3 protocol fixture that checks SigV4 request metadata and
immutable conditional writes, then independently returns the stored ciphertext
for integrity and decryption assertions. No third-party S3 emulator is a
repository or runtime dependency. The worker binary is compiled and composed by
the distribution and self-hosted image; live S3 deletion is intentionally still
recorded as production acceptance evidence.
