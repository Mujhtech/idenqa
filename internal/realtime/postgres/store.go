// Package postgres adapts realtime connection-ticket persistence to PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type transactionRunner interface {
	WithinTransaction(
		context.Context,
		platformpostgres.TransactionOptions,
		func(context.Context, platformpostgres.Transaction) error,
	) error
}

// Store persists tenant-scoped single-use connection tickets.
type Store struct{ pool transactionRunner }

// New constructs a connection-ticket PostgreSQL adapter.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("realtime postgres: pool is required")
	}

	return &Store{pool: pool}, nil
}

// Create persists a ticket only while its exact capture credential and session
// remain active through the requested ticket expiry.
func (store *Store) Create(ctx context.Context, scope tenant.Scope, ticket realtime.Ticket) error {
	if scope.ID().IsZero() || ticket.ID().IsZero() || ticket.TenantID().String() != scope.ID().String() {
		return realtime.ErrTicketUnavailable
	}

	return store.withTenant(ctx, scope.ID(), func(ctx context.Context, queries *sqlgen.Queries) error {
		rows, err := queries.CreateWebSocketConnectionTicket(ctx, sqlgen.CreateWebSocketConnectionTicketParams{
			ID: ticket.ID().String(), TenantID: scope.ID().String(),
			VerificationID: ticket.VerificationID().String(), CaptureTokenID: ticket.CaptureTokenID().String(),
			Digest: ticket.Digest().Bytes(), ClientKind: string(ticket.Binding().Kind()),
			ClientIdentity: ticket.Binding().Identity(), Region: ticket.Region(), Protocol: ticket.Protocol(),
			IssuedAt: timestamp(ticket.IssuedAt()), ExpiresAt: timestamp(ticket.ExpiresAt()),
		})
		if err != nil {
			return fmt.Errorf("create websocket connection ticket: %w", err)
		}
		if rows != 1 {
			return realtime.ErrTicketUnavailable
		}

		return nil
	})
}

// Redeem atomically consumes an exactly bound ticket. The tenant hint remains
// untrusted until the conditional update returns the matching durable record.
func (store *Store) Redeem(ctx context.Context, redemption realtime.Redemption) (realtime.Ticket, error) {
	if redemption.TenantHint.IsZero() || redemption.TicketID.IsZero() || redemption.Digest.IsZero() ||
		redemption.Binding.IsZero() || redemption.ConnectionID.IsZero() || redemption.RedeemedAt.IsZero() {
		return realtime.Ticket{}, realtime.ErrInvalidTicket
	}

	var ticket realtime.Ticket
	err := store.withTenant(ctx, redemption.TenantHint, func(ctx context.Context, queries *sqlgen.Queries) error {
		connectionID := redemption.ConnectionID.String()
		row, err := queries.RedeemWebSocketConnectionTicket(
			ctx,
			sqlgen.RedeemWebSocketConnectionTicketParams{
				RedeemedAt: timestamp(redemption.RedeemedAt), ConnectionID: &connectionID,
				TenantID: redemption.TenantHint.String(), ID: redemption.TicketID.String(),
				Digest: redemption.Digest.Bytes(), ClientKind: string(redemption.Binding.Kind()),
				ClientIdentity: redemption.Binding.Identity(), Region: redemption.Region,
				Protocol: redemption.Protocol,
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return realtime.ErrInvalidTicket
		}
		if err != nil {
			return fmt.Errorf("redeem websocket connection ticket: %w", err)
		}
		ticket, err = restoreTicket(row)
		if err != nil {
			return fmt.Errorf("restore redeemed websocket connection ticket: %w", err)
		}

		return nil
	})

	return ticket, err
}

// LoadSessionAuthority reloads the exact active capture authority bound to a
// redeemed ticket. Wrong-tenant and inactive rows deliberately collapse.
func (store *Store) LoadSessionAuthority(
	ctx context.Context,
	ticket realtime.Ticket,
	observedAt time.Time,
) (realtime.SessionAuthority, error) {
	if ticket.TenantID().IsZero() || ticket.VerificationID().IsZero() ||
		ticket.CaptureTokenID().IsZero() || ticket.ID().IsZero() || ticket.ConnectionID().IsZero() ||
		ticket.RedeemedAt() == nil || ticket.Protocol() != realtime.SubprotocolV1 ||
		observedAt.IsZero() || observedAt.Location() != time.UTC {
		return realtime.SessionAuthority{}, realtime.ErrSessionUnavailable
	}

	var authority realtime.SessionAuthority
	err := store.readTenant(ctx, ticket.TenantID(), func(ctx context.Context, queries *sqlgen.Queries) error {
		connectionID := ticket.ConnectionID().String()
		row, err := queries.LoadRealtimeSessionAuthority(ctx, sqlgen.LoadRealtimeSessionAuthorityParams{
			TenantID: ticket.TenantID().String(), TicketID: ticket.ID().String(),
			VerificationID: ticket.VerificationID().String(), CaptureTokenID: ticket.CaptureTokenID().String(),
			ConnectionID: &connectionID, ObservedAt: timestamp(observedAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return realtime.ErrSessionUnavailable
		}
		if err != nil {
			return fmt.Errorf("load realtime session authority: %w", err)
		}
		if !row.SessionExpiresAt.Valid {
			return errors.New("realtime postgres: invalid authority expiry")
		}
		authority, err = realtime.NewSessionAuthority(row.SessionVersion, row.SessionExpiresAt.Time.UTC())
		if err != nil {
			return fmt.Errorf("restore realtime session authority: %w", err)
		}

		return nil
	})

	return authority, err
}

// DeleteExpiredUnredeemed removes a bounded tenant-scoped batch of unused
// credentials. Redeemed records are deliberately retained because established
// connections and command attribution still depend on their immutable binding.
func (store *Store) DeleteExpiredUnredeemed(
	ctx context.Context,
	scope tenant.Scope,
	observedAt time.Time,
	batchSize int32,
) (int, error) {
	if scope.ID().IsZero() || observedAt.IsZero() || observedAt.Location() != time.UTC ||
		batchSize < 1 || batchSize > 1000 {
		return 0, errors.New("realtime postgres: cleanup scope, time, and batch are invalid")
	}
	deleted := 0
	err := store.withTenant(ctx, scope.ID(), func(ctx context.Context, queries *sqlgen.Queries) error {
		identifiers, err := queries.DeleteExpiredUnredeemedWebSocketTickets(
			ctx,
			sqlgen.DeleteExpiredUnredeemedWebSocketTicketsParams{
				TenantID: scope.ID().String(), ObservedAt: timestamp(observedAt), BatchSize: batchSize,
			},
		)
		if err != nil {
			return fmt.Errorf("delete expired unredeemed websocket tickets: %w", err)
		}
		deleted = len(identifiers)

		return nil
	})

	return deleted, err
}

func (store *Store) readTenant(
	ctx context.Context,
	tenantID id.Tenant,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{
			Isolation: platformpostgres.IsolationReadCommitted,
			ReadOnly:  true,
		},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, tenantID.String()); err != nil {
				return fmt.Errorf("set realtime authority tenant scope: %w", err)
			}

			return work(ctx, queries)
		},
	)
}

func (store *Store) withTenant(
	ctx context.Context,
	tenantID id.Tenant,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationReadCommitted},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, tenantID.String()); err != nil {
				return fmt.Errorf("set realtime ticket tenant hint: %w", err)
			}

			return work(ctx, queries)
		},
	)
}

func restoreTicket(row sqlgen.IdenqaWebsocketConnectionTicket) (realtime.Ticket, error) {
	if row.DigestVersion != 1 || !row.IssuedAt.Valid || !row.ExpiresAt.Valid ||
		!row.RedeemedAt.Valid || row.ConnectionID == nil {
		return realtime.Ticket{}, errors.New("realtime postgres: invalid durable ticket state")
	}
	ticketID, err := id.ParseWebSocketTicket(row.ID)
	if err != nil {
		return realtime.Ticket{}, err
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return realtime.Ticket{}, err
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return realtime.Ticket{}, err
	}
	captureTokenID, err := id.ParseCaptureToken(row.CaptureTokenID)
	if err != nil {
		return realtime.Ticket{}, err
	}
	connectionID, err := id.ParseConnection(*row.ConnectionID)
	if err != nil {
		return realtime.Ticket{}, err
	}
	digest, err := realtime.ParseTicketDigest(row.Digest)
	if err != nil {
		return realtime.Ticket{}, err
	}
	binding, err := realtime.RestoreClientBinding(realtime.ClientKind(row.ClientKind), row.ClientIdentity)
	if err != nil {
		return realtime.Ticket{}, err
	}
	redeemedAt := row.RedeemedAt.Time

	return realtime.RestoreTicket(
		ticketID, tenantID, verificationID, captureTokenID, digest, binding,
		row.Region, row.Protocol, row.IssuedAt.Time, row.ExpiresAt.Time, &redeemedAt, connectionID,
	)
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

var _ realtime.TicketRepository = (*Store)(nil)
var _ realtime.TicketCleanupRepository = (*Store)(nil)
