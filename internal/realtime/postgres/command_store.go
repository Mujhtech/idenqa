package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// CommandStore persists tenant-scoped, idempotent client command activity.
type CommandStore struct {
	pool    transactionRunner
	catalog evidence.Catalog
}

// NewCommandStore constructs the realtime command PostgreSQL adapter.
func NewCommandStore(pool transactionRunner, catalog evidence.Catalog) (*CommandStore, error) {
	if pool == nil || catalog.IsZero() {
		return nil, errors.New("realtime postgres: command pool and registry catalog are required")
	}

	return &CommandStore{pool: pool, catalog: catalog}, nil
}

// ApplyCaptureStep atomically rechecks current authority, validates the exact
// immutable requirement binding, and records one stable result per command ID.
func (store *CommandStore) ApplyCaptureStep(
	ctx context.Context,
	ticket realtime.Ticket,
	command realtime.CaptureStepCommand,
) (realtime.CommandApplication, error) {
	if ticket.TenantID().IsZero() || command.TenantID() != ticket.TenantID() ||
		command.VerificationID() != ticket.VerificationID() ||
		command.CaptureTokenID() != ticket.CaptureTokenID() || command.CommandID().IsZero() {
		return "", errors.New("realtime postgres: command binding is invalid")
	}

	var application realtime.CommandApplication
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationReadCommitted},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, command.TenantID().String()); err != nil {
				return fmt.Errorf("set realtime command tenant scope: %w", err)
			}
			stored, err := queries.FindRealtimeClientCommand(ctx, sqlgen.FindRealtimeClientCommandParams{
				TenantID: command.TenantID().String(), CommandID: command.CommandID().String(),
			})
			if err == nil {
				application, err = restoreCommandApplication(stored, command)

				return err
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("find realtime client command: %w", err)
			}

			application, err = store.evaluateCommand(ctx, queries, ticket, command)
			if err != nil {
				return err
			}
			payload := command.Payload()
			resultCode := optionalString(payload.Code)
			rejectionCode := optionalRejection(application)
			rows, err := queries.InsertRealtimeClientCommand(ctx, sqlgen.InsertRealtimeClientCommandParams{
				CommandID: command.CommandID().String(), TenantID: command.TenantID().String(),
				VerificationID: command.VerificationID().String(), CaptureTokenID: command.CaptureTokenID().String(),
				RequestFingerprint: command.Fingerprint().Bytes(), MessageType: string(command.MessageType()),
				StepState: string(payload.State), RequirementKey: payload.RequirementKey, Artefact: payload.Artefact,
				AcquisitionMethod: payload.AcquisitionMethod, ResultCode: resultCode,
				Disposition: commandDisposition(application), RejectionCode: rejectionCode,
				OccurredAt: timestamp(command.OccurredAt()), ReceivedAt: timestamp(command.ReceivedAt()),
			})
			if err != nil {
				return fmt.Errorf("insert realtime client command: %w", err)
			}
			if rows == 1 {
				return nil
			}
			stored, err = queries.FindRealtimeClientCommand(ctx, sqlgen.FindRealtimeClientCommandParams{
				TenantID: command.TenantID().String(), CommandID: command.CommandID().String(),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				application = realtime.CommandStateConflict

				return nil
			}
			if err != nil {
				return fmt.Errorf("find concurrent realtime client command: %w", err)
			}
			application, err = restoreCommandApplication(stored, command)

			return err
		},
	)

	return application, err
}

func (store *CommandStore) evaluateCommand(
	ctx context.Context,
	queries *sqlgen.Queries,
	ticket realtime.Ticket,
	command realtime.CaptureStepCommand,
) (realtime.CommandApplication, error) {
	connectionID := ticket.ConnectionID().String()
	requirements, err := queries.LoadRealtimeCommandAuthority(ctx, sqlgen.LoadRealtimeCommandAuthorityParams{
		TenantID: ticket.TenantID().String(), TicketID: ticket.ID().String(),
		VerificationID: ticket.VerificationID().String(), CaptureTokenID: ticket.CaptureTokenID().String(),
		ConnectionID: &connectionID, ObservedAt: timestamp(command.ReceivedAt()),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return realtime.CommandAuthorityUnavailable, nil
	}
	if err != nil {
		return "", fmt.Errorf("load realtime command authority: %w", err)
	}
	profile, err := verification.ParseProfileFromCatalog(requirements.Requirements, store.catalog)
	if err != nil {
		return "", fmt.Errorf("parse realtime command requirements: %w", err)
	}
	var selections map[string]string
	if err := json.Unmarshal(requirements.DocumentSelections, &selections); err != nil {
		return "", fmt.Errorf("decode command document selections: %w", err)
	}
	if !captureStepPermitted(profile, command.Payload(), selections) {
		return realtime.CommandPolicyConflict, nil
	}

	return realtime.CommandApplied, nil
}

func restoreCommandApplication(
	stored sqlgen.IdenqaRealtimeClientCommand,
	command realtime.CaptureStepCommand,
) (realtime.CommandApplication, error) {
	fingerprint, err := realtime.ParseCommandFingerprint(stored.RequestFingerprint)
	if err != nil {
		return "", fmt.Errorf("restore realtime command fingerprint: %w", err)
	}
	if fingerprint != command.Fingerprint() || stored.VerificationID != command.VerificationID().String() ||
		stored.CaptureTokenID != command.CaptureTokenID().String() {
		return realtime.CommandStateConflict, nil
	}
	if stored.Disposition == "accepted" && stored.RejectionCode == nil {
		return realtime.CommandApplied, nil
	}
	if stored.Disposition == "rejected" && stored.RejectionCode != nil {
		application := realtime.CommandApplication(*stored.RejectionCode)
		if application == realtime.CommandAuthorityUnavailable || application == realtime.CommandPolicyConflict {
			return application, nil
		}
	}

	return "", errors.New("realtime postgres: invalid stored command disposition")
}

func captureStepPermitted(profile verification.Profile, step realtime.CaptureStepUpdate, selection ...map[string]string) bool {
	var selections map[string]string
	if len(selection) > 0 {
		selections = selection[0]
	}
	artefact := evidence.Name(step.Artefact)
	method := evidence.Name(step.AcquisitionMethod)
	for _, requirement := range profile.Requirements {
		if requirement.Key != step.RequirementKey || !slices.Contains(verification.EffectiveArtefacts(requirement, selections), artefact) {
			continue
		}
		if slices.Contains(requirement.Acquisition.Methods, method) {
			return true
		}
		for _, fallback := range requirement.Fallbacks {
			if slices.Contains(fallback.Acquisition.Methods, method) {
				return true
			}
		}

		return false
	}

	return false
}

func commandDisposition(application realtime.CommandApplication) string {
	if application == realtime.CommandApplied {
		return "accepted"
	}

	return "rejected"
}

func optionalRejection(application realtime.CommandApplication) *string {
	if application == realtime.CommandApplied {
		return nil
	}
	value := string(application)

	return &value
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

var _ realtime.CaptureStepCommandRepository = (*CommandStore)(nil)
