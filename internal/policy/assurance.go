package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"time"
)

// AssuranceProfile defines independent requirements and exact capability mappings.
// Names are tenant policy labels, never certification or a universal scalar rank.
type AssuranceProfile struct {
	Name         string                 `json:"name"`
	Revision     uint32                 `json:"revision"`
	Requirements []AssuranceRequirement `json:"requirements"`
	Mappings     []AssuranceMapping     `json:"mappings"`
}

// AssuranceRequirement requires fresh, independently rooted successful sources.
type AssuranceRequirement struct {
	Dimension         string   `json:"dimension"`
	Property          string   `json:"property"`
	MinimumSources    uint16   `json:"minimum_sources"`
	MaximumAgeSeconds uint32   `json:"maximum_age_seconds"`
	Capabilities      []string `json:"capabilities"`
}

// AssuranceMapping binds a capability to an exact deployed runner and signal.
// A declaration alone never supplies a successful observation.
type AssuranceMapping struct {
	Capability          string `json:"capability"`
	Signal              string `json:"signal"`
	RunnerKind          string `json:"runner_kind"`
	RunnerID            string `json:"runner_id"`
	PackageDigest       string `json:"package_digest"`
	ConfigurationDigest string `json:"configuration_digest"`
	ThresholdDigest     string `json:"threshold_digest,omitempty"`
}

// AssuranceSource contains only server-projected provenance, never SDK advertisements.
type AssuranceSource struct {
	ExpiresAt           *time.Time       `json:"expires_at,omitempty"`
	CaptureAssurances   []string         `json:"capture_assurances,omitempty"`
	Reference           string           `json:"reference"`
	Signal              string           `json:"signal"`
	State               RequirementState `json:"state"`
	RunnerKind          string           `json:"runner_kind"`
	RunnerID            string           `json:"runner_id"`
	PackageDigest       string           `json:"package_digest"`
	ConfigurationDigest string           `json:"configuration_digest"`
	ThresholdDigest     string           `json:"threshold_digest,omitempty"`
	CollectedAt         time.Time        `json:"collected_at"`
	Roots               []string         `json:"roots"`
	EvaluationOnly      bool             `json:"evaluation_only,omitempty"`
}

// DecisionReference is a safe immutable node in the applicable input graph.
// Digest is omitted for opaque IDs whose durable row is itself immutable.
type DecisionReference struct {
	Kind    string   `json:"kind"`
	ID      string   `json:"id"`
	Version string   `json:"version,omitempty"`
	Digest  string   `json:"digest,omitempty"`
	Parents []string `json:"parents,omitempty"`
}

// DecisionContext augments legacy facts with exact references and requested assurance.
// An absent profile explicitly means no typed assurance was requested.
type DecisionContext struct {
	Version       uint16              `json:"version"`
	References    []DecisionReference `json:"references"`
	Profile       *AssuranceProfile   `json:"profile,omitempty"`
	ProfileDigest string              `json:"profile_digest,omitempty"`
	Sources       []AssuranceSource   `json:"sources"`
}

// AssuranceAchievement is deterministic at the snapshot time.
type AssuranceAchievement struct {
	RequestedDigest string                     `json:"requested_digest,omitempty"`
	Achieved        bool                       `json:"achieved"`
	Dimensions      []AssuranceDimensionResult `json:"dimensions"`
}

// AssuranceDimensionResult preserves each dimension independently.
type AssuranceDimensionResult struct {
	Dimension          string   `json:"dimension"`
	Property           string   `json:"property"`
	Satisfied          bool     `json:"satisfied"`
	IndependentSources int      `json:"independent_sources"`
	SourceReferences   []string `json:"source_references"`
}

// CanonicalAssuranceProfile validates and orders a bounded immutable revision.
func CanonicalAssuranceProfile(p AssuranceProfile) (AssuranceProfile, string, error) {
	if !validToken(p.Name, 64) || p.Revision == 0 || len(p.Requirements) == 0 || len(p.Requirements) > 16 || len(p.Mappings) == 0 || len(p.Mappings) > 128 {
		return p, "", ErrInvalid
	}
	p.Requirements = slices.Clone(p.Requirements)
	p.Mappings = slices.Clone(p.Mappings)
	capabilities := map[string]bool{}
	for _, m := range p.Mappings {
		if !validToken(m.Capability, 100) || !validToken(m.RunnerID, 128) || !capabilityMappingValid(m) || !validDigest(m.PackageDigest) || !validDigest(m.ConfigurationDigest) || (m.ThresholdDigest != "" && !validDigest(m.ThresholdDigest)) {
			return p, "", ErrInvalid
		}
		if _, e := NewFactKey(m.Signal); e != nil {
			return p, "", e
		}
		capabilities[m.Capability] = true
	}
	sort.Slice(p.Mappings, func(i, j int) bool {
		a, _ := json.Marshal(p.Mappings[i])
		b, _ := json.Marshal(p.Mappings[j])
		return string(a) < string(b)
	})
	for i := 1; i < len(p.Mappings); i++ {
		if p.Mappings[i] == p.Mappings[i-1] {
			return p, "", ErrInvalid
		}
	}
	dimensions := map[string]bool{}
	for i, r := range p.Requirements {
		switch r.Dimension {
		case "identity_resolution", "evidence_validation", "applicant_binding", "source_independence", "fraud_resistance", "human_oversight", "provenance", "freshness", "liveness", "integrity":
		default:
			return p, "", ErrInvalid
		}
		if dimensions[r.Dimension] || !validToken(r.Property, 64) || r.MinimumSources == 0 || r.MinimumSources > 32 || r.MaximumAgeSeconds == 0 || r.MaximumAgeSeconds > 31536000 || len(r.Capabilities) == 0 || len(r.Capabilities) > 32 {
			return p, "", ErrInvalid
		}
		dimensions[r.Dimension] = true
		c, e := canonicalTokens(r.Capabilities)
		if e != nil {
			return p, "", e
		}
		for _, v := range c {
			capability, ok := assuranceCapability(v)
			if !capabilities[v] || !ok || !slices.Contains(capability.Dimensions, r.Dimension) {
				return p, "", ErrInvalid
			}
		}
		p.Requirements[i].Capabilities = c
	}
	sort.Slice(p.Requirements, func(i, j int) bool { return p.Requirements[i].Dimension < p.Requirements[j].Dimension })
	b, e := json.Marshal(p)
	if e != nil {
		return p, "", e
	}
	h := sha256.Sum256(b)
	return p, hex.EncodeToString(h[:]), nil
}

func canonicalDecisionContext(c *DecisionContext) (*DecisionContext, error) {
	if c == nil {
		return nil, nil
	}
	if c.Version != 1 || len(c.References) > 8192 || len(c.Sources) > MaximumFacts*MaximumObservations {
		return nil, ErrInvalid
	}
	// Round-trip provides a defensive copy of every nested slice.
	b, e := json.Marshal(c)
	if e != nil || len(b) > MaximumSnapshotBytes/2 {
		return nil, ErrInvalid
	}
	var result DecisionContext
	if json.Unmarshal(b, &result) != nil {
		return nil, ErrInvalid
	}
	if result.References == nil {
		result.References = []DecisionReference{}
	}
	if result.Sources == nil {
		result.Sources = []AssuranceSource{}
	}
	if result.Profile != nil {
		p, d, e := CanonicalAssuranceProfile(*result.Profile)
		if e != nil || d != result.ProfileDigest {
			return nil, ErrInvalid
		}
		result.Profile = &p
	} else if result.ProfileDigest != "" {
		return nil, ErrInvalid
	}
	for i, r := range result.References {
		switch r.Kind {
		case "decision", "snapshot", "claim", "identifier", "observation", "fact", "evidence", "lineage", "check", "attempt", "signal", "review_finding", "authority", "subject_response", "region", "transfer_policy", "capture_profile", "identity_receipt", "fraud_receipt", "provider", "model", "runtime", "preprocessing", "configuration", "threshold", "policy", "evaluator":
		default:
			return nil, ErrInvalid
		}
		if r.ID == "" || len(r.ID) > 256 || len(r.Version) > 128 || (r.Digest != "" && !validDigest(r.Digest)) || len(r.Parents) > 128 {
			return nil, ErrInvalid
		}
		for _, parent := range r.Parents {
			if parent == "" || len(parent) > 256 {
				return nil, ErrInvalid
			}
		}
		sort.Strings(result.References[i].Parents)
		result.References[i].Parents = slices.Compact(result.References[i].Parents)
	}
	sort.Slice(result.References, func(i, j int) bool {
		a, _ := json.Marshal(result.References[i])
		b, _ := json.Marshal(result.References[j])
		return string(a) < string(b)
	})
	result.References = slices.CompactFunc(result.References, func(a, b DecisionReference) bool {
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(b)
		return string(left) == string(right)
	})
	for i, s := range result.Sources {
		switch s.RunnerKind {
		case "provider", "model", "capture", "identity", "fraud", "review":
		default:
			return nil, ErrInvalid
		}
		if _, err := NewFactKey(s.Signal); err != nil {
			return nil, err
		}
		if !validToken(s.RunnerID, 128) || (s.ThresholdDigest != "" && !validDigest(s.ThresholdDigest)) || !validUTC(s.CollectedAt) || !validRequirementState(s.State) || s.Reference == "" || len(s.Reference) > 256 || len(s.Roots) == 0 || len(s.Roots) > 128 || !validDigest(s.PackageDigest) || !validDigest(s.ConfigurationDigest) {
			return nil, ErrInvalid
		}
		if s.ExpiresAt != nil && (!validUTC(*s.ExpiresAt) || !s.ExpiresAt.After(s.CollectedAt)) {
			return nil, ErrInvalid
		}
		roots, e := canonicalAssuranceRoots(s.Roots)
		if e != nil {
			return nil, e
		}
		result.Sources[i].Roots = roots
		assurances, err := canonicalTokens(s.CaptureAssurances)
		if err != nil {
			return nil, err
		}
		result.Sources[i].CaptureAssurances = assurances
	}
	sort.Slice(result.Sources, func(i, j int) bool { return result.Sources[i].Reference < result.Sources[j].Reference })
	for i := 1; i < len(result.Sources); i++ {
		if result.Sources[i].Reference == result.Sources[i-1].Reference {
			return nil, ErrConflict
		}
	}
	return &result, nil
}

// AchieveAssurance recomputes freshness and connected source groups at explicit time.
// Shared or transitively shared roots count once; evaluation-only models never qualify.
func AchieveAssurance(c *DecisionContext, at time.Time) (AssuranceAchievement, error) {
	result := AssuranceAchievement{Dimensions: []AssuranceDimensionResult{}}
	c, e := canonicalDecisionContext(c)
	if e != nil {
		return result, e
	}
	if !validUTC(at) {
		return result, ErrInvalid
	}
	if c == nil || c.Profile == nil {
		return result, nil
	}
	result.RequestedDigest = c.ProfileDigest
	result.Achieved = true
	for _, r := range c.Profile.Requirements {
		d := AssuranceDimensionResult{Dimension: r.Dimension, Property: r.Property, SourceReferences: []string{}}
		var sources []AssuranceSource
		for _, s := range c.Sources {
			if s.EvaluationOnly || (s.ExpiresAt != nil && !at.Before(*s.ExpiresAt)) || s.State != RequirementSatisfied || s.CollectedAt.After(at) || at.Sub(s.CollectedAt) >= time.Duration(r.MaximumAgeSeconds)*time.Second {
				continue
			}
			for _, m := range c.Profile.Mappings {
				if slices.Contains(r.Capabilities, m.Capability) && s.Signal == m.Signal && s.RunnerKind == m.RunnerKind && s.RunnerID == m.RunnerID && s.PackageDigest == m.PackageDigest && s.ConfigurationDigest == m.ConfigurationDigest && s.ThresholdDigest == m.ThresholdDigest && sourceCapabilityEligible(s, m) {
					sources = append(sources, s)
					d.SourceReferences = append(d.SourceReferences, s.Reference)
					break
				}
			}
		}
		parents := make([]int, len(sources))
		for i := range parents {
			parents[i] = i
		}
		root := func(i int) int {
			for parents[i] != i {
				i = parents[i]
			}
			return i
		}
		roots := map[string]int{}
		for i, s := range sources {
			for _, v := range s.Roots {
				if prior, ok := roots[v]; ok {
					parents[root(i)] = root(prior)
				} else {
					roots[v] = i
				}
			}
		}
		groups := map[int]bool{}
		for i := range sources {
			groups[root(i)] = true
		}
		d.IndependentSources = len(groups)
		d.Satisfied = d.IndependentSources >= int(r.MinimumSources)
		result.Achieved = result.Achieved && d.Satisfied
		result.Dimensions = append(result.Dimensions, d)
	}
	return result, nil
}

// Context returns a defensive copy of complete immutable provenance.
func (s Snapshot) Context() *DecisionContext { c, _ := canonicalDecisionContext(s.context); return c }

// Achievement resolves the profile using original source times, including in review.
func (s Snapshot) Achievement() (AssuranceAchievement, error) {
	return AchieveAssurance(s.context, s.evaluatedAt)
}

// ValidateAssuranceProfile returns canonical profile bytes for public validation.
func ValidateAssuranceProfile(p AssuranceProfile) ([]byte, string, error) {
	p, d, e := CanonicalAssuranceProfile(p)
	if e != nil {
		return nil, "", fmt.Errorf("validate assurance profile: %w", e)
	}
	b, e := json.Marshal(p)
	return b, d, e
}

// bindSnapshotContext records every public fact and the exact evaluator/policy
// even when a review copy adds new findings. Legacy snapshots remain untouched.
func bindSnapshotContext(input SnapshotInput) (*DecisionContext, error) {
	c, e := canonicalDecisionContext(input.Context)
	if e != nil || c == nil {
		return c, e
	}
	c.References = append(c.References, DecisionReference{Kind: "policy", ID: input.Policy.ID.String(), Version: fmt.Sprint(input.Policy.Revision), Digest: input.Policy.Digest}, DecisionReference{Kind: "evaluator", ID: input.Evaluator.Digest, Version: fmt.Sprintf("%d.%d", input.Evaluator.Major, input.Evaluator.Minor), Digest: input.Evaluator.Digest})
	for _, f := range input.Facts {
		parents := []string{}
		if f.Source.Check != nil {
			for _, o := range f.Source.Check.ObservationIDs {
				parents = append(parents, o.String())
			}
		}
		if f.Source.Authority != nil {
			parents = append(parents, f.Source.Authority.AuthorityID.String())
		}
		if f.Source.SubjectResponse != nil {
			parents = append(parents, f.Source.SubjectResponse.AcknowledgementID.String())
		}
		if f.Source.Identity != nil {
			parents = append(parents, f.Source.Identity.ReceiptDigest)
		}
		if f.Source.Fraud != nil {
			parents = append(parents, f.Source.Fraud.ReceiptDigest)
		}
		if f.Source.ReviewFinding != nil {
			parents = append(parents, f.Source.ReviewFinding.Reference)
			c.References = append(c.References, DecisionReference{Kind: "review_finding", ID: f.Source.ReviewFinding.Reference})
		}
		c.References = append(c.References, DecisionReference{Kind: "fact", ID: string(f.Key), Parents: parents})
	}
	return canonicalDecisionContext(c)
}

// SameAssuranceRequest checks that copying or replacing inputs preserves intent.
func SameAssuranceRequest(a, b Snapshot) bool {
	left, right := "", ""
	if a.context != nil {
		left = a.context.ProfileDigest
	}
	if b.context != nil {
		right = b.context.ProfileDigest
	}
	return left == right
}

// MergeRecaptureContext preserves parent intent and imports separately referenced
// child evidence. The parent snapshot time will re-evaluate all source freshness.
func MergeRecaptureContext(parent Snapshot, child Decision) (*DecisionContext, error) {
	if !SameAssuranceRequest(parent, child.Snapshot()) {
		return nil, ErrConflict
	}
	c := parent.Context()
	if c == nil {
		return nil, nil
	}
	other := child.Snapshot().Context()
	if other == nil {
		return c, nil
	}
	c.References = append(c.References, other.References...)
	c.References = append(c.References, DecisionReference{Kind: "decision", ID: child.ID().String(), Digest: child.Digest()}, DecisionReference{Kind: "snapshot", ID: child.Snapshot().Digest(), Digest: child.Snapshot().Digest()})
	seen := map[string]bool{}
	for _, source := range c.Sources {
		seen[source.Reference] = true
	}
	for _, source := range other.Sources {
		if !seen[source.Reference] {
			c.Sources = append(c.Sources, source)
			seen[source.Reference] = true
		}
	}
	return canonicalDecisionContext(c)
}

// AssuranceSummary is safe for ordinary decision reads: no source identifiers.
type AssuranceSummary struct {
	Requested  *AssuranceSelection         `json:"requested,omitempty"`
	Achieved   bool                        `json:"achieved"`
	Dimensions []AssuranceDimensionSummary `json:"dimensions"`
}

// AssuranceDimensionSummary preserves dimension meaning without evidence references.
type AssuranceDimensionSummary struct {
	Dimension          string `json:"dimension"`
	Property           string `json:"property"`
	Satisfied          bool   `json:"satisfied"`
	IndependentSources int    `json:"independent_sources"`
}

// AssuranceSummary returns requested and achieved dimensions without source references.
func (s Snapshot) AssuranceSummary() *AssuranceSummary {
	if s.context == nil {
		return nil
	}
	achievement, e := s.Achievement()
	if e != nil {
		return nil
	}
	summary := &AssuranceSummary{Achieved: achievement.Achieved, Dimensions: []AssuranceDimensionSummary{}}
	if s.context.Profile != nil {
		summary.Requested = &AssuranceSelection{Name: s.context.Profile.Name, Revision: s.context.Profile.Revision, Digest: s.context.ProfileDigest}
	}
	for _, d := range achievement.Dimensions {
		summary.Dimensions = append(summary.Dimensions, AssuranceDimensionSummary{Dimension: d.Dimension, Property: d.Property, Satisfied: d.Satisfied, IndependentSources: d.IndependentSources})
	}
	return summary
}

var assuranceRootPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]*$`)

func canonicalAssuranceRoots(values []string) ([]string, error) {
	r := slices.Clone(values)
	for _, v := range r {
		if len(v) > 256 || !assuranceRootPattern.MatchString(v) {
			return nil, ErrInvalid
		}
	}
	sort.Strings(r)
	for i := 1; i < len(r); i++ {
		if r[i] == r[i-1] {
			return nil, ErrInvalid
		}
	}
	return r, nil
}
