package fraud

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Command is a closed authorized, idempotent mutation.
type Command struct {
	Operation       string
	ExpectedVersion int64
	Configuration   *Configuration
	Input           *Input
	Proposal        *Proposal
	Retry           idempotency.Request
	At              time.Time
}

// Result exposes configuration or reference-only evaluation state.
type Result struct {
	Version       int64          `json:"version,omitempty"`
	Digest        string         `json:"digest,omitempty"`
	Configuration *Configuration `json:"configuration,omitempty"`
	Receipt       *Receipt       `json:"receipt,omitempty"`
	Proposal      *Proposal      `json:"proposal,omitempty"`
}

// Repository owns transactional fraud mutations and safe reads.
type Repository interface {
	Execute(context.Context, tenant.Scope, Command) (Result, error)
	Read(context.Context, tenant.Scope, string, string) (Result, error)
}

// Service authorizes all tenant operations at the application boundary.
type Service struct {
	repository Repository
	now        func() time.Time
}

// NewService constructs authorized fraud operations with an explicit clock.
func NewService(r Repository, now func() time.Time) (*Service, error) {
	if r == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Service{r, now}, nil
}

// Execute validates and fingerprints a tenant command.
func (s *Service) Execute(ctx context.Context, auth access.Context, key string, c Command) (Result, error) {
	permission := access.PermissionFraudWrite
	if c.Operation == "configure" {
		permission = access.PermissionFraudConfigure
	}
	if e := auth.Require(permission); e != nil {
		return Result{}, e
	}
	switch c.Operation {
	case "configure":
		if c.Configuration == nil || c.Configuration.Validate() != nil || c.ExpectedVersion < 0 || c.ExpectedVersion == 9223372036854775807 || c.Input != nil || c.Proposal != nil {
			return Result{}, ErrInvalid
		}
	case "ingest":
		if c.Input == nil || c.Input.Validate() != nil || c.Configuration != nil || c.Proposal != nil {
			return Result{}, ErrInvalid
		}
	case "propose":
		if c.Proposal == nil || !namePattern.MatchString(c.Proposal.Hypothesis) || len(c.Proposal.Signals) < 1 || len(c.Proposal.Signals) > 12 || len(c.Proposal.ReceiptDigest) != 64 || c.Configuration != nil || c.Input != nil {
			return Result{}, ErrInvalid
		}
	default:
		return Result{}, ErrInvalid
	}
	c.At = s.now().UTC().Truncate(time.Microsecond)
	b, e := json.Marshal(struct {
		Operation string
		Version   int64
		Config    *Configuration
		Input     *Input
		Proposal  *Proposal
	}{c.Operation, c.ExpectedVersion, c.Configuration, c.Input, c.Proposal})
	if e != nil {
		return Result{}, e
	}
	c.Retry, e = idempotency.NewRequest(auth.TenantScope().ID(), auth.Principal().KeyID(), "fraud."+c.Operation, key, b, c.At, 24*time.Hour)
	if e != nil {
		return Result{}, e
	}
	return s.repository.Execute(ctx, auth.TenantScope(), c)
}

// Read authorizes access to safe fraud metadata.
func (s *Service) Read(ctx context.Context, auth access.Context, kind, reference string) (Result, error) {
	if e := auth.Require(access.PermissionFraudRead); e != nil {
		return Result{}, e
	}
	return s.repository.Read(ctx, auth.TenantScope(), kind, reference)
}
