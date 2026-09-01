# Policy conformance test kit

`conformance/policy` is the dependency-light public Go test kit for the
engine-neutral policy v1 contract. Adapters implement `Engine`; `Run` proves
exact expected results, repeated-evaluation determinism, input ownership, and
cancellation. The package does not expose CEL types or load tenant state.
