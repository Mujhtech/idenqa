# ONNX runtime v0.1

**Status — 7 September 2026:** Implemented evaluation runtime; ML-01 remains In progress. **Selected:** ONNX Runtime, with presentation-attack detection (PAD) / liveness as the first capability. No trained model, biometric threshold or production liveness claim is accepted by this slice.

## 1. Implemented boundary

`cmd/model-runner` composes the private TLS gRPC ModelRunnerService. The owned adapter in `adapters/models/onnx` runs official ONNX Runtime through an isolated Python subprocess. Core uses the public model contract and contains no ONNX tensor types or native library linkage. The Go binary requires a separately installed Python environment and model file.

The additional evaluation-only document/selfie route is documented in [Face-matching runtime v0.1](face-matching-runtime-v0.1.md). It shares this runtime and durable boundary.

The initial route is `idenqa.check.passive_pad`, accepting one selfie image. Successful inference always emits `idenqa.signal.passive_pad = inconclusive`, with reason `pad_evaluation_only`. Startup rejects `evaluation_only: false`. The inference score is not returned as accepted assurance, persisted as a biometric score, or used to automatically select a threshold. A failed runtime returns a bounded classified failure requiring reconciliation. A static image does not establish freshness, challenge response or active liveness.

The worker binds one tenant, policy ID, immutable capture-profile digest and selfie requirement to a model configuration. Migration 38 adds tenant-scoped, RLS-protected `model_requests` and `model_dispatches`. Planning commits the exact request, evidence grant, attempt and task intent together. Dispatch checks current processing authority and the current running attempt. Concurrent or ambiguous dispatches cannot blindly repeat an evidence read; the first validated receipt remains immutable. [Composed runtime](composed-verification-runtime-v0.1.md) now permits a model bundle alongside one provider route for the same tenant/policy/profile. Synthetic processing remains mutually exclusive.

The authenticated private `/internal/v1/model-evidence` endpoint accepts only the saved attempt's exact grant/redemption pair. It checks processing authority, runner, purpose and evidence state, and commits the controlled-read audit before returning plaintext. Raw evidence never enters the gRPC envelope, task payload or durable model request/result. The endpoint must remain on the private workload network.

## 2. Runtime intake and pins

The reference implementation uses official Python ONNX Runtime **1.29.0**, `CPUExecutionProvider`, sequential execution, one native thread and one in-flight native inference per adapter. It disables graph optimization, memory-pattern optimization, custom operator registration and execution-provider fallback. The subprocess sets `ORT_DISABLE_TELEMETRY=1` before import and calls the telemetry-disable API; the native test checks that no telemetry session files appear in its working directory. This uses the release’s [full POSIX telemetry opt-out](https://github.com/microsoft/onnxruntime/blob/v1.29.0/onnxruntime/core/platform/telemetry_environment.h). The official [Python runtime guide](https://onnxruntime.ai/docs/get-started/with-python.html) and [session API](https://onnxruntime.ai/docs/api/python/api_summary.html) describe the binding. Release and licence intake used [Microsoft's releases](https://github.com/microsoft/onnxruntime/releases/tag/v1.29.0) and [MIT licence](https://github.com/microsoft/onnxruntime/blob/main/LICENSE).

The wheel-only [runtime lock](../adapters/models/onnx/requirements.lock) pins the base graph (plus the OpenCV preparation dependency documented below): ONNX Runtime 1.29.0, NumPy 2.5.3, protobuf 7.36.1, flatbuffers 25.12.19 and packaging 26.3. Licences reviewed: MIT; NumPy's bundled BSD/MIT/0BSD/Zlib/CC0 notices; BSD-3-Clause; Apache-2.0; and Apache-2.0/BSD-2-Clause respectively. All published wheel hashes are allowed so supported platforms resolve their matching wheel without a source build. Python 3.12 or newer is required by this graph; the local execution evidence uses Python 3.13.3 on macOS arm64.

The separate [development lock](../adapters/models/onnx/requirements-dev.lock) adds ONNX 1.22.0, ml_dtypes 0.6.0 and typing_extensions 4.16.0 for generating the synthetic test graph. Their licences are Apache-2.0, Apache-2.0 and PSF-2.0. Development packages are not needed to run inference. Exact-version PyPI advisory metadata and OSV batch queries for all eight packages returned no reported vulnerabilities at intake on 7 September 2026; that is a dated result, not a guarantee for future deployments. Package versions, supported wheels and dependency metadata are published by [PyPI](https://pypi.org/project/onnxruntime/1.29.0/).

Startup checks the model file's SHA-256, runtime fingerprint and exact tensor names/types/shapes. The runtime fingerprint covers interpreter version, OS/architecture class, installed runtime dependency file contents and the embedded inference program. It is remeasured for every subprocess. It is **not** an OCI image digest or complete operating-system attestation. Deployment image/interpreter pinning and hardware acceptance remain separate requirements.

Model files are bounded at 64 MiB and loaded from pinned bytes; filesystem-dependent external tensor data is unsupported. The original prepared-image fixture mode requires a 128×128 opaque JPEG/PNG. With `face_preparation` enabled, the adapter accepts bounded source images, runs a pinned YuNet detector and performs contextual cropping before RGB NCHW normalization; see [PAD preparation and evaluation](pad-evaluation-v0.1.md). Input tensor `input` is `[1,3,128,128]`; symbolic batch `batch_size` is accepted. Output `output` is `[1,2]` finite float32 logits, ordered real/spoof, converted by stable softmax. Invalid dimensions, nonfinite values, incompatible schemas and mismatched pins fail closed.

Input evidence is capped at 10 MiB, native control input at 96 MiB and native output at 4 KiB. A subprocess deadline of at most 30 seconds and cancellation terminate native execution. These bounds and the single execution slot **do not impose a kernel memory, CPU or network sandbox**. OCI packaging, non-root/read-only deployment, memory/CPU/PID quotas, native egress denial and supported-hardware validation remain required before production activation. The Go wrapper alone does not satisfy those deployment gates.

## 3. PAD candidate and remaining model work

The evaluation candidate is [facenox/face-antispoof-onnx](https://github.com/facenox/face-antispoof-onnx/tree/fa6489fb221dcf6b803095ab4b15a0fa0f56cfe5), revision `fa6489fb221dcf6b803095ab4b15a0fa0f56cfe5`, file `models/best/98.20/best_model.onnx`, SHA-256 `af2381b88f38769222ed93379e12444e2a50814575de1c46170de570c55a42b6`. It is a small MiniFASNetV2-SE model with an Apache-2.0 repository licence. Its repository reports CelebA-Spoof training/evaluation; dataset rights, redistribution permission for the complete deployment and suitability for Idenqa remain independent acceptance gates. Weights are not bundled in this repository.

The candidate's [preprocessing](https://github.com/facenox/face-antispoof-onnx/blob/fa6489fb221dcf6b803095ab4b15a0fa0f56cfe5/src/inference/preprocess.py) and [limitations](https://github.com/facenox/face-antispoof-onnx/blob/fa6489fb221dcf6b803095ab4b15a0fa0f56cfe5/docs/LIMITATIONS.md) require contextual face preparation and restrict supported capture/attack conditions. The reference implementation now supplies a pinned YuNet detector and 1.5× contextual crop/resize pipeline, with synthetic/native verification. Production detector/preprocessing quality acceptance and temporal provenance are still open. A correctly sized uploaded image does not prove that preprocessing occurred. Therefore even the candidate's native output stays inconclusive.

Next acceptance work must validate face localization and the implemented contextual transform against real capture data, image quality and capture constraints, representative genuine/attack datasets, reproducible preprocessing and threshold revisions, error rates across supported devices and subject populations, training-data/model licensing, and deployment resource/region/deletion evidence. Active liveness additionally requires a versioned challenge/capture protocol and trustworthy acquisition evidence. Neither the candidate smoke test nor the synthetic graph closes any of these gates.

## 4. Running the evaluation workload

Create an operator-owned Python 3.12+ environment and install the runtime lock:

```sh
python3 -m venv /absolute/operator/path/onnx-env
/absolute/operator/path/onnx-env/bin/python -m pip install --require-hashes --only-binary=:all: -r adapters/models/onnx/requirements.lock
go build -o /absolute/operator/path/model-runner ./cmd/model-runner
/absolute/operator/path/model-runner --runtime-digest --python /absolute/operator/path/onnx-env/bin/python
```

Prepare the closed JSON [runner settings](../internal/bootstrap/modelrunner/process.go) with absolute `python`, `model_file`, certificate, private-key and credential paths; an explicit listen address; and the private HTTPS evidence gateway plus its CA/credential files. `model` uses the [adapter configuration](../adapters/models/onnx/adapter.go): tenant ID, model registration ID/reference/digest, manifest, `width: 128`, `height: 128`, and mandatory `evaluation_only: true`. Manifest provenance uses the same `mdl_` registration identifier, model version and content digest, the measured runtime digest, `PreprocessingDigest(128,128)`, `OutputSchemaDigest()` and model contract 1.0. Capabilities and restrictions must match sections 1–2; no required assurance may be advertised by this evaluation adapter.

The configuration digest covers this entire model configuration, excluding its own digest field. Compute it before startup:

```sh
/absolute/operator/path/model-runner --configuration-digest --config /absolute/operator/path/model-runner.json
/absolute/operator/path/model-runner --config /absolute/operator/path/model-runner.json
```

Copy the resulting digest into the runner registration and the Core binding. Core API and worker use `IDENQA_MODEL_RUNTIME_FILE`, a mounted [ModelRuntime JSON configuration](../internal/config/model.go) containing the matching manifest/binding, runner address, server name, CA file, runner credential file and gateway credential file. The [native public-workflow test](../test/integration/model_runtime_test.go) constructs a complete working configuration and local TLS topology. Config changes require restart and never rewrite existing attempt envelopes. No dynamic model registry or activation API is introduced.

Apply migration 38 using the existing operational CLI. In addition to existing runtime grants, the restricted runtime role needs SELECT/INSERT on `idenqa.model_requests` and SELECT/INSERT/UPDATE on `idenqa.model_dispatches`; both tables require the established tenant scope and RLS. Mount workload credentials independently from tenant API keys. The supplied Go release archive does not bundle Python, native wheels, weights or a hardened OCI image.

## 5. Verification and scope of evidence

Install the development lock only to regenerate `adapters/models/onnx/testdata/pad_fixture.onnx` with `testdata/generate.py`. This deterministic graph computes two simple logits; it is not a trained PAD model. Its reference softmax assertion uses absolute tolerance `1e-6` on the recorded runtime/hardware class.

```sh
ONNX_TEST_PYTHON=/absolute/operator/path/onnx-env/bin/python go test -race ./adapters/models/onnx ./internal/model/...
ONNX_TEST_PYTHON=/absolute/operator/path/onnx-env/bin/python DATABASE_TEST_URL="$DATABASE_TEST_URL" go test -race -tags=integration ./test/integration -run '^TestONNX'
```

The native workflow test uses public profile/session/authority/upload APIs, encrypted evidence, a TLS model runner, persisted requests and receipts, restricted-role PostgreSQL, Headgate, policy evaluation and signed-webhook retry after worker restart. It checks rollback when task insertion fails, immutable history, cross-tenant rejection and denial of completed evidence replay. Its policy deliberately completes with `synthetic.fixture` assurance when it observes an inconclusive signal; this proves plumbing and is not a production verification policy.

`ONNX_TEST_PAD_MODEL` optionally points to the separately reviewed candidate file for a pinned native smoke test. A zero tensor proves load/schema/execution compatibility only. Tests skip native execution when `ONNX_TEST_PYTHON` is absent; ordinary Go test success alone must not be presented as native ONNX evidence.

## 6. Contextual preparation and offline evaluation

[PAD preparation and evaluation v0.1](pad-evaluation-v0.1.md) records the new hash-pinned OpenCV 5.0.0.93 transform dependency, YuNet detector candidate, versioned crop semantics, dataset manifest, offline evaluation command, public dataset shortlist and remaining acceptance. Both detector and PAD inference still use ONNX Runtime. Public dataset availability does not establish approved data rights or representative performance.

## 7. Evaluation registry increment — 8 September 2026

[Model registry v0.1](model-registry-v0.1.md) adds migration 43, immutable model/threshold revisions, audited evaluation activation/retirement/rollback, optional worker binding pins, per-dispatch readiness checks and experimental report gates. Registry participation is opt-in for existing mounted routes; live dynamic routing and hardened deployment remain open.
