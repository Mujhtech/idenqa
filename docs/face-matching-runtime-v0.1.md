# Face-matching runtime v0.1

**Status — 7 September 2026:** Evaluation-only engineering integration implemented following the user's instruction to build face matching next. No trained face-recognition model, matching threshold or production identity assurance has been accepted. The checked-in embedding graph is synthetic and has no biometric accuracy.

## 1. Owned route and evidence binding

The existing ONNX adapter now supports `idenqa.check.face_match_1to1`, accepting document-image and selfie-image evidence and emitting `idenqa.signal.face_match_1to1`. The same public model/gRPC contracts, durable request and receipt stores, processing-authority checks, audit, policy input and webhook path are used. No dependency, public contract generation or database migration is added.

A matching route sets `binding.requirement` to the selfie requirement and the additional `binding.document_requirement` to a distinct document requirement. Preparation requires exactly one accepted, available, integrity-verified asset per requirement in the selected region: `idenqa.artefact.document_front` and `idenqa.artefact.selfie_image`. Both scoped grants, the immutable request, attempt and task intent commit in one transaction. Ambiguous or missing assets fail preparation. This reference does not yet support document-back portraits, NFC portraits or document-template selection.

The request contains the roles `document.front` and `selfie`. The adapter checks these roles independently of order and rejects missing/duplicate roles, reused asset/grant identities, incompatible purposes, altered configuration/provenance and tenant mismatch before reading evidence. Redemption remains bound to the exact saved attempt/grant/redemption and current authority. Matching grants use the capability-specific output destination `model.face_match_1to1`; PAD retains `model.passive_pad`.

The route consumes two distinct assets. This does not prove that their contents differ, that the selfie was acquired live, or that the document is authentic. Required capture assurance and document authenticity remain separate checks. [Composed runtime](composed-verification-runtime-v0.1.md) now permits PAD, matching and document checks together under one tenant/policy/profile, with unique output signals and all-check completion gates.

## 2. Native preparation and comparison

Configure the model runner with `model.face_matching: true`, `model.evaluation_only: true`, width/height 112 and the existing pinned YuNet `face_preparation`/`detector_file`. The detector settings and input bounds are described in [PAD preparation](pad-evaluation-v0.1.md#2-pinned-contextual-preparation). Here the detector supplies one bounded face box in each image; the PAD-specific 1.5× context crop is not used.

Matching preparation revision 1 crops the detected bounding box, resizes to 112×112 using OpenCV linear interpolation, and converts RGB NCHW float32 using `(pixel - 127.5) / 127.5`. It does not perform landmark alignment, deskew, EXIF orientation correction or document-template inference. Multiple visible portraits, including security/ghost portraits, yield an inconclusive quality nonresponse rather than an arbitrary selection. Real document layouts and capture conditions must be evaluated before accepting this transform.

The embedding model contract is input `input`, float32 `[1,3,112,112]`, and output `output`, float32 `[1,512]`; symbolic batch `batch_size` is also supported. Each embedding must be finite and have L2 norm at least `1e-12`. Both detector and embedding graphs run with ONNX Runtime's CPU provider. Embeddings are normalized and compared by cosine similarity in the same bounded subprocess. Only a finite score in [-1,1] or a role-specific quality reason returns to the private adapter. Embeddings, crops and face coordinates never enter Core contracts, ordinary logs or persistence.

Use `MatchingPreprocessingDigest()` and `MatchingOutputSchemaDigest()` for the manifest pins, and recompute `ConfigurationDigest()` after all settings are populated. The capability must accept exactly document and selfie evidence, emit the face-match signal, declare no required assurance, and allow exactly two grants. Total encoded evidence across both images is capped by `maximum_input_bytes`, at most 10 MiB. Each decoded image is limited to 4096 pixels per side and 4 million pixels. The existing 30-second operation bound and native execution slot apply to the whole pair.

The embedded native program changed, so **remeasure the runtime digest for both PAD and matching deployments** before preparing new requests. Existing immutable requests must not be rewritten. Model/runtime/configuration pins remain mandatory, and no weights are downloaded automatically.

## 3. Results and acceptance

Every successful comparison returns **inconclusive**, with `face_match_evaluation_only`; no threshold is applied and no score is persisted as identity evidence. Quality reasons are prefixed `document_` or `selfie_` and use `face_not_found`, `multiple_faces`, `face_too_small` or `face_at_edge`. Runtime/schema/cancellation failures use the existing bounded failure contract. Startup rejects production mode.

Local evidence covers a deterministic synthetic embedding graph, identical-input cosine within `1e-6`, distinct synthetic input responses, portrait crop/color normalization, zero/nonfinite/wrong-shaped embeddings, malformed pair roles, cancellation and output-schema separation from PAD. The public PostgreSQL fixture exercises two grant redemptions, planning rollback, native detector/embedding execution, immutable receipts, cross-tenant denial and signed-webhook recovery. Its policy explicitly consumes an inconclusive synthetic signal to exercise orchestration; it is not an identity-approval policy.

Production acceptance still requires a licensed trained model, model-specific alignment/preprocessing, supported document-portrait extraction, genuine/impostor pair datasets with identity separation, independent threshold calibration and error-rate evaluation, capture provenance and hardened deployment. The current offline `--evaluate-dataset`/`--compare-config` commands remain PAD-specific; pair-dataset evaluation is a separate follow-up. No live-person or production model-quality evidence is claimed.

**Local verification — 7 September 2026:** Root Go race tests, five native Python preparation tests, the full restricted-role PostgreSQL/Headgate suites, production lint and API/worker/model-runner builds pass. All three synthetic ONNX fixtures reproduce byte for byte. Integration lint retains only the existing findings in `privacy_review_test.go` and `policy_catalog_test.go`. Vulnerability scanning reports no affected called/imported code and three advisories in required modules outside the call paths. Markdown checks pass; remote CI and representative biometric evaluation were not run.
