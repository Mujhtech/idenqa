# Idenqa Kotlin SDK

The open-source Android foundation targets API 26+. It encrypts capture tokens with an Android Keystore key, requires a hardware-backed P-256 native-bootstrap proof key, and provides one-shot Camera2 JPEG capture. Cancellable coroutine HTTP calls, direct upload, Flow-based WebSocket observation, ordered session state, and capability advertisement remain SDK-owned; capability advertisement does not prove assurance.

`AcquisitionCoordinator` executes server-issued acquisition requirements, presents bounded liveness prompts, and rejects frames that fail the supplied local image-quality policy. `AndroidImageQualityAssessor` evaluates bounded image samples and face count using platform APIs. These measurements and the prompt transcript are capture metadata only: they do not establish presentation-attack detection, face-match, document-authenticity, MRZ, or barcode assurance. The library declares `CAMERA`; the host application must request runtime permission before capture.

Run `./gradlew :idenqa:test` from this directory.
