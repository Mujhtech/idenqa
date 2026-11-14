# Code-scanning review — 14 November 2026

This review covers the 16 open CodeQL alerts on `main` commit
`91ff1fcef24fba58afa56370deeb0f327775dca8`: two critical, ten high and four
medium. Alert numbers link to the repository's security records. Fixes retain
the existing public contracts and keep Go and JavaScript security-extended
analysis enabled. A code change does not by itself establish that an alert is
closed; the subsequent default-branch analysis is the closure evidence.

## Findings and treatment

| Alerts                                                                                                                                                                                               | Rule and location                                                             | Assessment and treatment                                                                                                                                                                                                                                                                                                                                                                       |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [15](https://github.com/Mujhtech/idenqa/security/code-scanning/15), [16](https://github.com/Mujhtech/idenqa/security/code-scanning/16)                                                               | `go/request-forgery`; `internal/bootstrap/idenqa/synthetic.go`                | One genuine managed-bridge trust-boundary defect at two HTTP sinks. A bridge caller could select the destination for a locally issued synthetic credential. Bind managed execution to the operator-configured local API listener; caller-supplied `CoreEndpoint` remains validated metadata. Preserve the standalone operator CLI's explicit self-hosted URL and disable credential redirects. |
| [13](https://github.com/Mujhtech/idenqa/security/code-scanning/13), [14](https://github.com/Mujhtech/idenqa/security/code-scanning/14)                                                               | `go/log-injection`; `internal/transport/httpapi/handler.go`                   | Raw application and response-write errors could retain request-controlled content. Remove those values and retain request ID, status and a bounded ASCII public classification. Standard slog handlers already escape newlines; this repair also protects custom handlers and avoids sensitive content retention.                                                                              |
| [4](https://github.com/Mujhtech/idenqa/security/code-scanning/4)                                                                                                                                     | `go/incorrect-integer-conversion`; `internal/transport/httpapi/model.go`      | Validate the selected history limit of 1–100 before converting the parsed `int64` to native `int`. This prevents architecture-dependent truncation without changing the limit contract.                                                                                                                                                                                                        |
| [17](https://github.com/Mujhtech/idenqa/security/code-scanning/17)                                                                                                                                   | `go/incorrect-integer-conversion`; `internal/experience/postgres/store.go`    | The rollback path reads its supplied version before domain transition planning, reaching unchecked narrowing. Check `contract.MaxVersion` at every storage conversion and retain zero as the absent-version sentinel.                                                                                                                                                                          |
| [1](https://github.com/Mujhtech/idenqa/security/code-scanning/1), [12](https://github.com/Mujhtech/idenqa/security/code-scanning/12)                                                                 | `go/incorrect-integer-conversion`; verification and audit PostgreSQL stores   | Existing guards already reject values above `MaxInt32` and `MaxInt64`, respectively. Move the checked conversions beside those guards, before transaction closures, so both runtime and static analysis retain the bounds. Preserve cursor and not-found behavior.                                                                                                                             |
| [3](https://github.com/Mujhtech/idenqa/security/code-scanning/3), [8](https://github.com/Mujhtech/idenqa/security/code-scanning/8)                                                                   | `go/allocation-size-overflow`; tenant and subject export envelopes            | Check the complete capacity sum against native `MaxInt` before adding lengths or allocating. Preserve streamed output and existing limits; return the existing limit error on impossible envelope sizes.                                                                                                                                                                                       |
| [2](https://github.com/Mujhtech/idenqa/security/code-scanning/2)                                                                                                                                     | `go/allocation-size-overflow`; local KMS AAD                                  | Public operations already bound purpose to 200 bytes and authenticated context to 32 KiB. Restate those limits beside allocation and propagate failures through the existing wrap/unwrap errors. The AAD encoding and ciphertext compatibility remain unchanged.                                                                                                                               |
| [5](https://github.com/Mujhtech/idenqa/security/code-scanning/5), [6](https://github.com/Mujhtech/idenqa/security/code-scanning/6), [9](https://github.com/Mujhtech/idenqa/security/code-scanning/9) | `go/weak-sensitive-data-hashing`; review and privacy PostgreSQL audit helpers | False positives: the reported password source is a public API-key **record identifier**, not a bearer credential or password. SHA-256 binds public event metadata and creates deterministic audit references. Keep the existing hash format and document the data distinction.                                                                                                                 |
| [10](https://github.com/Mujhtech/idenqa/security/code-scanning/10)                                                                                                                                   | `js/missing-origin-check`; `capture/web/src/pose-worker.ts`                   | False positive: this is a dedicated worker's private message channel, not a Window message listener reachable by another origin. The creator validates a same-origin asset base before constructing its private Worker.                                                                                                                                                                        |
| [11](https://github.com/Mujhtech/idenqa/security/code-scanning/11)                                                                                                                                   | `js/http-to-file-access`; `capture/web/scripts/prepare-liveness.mjs`          | False positive: a fixed HTTPS model download must match the pinned SHA-256 before any directory creation, removal, copy or write. The output filename is fixed and its base directory is a trusted local CLI argument.                                                                                                                                                                         |

Eleven alert locations receive code repairs or explicit boundary hardening.
The five false positives receive individual, evidence-based security-record
dispositions; they are not accepted vulnerabilities or disabled queries.

## False-positive evidence

### Public identifiers and audit hashing

[`id.APIKey`](../../internal/platform/id/id.go) explicitly represents the
public `key_…` record identifier and is safe to persist and display.
`ParseAPIKey` validates that identifier; it does not parse a presented secret.
The review and privacy transport paths construct actors from the authenticated
principal's `KeyID()`, and application actor validation uses that typed public
identifier. The reported paths reach deterministic reference helpers in
[`review/postgres`](../../internal/review/postgres/store.go) and
[`privacy/postgres`](../../internal/privacy/postgres/store.go), or the digest
of event type, aggregate ID and version. None is a password verifier.

The [CodeQL rule](https://codeql.github.com/codeql-query-help/go/go-weak-sensitive-data-hashing/)
addresses fast hashing of passwords. Changing these audit references to a
password KDF would not protect credentials and would alter stable audit and
replay identities. This disposition must be revisited if a caller starts
passing a credential secret instead of a record identifier.

### Dedicated worker message channel

[`pose-tracker.ts`](../../capture/web/src/pose-tracker.ts) validates the asset
origin and creates a private Worker; the separate classic IIFE build is in
[`tsdown.config.ts`](../../capture/web/tsdown.config.ts). As specified for
[dedicated workers](https://html.spec.whatwg.org/multipage/workers.html#dedicated-workers-and-the-dedicatedworkerglobalscope-interface),
messages use the worker's implicit private port. A different-origin iframe
can message the host Window but does not acquire this Worker handle.

A real-browser probe delivered a forged initialization message from a
different-origin iframe to the host Window, then initialized the actual
worker/model and inferred a blank frame. Only the expected ready and pose
messages reached the worker client; no attacker-origin network request or
worker error occurred. This validates channel isolation, not face-model,
active-liveness, PAD or product-demo acceptance.

### Pinned model preparation

[`prepare-liveness.mjs`](../../capture/web/scripts/prepare-liveness.mjs) checks
SHA-256 `64184e229b263107bc2b804c6625db1341ff2bb731874b0bcc2fe6544e0bc9ff`
before its first filesystem mutation. An exact-source probe with real SHA-256
and filesystem spies rejected arbitrary downloaded bytes with zero mutations;
the pinned model alone reached the fixed model destination, byte-for-byte.
This evidence addresses the [HTTP-to-file heuristic](https://codeql.github.com/codeql-query-help/javascript/js-http-to-file-access/).
Reassess if the URL, digest check order, filename or trust of CLI arguments
changes. MediaPipe remains evaluation-only under the existing acceptance gates.

## Verification and operational limits

Regression tests cover untrusted-target credential isolation, redirect
rejection for ordinary and upload requests, explicit CLI base prefixes,
content-free logs, integer boundaries, capacity sums near native `MaxInt`,
and maximum supported KMS purpose/context round trips with unchanged AAD.
The affected Go package tests passed with the race detector; formatting, vet
and configured lint passed with zero issues. The two browser probes passed.
Repository PR checks and default-branch CodeQL analysis provide separate
merge and alert-closure evidence.

Managed readiness tests the configured local Core listener. Wildcard bind
addresses resolve to their loopback counterparts. TLS verification remains
enabled and the certificate must match the configured listener hostname;
this does not establish reachability through a public reverse proxy. The
managed bridge remains a fixed Core-owned operation, not an HTTP forwarding
interface.

The separately documented provider/model successor retry-preparation gap
remains deferred by the user's explicit decision. This security repair does
not change that retry policy or claim the broader work is complete.
