package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// SnapshotInput contains explicit immutable evaluation inputs. No dependency
// is permitted to supply a clock implicitly.
type SnapshotInput struct {
	Context           *DecisionContext
	TenantID          id.Tenant
	VerificationID    id.Verification
	AuthorityID       id.Authority
	AcknowledgementID id.Acknowledgement
	Region            string
	Policy            Reference
	Evaluator         EvaluatorReference
	EvaluatedAt       time.Time
	Facts             []Fact
}

// Snapshot is the canonical fact set used for exactly one evaluation.
type Snapshot struct {
	context           *DecisionContext
	tenantID          id.Tenant
	verificationID    id.Verification
	authorityID       id.Authority
	acknowledgementID id.Acknowledgement
	region            string
	policy            Reference
	evaluator         EvaluatorReference
	evaluatedAt       time.Time
	facts             []Fact
	canonical         []byte
	digest            string
}

type canonicalSnapshot struct {
	Context           *DecisionContext   `json:"context,omitempty"`
	SchemaMajor       uint16             `json:"schema_major"`
	SchemaMinor       uint16             `json:"schema_minor"`
	TenantID          string             `json:"tenant_id"`
	VerificationID    string             `json:"verification_id"`
	AuthorityID       string             `json:"authority_id"`
	AcknowledgementID string             `json:"acknowledgement_id"`
	Region            string             `json:"region"`
	Policy            canonicalPolicy    `json:"policy"`
	Evaluator         canonicalEvaluator `json:"evaluator"`
	EvaluatedAt       time.Time          `json:"evaluated_at"`
	Facts             []canonicalFact    `json:"facts"`
}

type canonicalPolicy struct {
	ID          string `json:"id"`
	Revision    uint32 `json:"revision"`
	SchemaMajor uint16 `json:"schema_major"`
	SchemaMinor uint16 `json:"schema_minor"`
	Digest      string `json:"digest"`
}

type canonicalEvaluator struct {
	Major  uint16 `json:"major"`
	Minor  uint16 `json:"minor"`
	Digest string `json:"digest"`
}

type canonicalFact struct {
	Key         string           `json:"key"`
	State       RequirementState `json:"state"`
	Source      canonicalSource  `json:"source"`
	ObservedAt  time.Time        `json:"observed_at"`
	ExpiresAt   *time.Time       `json:"expires_at,omitempty"`
	ReasonCodes []string         `json:"reason_codes"`
}

type canonicalSource struct {
	IdentityReceipt      string         `json:"identity_receipt,omitempty"`
	Kind                 FactSourceKind `json:"kind"`
	CheckID              string         `json:"check_id,omitempty"`
	CheckVersion         int64          `json:"check_version,omitempty"`
	AttemptID            string         `json:"attempt_id,omitempty"`
	ObservationIDs       []string       `json:"observation_ids,omitempty"`
	ContractDigest       string         `json:"contract_digest,omitempty"`
	ImplementationDigest string         `json:"implementation_digest,omitempty"`
	AuthorityID          string         `json:"authority_id,omitempty"`
	AcknowledgementID    string         `json:"acknowledgement_id,omitempty"`
	ReviewFinding        string         `json:"review_finding,omitempty"`
	FraudReceipt         string         `json:"fraud_receipt,omitempty"`
}

// NewSnapshot validates, copies, orders, serialises, and digests exact inputs.
func NewSnapshot(input SnapshotInput) (Snapshot, error) {
	if input.TenantID.IsZero() || input.VerificationID.IsZero() || input.AuthorityID.IsZero() ||
		input.AcknowledgementID.IsZero() || !validToken(input.Region, 64) || !validUTC(input.EvaluatedAt) ||
		len(input.Facts) == 0 || len(input.Facts) > MaximumFacts {
		return Snapshot{}, ErrInvalid
	}
	if err := input.Policy.validate(); err != nil {
		return Snapshot{}, err
	}
	if err := input.Evaluator.validate(); err != nil {
		return Snapshot{}, err
	}

	context, err := bindSnapshotContext(input)
	if err != nil {
		return Snapshot{}, err
	}
	input.Context = context
	facts := make([]Fact, len(input.Facts))
	reasonCount := 0
	for index, fact := range input.Facts {
		validated, err := validateFact(fact, input.EvaluatedAt)
		if err != nil {
			return Snapshot{}, fmt.Errorf("validate fact: %w", err)
		}
		facts[index] = validated
		reasonCount += len(validated.ReasonCodes)
		if reasonCount > MaximumSnapshotReasons {
			return Snapshot{}, ErrInvalid
		}
	}
	sort.Slice(facts, func(left, right int) bool { return facts[left].Key < facts[right].Key })
	for index := 1; index < len(facts); index++ {
		if facts[index-1].Key != facts[index].Key {
			continue
		}
		left, leftErr := json.Marshal(canonicalFactOf(facts[index-1]))
		right, rightErr := json.Marshal(canonicalFactOf(facts[index]))
		if leftErr != nil || rightErr != nil {
			return Snapshot{}, ErrInvalid
		}
		if bytes.Equal(left, right) {
			return Snapshot{}, ErrInvalid
		}
		return Snapshot{}, ErrConflict
	}

	encoded, err := json.Marshal(canonicalSnapshotOf(input, facts))
	if err != nil {
		return Snapshot{}, fmt.Errorf("encode policy snapshot: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return Snapshot{
		tenantID: input.TenantID, verificationID: input.VerificationID,
		authorityID: input.AuthorityID, acknowledgementID: input.AcknowledgementID,
		region: input.Region, policy: input.Policy, evaluator: input.Evaluator,
		evaluatedAt: input.EvaluatedAt, facts: facts, context: context,
		canonical: slices.Clone(encoded), digest: hex.EncodeToString(sum[:]),
	}, nil
}

// RestoreSnapshot accepts stored input only when its canonical digest matches.
func RestoreSnapshot(input SnapshotInput, digest string) (Snapshot, error) {
	if !validDigest(digest) {
		return Snapshot{}, ErrReproduction
	}
	snapshot, err := NewSnapshot(input)
	if err != nil {
		return Snapshot{}, err
	}
	if snapshot.digest != digest {
		return Snapshot{}, ErrReproduction
	}
	return snapshot, nil
}

// TenantID returns the tenant bound to the snapshot.
func (snapshot Snapshot) TenantID() id.Tenant { return snapshot.tenantID }

// VerificationID returns the verification bound to the snapshot.
func (snapshot Snapshot) VerificationID() id.Verification { return snapshot.verificationID }

// AuthorityID returns the processing authority used by the snapshot.
func (snapshot Snapshot) AuthorityID() id.Authority { return snapshot.authorityID }

// AcknowledgementID returns the subject response used by the snapshot.
func (snapshot Snapshot) AcknowledgementID() id.Acknowledgement { return snapshot.acknowledgementID }

// Region returns the explicit policy region.
func (snapshot Snapshot) Region() string { return snapshot.region }

// Policy returns the exact policy reference.
func (snapshot Snapshot) Policy() Reference { return snapshot.policy }

// Evaluator returns the exact evaluator reference.
func (snapshot Snapshot) Evaluator() EvaluatorReference { return snapshot.evaluator }

// EvaluatedAt returns the explicit evaluation time.
func (snapshot Snapshot) EvaluatedAt() time.Time { return snapshot.evaluatedAt }

// Digest returns the canonical snapshot digest.
func (snapshot Snapshot) Digest() string { return snapshot.digest }

// Canonical returns a defensive copy of canonical snapshot bytes.
func (snapshot Snapshot) Canonical() []byte { return slices.Clone(snapshot.canonical) }

// Facts returns an ordered defensive copy.
func (snapshot Snapshot) Facts() []Fact {
	result := make([]Fact, len(snapshot.facts))
	for index, fact := range snapshot.facts {
		result[index] = cloneFact(fact)
	}
	return result
}

func validateFact(value Fact, evaluatedAt time.Time) (Fact, error) {
	if _, err := NewFactKey(string(value.Key)); err != nil || !validRequirementState(value.State) ||
		!validUTC(value.ObservedAt) || value.ObservedAt.After(evaluatedAt) ||
		len(value.ReasonCodes) > MaximumReasonsPerItem {
		return Fact{}, ErrInvalid
	}
	if value.ExpiresAt != nil {
		if !validUTC(*value.ExpiresAt) || !value.ExpiresAt.After(value.ObservedAt) {
			return Fact{}, ErrInvalid
		}
		if !evaluatedAt.Before(*value.ExpiresAt) {
			return Fact{}, ErrStaleFact
		}
	}
	reasons, err := canonicalTokens(value.ReasonCodes)
	if err != nil {
		return Fact{}, err
	}
	source, err := validateSource(value.Source)
	if err != nil {
		return Fact{}, err
	}
	validated := cloneFact(value)
	validated.Source, validated.ReasonCodes = source, reasons
	return validated, nil
}

func validateSource(value FactSource) (FactSource, error) {
	set := 0
	for _, present := range []bool{value.Check != nil, value.Authority != nil,
		value.SubjectResponse != nil, value.ReviewFinding != nil, value.Fraud != nil, value.Identity != nil} {
		if present {
			set++
		}
	}
	if set != 1 {
		return FactSource{}, ErrInvalid
	}
	cloned := cloneSource(value)
	switch value.Kind {
	case FactSourceIdentity:
		if value.Identity == nil || !validDigest(value.Identity.ReceiptDigest) {
			return FactSource{}, ErrInvalid
		}
	case FactSourceFraud:
		if value.Fraud == nil || !validDigest(value.Fraud.ReceiptDigest) {
			return FactSource{}, ErrInvalid
		}
	case FactSourceCheck:
		if value.Check == nil || value.Authority != nil || value.SubjectResponse != nil || value.ReviewFinding != nil ||
			value.Check.CheckID.IsZero() || value.Check.CheckVersion < 1 || value.Check.AttemptID.IsZero() ||
			len(value.Check.ObservationIDs) == 0 || len(value.Check.ObservationIDs) > MaximumObservations ||
			!validDigest(value.Check.ContractDigest) || !validDigest(value.Check.ImplementationDigest) {
			return FactSource{}, ErrInvalid
		}
		sort.Slice(cloned.Check.ObservationIDs, func(left, right int) bool {
			return cloned.Check.ObservationIDs[left].String() < cloned.Check.ObservationIDs[right].String()
		})
		for index, observationID := range cloned.Check.ObservationIDs {
			if observationID.IsZero() || (index > 0 && observationID.String() == cloned.Check.ObservationIDs[index-1].String()) {
				return FactSource{}, ErrInvalid
			}
		}
	case FactSourceProcessingAuthority:
		if value.Authority == nil || value.Check != nil || value.SubjectResponse != nil || value.ReviewFinding != nil ||
			value.Authority.AuthorityID.IsZero() {
			return FactSource{}, ErrInvalid
		}
	case FactSourceSubjectResponse:
		if value.SubjectResponse == nil || value.Check != nil || value.Authority != nil || value.ReviewFinding != nil ||
			value.SubjectResponse.AcknowledgementID.IsZero() {
			return FactSource{}, ErrInvalid
		}
	case FactSourceReviewFinding:
		if value.ReviewFinding == nil || value.Check != nil || value.Authority != nil || value.SubjectResponse != nil ||
			!validToken(value.ReviewFinding.Reference, 128) {
			return FactSource{}, ErrInvalid
		}
	default:
		return FactSource{}, ErrInvalid
	}
	return cloned, nil
}

func canonicalTokens(values []string) ([]string, error) {
	result := slices.Clone(values)
	for _, value := range result {
		if !validToken(value, 100) {
			return nil, ErrInvalid
		}
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, ErrInvalid
		}
	}
	return result, nil
}

func canonicalSnapshotOf(input SnapshotInput, facts []Fact) canonicalSnapshot {
	canonicalFacts := make([]canonicalFact, len(facts))
	for index, fact := range facts {
		canonicalFacts[index] = canonicalFactOf(fact)
	}
	return canonicalSnapshot{
		Context:     input.Context,
		SchemaMajor: SnapshotSchemaMajor, SchemaMinor: SnapshotSchemaMinor,
		TenantID: input.TenantID.String(), VerificationID: input.VerificationID.String(),
		AuthorityID: input.AuthorityID.String(), AcknowledgementID: input.AcknowledgementID.String(),
		Region: input.Region,
		Policy: canonicalPolicy{ID: input.Policy.ID.String(), Revision: input.Policy.Revision,
			SchemaMajor: input.Policy.SchemaMajor, SchemaMinor: input.Policy.SchemaMinor, Digest: input.Policy.Digest},
		Evaluator:   canonicalEvaluator{Major: input.Evaluator.Major, Minor: input.Evaluator.Minor, Digest: input.Evaluator.Digest},
		EvaluatedAt: input.EvaluatedAt, Facts: canonicalFacts,
	}
}

func canonicalFactOf(value Fact) canonicalFact {
	expiresAt := value.ExpiresAt
	return canonicalFact{
		Key: string(value.Key), State: value.State, Source: canonicalSourceOf(value.Source),
		ObservedAt: value.ObservedAt, ExpiresAt: expiresAt, ReasonCodes: slices.Clone(value.ReasonCodes),
	}
}

func canonicalSourceOf(value FactSource) canonicalSource {
	result := canonicalSource{Kind: value.Kind}
	if value.Check != nil {
		result.CheckID, result.CheckVersion = value.Check.CheckID.String(), value.Check.CheckVersion
		result.AttemptID = value.Check.AttemptID.String()
		result.ContractDigest, result.ImplementationDigest = value.Check.ContractDigest, value.Check.ImplementationDigest
		result.ObservationIDs = make([]string, len(value.Check.ObservationIDs))
		for index, observationID := range value.Check.ObservationIDs {
			result.ObservationIDs[index] = observationID.String()
		}
	}
	if value.Authority != nil {
		result.AuthorityID = value.Authority.AuthorityID.String()
	}
	if value.SubjectResponse != nil {
		result.AcknowledgementID = value.SubjectResponse.AcknowledgementID.String()
	}
	if value.ReviewFinding != nil {
		result.ReviewFinding = value.ReviewFinding.Reference
	}
	if value.Identity != nil {
		result.IdentityReceipt = value.Identity.ReceiptDigest
	}
	if value.Fraud != nil {
		result.FraudReceipt = value.Fraud.ReceiptDigest
	}
	return result
}

func snapshotInput(snapshot Snapshot) SnapshotInput {
	return SnapshotInput{
		TenantID: snapshot.tenantID, VerificationID: snapshot.verificationID,
		AuthorityID: snapshot.authorityID, AcknowledgementID: snapshot.acknowledgementID,
		Region: snapshot.region, Policy: snapshot.policy, Evaluator: snapshot.evaluator,
		EvaluatedAt: snapshot.evaluatedAt, Facts: snapshot.Facts(), Context: snapshot.Context(),
	}
}

func validateSnapshot(snapshot Snapshot) error {
	_, err := RestoreSnapshot(snapshotInput(snapshot), snapshot.digest)
	return err
}
