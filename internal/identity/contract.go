package identity

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	// MaximumRecords bounds one deterministic policy projection. Reads use pagination.
	MaximumRecords = 2000
	// MaximumSources bounds transitive provenance and prevents unbounded ancestry.
	MaximumSources = 64
)

// Subject is the durable tenant-local identity aggregate. ExternalReference is revealed explicitly.
type Subject struct {
	ID                string     `json:"id"`
	Region            string     `json:"region"`
	State             string     `json:"state"`
	Version           int64      `json:"version"`
	ExternalReference *string    `json:"external_reference,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	DeletionID        string     `json:"deletion_id,omitempty"`
	ErasedAt          *time.Time `json:"erased_at,omitempty"`
}

// IdentifierMetadata describes identifier interpretation, never identity equivalence.
type IdentifierMetadata struct {
	VerificationStateSource string `json:"verification_state_source,omitempty"`
	VerificationActorID     string `json:"verification_actor_id,omitempty"`
	Namespace               string `json:"namespace"`
	Issuer                  string `json:"issuer"`
	VerificationState       string `json:"verification_state"`
	Masked                  string `json:"masked,omitempty"`
}

// Record is immutable metadata. Revealed values are transient and stored separately, encrypted.
type Record struct {
	ActorKeyID                 string              `json:"actor_key_id"`
	ID                         string              `json:"id"`
	SubjectID                  string              `json:"subject_id"`
	Kind                       string              `json:"kind"`
	Name                       string              `json:"name"`
	ValueType                  string              `json:"value_type"`
	SchemaVersion              int                 `json:"schema_version"`
	Sequence                   int64               `json:"sequence"`
	SeriesID                   string              `json:"series_id"`
	Supersedes                 string              `json:"supersedes,omitempty"`
	Normalization              string              `json:"normalization"`
	InputVersion               string              `json:"input_version"`
	SourceClass                string              `json:"source_class"`
	SourceClasses              []string            `json:"source_classes"`
	SourceConfigurationVersion int64               `json:"source_configuration_version"`
	SourceName                 string              `json:"source_name"`
	VerificationID             string              `json:"verification_id"`
	AuthorityID                string              `json:"authority_id"`
	AcknowledgementID          string              `json:"acknowledgement_id"`
	EvidenceIDs                []string            `json:"evidence_ids"`
	SourceRecordIDs            []string            `json:"source_record_ids"`
	AncestorIDs                []string            `json:"ancestor_ids"`
	OriginObservationID        string              `json:"origin_observation_id,omitempty"`
	OriginOutcome              string              `json:"origin_outcome,omitempty"`
	CheckID                    string              `json:"check_id,omitempty"`
	AttemptID                  string              `json:"attempt_id,omitempty"`
	PackageDigest              string              `json:"package_digest,omitempty"`
	RequestDigest              string              `json:"request_digest,omitempty"`
	ConfigurationDigest        string              `json:"configuration_digest,omitempty"`
	LineageRoots               []string            `json:"lineage_roots"`
	CollectedAt                time.Time           `json:"collected_at"`
	ObservedAt                 time.Time           `json:"observed_at"`
	RecordedAt                 time.Time           `json:"recorded_at"`
	ValidFrom                  time.Time           `json:"valid_from"`
	ValidUntil                 time.Time           `json:"valid_until"`
	FreshUntil                 *time.Time          `json:"fresh_until,omitempty"`
	RetainUntil                time.Time           `json:"retain_until"`
	ConfidenceBPS              *int                `json:"confidence_bps,omitempty"`
	Unit                       string              `json:"unit,omitempty"`
	Identifier                 *IdentifierMetadata `json:"identifier,omitempty"`
	Value                      *Value              `json:"value,omitempty"`
	Original                   *Value              `json:"original,omitempty"`
	Freshness                  string              `json:"freshness,omitempty"`
	Current                    bool                `json:"current"`
	Available                  bool                `json:"available"`
}

// RecordInput cannot claim a trusted source class or supply its own lineage roots.
// An existing runner observation can be imported by reference; values then come from Core.
type RecordInput struct {
	Kind                string              `json:"kind"`
	Name                string              `json:"name"`
	VerificationID      string              `json:"verification_id"`
	EvidenceIDs         []string            `json:"evidence_ids,omitempty"`
	SourceRecordIDs     []string            `json:"source_record_ids,omitempty"`
	OriginObservationID string              `json:"origin_observation_id,omitempty"`
	SourceName          string              `json:"source_name,omitempty"`
	InputVersion        string              `json:"input_version,omitempty"`
	Normalization       string              `json:"normalization"`
	Value               *Value              `json:"value,omitempty"`
	Original            *Value              `json:"original,omitempty"`
	Supersedes          string              `json:"supersedes,omitempty"`
	CollectedAt         time.Time           `json:"collected_at"`
	ObservedAt          time.Time           `json:"observed_at"`
	ValidFrom           time.Time           `json:"valid_from"`
	ValidUntil          time.Time           `json:"valid_until"`
	FreshUntil          *time.Time          `json:"fresh_until,omitempty"`
	RetainUntil         time.Time           `json:"retain_until"`
	ConfidenceBPS       *int                `json:"confidence_bps,omitempty"`
	Unit                string              `json:"unit,omitempty"`
	Identifier          *IdentifierMetadata `json:"identifier,omitempty"`
}

// Validate rejects incomplete or falsely authoritative inputs before persistence.
func (r RecordInput) Validate(at time.Time) error {
	if !validName(r.Name) || !validTime(at) || !validTime(r.CollectedAt) || !validTime(r.ObservedAt) || !validTime(r.ValidFrom) || !validTime(r.ValidUntil) || !validTime(r.RetainUntil) || r.CollectedAt.After(r.ObservedAt) || r.ObservedAt.After(at) || !r.ValidUntil.After(r.ValidFrom) || !r.RetainUntil.After(at) || r.RetainUntil.After(at.Add(30*24*time.Hour)) {
		return ErrInvalid
	}
	if _, e := id.ParseVerification(r.VerificationID); e != nil {
		return ErrInvalid
	}
	if r.FreshUntil != nil && (!validTime(*r.FreshUntil) || r.FreshUntil.Before(r.ObservedAt) || r.FreshUntil.After(r.ValidUntil)) {
		return ErrInvalid
	}
	if r.ConfidenceBPS != nil && (*r.ConfidenceBPS < 0 || *r.ConfidenceBPS > 10000) {
		return ErrInvalid
	}
	if r.Unit != "" && !validName(r.Unit) {
		return ErrInvalid
	}
	if len(r.EvidenceIDs) > MaximumSources || len(r.SourceRecordIDs) > MaximumSources || duplicates(r.EvidenceIDs) || duplicates(r.SourceRecordIDs) {
		return ErrInvalid
	}
	for _, e := range r.EvidenceIDs {
		if _, err := id.ParseEvidence(e); err != nil {
			return ErrInvalid
		}
	}
	if r.Supersedes != "" && !ValidRecordID(r.Supersedes) {
		return ErrInvalid
	}
	for _, s := range r.SourceRecordIDs {
		if !ValidRecordID(s) {
			return ErrInvalid
		}
	}
	if r.Identifier != nil && (r.Kind != "identifier" || !validName(r.Identifier.Namespace) || !validName(r.Identifier.Issuer) || !slices.Contains([]string{"unverified", "verified", "not_verified", "inconclusive"}, r.Identifier.VerificationState) || r.Identifier.VerificationStateSource != "" || r.Identifier.VerificationActorID != "" || r.Identifier.Masked != "") {
		return ErrInvalid
	}
	switch r.Kind {
	case "observation":
		if len(r.SourceRecordIDs) != 0 || r.Identifier != nil || r.Normalization != "identity.exact.v1" {
			return ErrInvalid
		}
		if r.OriginObservationID != "" {
			if _, e := id.ParseObservation(r.OriginObservationID); e != nil {
				return ErrInvalid
			}
			if r.Value != nil || r.Original != nil || r.SourceName != "" || r.InputVersion != "" || r.ConfidenceBPS != nil || r.Unit != "" || len(r.EvidenceIDs) != 0 {
				return ErrInvalid
			}
		} else if r.Value == nil || r.Value.Validate() != nil || !validName(r.SourceName) || !validName(r.InputVersion) {
			return ErrInvalid
		}
		if r.Original != nil && (r.Original.Validate() != nil || r.Value == nil || r.Original.Type != r.Value.Type) {
			return ErrInvalid
		}
	case "fact", "claim", "identifier":
		if len(r.SourceRecordIDs) == 0 || r.Value != nil || r.Original != nil || r.OriginObservationID != "" || r.SourceName != "" || r.InputVersion != "" || len(r.EvidenceIDs) != 0 {
			return ErrInvalid
		}
		if r.Kind != "fact" && len(r.SourceRecordIDs) != 1 {
			return ErrInvalid
		}
		if r.Kind == "identifier" && r.Identifier == nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	switch r.Normalization {
	case "identity.exact.v1", "identity.trim.v1", "identity.ascii_upper.v1":
	default:
		return ErrInvalid
	}
	return nil
}

// ValidRecordID accepts only the four owned opaque record identifiers.
func ValidRecordID(s string) bool {
	for _, prefix := range []id.Prefix{"obs", "fct", "clm", "idi"} {
		if _, e := id.Parse(prefix, s); e == nil {
			return true
		}
	}
	return false
}
func duplicates(s []string) bool {
	c := slices.Clone(s)
	slices.Sort(c)
	return len(slices.Compact(c)) != len(s)
}

// ProtectedValue is serialized only within the encryption boundary.
type ProtectedValue struct {
	Value    Value  `json:"value"`
	Original *Value `json:"original,omitempty"`
}

// CloneRecord takes an owned copy of nested metadata and transient values.
func CloneRecord(r Record) (Record, error) {
	b, e := json.Marshal(r)
	if e != nil {
		return Record{}, e
	}
	var c Record
	e = json.Unmarshal(b, &c)
	return c, e
}
