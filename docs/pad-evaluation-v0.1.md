# PAD preparation and evaluation v0.1

**Status — 7 September 2026:** Contextual preparation and offline evaluation tooling implemented. Production PAD, dataset acceptance and active liveness remain unapproved. The user confirmed there is currently no approved local evaluation dataset and asked to investigate Hugging Face/public sources. No public dataset has been selected, downloaded into the repository or used to claim model accuracy.

## 1. Public dataset intake

The recommended next data step is a reviewed physical-attack subset from Persona for exploratory stress testing, paired with independently collected or commercially licensed genuine captures before making balanced performance claims. This is **Proposed**, not an accepted dataset or permission to purchase data. Do not infer missing subject/device metadata, copy another model's published scores into our report, or mix digital injection/deepfake attacks with physical presentation attacks without separate categories.

| Source | What is available | Constraint and proposed use |
| --- | --- | --- |
| [Persona FAS benchmark on Hugging Face](https://huggingface.co/datasets/withpersona/persona-fas-bench-v1) | Publisher-labelled CC BY 4.0 release with 21,056 attack images, including masks, replicas and synthetic attacks; the viewer also exposes other models' score reports | Genuine evaluation images are withheld. Inspect and pin the image subset, attribution and source provenance; use physical attacks for exploratory attack-only testing. BPCER must remain null without genuine samples. See the publisher's [reproducibility/data description](https://huggingface.co/datasets/withpersona/persona-fas-bench-v1/commit/2ed3d07c012806634265a8498985c6ce2b5e7b4c) for the withheld real-class data; counts there precede the filtered release. |
| [CelebA-Spoof official source](https://mmlab.ie.cuhk.edu.hk/projects/CelebA/CelebA_Spoof.html) | Genuine/spoof labels and rich capture/attack annotations | Non-commercial research only, with redistribution restrictions. A Hugging Face mirror does not change these terms. The current PAD candidate was trained/evaluated on this source, so it cannot be our sole independent generalisation benchmark. |
| [AxonData liveness collection on Hugging Face](https://huggingface.co/datasets/AxonData/liveness-detection-dataset) | Video samples and a supplier-described larger collection with multiple attack types | The linked sample card is CC BY-NC 4.0; the publisher offers the full collection separately for commercial usage. Exact licence, consent/provenance, genuine/attack balance, devices, protocols and subject metadata must be assessed before purchase/use. Publisher references to certification do not certify Idenqa. |
| [SynthASpoof official source](https://github.com/meilfang/SynthASpoof) | Synthetic identities with captured presentation attacks | Research-only terms expressly exclude product development. It is not selected for this product work and is not a substitute for genuine-capture validation. |

Public availability and an uploader's licence field are intake evidence, not a guarantee of underlying rights or representativeness. Accepted collection purpose, model/dataset rights, intended regions, retention/deletion and meaningful capture/device coverage remain dataset-selection gates. A small public attack subset can expose failures but cannot establish production error rates.

## 2. Pinned contextual preparation

The optional `model.face_preparation` configuration is:

```json
{
  "algorithm": "yunet-context-v1",
  "detector_digest": "sha256:8f2383e4dd3cfbb4553ea8718107fc0423210dc964f9f4280604804ed2552fa4"
}
```

`detector_file` in model-runner settings is the absolute path to that detector. The reference candidate is OpenCV Zoo's `face_detection_yunet_2023mar.onnx`, pinned at repository revision `47534e27c9851bb1128ccc0102f1145e27f23f98`; [source and licence](https://github.com/opencv/opencv_zoo/tree/47534e27c9851bb1128ccc0102f1145e27f23f98/models/face_detection_yunet) identify the MIT-licensed model. Weights are operator supplied, not bundled. This is an evaluation detector, not an accepted production face-localisation model.

The pipeline runs both detector and PAD graphs in ONNX Runtime's CPU provider inside one bounded subprocess. It accepts upright, opaque JPEG/PNG source images up to 4096 pixels per side and 4 million pixels total. It does not apply JPEG EXIF orientation; operators must supply upright pixels. The Go adapter decodes RGB once, and native code performs:

1. Preserve aspect ratio while resizing detector input to fit 640×640, using linear interpolation; pad the bottom/right with zeros and convert RGB to BGR float32 NCHW without unit scaling.
2. Decode YuNet stride-8/16/32 outputs and validate shapes/finite values. Apply confidence 0.8, integer-box NMS 0.3 and top-k 5000. Detection decoding follows the [OpenCV YuNet implementation](https://github.com/opencv/opencv/blob/4.x/modules/objdetect/src/face_detect.cpp), with the owned fixed-resolution transform described here.
3. Require exactly one detected face before quality filtering. Return `face_not_found`, `multiple_faces`, `face_too_small` (below 64 original-image pixels on either side), or `face_at_edge` (under 5 pixels of original-image margin), without a PAD score.
4. Expand the detected face to a centred square at 1.5× the larger dimension, with truncation and reflected boundary padding (`BORDER_REFLECT_101`). Resize to 128×128 with Lanczos4 when enlarging and area interpolation when shrinking. Convert RGB to float32 NCHW in [0,1], then execute PAD.

These fixed detector/size/edge settings are experimental preparation semantics, not accepted biometric thresholds. Their digest includes the detector content digest and transform settings. `FacePreprocessingDigest()` replaces `PreprocessingDigest()` in the manifest when face preparation is enabled. Recompute the runtime digest and complete configuration digest after installing the new runtime lock; restart API/worker/runner with the matching immutable binding. Existing request history is never rewritten. Omitting face preparation retains only the earlier prepared-128×128 evaluation fixture mode.

OpenCV supplies image transforms and NMS; it does not replace ONNX Runtime for inference. The runtime lock now includes `opencv-python-headless==5.0.0.93` (release 2 July 2026), whose Python dependency is NumPy >=2 on this Python version, already satisfied by the lock. Intake checked [PyPI version metadata](https://pypi.org/project/opencv-python-headless/5.0.0.93/), the MIT wrapper licence and bundled Apache-2.0/third-party notices. Exact-version PyPI advisories and OSV returned no reported advisories at review. The new wheel's installed contents participate in the runtime fingerprint. No Go dependency was added.

The original image and face coordinates stay inside the controlled model workload; only normalised inconclusive reasons reach Core. This implementation does not prove pose/blur/illumination acceptance, temporal provenance, active liveness or resilience to every attack. Kernel quotas, a hardened OCI image and native network isolation remain open deployment gates.

## 3. Dataset manifest and offline report

`model-runner --evaluate-dataset` runs on operator-approved local images. It does not access Core, use tenant credentials, issue evidence grants, tune a threshold or activate a model. The operator's dataset approval reference is an attestation to be reviewed, not automatic legal approval.

The manifest is closed JSON, version 1, capped at 4 MiB and 10,000 sample records. Example placeholders below must be replaced with actual content digests and reviewed references:

```json
{
  "version": 1,
  "reference": "pad-study-v1",
  "approval_reference": "dataset-review-001",
  "threshold_reference": "offline-operating-point-001",
  "real_score_threshold": 0.8,
  "samples": [
    {
      "id": "sample-001",
      "subject_id": "pseudonymous-subject-001",
      "split": "evaluation",
      "path": "images/sample-001.jpg",
      "sha256": "REPLACE_WITH_64_LOWERCASE_HEX_CHARACTERS",
      "label": "attack",
      "attack_type": "print",
      "device_class": "phone-family-a",
      "capture_condition": "indoor-daylight"
    }
  ]
}
```

`real_score_threshold` is a required experimental operating point, not a default production threshold. Scores at or above it count as accepted genuine presentations for offline counting. `threshold_reference` records how it was chosen; this implementation does not auto-calibrate or choose an optimal threshold using evaluation labels. Both `calibration` and `evaluation` records can be listed; only evaluation records are scored. Duplicate image digests/IDs are rejected across the entire manifest, and declared subjects cannot cross splits.

`subject_id` should be pseudonymous. If source subject identity is unavailable, omit it; do not invent distinct people from filenames. Such a dataset must be evaluation-only and the report marks subject separation unavailable, with an unknown-subject sample count. `device_class` and `capture_condition` must use explicit `unknown` labels when unavailable. The tooling does not infer demographics or other sensitive characteristics. Exact duplicate detection does not detect near-duplicates or prove absence of overlap with model training data.

Paths must be relative to the manifest directory. Confined `os.Root` access rejects traversal/symlink escapes; regular files are bounded at 10 MiB and verified against their SHA-256 before decoding. Content mismatch, malformed images or runtime errors fail the run without returning a partial success report. Quality/no-face cases are nonresponses, not silently dropped samples. Calibration files are also digest checked.

```sh
umask 077
model-runner --config /absolute/model-runner.json \
  --evaluate-dataset /absolute/dataset/manifest.json > /absolute/reports/pad-study.json
```

The same model/runtime/preprocessing/configuration pins apply to online and offline execution. The report contains aggregate image counts, declared device/capture/attack groups, model/configuration/dataset digests and the experimental threshold. It includes:

- `apcer_scored_only`: attacks accepted divided by scored attacks.
- `bpcer_scored_only`: genuine images rejected divided by scored genuine images.
- Attack/genuine nonresponse rates, each using all images of that class.
- Explicit null rates for absent denominators, and `attack_only`/`genuine_only` coverage when applicable.
- `production_accepted: false` unconditionally.

No images, face boxes, individual scores, subject IDs or file paths appear in the aggregate report. These image-level exploratory rates are not a certification result; sample dependence, representative confidence intervals, video/attack-instrument protocols and subgroup acceptance require further study. Even a report with both classes is not a representativeness approval.

## 4. Verification and remaining acceptance

Tests cover synthetic crop surroundings and reflected edges, up/down interpolation, zero/multiple/small/edge detections, malformed detector outputs, image bounds/transparency, detector pin mismatch and cancellation. A separately supplied pinned YuNet model rejects a blank frame in the native smoke test. Neither synthetic detector outputs nor a blank-frame smoke test establish real-face localisation quality.

Offline evaluation tests cover threshold equality, scored-only denominators versus nonresponse denominators, attack-only null genuine metrics, subject/content leakage, tampering and filesystem confinement. The PostgreSQL public-capture journey runs detector/crop/PAD inference, durable request/receipt processing and signed-webhook retry; the same integration fixture runs the offline evaluator. All image and label fixtures are synthetic runtime evidence, not a representative PAD benchmark. CI runs the native Python preparation tests alongside Go/native workflow tests.

Next work requires a reviewed dataset subset and genuine captures, pinned ingestion with available subject/device metadata, a documented calibration protocol and independent evaluation, production model/dataset rights, representative capture-quality acceptance, accepted temporal-model semantics and integration, and hardened deployment. Web sequence transport alone does not meet those gates. ML-01 remains In progress; the online result remains inconclusive.

Local validation on 7 September 2026 passed Go race tests, native preparation tests, the full PostgreSQL and Headgate integration suites, production lint, generated-contract reproducibility and binary builds. Both synthetic ONNX fixtures reproduce byte for byte. An initial full integration run timed out in the existing offline-expiry test; that test passed alone and the complete suite passed on rerun. Integration lint still reports two existing findings in `privacy_review_test.go` and `policy_catalog_test.go`. Go vulnerability scanning found no affected called/imported packages and reported three advisories in required modules outside the code's call paths. No representative dataset evaluation or remote CI run is claimed.

## 5. Import and compare workflow

**Implemented engineering workflow — 7 September 2026:** The operator can import a labelled local inventory, evaluate its images in a bounded sequential batch, and compare baseline and candidate configurations. This supports testing model/runtime/preprocessing changes before accepting them for production. It does not download data, infer labels, choose a threshold or approve dataset rights.

Create `inventory.json` using the section 3 schema, omitting `sha256` for files that have not yet been pinned. All other required fields remain explicit, including approval and threshold references. Run:

```sh
umask 077
model-runner --import-dataset /absolute/dataset/inventory.json \
  > /absolute/dataset/manifest.pending.json && \
  mv /absolute/dataset/manifest.pending.json /absolute/dataset/manifest.json
```

Keep the emitted manifest in the **same directory as the inventory**: its image paths are relative to that directory. The importer reads only listed local images, checks supported decoding and existing size limits, computes SHA-256, and rechecks duplicate content and subject separation. If a hash is already supplied, a mismatch fails; the importer never silently replaces a stale pin. Missing labels, approval references, malformed images, escaping paths and cancellation fail the import. Subject/device information remains supplied metadata; unknown values must not be fabricated. The manifest includes pseudonymous subject IDs and local image paths and should be retained with the restricted dataset, unlike aggregate reports.

Evaluate one configuration using the section 3 command. To compare two configurations, run:

```sh
model-runner --config /absolute/baseline.json \
  --evaluate-dataset /absolute/dataset/manifest.json \
  --compare-config /absolute/candidate.json > /absolute/reports/comparison.json
```

Both runs use the same manifest and fixed operating point, verify every content pin, and retain their own complete model/runtime/preprocessing/configuration provenance. Runs and image inference are sequential; existing per-image resource/deadline limits apply. This version is fail-fast, has no checkpoint/resume or automatic retry, and emits JSON only after the entire operation succeeds. A failed candidate or changed manifest discards the comparison. Cancellation is propagated to the native workload. It does not start a Core connection or TLS server.

The comparison embeds both aggregate reports and reports **candidate minus baseline** rate differences overall and for each declared device/capture/attack group. Lower error rates are favourable only in context: compare nonresponses and scored denominators as well. Either missing denominator makes the corresponding delta null. The scored image subsets can differ between models, so these are descriptive aggregate differences, not paired statistical tests, confidence intervals, a winner selection or production approval. Model-specific threshold calibration and independent evaluation remain separate acceptance work.

Verification covers hash preservation, duplicate images, invalid labels, malformed input, subject separation, cancellation, incompatible command modes, changed comparison cohorts, null rates and nonresponse tradeoffs. A native CLI fixture imports an image and compares two separately pinned configuration versions; matching synthetic weights produce zero rate differences, and a candidate model-pin failure produces no partial JSON. Synthetic data verifies the workflow mechanics only.
