package identity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
)

// SourceBinding declares an accepted provider's upstream correlation domains.
// Unknown providers share one conservative group. Bindings do not prove source accuracy.
type SourceBinding struct {
	RunnerID      string   `json:"runner_id"`
	PackageDigest string   `json:"package_digest"`
	Groups        []string `json:"groups"`
}

// Requirement evaluates corroboration of a named typed fact without exposing its value to CEL.
type Requirement struct {
	MaximumAgeSeconds    int      `json:"maximum_age_seconds"`
	ExpectedBoolean      *bool    `json:"expected_boolean,omitempty"`
	Key                  string   `json:"key"`
	FactName             string   `json:"fact_name"`
	ValueType            string   `json:"value_type"`
	Unit                 string   `json:"unit,omitempty"`
	MinimumSources       int      `json:"minimum_independent_sources"`
	MinimumConfidenceBPS int      `json:"minimum_confidence_bps"`
	RequireFreshness     bool     `json:"require_freshness"`
	SourceClasses        []string `json:"source_classes"`
}

// Configuration is an immutable, explicitly activated tenant identity interpretation contract.
type Configuration struct {
	Region          string          `json:"region"`
	ProviderSources []SourceBinding `json:"provider_sources,omitempty"`
	Requirements    []Requirement   `json:"requirements,omitempty"`
}

// Validate bounds configuration and rejects ambiguous rule or source mappings.
func (c Configuration) Validate() error {
	if !validRegion(c.Region) || len(c.ProviderSources) > 64 || len(c.Requirements) > 16 {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, s := range c.ProviderSources {
		if !validRunner(s.RunnerID) || !validDigest(s.PackageDigest) || len(s.Groups) < 1 || len(s.Groups) > 16 || duplicates(s.Groups) {
			return ErrInvalid
		}
		for _, g := range s.Groups {
			if !validName(g) {
				return ErrInvalid
			}
		}
		k := s.RunnerID + ":" + s.PackageDigest
		if seen[k] {
			return ErrInvalid
		}
		seen[k] = true
	}
	seen = map[string]bool{}
	for _, r := range c.Requirements {
		if !validName(r.Key) || len(r.Key) < 9 || r.Key[:9] != "identity." || !validName(r.FactName) || r.MinimumSources < 1 || r.MinimumSources > 8 || r.MinimumConfidenceBPS < 0 || r.MinimumConfidenceBPS > 10000 || len(r.SourceClasses) == 0 || len(r.SourceClasses) > 3 || duplicates(r.SourceClasses) || (r.Unit != "" && !validName(r.Unit)) {
			return ErrInvalid
		}
		if r.MaximumAgeSeconds < 0 || r.MaximumAgeSeconds > 30*24*60*60 || (r.RequireFreshness && r.MaximumAgeSeconds == 0) {
			return ErrInvalid
		}
		if (r.ValueType == "boolean") != (r.ExpectedBoolean != nil) {
			return ErrInvalid
		}
		switch r.ValueType {
		case "string", "boolean", "integer", "decimal", "date", "timestamp":
		default:
			return ErrInvalid
		}
		for _, kind := range r.SourceClasses {
			if !slices.Contains([]string{"tenant_attested", "provider", "model"}, kind) {
				return ErrInvalid
			}
		}
		if seen[r.Key] {
			return ErrInvalid
		}
		seen[r.Key] = true
	}
	return nil
}
func validRegion(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return false
		}
	}
	return true
}
func validDigest(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

// Finding is a reference-only interpretation result, never a subject identity verdict.
type Finding struct {
	Key                string   `json:"key"`
	State              string   `json:"state"`
	Reason             string   `json:"reason"`
	IndependentSources int      `json:"independent_sources"`
	RecordIDs          []string `json:"record_ids"`
}

// Receipt pins immutable records, source configuration and the exact evaluation instant.
type Receipt struct {
	SubjectID            string    `json:"subject_id"`
	SubjectVersion       int64     `json:"subject_version"`
	VerificationID       string    `json:"verification_id"`
	ConfigurationVersion int64     `json:"configuration_version"`
	ConfigurationDigest  string    `json:"configuration_digest"`
	EvaluatedAt          time.Time `json:"evaluated_at"`
	Findings             []Finding `json:"findings"`
}

// Digest hashes reference-only metadata; it must not be used for predictable identity values.
func Digest(value any) (string, error) {
	b, e := json.Marshal(value)
	if e != nil {
		return "", e
	}
	d := sha256.Sum256(b)
	return hex.EncodeToString(d[:]), nil
}

// Evaluate requires equal eligible values across disjoint transitive lineage components.
// Shared roots connect components before value comparison, so a bridge cannot create independence.
func Evaluate(rule Requirement, records []Record, at time.Time, complete bool) Finding {
	f := Finding{Key: rule.Key, State: "inconclusive", Reason: "identity.input_unavailable", RecordIDs: []string{}}
	if !complete {
		return f
	}
	var eligible []Record
	for _, r := range records {
		if r.Kind != "fact" || r.Name != rule.FactName || r.ValueType != rule.ValueType || r.Unit != rule.Unit || !r.Current || !r.Available || r.Value == nil || at.Before(r.ValidFrom) || !at.Before(r.ValidUntil) || !at.Before(r.RetainUntil) {
			continue
		}
		if rule.MaximumAgeSeconds > 0 && !at.Before(r.ObservedAt.Add(time.Duration(rule.MaximumAgeSeconds)*time.Second)) {
			continue
		}
		if rule.RequireFreshness && (r.FreshUntil == nil || !at.Before(*r.FreshUntil)) {
			continue
		}
		if rule.MinimumConfidenceBPS > 0 && (r.ConfidenceBPS == nil || *r.ConfidenceBPS < rule.MinimumConfidenceBPS) {
			continue
		}
		accepted := len(r.SourceClasses) > 0
		for _, s := range r.SourceClasses {
			if !slices.Contains(rule.SourceClasses, s) {
				accepted = false
			}
		}
		if accepted && len(r.LineageRoots) > 0 {
			eligible = append(eligible, r)
			f.RecordIDs = append(f.RecordIDs, r.ID)
		}
	}
	if rule.ExpectedBoolean != nil {
		for _, r := range eligible {
			if r.Value.Text != strconv.FormatBool(*rule.ExpectedBoolean) {
				f.State = "not_satisfied"
				f.Reason = "identity.value_mismatch"
				slices.Sort(f.RecordIDs)
				return f
			}
		}
	}
	slices.Sort(f.RecordIDs)
	if len(eligible) == 0 {
		return f
	}
	parent := make([]int, len(eligible))
	for i := range parent {
		parent[i] = i
	}
	root := func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	owners := map[string]int{}
	for i, r := range eligible {
		for _, g := range r.LineageRoots {
			if j, ok := owners[g]; ok {
				parent[root(i)] = root(j)
			} else {
				owners[g] = i
			}
		}
	}
	// A component with conflicting values cannot supply corroboration of either value.
	values := map[int]Value{}
	conflict := map[int]bool{}
	for i, r := range eligible {
		g := root(i)
		v := *r.Value
		if old, ok := values[g]; ok && old != v {
			conflict[g] = true
		}
		values[g] = v
	}
	counts := map[Value]int{}
	for group, v := range values {
		if !conflict[group] {
			counts[v]++
		}
	}
	for _, n := range counts {
		f.IndependentSources = max(f.IndependentSources, n)
	}
	if len(conflict) > 0 || len(counts) > 1 {
		f.State = "not_satisfied"
		f.Reason = "identity.conflicting_values"
		return f
	}
	if f.IndependentSources >= rule.MinimumSources {
		f.State = "satisfied"
		f.Reason = "identity.corroborated"
	}
	return f
}

func validRunner(s string) bool {
	if len(s) < 1 || len(s) > 128 || !strings.ContainsAny(s[:1], "abcdefghijklmnopqrstuvwxyz") {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '_' && r != ':' && r != '-' {
			return false
		}
	}
	return true
}
