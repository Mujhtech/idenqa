# Idenqa policy document v1

This package defines the engine-neutral public JSON contract for one immutable
policy revision. The canonical document contains named rules. A rule's bounded
boolean expression may inspect only the closed variables exposed by the selected
evaluator; the rule result remains Idenqa-owned state, directive, priority,
provenance, and reason-code data.

The initial CEL adapter exposes only `facts: map<string,string>` and
`region: string`. It disables macros and rejects functions, field selection,
construction, comprehensions, arithmetic, dynamic indexing, clocks, and I/O.
Every statically referenced fact must exactly equal the rule's declared
`contributing_facts` set.

Canonical JSON is UTF-8, contains no unknown or duplicate fields, orders rules
by name, and orders fact and reason-code lists lexically. The policy digest is
SHA-256 over those canonical bytes. YAML may be added later only as an authoring
format that compiles to the same canonical JSON; it is not v1 policy identity.
