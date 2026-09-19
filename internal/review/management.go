package review

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Administration is a versioned, audited change to tenant-owned operational configuration.
type Administration struct {
	Kind            string
	Reference       string
	ExpectedVersion int64
	Configuration   json.RawMessage
	Actor           Actor
	At              time.Time
	Retry           idempotency.Request
}

// AdministrationRecord returns the committed configuration and version.
type AdministrationRecord struct {
	Kind          string          `json:"kind"`
	Reference     string          `json:"reference"`
	Version       int64           `json:"version"`
	Configuration json.RawMessage `json:"configuration"`
}

// AdministrationRepository owns atomic configuration history and current state.
type AdministrationRepository interface {
	Administer(context.Context, tenant.Scope, Administration) (AdministrationRecord, error)
	ReadAdministration(context.Context, tenant.Scope, string, string) (AdministrationRecord, error)
}

// Management authorizes tenant review administration.
type Management struct {
	repository AdministrationRepository
	now        func() time.Time
}

// NewManagement constructs review administration.
func NewManagement(repository AdministrationRepository, now func() time.Time) (*Management, error) {
	if repository == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Management{repository, now}, nil
}

// OperatorConfiguration is an explicit tenant attestation, not proof from an external issuer.
type OperatorConfiguration struct {
	Assignment Assignment `json:"assignment"`
	Revoked    bool       `json:"revoked"`
}

// PutOperator versions a tenant-attested operator assignment.
func (m *Management) PutOperator(ctx context.Context, auth access.Context, keyID id.APIKey, input OperatorConfiguration, version int64, key string) (AdministrationRecord, error) {
	if err := auth.Require(access.PermissionReviewsAdmin); err != nil {
		return AdministrationRecord{}, err
	}
	if input.Assignment.TenantID != auth.TenantScope().ID().String() || input.Assignment.APIKeyID != keyID.String() {
		return AdministrationRecord{}, ErrInvalid
	}
	if _, err := NewRegistry([]Assignment{input.Assignment}); err != nil {
		return AdministrationRecord{}, err
	}
	return m.change(ctx, auth, "operator", keyID.String(), input, version, key)
}

// PutPolicy versions settings for an exact policy revision.
func (m *Management) PutPolicy(ctx context.Context, auth access.Context, input PolicySettings, version int64, key string) (AdministrationRecord, error) {
	if err := auth.Require(access.PermissionReviewsAdmin); err != nil {
		return AdministrationRecord{}, err
	}
	if input.TenantID != auth.TenantScope().ID().String() || input.Validate() != nil || input.Revision == 0 || len(input.PolicyDigest) != 64 {
		return AdministrationRecord{}, ErrInvalid
	}
	if _, err := id.ParsePolicy(input.PolicyID); err != nil {
		return AdministrationRecord{}, ErrInvalid
	}
	return m.change(ctx, auth, "policy", fmt.Sprintf("%s/%d", input.PolicyID, input.Revision), input, version, key)
}
func (m *Management) change(ctx context.Context, auth access.Context, kind, reference string, input any, version int64, key string) (AdministrationRecord, error) {
	if version < 0 {
		return AdministrationRecord{}, ErrInvalid
	}
	body, err := json.Marshal(input)
	if err != nil {
		return AdministrationRecord{}, err
	}
	canonical, err := json.Marshal(struct {
		Kind      string
		Reference string
		Version   int64
		Body      json.RawMessage
	}{kind, reference, version, body})
	if err != nil {
		return AdministrationRecord{}, err
	}
	now := m.now().UTC().Truncate(time.Microsecond)
	retry, err := idempotency.NewRequest(auth.TenantScope().ID(), auth.Principal().KeyID(), "reviews.admin."+kind, key, canonical, now, 24*time.Hour)
	if err != nil {
		return AdministrationRecord{}, err
	}
	return m.repository.Administer(ctx, auth.TenantScope(), Administration{kind, reference, version, body, Actor{auth.Principal().KeyID().String()}, now, retry})
}
func (m *Management) Read(ctx context.Context, auth access.Context, kind, reference string) (AdministrationRecord, error) {
	if err := auth.Require(access.PermissionReviewsAdmin); err != nil {
		return AdministrationRecord{}, err
	}
	return m.repository.ReadAdministration(ctx, auth.TenantScope(), kind, reference)
}

// QueueConfiguration changes operational priority and SLA without changing policy or case versions.
type QueueConfiguration struct {
	Priority  int       `json:"priority"`
	DueAt     time.Time `json:"due_at"`
	Language  string    `json:"language"`
	Reason    string    `json:"reason"`
	Assurance string    `json:"assurance"`
	Risk      string    `json:"risk"`
	Sampled   bool      `json:"sampled"`
}

// Validate checks the bounded configuration before it is persisted.
func (q QueueConfiguration) Validate() error {
	if q.Priority < 0 || q.Priority > 9 || q.DueAt.IsZero() {
		return ErrInvalid
	}
	for _, label := range []string{q.Language, q.Reason, q.Assurance, q.Risk} {
		if !authorityLabel(label) {
			return ErrInvalid
		}
	}
	return nil
}

// PutQueue updates queue metadata without altering case findings.
func (m *Management) PutQueue(ctx context.Context, auth access.Context, caseID id.ReviewCase, input QueueConfiguration, version int64, key string) (AdministrationRecord, error) {
	if err := auth.Require(access.PermissionReviewsAdmin); err != nil {
		return AdministrationRecord{}, err
	}
	if caseID.IsZero() || input.Validate() != nil {
		return AdministrationRecord{}, ErrInvalid
	}
	return m.change(ctx, auth, "queue", caseID.String(), input, version, key)
}

// BindCase pins explicit display and operational settings to an existing case.
func (m *Management) BindCase(ctx context.Context, auth access.Context, caseID id.ReviewCase, input PolicySettings, key string) (AdministrationRecord, error) {
	if err := auth.Require(access.PermissionReviewsAdmin); err != nil {
		return AdministrationRecord{}, err
	}
	if caseID.IsZero() || input.Validate() != nil || input.TenantID != auth.TenantScope().ID().String() {
		return AdministrationRecord{}, ErrInvalid
	}
	return m.change(ctx, auth, "case-settings", caseID.String(), input, 0, key)
}
