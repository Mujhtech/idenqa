# Proposal v1 Contract

`contracts/proposal/v1` is the **Proposed** non-authoritative AI proposal envelope.

- `SignalModel` (`contracts/model/v1`) remains `evidence refs -> scored signals -> deterministic policy` (Selected).
- `ProposalModel` is `redacted bounded context -> bounded proposal -> deterministic guardrails/human approval -> stored AcceptedCommand` (Proposed, Milestone 6).

Raw evidence bytes, biometric templates, national identifier values, credentials, and provider payloads never enter the envelope — only validated references. `AcceptedCommand` is stored separately; replay re-executes the stored command without re-querying the model.

Allow-listed kinds cover audit §3.4: review copilot, adaptive routing, policy draft/diff/adversarial, accessibility/exception proposals, and document layout proposals. Unknown kinds fail closed and require a contract minor bump.

Guardrail and automation-mode semantics live in `internal/proposal` (domain must not import HTTP/SQL/task SDKs). Default mode for v1 is `disabled`.
