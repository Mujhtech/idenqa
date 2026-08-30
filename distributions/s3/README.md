# Idenqa S3 distribution

This module builds the public `api` binary with Idenqa's evidence-ingress flow
backed by an S3-compatible immutable ciphertext store and a mounted local KEK
keyring. It reuses the core API contracts and composition boundary while
keeping the AWS SDK dependency graph outside the root module.

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
```

The workspace supplies the unreleased core and S3 adapter modules during
pre-release development. Before this distribution's first independent release,
its module must require compatible published versions and pass its standalone
module gates with `GOWORK=off`; no local `replace` directive is committed.

## Workspace live proof

With the development PostgreSQL instance running, execute:

```sh
make integration-s3
```

This builds and starts this distribution as a separate process and drives the
public capture-token flow through encrypted evidence acceptance. The production
process uses the real AWS SDK adapter. The test supplies an owned loopback,
filesystem-backed S3 protocol fixture that checks SigV4 request metadata and
immutable conditional writes, then independently returns the stored ciphertext
for integrity and decryption assertions. No third-party S3 emulator is a
repository or runtime dependency.
