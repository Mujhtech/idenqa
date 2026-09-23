# Liveness tracking assets

The bundled pose worker uses Google's MediaPipe Tasks Vision 1.0.1,
copyright Google LLC, licensed under Apache License 2.0.
Source and full licence: https://github.com/google-ai-edge/mediapipe
and https://www.apache.org/licenses/LICENSE-2.0 . Upstream licence notices are
retained in the bundled worker and WASM loaders.

The asset preparation command downloads the public MediaPipe Face Landmarker
float16 model, version 1, from Google's model distribution and verifies SHA-256
`64184e229b263107bc2b804c6625db1341ff2bb731874b0bcc2fe6544e0bc9ff`.
Model documentation: https://ai.google.dev/edge/mediapipe/solutions/vision/face_landmarker

The model is a landmark/expression estimator, not a presentation-attack detector
or an identity-verification model. Runtime assets are self-hosted. Camera frames
are processed transiently in the local worker and are not sent to Google.
