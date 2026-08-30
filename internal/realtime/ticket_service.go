package realtime

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// TicketIDGenerator supplies ticket and connection identifiers.
type TicketIDGenerator interface {
	NewWebSocketTicket() (id.WebSocketTicket, error)
	NewConnection() (id.Connection, error)
}

// TicketRepository persists and atomically redeems connection tickets.
type TicketRepository interface {
	Create(context.Context, tenant.Scope, Ticket) error
	Redeem(context.Context, Redemption) (Ticket, error)
}

// IssueInput contains authenticated capture and deployment bindings.
type IssueInput struct {
	Scope            tenant.Scope
	VerificationID   id.Verification
	CaptureTokenID   id.CaptureToken
	Binding          ClientBinding
	Region           string
	SessionExpiresAt time.Time
}

// IssuedTicket contains a durable record and its display-once credential.
type IssuedTicket struct {
	Ticket     Ticket
	Credential PresentedTicket
}

// Redemption contains untrusted lookup hints and every exact handshake binding.
type Redemption struct {
	TenantHint   id.Tenant
	TicketID     id.WebSocketTicket
	Digest       TicketDigest
	Binding      ClientBinding
	Region       string
	Protocol     string
	ConnectionID id.Connection
	RedeemedAt   time.Time
}

// Service issues and redeems single-use connection tickets.
type Service struct {
	repository  TicketRepository
	identifiers TicketIDGenerator
	generator   *TicketGenerator
	clock       clock.Clock
	limits      Limits
}

// NewService constructs the connection-ticket application service.
func NewService(
	repository TicketRepository,
	identifiers TicketIDGenerator,
	generator *TicketGenerator,
	source clock.Clock,
	limits Limits,
) (*Service, error) {
	if repository == nil || identifiers == nil || generator == nil || source == nil || limits.TicketLifetime() == 0 {
		return nil, errors.New("realtime: ticket service dependencies are required")
	}

	return &Service{repository: repository, identifiers: identifiers, generator: generator, clock: source, limits: limits}, nil
}

// Issue creates one display-once ticket within the authenticated session lifetime.
func (service *Service) Issue(ctx context.Context, input IssueInput) (IssuedTicket, error) {
	if service == nil || input.Scope.ID().IsZero() || input.VerificationID.IsZero() ||
		input.CaptureTokenID.IsZero() || input.Binding.IsZero() || !validRegion(input.Region) {
		return IssuedTicket{}, ErrTicketUnavailable
	}
	now := service.clock.Now().UTC()
	expiresAt := now.Add(service.limits.TicketLifetime())
	if input.SessionExpiresAt.IsZero() || expiresAt.After(input.SessionExpiresAt.UTC()) {
		return IssuedTicket{}, ErrTicketUnavailable
	}
	ticketID, err := service.identifiers.NewWebSocketTicket()
	if err != nil {
		return IssuedTicket{}, fmt.Errorf("realtime: generate ticket id: %w", err)
	}
	presented, err := service.generator.Generate(input.Scope.ID(), ticketID)
	if err != nil {
		return IssuedTicket{}, err
	}
	digest, err := DigestTicket(presented)
	if err != nil {
		return IssuedTicket{}, err
	}
	record, err := NewTicket(ticketID, input.Scope.ID(), input.VerificationID, input.CaptureTokenID,
		digest, input.Binding, input.Region, SubprotocolV1, now, expiresAt)
	if err != nil {
		return IssuedTicket{}, err
	}
	if err := service.repository.Create(ctx, input.Scope, record); err != nil {
		if errors.Is(err, ErrTicketUnavailable) {
			return IssuedTicket{}, ErrTicketUnavailable
		}

		return IssuedTicket{}, fmt.Errorf("realtime: persist connection ticket: %w", err)
	}

	return IssuedTicket{Ticket: record, Credential: presented}, nil
}

// Redeem atomically consumes one ticket for an exactly bound handshake.
func (service *Service) Redeem(
	ctx context.Context,
	encoded string,
	binding ClientBinding,
	region string,
	protocol string,
) (Ticket, error) {
	if service == nil || binding.IsZero() || !validRegion(region) || protocol != SubprotocolV1 {
		return Ticket{}, ErrInvalidTicket
	}
	presented, err := ParsePresentedTicket(encoded)
	if err != nil {
		return Ticket{}, ErrInvalidTicket
	}
	digest, err := DigestTicket(presented)
	if err != nil {
		return Ticket{}, ErrInvalidTicket
	}
	connectionID, err := service.identifiers.NewConnection()
	if err != nil {
		return Ticket{}, fmt.Errorf("realtime: generate connection id: %w", err)
	}
	record, err := service.repository.Redeem(ctx, Redemption{
		TenantHint: presented.TenantHint(), TicketID: presented.ID(), Digest: digest,
		Binding: binding, Region: region, Protocol: protocol,
		ConnectionID: connectionID, RedeemedAt: service.clock.Now().UTC(),
	})
	if errors.Is(err, ErrInvalidTicket) {
		return Ticket{}, ErrInvalidTicket
	}
	if err != nil {
		return Ticket{}, fmt.Errorf("realtime: redeem connection ticket: %w", err)
	}

	return record, nil
}
