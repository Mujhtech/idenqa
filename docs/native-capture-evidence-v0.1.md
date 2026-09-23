# Native capture implementation and device evidence v0.1

**Status:** Capture Web parity implemented for the Core, document, review, recovery and measured-liveness boundary; product, calibration and physical-device acceptance open. Updated 23 September 2026.

## 1. Implemented increment

- SwiftUI `CaptureView` and Android View-based `CaptureView` compose the existing native journey lifecycle.
- Exact plain-text Core notice display, explicit consent/acknowledgement/refusal, session-bound authority validation, expiry/region/purpose/evidence-scope checks and persisted response idempotency keys.
- Published document choices and Core-recorded selection, expected-version/idempotent selection requests, selected-branch planning, front-before-back ordering and missing-selection upload guards.
- Guided dark document and selfie surfaces with side-specific instruction copy, an explicit frame, a side label, Help, review/retake, actionable recovery, cancellation and authoritative processing/completion states. iOS uses an SDK `AVCaptureSession` preview; Android uses an SDK Camera2 preview with aspect-correct rotation/mirroring.
- A published acquisition plan is now required before capture: the provider is bound to the attached session, evidence type/artefact/method must match the authoritative task, and plan, quality and pose bounds are validated before any frame is accepted.
- Quality policy is enforced before review and again before submission with bounded JPEG checks. Bounded EXIF-normalized decoding covers review display and inference.
- Measured active liveness replaces any timed path. Both SDKs share one gate: neutral calibration, directional target/tolerance/hold, two-sample closed-blink then open hold, stale-sample reset, face-count/centring/distance/roll rejection and quality-failure rejection.
- Apple Vision is the iOS tracker (first-party only, legacy request retained for the iOS 16 floor); MediaPipe Tasks Vision 1.0.0 is the Android tracker, packaged with the digest-pinned evaluation model and no runtime download. Both convert to the same subject-right/up-positive pose and reuse the Web gate thresholds.
- Native pose compliance stays local: only passing frames and bounded metadata leave the device. No tracker type, landmark, matrix or bitmap crosses the SDK acquisition boundary.
- Multi-frame sequences submit in challenge order with a digest manifest, per-frame count/index/challenge binding and previous-digest chaining.
- Measured segmented-ring progress and short subject instructions are rendered from the gate feedback, and the passing frame is captured automatically once a challenge completes.
- Unfinished review bytes are released on backgrounding. Upload intent keys include the evidence digest and sequence position so a retaken or re-indexed image never reuses another image's request key.
- Assurance-bearing tasks that the plan cannot satisfy stop at an explicit unsupported-adapter state rather than using an ordinary still photograph.

Sources: [Swift UI](../sdk/swift/Sources/Idenqa/CaptureView.swift), [Swift camera](../sdk/swift/Sources/Idenqa/CaptureCameraView.swift), [Swift tracker](../sdk/swift/Sources/Idenqa/VisionPoseTracker.swift), [Swift gate](../sdk/swift/Sources/Idenqa/Pose.swift), [Swift plan](../sdk/swift/Sources/Idenqa/AcquisitionPlan.swift), [Swift sequence](../sdk/swift/Sources/Idenqa/CaptureSequence.swift), [Android UI](../sdk/kotlin/idenqa/src/main/kotlin/dev/idenqa/sdk/CaptureView.kt), [Android camera](../sdk/kotlin/idenqa/src/main/kotlin/dev/idenqa/sdk/CaptureCameraView.kt), [Android tracker](../sdk/kotlin/idenqa/src/main/kotlin/dev/idenqa/sdk/MediaPipePoseTracker.kt), [Android gate](../sdk/kotlin/idenqa/src/main/kotlin/dev/idenqa/sdk/Pose.kt).

## 2. Executed evidence

| Check | Result | What it establishes |
| --- | --- | --- |
| `swift test` | 36 passed | SDK/unit contracts: notice binding/refusal, document-branch ordering, plan/quality/pose bounds, directional and blink gates, stale/invalid/lost-face rejection, and that a challenged requirement cannot fall back to a timed still. |
| iOS Simulator build (`generic/platform=iOS Simulator`) | Passed | iOS-only SwiftUI, AVFoundation, Vision and sequence code compiles. |
| `xcodebuild … test` on iPhone Duo, iOS 27.1 Simulator | 38 passed | Host tests plus presentation-model tests for consent/refusal, background cleanup and cancellation. Not touch-driven UI automation. |
| `./gradlew :idenqa:test` | 36 passed | Kotlin unit contracts including consent/scope, document branches, pose bounds, gate behaviour and fail-closed quality. |
| `./gradlew :idenqa:assembleDebug :idenqa:lintDebug` | Passed | Android library build and lint over the camera, tracker and sequence code. |
| `./gradlew :idenqa:connectedDebugAndroidTest` on Pixel_3a_API_34 (Android 14, `emulator-5554`) | 1 passed in 4.6 s | The digest check accepted the packaged model, MediaPipe/`tasks-core` loaded on-device, a blank frame produced zero tracked faces, and the gate did not complete. This is real JNI/model smoke evidence, not camera, biometric, PAD or UI acceptance. |
| Dependency review (Maven metadata, published POMs, OSV, Apache-2.0 licence) | No advisories returned | MediaPipe `tasks-vision` 1.0.0 and `tasks-core` 1.0.0 are Apache-2.0. Its published POM requests vulnerable or stale transitive versions, so the SDK pins `com.google.guava:guava` 33.7.1-android and `com.google.protobuf:protobuf-javalite` 4.36.2; every resolved module was queried and returned no advisory. |

The paired physical iPhone was unavailable and no physical Android device was connected, so the emulator is the only executed Android runtime evidence. No real-Core native journey, physical-camera accuracy, mirrored-direction confirmation, representative-subject performance, blink/aperture calibration, biometric or PAD acceptance, accessibility acceptance or visual/interaction acceptance is claimed. Successful gate tests use synthetic pose measurements and prove orchestration and gating, not landmark accuracy.

## 3. Remaining implementation and acceptance

- [ ] Obtain a session-bound native acquisition plan from a real tenant backend and exercise the full plan path against self-hosted Core; the current plan boundary is exercised with fixtures only.
- [ ] Connect the native screens to the pinned portable experience and localisation contracts; document guidance, Help and instructions are currently SDK copy, not server-resolved copy.
- [ ] Calibrate native pose measurements per platform and device: verify yaw/pitch sign conventions, mirrored selfie direction, eye-aperture versus blend-shape blink equivalence, distance/centring thresholds and hold timing against real faces.
- [ ] Confirm passing-frame quality on real cameras: both assessors already measure brightness, contrast, sharpness, glare and face count, so this is calibration of plan thresholds against real capture failures plus confirmation of JPEG/EXIF orientation through review, upload and Core.
- [ ] Harden camera and tracker lifecycle: startup/capture deadlines, interruption and session-loss recovery, cancelled asynchronous callbacks, background/re-entry ordering, permission revocation, retake and upload retry without duplicate effects, and Activity/view disposal.
- [ ] Wire and verify sensitive-screen protection for preview **and** review, including app-switcher snapshots and recording/mirroring. A protection-port call alone is not proof of a protected surface.
- [ ] Add native host applications and touch-driven UI automation for consent, branch selection, guided capture, refusal, retry, cancellation and authoritative completion against self-hosted Core.
- [ ] Exercise VoiceOver/TalkBack, large text, contrast, focus restoration, orientation and supported screen sizes on both platforms; obtain explicit visual/interaction acceptance against the agreed references.
- [ ] Record supported-device/OS coverage with physical devices and secure-key/attestation evidence separate from simulator and emulator results; decide acceptable integrity signals.
- [ ] Complete supported background-transfer recovery and MRZ/barcode result presentation; preserve the separate NFC, voice, video and provider-adapter gates.
- [ ] Record the Android model asset provenance in the release evidence: the pinned SHA-256, the licence/NOTICE obligations for bundled MediaPipe and model files, and the production acceptance decision for the evaluation tracker.

These remain open in [gap audit §15](global-identity-core-implementation-gap-audit-v0.1-draft.md#15-capture-sdk-lifecycle-and-platform-assurance). This increment does not close E-04 or M-2.
