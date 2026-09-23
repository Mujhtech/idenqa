# Idenqa Kotlin SDK

The open-source Android foundation targets API 26+. It encrypts capture tokens with an Android Keystore key, requires a hardware-backed P-256 native-bootstrap proof key, and provides one-shot Camera2 JPEG capture. Cancellable coroutine HTTP calls, direct upload, Flow-based WebSocket observation, ordered session state, and capability advertisement remain SDK-owned; capability advertisement does not prove assurance.

`AcquisitionCoordinator` executes server-issued acquisition requirements, presents bounded liveness prompts, and rejects frames that fail the supplied local image-quality policy. `AndroidImageQualityAssessor` evaluates bounded image samples and face count using platform APIs. These measurements and the prompt transcript are capture metadata only: they do not establish presentation-attack detection, face-match, document-authenticity, MRZ, or barcode assurance. The library declares `CAMERA`; the host application must request runtime permission before capture.

Run `./gradlew :idenqa:test` from this directory.

## Native capture UI — Capture Web parity

Attach `CaptureView(activity, journey, bootstrapToken, acquisitionPlans)` to the host Activity, or omit the token to resume the securely persisted journey. Forward `onRequestPermissionsResult` with its request code and grant results, forward `onStop` to `onHostStopped`, and `onResume` to `onHostResumed`; call `close` when disposing the view. Starting requires an explicit subject action. Avoid retaining the view beyond its Activity's lifetime. The host must supply a `CaptureAcquisitionPlanProvider` that returns the backend-issued plan for the attached session. Capture is blocked, not degraded, when no plan is available or when the plan requires a challenge the SDK cannot measure.

The screen presents exact Core notice text and explicit consent/acknowledgement/refusal, profile-pinned document choices, side-specific guidance and an explicit frame with Help, review/retake, recovery, cancellation, and authoritative processing/completion. Document selection is persisted by Core with expected-version and idempotency protection. Unknown document selections fail closed; front precedes back.

Capture enforces the plan before evidence is accepted: quality policy is checked before review and again before upload, and challenged requirements run measured liveness through `MediaPipePoseTracker` with the shared gate — neutral calibration, directional target/tolerance/hold, blink close-then-open, stale/invalid/lost-face rejection — before the ordered digest-chained sequence is submitted. Pose measurements and pixels stay in the SDK; local pose compliance does not establish PAD.

Measured liveness needs the pinned evaluation model, which is not committed. Prepare it once per checkout:

```sh
python3 scripts/prepare-liveness.py                     # downloads and verifies the pinned model
python3 scripts/prepare-liveness.py --model <path>      # or use a local copy of the same model
```

The script writes `idenqa/src/main/assets/idenqa/face_landmarker.task` (git-ignored) and refuses any file whose SHA-256 does not match the pinned digest. The application must package that asset; there is no runtime download. Run the on-device tracker smoke test with `./gradlew :idenqa:connectedDebugAndroidTest` against a connected device or emulator.

Not yet accepted for production. Real-Core journeys, per-device tracker and blink calibration, mirrored-direction confirmation, portrait/quality thresholds on real cameras, portable-experience rendering, accessibility and camera/device acceptance remain open.

Validation and remaining device gates are recorded in [native capture evidence](../../docs/native-capture-evidence-v0.1.md).
