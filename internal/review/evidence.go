package review

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Redaction is a normalized rectangle removed before evidence leaves Core.
type Redaction struct {
	X      int `json:"x"`
	Y      int `json:"y"`
	Width  int `json:"width"`
	Height int `json:"height"`
}

// DisplayRule is explicitly configured for an evidence requirement. Missing rules deny display.
type DisplayRule struct {
	Requirement string      `json:"requirement"`
	Purpose     string      `json:"purpose"`
	Redactions  []Redaction `json:"redactions"`
}

// Validate checks the bounded configuration before it is persisted.
func (rule DisplayRule) Validate() error {
	if !authorityLabel(rule.Requirement) || !authorityLabel(rule.Purpose) || len(rule.Redactions) > 64 {
		return ErrInvalid
	}
	for _, r := range rule.Redactions {
		if r.X < 0 || r.X > 10000 || r.Y < 0 || r.Y > 10000 || r.Width < 1 || r.Width > 10000 || r.Height < 1 || r.Height > 10000 || r.X+r.Width > 10000 || r.Y+r.Height > 10000 {
			return ErrInvalid
		}
	}
	return nil
}

// EvidenceAccess records a short-lived case/version/operator capability, never evidence bytes.
type EvidenceAccess struct {
	GrantID      id.Grant      `json:"grant_id"`
	RedemptionID id.Redemption `json:"redemption_id"`
	CaseID       id.ReviewCase `json:"case_id"`
	CaseVersion  int64         `json:"case_version"`
	EvidenceID   id.Evidence   `json:"evidence_id"`
	ReviewerID   string        `json:"reviewer_id"`
	ExpiresAt    time.Time     `json:"expires_at"`
	Redactions   []Redaction   `json:"-"`
}

// EvidenceRequest contains the authenticated command and expected case version.
type EvidenceRequest struct {
	CaseID     id.ReviewCase
	Version    int64
	EvidenceID id.Evidence
	Actor      Actor
	Retry      idempotency.Request
}

// EvidenceRepository keeps transactional evidence processing behind an owned port.
type EvidenceRepository interface {
	IssueEvidence(context.Context, tenant.Scope, EvidenceRequest) (EvidenceAccess, error)
	ReadEvidence(context.Context, tenant.Scope, Actor, id.Grant, evidence.PlaintextReceiver) error
}

// EvidenceService requires application permissions as well as current repository-backed reviewer authority.
type EvidenceService struct {
	repository EvidenceRepository
	now        func() time.Time
}

// NewEvidenceService constructs application-scoped evidence access.
func NewEvidenceService(repository EvidenceRepository, now func() time.Time) (*EvidenceService, error) {
	if repository == nil || now == nil {
		return nil, ErrInvalid
	}
	return &EvidenceService{repository, now}, nil
}

// Issue creates a short-lived case-bound display grant.
func (s *EvidenceService) Issue(ctx context.Context, auth access.Context, caseID id.ReviewCase, version int64, evidenceID id.Evidence, key string) (EvidenceAccess, error) {
	if err := auth.Require(access.PermissionReviewsWrite); err != nil {
		return EvidenceAccess{}, err
	}
	if caseID.IsZero() || version < 1 || evidenceID.IsZero() {
		return EvidenceAccess{}, ErrInvalid
	}
	encoded := []byte(caseID.String() + "/" + evidenceID.String() + "/" + fmt.Sprint(version))
	retry, err := idempotency.NewRequest(auth.TenantScope().ID(), auth.Principal().KeyID(), "reviews.evidence.issue", key, encoded, s.now().UTC().Truncate(time.Microsecond), 24*time.Hour)
	if err != nil {
		return EvidenceAccess{}, err
	}
	return s.repository.IssueEvidence(ctx, auth.TenantScope(), EvidenceRequest{caseID, version, evidenceID, Actor{auth.Principal().KeyID().String()}, retry})
}
func (s *EvidenceService) Read(ctx context.Context, auth access.Context, grantID id.Grant, receiver evidence.PlaintextReceiver) error {
	if err := auth.Require(access.PermissionReviewsWrite); err != nil {
		return err
	}
	return s.repository.ReadEvidence(ctx, auth.TenantScope(), Actor{auth.Principal().KeyID().String()}, grantID, receiver)
}

// CanReadEvidence applies the same assignment and independence rules as finding submission.
func (c Case) CanReadEvidence(principal Principal, version int64) error {
	if c.Validate() != nil || c.Version != version {
		return ErrConflict
	}
	if c.State == CaseEscalated && principal.permits(PermissionResolve) && slices.Contains(principal.Certifications, c.RequiredCertificate) {
		for _, finding := range c.Findings {
			if finding.ReviewerID == principal.ID {
				return ErrForbidden
			}
		}
		return nil
	}

	if !principal.permits(PermissionFind) || !slices.Contains(principal.Certifications, c.RequiredCertificate) {
		return ErrForbidden
	}
	if c.State == CaseClaimed && c.AssignedReviewer == principal.ID {
		return nil
	}
	if c.State == CaseAwaitingSecond && len(c.Findings) == 1 && c.Findings[0].ReviewerID != principal.ID {
		return nil
	}
	return ErrForbidden
}

// EvidenceMetadata contains only the artefact identifiers needed to request display.
type EvidenceMetadata struct {
	ID                string `json:"id"`
	Requirement       string `json:"requirement"`
	EvidenceType      string `json:"evidence_type"`
	Artefact          string `json:"artefact"`
	AcquisitionMethod string `json:"acquisition_method"`
}

// List returns only display-eligible artefact metadata.
func (s *EvidenceService) List(ctx context.Context, auth access.Context, caseID id.ReviewCase, version int64) ([]EvidenceMetadata, error) {
	if err := auth.Require(access.PermissionReviewsWrite); err != nil {
		return nil, err
	}
	repository, ok := s.repository.(interface {
		ListEvidence(context.Context, tenant.Scope, Actor, id.ReviewCase, int64) ([]EvidenceMetadata, error)
	})
	if !ok || caseID.IsZero() || version < 1 {
		return nil, ErrInvalid
	}
	return repository.ListEvidence(ctx, auth.TenantScope(), Actor{auth.Principal().KeyID().String()}, caseID, version)
}
