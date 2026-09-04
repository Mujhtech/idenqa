# Capture acquisition contract v1

This contract describes the provider-neutral work a supported capture client is
asked to perform. It separates the evidence type, artefact, acquisition method,
quality gates, and requested biometric challenge. Raw evidence bytes never
enter this document.

Version 1 supports the D-014 launch boundary:

- live document-front and document-back photography;
- live selfie photography;
- an ordered, server-authored selfie challenge sequence for a provider to
  evaluate for liveness/PAD and one-to-one face comparison; and
- local advisory checks for dimensions, brightness, contrast, blur, glare,
  framing, and face count.

Local checks determine whether a client should ask the subject to retry. They
do not establish liveness, document authenticity, or face match. Those
assurances require a compatible verification provider and authoritative
normalised result. MRZ and barcode extraction are provider capabilities in the
initial pack; the SDK captures the document image but does not claim a parsed
or authentic machine-readable zone.

NFC and voice are intentionally absent from this version.
