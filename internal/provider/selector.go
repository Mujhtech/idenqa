// Package provider owns deterministic provider routing without importing any
// external adapter implementation.
package provider

import (
	"errors"
	"slices"
)

var (
	// ErrNoCompatibleProvider means no healthy registration preserves the required meaning.
	ErrNoCompatibleProvider = errors.New("provider: no compatible provider")
	// ErrInvalidRoute means the route or candidate catalogue is malformed.
	ErrInvalidRoute = errors.New("provider: invalid route")
)

// Semantics is the complete meaning that must survive provider replacement.
type Semantics struct {
	Check               string
	Country             string
	Evidence            []string
	Assurances          []string
	ProcessingAuthority string
	Recipient           string
	Region              string
	Purpose             string
}

// Candidate is one policy-approved immutable provider registration snapshot.
type Candidate struct {
	ProviderID string
	Healthy    bool
	Approved   bool
	Semantics  Semantics
}

// Select returns the preferred healthy provider, or the first explicitly
// approved equivalent fallback in the supplied deterministic order.
func Select(required Semantics, preferred string, candidates []Candidate) (Candidate, error) {
	if !validSemantics(required) || preferred == "" || len(candidates) == 0 || len(candidates) > 32 {
		return Candidate{}, ErrInvalidRoute
	}
	var preferredCandidate *Candidate
	seen := make(map[string]struct{}, len(candidates))
	for index := range candidates {
		candidate := candidates[index]
		if candidate.ProviderID == "" || !validSemantics(candidate.Semantics) {
			return Candidate{}, ErrInvalidRoute
		}
		if _, exists := seen[candidate.ProviderID]; exists {
			return Candidate{}, ErrInvalidRoute
		}
		seen[candidate.ProviderID] = struct{}{}
		if candidate.ProviderID == preferred {
			copyOfCandidate := candidate
			preferredCandidate = &copyOfCandidate
		}
	}
	if preferredCandidate == nil || !equivalent(required, preferredCandidate.Semantics) {
		return Candidate{}, ErrInvalidRoute
	}
	if preferredCandidate.Healthy && preferredCandidate.Approved {
		return *preferredCandidate, nil
	}
	for _, candidate := range candidates {
		if candidate.ProviderID != preferred && candidate.Healthy && candidate.Approved && equivalent(required, candidate.Semantics) {
			return candidate, nil
		}
	}
	return Candidate{}, ErrNoCompatibleProvider
}

func equivalent(left, right Semantics) bool {
	return left.Check == right.Check && left.Country == right.Country &&
		left.ProcessingAuthority == right.ProcessingAuthority && left.Recipient == right.Recipient &&
		left.Region == right.Region && left.Purpose == right.Purpose &&
		equalSet(left.Evidence, right.Evidence) && equalSet(left.Assurances, right.Assurances)
}

func equalSet(left, right []string) bool {
	leftCopy, rightCopy := slices.Clone(left), slices.Clone(right)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}

func validSemantics(value Semantics) bool {
	return value.Check != "" && value.Country != "" && len(value.Evidence) > 0 &&
		value.ProcessingAuthority != "" && value.Recipient != "" && value.Region != "" && value.Purpose != "" &&
		uniqueSet(value.Evidence) && uniqueSet(value.Assurances)
}

func uniqueSet(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
