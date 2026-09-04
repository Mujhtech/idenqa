// Package review owns manual-review, correction, and appeal authority.
package review

import (
	"errors"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

var (
	// ErrInvalid means review meaning is malformed or unauthorised.
	ErrInvalid = errors.New("review: invalid operation")
	// ErrConflict means a stale version, duplicate claim, or changed replay.
	ErrConflict = errors.New("review: conflict")
	// ErrForbidden means the principal lacks authority or independence.
	ErrForbidden = errors.New("review: forbidden")
)

// Permission is an application authority required by review operations.
type Permission string

const (
	// PermissionClaim permits assignment of an eligible open review.
	PermissionClaim Permission = "reviews:claim"
	// PermissionFind permits a reasoned finding under an evidence grant.
	PermissionFind Permission = "reviews:find"
	// PermissionResolve permits independent correction authorship.
	PermissionResolve Permission = "reviews:resolve"
	// PermissionAppeal permits independent appeal resolution.
	PermissionAppeal Permission = "appeals:resolve"
)

// Principal is an authenticated reviewer with immutable authority input.
type Principal struct {
	ID             string
	Permissions    []Permission
	Certifications []string
}

func (principal Principal) permits(permission Permission) bool {
	return principal.ID != "" && slices.Contains(principal.Permissions, permission)
}

// CaseState is the bounded review lifecycle.
type CaseState string

const (
	// CaseOpen is an unassigned review case.
	CaseOpen CaseState = "open"
	// CaseClaimed has one current assigned reviewer.
	CaseClaimed CaseState = "claimed"
	// CaseAwaitingSecond needs an independent second finding.
	CaseAwaitingSecond CaseState = "awaiting_second"
	// CaseResolved has the required immutable findings.
	CaseResolved CaseState = "resolved"
)

// Oversight pins whether one or two independent findings are required.
type Oversight string

const (
	// OversightSingle requires one authorised finding.
	OversightSingle Oversight = "single"
	// OversightDual requires findings from two independent reviewers.
	OversightDual Oversight = "dual"
)

// Resolution is a closed human proposal consumed by deterministic decisioning.
type Resolution string

const (
	// ResolutionSatisfy records that reviewed requirements are satisfied.
	ResolutionSatisfy Resolution = "satisfy"
	// ResolutionNotSatisfy records that reviewed requirements are not satisfied.
	ResolutionNotSatisfy Resolution = "not_satisfy"
	// ResolutionRequestInput asks for additional subject input.
	ResolutionRequestInput Resolution = "request_input"
)

// EvidenceGrant is the narrow evidence authority presented to a reviewer.
type EvidenceGrant struct {
	ID         id.Grant
	ReviewerID string
	Region     string
	ExpiresAt  time.Time
	RevokedAt  time.Time
}

// Finding is one immutable reasoned reviewer statement.
type Finding struct {
	ID         id.Finding
	ReviewerID string
	Resolution Resolution
	ReasonCode string
	GrantIDs   []id.Grant
	RecordedAt time.Time
}

// Case is one tenant-owned optimistic review aggregate.
type Case struct {
	ID                  id.ReviewCase
	VerificationID      id.Verification
	ChallengedDecision  id.Decision
	SupersedesDecision  id.Decision
	Region              string
	RequiredCertificate string
	Oversight           Oversight
	State               CaseState
	AssignedReviewer    string
	Findings            []Finding
	Version             int64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// NewCase creates an unassigned case whose transport cannot resolve it.
func NewCase(identifier id.ReviewCase, verification id.Verification, challenged id.Decision, region, certificate string, oversight Oversight, now time.Time) (Case, error) {
	result := Case{ID: identifier, VerificationID: verification, ChallengedDecision: challenged, Region: region, RequiredCertificate: certificate, Oversight: oversight, State: CaseOpen, Version: 1, CreatedAt: now, UpdatedAt: now}
	if result.Validate() != nil {
		return Case{}, ErrInvalid
	}
	return result, nil
}

// Validate checks durable review meaning without consulting transport state.
func (review Case) Validate() error {
	if review.ID.IsZero() || review.VerificationID.IsZero() || review.ChallengedDecision.IsZero() || !bounded(review.Region, 63) || !bounded(review.RequiredCertificate, 128) ||
		(review.Oversight != OversightSingle && review.Oversight != OversightDual) || review.Version < 1 || review.CreatedAt.IsZero() || review.CreatedAt.Location() != time.UTC || review.UpdatedAt.Before(review.CreatedAt) || review.UpdatedAt.Location() != time.UTC {
		return ErrInvalid
	}
	return nil
}

// Claim assigns exactly one authorised certified reviewer at an expected version.
func (review Case) Claim(principal Principal, expectedVersion int64, now time.Time) (Case, error) {
	if review.Validate() != nil || expectedVersion != review.Version || review.State != CaseOpen || now.Before(review.UpdatedAt) || now.Location() != time.UTC {
		return Case{}, ErrConflict
	}
	if !principal.permits(PermissionClaim) || !slices.Contains(principal.Certifications, review.RequiredCertificate) {
		return Case{}, ErrForbidden
	}
	next := review
	next.AssignedReviewer, next.State, next.Version, next.UpdatedAt = principal.ID, CaseClaimed, review.Version+1, now
	return next, nil
}

// SubmitFinding appends one immutable finding after validating assignment,
// evidence grants, and dual-control separation.
func (review Case) SubmitFinding(principal Principal, finding Finding, grants []EvidenceGrant, expectedVersion int64) (Case, error) {
	if review.Validate() != nil || expectedVersion != review.Version || (review.State != CaseClaimed && review.State != CaseAwaitingSecond) {
		return Case{}, ErrConflict
	}
	if !principal.permits(PermissionFind) || principal.ID != finding.ReviewerID || finding.ID.IsZero() || !validResolution(finding.Resolution) || !bounded(finding.ReasonCode, 128) || finding.RecordedAt.Before(review.UpdatedAt) || finding.RecordedAt.Location() != time.UTC || len(finding.GrantIDs) == 0 {
		return Case{}, ErrForbidden
	}
	if review.State == CaseClaimed && principal.ID != review.AssignedReviewer {
		return Case{}, ErrForbidden
	}
	if review.State == CaseAwaitingSecond && (len(review.Findings) != 1 || principal.ID == review.Findings[0].ReviewerID) {
		return Case{}, ErrForbidden
	}
	for _, grantID := range finding.GrantIDs {
		found := false
		for _, grant := range grants {
			if grant.ID.String() == grantID.String() && grant.ReviewerID == principal.ID && grant.Region == review.Region && grant.ExpiresAt.After(finding.RecordedAt) && grant.RevokedAt.IsZero() {
				found = true
			}
		}
		if !found {
			return Case{}, ErrForbidden
		}
	}
	next := review
	next.Findings = append(append([]Finding(nil), review.Findings...), cloneFinding(finding))
	next.Version, next.UpdatedAt = review.Version+1, finding.RecordedAt
	if review.Oversight == OversightDual && len(next.Findings) == 1 {
		next.State, next.AssignedReviewer = CaseAwaitingSecond, ""
	} else {
		next.State = CaseResolved
	}
	return next, nil
}

// AuthorCorrection records only supersession lineage; prior decisions remain immutable.
func (review Case) AuthorCorrection(principal Principal, superseding id.Decision, expectedVersion int64, now time.Time) (Case, error) {
	if review.Validate() != nil || review.State != CaseResolved || expectedVersion != review.Version || superseding.IsZero() || superseding.String() == review.ChallengedDecision.String() || now.Before(review.UpdatedAt) || now.Location() != time.UTC {
		return Case{}, ErrConflict
	}
	if !principal.permits(PermissionResolve) || slices.ContainsFunc(review.Findings, func(finding Finding) bool { return finding.ReviewerID == principal.ID }) {
		return Case{}, ErrForbidden
	}
	next := review
	next.SupersedesDecision, next.Version, next.UpdatedAt = superseding, review.Version+1, now
	return next, nil
}

// AppealState is the bounded reconsideration lifecycle.
type AppealState string

const (
	// AppealRequested is an appeal awaiting an independent reviewer.
	AppealRequested AppealState = "requested"
	// AppealIndependentReview is assigned to an eligible independent reviewer.
	AppealIndependentReview AppealState = "independent_review"
	// AppealResolved has one immutable reasoned outcome.
	AppealResolved AppealState = "resolved"
)

// AppealOutcome is the closed independent-review result.
type AppealOutcome string

const (
	// AppealUpheld leaves the challenged decision authoritative.
	AppealUpheld AppealOutcome = "upheld"
	// AppealOverturned identifies an immutable superseding decision.
	AppealOverturned AppealOutcome = "overturned"
	// AppealMoreInput requires additional input before a new decision.
	AppealMoreInput AppealOutcome = "more_input"
)

// Appeal challenges one immutable decision and requires an independent resolver.
type Appeal struct {
	ID                  id.Appeal
	CaseID              id.ReviewCase
	ChallengedDecision  id.Decision
	OriginalReviewers   []string
	AssignedReviewer    string
	State               AppealState
	Outcome             AppealOutcome
	ReasonCode          string
	SupersedingDecision id.Decision
	Deadline            time.Time
	Version             int64
}

// Resolve records a reasoned independent outcome. An overturned decision must
// identify its immutable successor; other outcomes must not invent one.
func (appeal Appeal) Resolve(principal Principal, outcome AppealOutcome, reasonCode string, superseding id.Decision, expectedVersion int64, now time.Time) (Appeal, error) {
	if appeal.ID.IsZero() || appeal.CaseID.IsZero() || appeal.ChallengedDecision.IsZero() || appeal.State != AppealIndependentReview ||
		appeal.Version != expectedVersion || now.IsZero() || now.Location() != time.UTC || now.After(appeal.Deadline) {
		return Appeal{}, ErrConflict
	}
	if !principal.permits(PermissionAppeal) || principal.ID != appeal.AssignedReviewer ||
		slices.Contains(appeal.OriginalReviewers, principal.ID) || !bounded(reasonCode, 128) ||
		(outcome != AppealUpheld && outcome != AppealOverturned && outcome != AppealMoreInput) ||
		(outcome == AppealOverturned) != !superseding.IsZero() || superseding.String() == appeal.ChallengedDecision.String() {
		return Appeal{}, ErrForbidden
	}
	next := appeal
	next.State, next.Outcome, next.ReasonCode = AppealResolved, outcome, reasonCode
	next.SupersedingDecision, next.Version = superseding, appeal.Version+1
	return next, nil
}

// Assign selects an independent reviewer before the deadline.
func (appeal Appeal) Assign(principal Principal, expectedVersion int64, now time.Time) (Appeal, error) {
	if appeal.ID.IsZero() || appeal.CaseID.IsZero() || appeal.ChallengedDecision.IsZero() || appeal.State != AppealRequested || appeal.Version != expectedVersion || !now.Before(appeal.Deadline) {
		return Appeal{}, ErrConflict
	}
	if !principal.permits(PermissionAppeal) || slices.Contains(appeal.OriginalReviewers, principal.ID) {
		return Appeal{}, ErrForbidden
	}
	next := appeal
	next.AssignedReviewer, next.State, next.Version = principal.ID, AppealIndependentReview, appeal.Version+1
	return next, nil
}

func validResolution(value Resolution) bool {
	return value == ResolutionSatisfy || value == ResolutionNotSatisfy || value == ResolutionRequestInput
}
func bounded(value string, maximum int) bool { return len(value) > 0 && len(value) <= maximum }
func cloneFinding(value Finding) Finding {
	value.GrantIDs = append([]id.Grant(nil), value.GrantIDs...)
	return value
}
