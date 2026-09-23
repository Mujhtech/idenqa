# Idenqa Swift SDK

The open-source Swift 6.2 foundation targets iOS 16+ and has no third-party runtime dependencies. It uses Apple first-party frameworks for HTTPS and WebSocket transport, Keychain token storage, a non-exportable Secure Enclave P-256 bootstrap proof key, and one-shot AVFoundation JPEG capture. It advertises device capabilities without treating them as assurance, supports cancellable direct upload and realtime observation, and maintains ordered capture-session state.

`AcquisitionCoordinator` executes server-issued acquisition requirements, presents bounded liveness prompts, and rejects frames that fail the supplied local image-quality policy. `AppleImageQualityAssessor` uses ImageIO, Core Graphics, and Vision. These measurements and the prompt transcript are capture metadata only: they do not establish presentation-attack detection, face-match, document-authenticity, MRZ, or barcode assurance. Applications must include a camera usage description in their `Info.plist` and obtain camera permission before capture.

Run `swift test` from this directory.

## Native capture UI — Capture Web parity

Embed `CaptureView(journey: journey, bootstrapToken: token, acquisitionPlans: plans)` in a SwiftUI host, or omit the token to resume the securely persisted journey. Starting requires an explicit subject action. The host must supply the configured `CaptureJourney`, an `NSCameraUsageDescription`, and a `CaptureAcquisitionPlanProvider` that returns the backend-issued plan for the attached session. Capture is blocked, not degraded, when no plan is available or when the plan requires a challenge the SDK cannot measure.

The screen presents exact Core notice text and explicit consent/acknowledgement/refusal, profile-pinned document choices, side-specific guidance and an explicit frame with Help, review/retake, recovery, cancellation, and authoritative processing/completion. Document selection is persisted by Core with expected-version and idempotency protection. Unknown document selections fail closed; front precedes back.

Capture enforces the plan before evidence is accepted: quality policy is checked before review and again before upload, and challenged requirements run measured liveness through `VisionPoseTracker` with the shared gate — neutral calibration, directional target/tolerance/hold, blink close-then-open, stale/invalid/lost-face rejection — before the ordered digest-chained sequence is submitted. Pose measurements and pixels stay in the SDK; local pose compliance does not establish PAD.

Not yet accepted for production. Real-Core journeys, per-device tracker and blink calibration, mirrored-direction confirmation, portrait/quality thresholds on real cameras, portable-experience rendering, accessibility and camera/device acceptance remain open.

Validation and remaining device gates are recorded in [native capture evidence](../../docs/native-capture-evidence-v0.1.md).
