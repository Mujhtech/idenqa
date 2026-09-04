# Idenqa Swift SDK

The open-source Swift 6.2 foundation targets iOS 16+ and has no third-party runtime dependencies. It uses Apple first-party frameworks for HTTPS and WebSocket transport, Keychain token storage, a non-exportable Secure Enclave P-256 bootstrap proof key, and one-shot AVFoundation JPEG capture. It advertises device capabilities without treating them as assurance, supports cancellable direct upload and realtime observation, and maintains ordered capture-session state.

`AcquisitionCoordinator` executes server-issued acquisition requirements, presents bounded liveness prompts, and rejects frames that fail the supplied local image-quality policy. `AppleImageQualityAssessor` uses ImageIO, Core Graphics, and Vision. These measurements and the prompt transcript are capture metadata only: they do not establish presentation-attack detection, face-match, document-authenticity, MRZ, or barcode assurance. Applications must include a camera usage description in their `Info.plist` and obtain camera permission before capture.

Run `swift test` from this directory.
