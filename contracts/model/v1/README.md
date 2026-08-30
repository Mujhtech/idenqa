# Model adapter contract v1

This package is the public, transport-independent model adapter contract. Each
execution pins model, runtime, preprocessing, configuration, output-schema, and
contract digests together with capability and resource restrictions.

Inputs are purpose-bound evidence-grant redemptions rather than evidence bytes
or storage references. Results are bounded stable signals or redacted failures;
model output never receives authority merely because it came from a model.

The public conformance harness is in `conformance/model`. Protobuf/gRPC mapping
and process isolation remain the next explicit dependency decision.
