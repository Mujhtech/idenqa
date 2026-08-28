package evidence

import (
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	// OperationPlaintextRead permits only a purpose-bound plaintext stream.
	OperationPlaintextRead = "evidence.plaintext.read"
	// VariantOriginal permits the original collected artefact and no derivative.
	VariantOriginal  = "evidence.variant.original"
	maxGrantUses     = 32
	maxGrantLifetime = time.Hour
)

var (
	// ErrGrantDenied deliberately combines missing, mismatched, expired,
	// revoked, and exhausted grant states.
	ErrGrantDenied = errors.New("evidence: processing grant denied")
	// ErrGrantConflict identifies an invalid grant lifecycle transition.
	ErrGrantConflict = errors.New("evidence: processing grant conflict")
)

// Runner is the separately authenticated workload redeeming a grant.
type Runner struct {
	Identity        string
	WorkloadVersion string
}

// Actor identifies one authenticated or effective tenant actor in an audit record.
type Actor struct {
	Type string
	ID   string
}

// CommandAttribution records both identities responsible for a grant mutation.
type CommandAttribution struct {
	Principal   Actor
	TenantActor Actor
	Reason      string
}

// Valid reports whether the immutable command attribution is safe and bounded.
func (attribution CommandAttribution) Valid() bool {
	return validClassification(attribution.Principal.Type) &&
		validGrantLabel(attribution.Principal.ID) &&
		validClassification(attribution.TenantActor.Type) &&
		validGrantLabel(attribution.TenantActor.ID) &&
		validAuditReason(attribution.Reason)
}

// Valid reports whether both authenticated workload labels are safe and bounded.
func (runner Runner) Valid() bool {
	return validGrantLabel(runner.Identity) && validGrantLabel(runner.WorkloadVersion)
}

// GrantRecord is the complete durable non-secret processing-grant representation.
type GrantRecord struct {
	ID                 id.Grant
	TenantID           id.Tenant
	SubjectID          id.Subject
	VerificationID     id.Verification
	EvidenceID         id.Evidence
	RequirementKey     string
	AuthorityID        id.Authority
	ResponseID         id.Acknowledgement
	CheckReference     string
	Runner             Runner
	Purpose            Name
	Operation          string
	PermittedVariants  []string
	Region             string
	RecipientReference string
	OutputDestination  string
	PolicyReference    string
	MaximumUses        uint32
	Uses               uint32
	CreatedAt          time.Time
	ExpiresAt          time.Time
	RevokedAt          *time.Time
}

// Grant is one purpose-, runner-, asset-, and time-bound processing capability.
type Grant struct{ record GrantRecord }

// NewGrant creates a new unredeemed processing grant.
func NewGrant(record GrantRecord) (Grant, error) {
	record.Uses = 0
	record.RevokedAt = nil
	return restoreGrant(record)
}

// RestoreGrant validates a processing grant loaded from authoritative state.
func RestoreGrant(record GrantRecord) (Grant, error) { return restoreGrant(record) }

func restoreGrant(record GrantRecord) (Grant, error) {
	record.CreatedAt = record.CreatedAt.UTC()
	record.ExpiresAt = record.ExpiresAt.UTC()
	if record.ID.IsZero() || record.TenantID.IsZero() || record.SubjectID.IsZero() ||
		record.VerificationID.IsZero() || record.EvidenceID.IsZero() || record.AuthorityID.IsZero() ||
		record.ResponseID.IsZero() {
		return Grant{}, errors.New("evidence: processing grant identity is invalid")
	}
	if !record.Runner.Valid() ||
		!validClassification(record.CheckReference) || !validClassification(string(record.Purpose)) ||
		!validRequirementKey(record.RequirementKey) ||
		record.Operation != OperationPlaintextRead || !validClassification(record.Region) ||
		!validClassification(record.RecipientReference) || !validClassification(record.OutputDestination) ||
		!validClassification(record.PolicyReference) {
		return Grant{}, errors.New("evidence: processing grant scope is invalid")
	}
	if len(record.PermittedVariants) == 0 || len(record.PermittedVariants) > 16 {
		return Grant{}, errors.New("evidence: processing grant variants are invalid")
	}
	record.PermittedVariants = slices.Clone(record.PermittedVariants)
	slices.Sort(record.PermittedVariants)
	record.PermittedVariants = slices.Compact(record.PermittedVariants)
	for _, variant := range record.PermittedVariants {
		if !validClassification(variant) {
			return Grant{}, errors.New("evidence: processing grant variant is invalid")
		}
	}
	if !slices.Contains(record.PermittedVariants, VariantOriginal) {
		return Grant{}, errors.New("evidence: processing grant original variant is required")
	}
	if record.MaximumUses == 0 || record.MaximumUses > maxGrantUses || record.Uses > record.MaximumUses ||
		record.CreatedAt.IsZero() || !record.ExpiresAt.After(record.CreatedAt) ||
		record.ExpiresAt.Sub(record.CreatedAt) > maxGrantLifetime {
		return Grant{}, errors.New("evidence: processing grant use or time bounds are invalid")
	}
	if record.RevokedAt != nil {
		revokedAt := record.RevokedAt.UTC()
		if revokedAt.Before(record.CreatedAt) {
			return Grant{}, errors.New("evidence: processing grant revocation time is invalid")
		}
		record.RevokedAt = &revokedAt
	}

	return Grant{record: record}, nil
}

// Record returns a defensive durable representation.
func (grant Grant) Record() GrantRecord {
	record := grant.record
	record.PermittedVariants = slices.Clone(record.PermittedVariants)
	if record.RevokedAt != nil {
		revokedAt := *record.RevokedAt
		record.RevokedAt = &revokedAt
	}
	return record
}

// ID returns the processing-grant identifier.
func (grant Grant) ID() id.Grant { return grant.record.ID }

// TenantID returns the owning tenant identifier.
func (grant Grant) TenantID() id.Tenant { return grant.record.TenantID }

// Uses returns the number of atomically claimed uses.
func (grant Grant) Uses() uint32 { return grant.record.Uses }

// ClaimedFor validates the authoritative post-claim state returned by a durable adapter.
func (grant Grant) ClaimedFor(runner Runner, now time.Time) bool {
	now = now.UTC()
	return grant.record.Runner == runner && grant.record.Uses > 0 &&
		grant.record.Uses <= grant.record.MaximumUses && grant.record.RevokedAt == nil &&
		!now.Before(grant.record.CreatedAt) && now.Before(grant.record.ExpiresAt)
}

// Claim validates an authenticated runner and atomically modelled use boundary.
// Durable adapters must enforce the same predicates in their update statement.
func (grant Grant) Claim(runner Runner, now time.Time) (Grant, error) {
	now = now.UTC()
	record := grant.Record()
	if runner != record.Runner || now.Before(record.CreatedAt) || !now.Before(record.ExpiresAt) ||
		record.RevokedAt != nil || record.Uses >= record.MaximumUses {
		return Grant{}, ErrGrantDenied
	}
	record.Uses++
	return Grant{record: record}, nil
}

// Revoke irreversibly blocks every future use.
func (grant Grant) Revoke(now time.Time) (Grant, error) {
	now = now.UTC()
	if grant.record.RevokedAt != nil || now.Before(grant.record.CreatedAt) {
		return Grant{}, ErrGrantConflict
	}
	record := grant.Record()
	record.RevokedAt = &now
	return Grant{record: record}, nil
}

// Binds reports whether this grant authorises this exact immutable asset.
func (grant Grant) Binds(asset Asset) bool {
	assetRecord := asset.Record()
	return grant.record.TenantID == assetRecord.TenantID &&
		grant.record.SubjectID == assetRecord.SubjectID &&
		grant.record.VerificationID == assetRecord.VerificationID &&
		grant.record.EvidenceID == assetRecord.ID &&
		grant.record.RequirementKey == assetRecord.RequirementKey &&
		grant.record.Region == assetRecord.Region &&
		grant.record.Operation == OperationPlaintextRead &&
		slices.Contains(grant.record.PermittedVariants, VariantOriginal)
}

// ReadAuthorization is the exact live authority decision requested by evidence workflows.
type ReadAuthorization struct {
	TenantID           id.Tenant
	SubjectID          id.Subject
	VerificationID     id.Verification
	EvidenceID         id.Evidence
	RequirementKey     string
	Purpose            Name
	EvidenceType       Name
	RecipientReference string
	Region             string
}

// Redemption is one durable, retry-safe use of a processing grant.
// Reusing its ID must return the same attempt without consuming another use.
type Redemption struct {
	id      id.Redemption
	grant   Grant
	outcome GrantOutcome
}

// NewRedemption creates a pending attempt around an already claimed grant use.
func NewRedemption(identifier id.Redemption, claimed Grant) (Redemption, error) {
	return RestoreRedemption(identifier, claimed, "")
}

// RestoreRedemption validates a pending or terminal attempt loaded from persistence.
func RestoreRedemption(
	identifier id.Redemption,
	claimed Grant,
	outcome GrantOutcome,
) (Redemption, error) {
	if identifier.IsZero() || claimed.ID().IsZero() || claimed.Uses() == 0 ||
		(outcome != "" && !validGrantOutcome(outcome)) {
		return Redemption{}, errors.New("evidence: grant redemption is invalid")
	}
	return Redemption{id: identifier, grant: claimed, outcome: outcome}, nil
}

// ID returns the retry-safe redemption identifier.
func (redemption Redemption) ID() id.Redemption { return redemption.id }

// Grant returns the claimed grant snapshot associated with the attempt.
func (redemption Redemption) Grant() Grant { return redemption.grant }

// Complete returns the terminal form persisted by an outcome recorder.
func (redemption Redemption) Complete(outcome GrantOutcome) (Redemption, error) {
	if redemption.outcome != "" {
		if redemption.outcome == outcome {
			return redemption, nil
		}
		return Redemption{}, ErrGrantConflict
	}
	return RestoreRedemption(redemption.id, redemption.grant, outcome)
}

// ValidFor verifies the identity and claimed grant state returned by persistence.
func (redemption Redemption) ValidFor(
	grantID id.Grant,
	runner Runner,
	now time.Time,
) bool {
	if redemption.id.IsZero() || redemption.grant.ID() != grantID ||
		redemption.grant.Record().Runner != runner || redemption.grant.Uses() == 0 {
		return false
	}
	if redemption.outcome == "" {
		return redemption.grant.ClaimedFor(runner, now)
	}
	return validGrantOutcome(redemption.outcome)
}

// TerminalOutcome reports a previously persisted terminal result. A blank
// outcome means the redemption is pending and may resume idempotently.
func (redemption Redemption) TerminalOutcome() (GrantOutcome, bool) {
	return redemption.outcome, validGrantOutcome(redemption.outcome)
}

// ReceiverBinding is the trusted, authenticated delivery endpoint identity.
type ReceiverBinding struct {
	Runner            Runner
	CheckReference    string
	OutputDestination string
}

// Binds reports whether the receiver is the exact endpoint named by the grant.
func (binding ReceiverBinding) Binds(grant Grant) bool {
	record := grant.Record()
	return binding.Runner == record.Runner &&
		binding.CheckReference == record.CheckReference &&
		binding.OutputDestination == record.OutputDestination
}

// AuthorizationDecision pins the live authority and subject response used for a grant.
type AuthorizationDecision struct {
	AuthorityID     id.Authority
	ResponseID      id.Acknowledgement
	PolicyReference string
}

// GrantOutcome is the terminal audit classification for one claimed use.
type GrantOutcome string

const (
	// GrantOutcomeSucceeded means authenticated plaintext was staged, audited, and committed.
	GrantOutcomeSucceeded GrantOutcome = "succeeded"
	// GrantOutcomeDenied means current scope, authority, or evidence state denied release.
	GrantOutcomeDenied GrantOutcome = "denied"
	// GrantOutcomeIntegrityFailed means deterministic content integrity failed closed.
	GrantOutcomeIntegrityFailed GrantOutcome = "integrity_failed"
	// GrantOutcomeFailed means release failed without proving a content-integrity fault.
	GrantOutcomeFailed GrantOutcome = "failed"
)

func validGrantOutcome(outcome GrantOutcome) bool {
	switch outcome {
	case GrantOutcomeSucceeded, GrantOutcomeDenied, GrantOutcomeIntegrityFailed, GrantOutcomeFailed:
		return true
	default:
		return false
	}
}

func validGrantLabel(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || len(value) > 512 {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validAuditReason(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || len(value) > 500 {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
