package admission

// Operation identifies one newly accepted resource in its owning transaction.
// IDs remain regional; an issuer never needs operation identifiers to sign policy.
type Operation struct {
	TenantID string
	ID       string
	Rule     string
}

// Resource identifies a durable dependent resource of an admitted verification.
// Its owning adapter must insert the authoritative source before this check.
type Resource struct {
	TenantID       string
	VerificationID string
	Kind           string
	ID             string
}
