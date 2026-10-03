package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// Signals returns at most 200 immutable, provider-independent observations.
func (store *SessionStore) Signals(ctx context.Context, scope tenant.Scope, identifier id.Verification) (verification.SignalPage, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return verification.SignalPage{}, verification.ErrSessionNotFound
	}
	result := verification.SignalPage{Items: []verification.SignalRead{}}
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		tenantID, verificationID := scope.ID().String(), identifier.String()
		if _, err := queries.FindVerificationSignalSession(ctx, sqlgen.FindVerificationSignalSessionParams{TenantID: tenantID, ID: verificationID}); errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		} else if err != nil {
			return fmt.Errorf("find verification signal session: %w", err)
		}
		rows, err := queries.ListVerificationSignals(ctx, sqlgen.ListVerificationSignalsParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification signals: %w", err)
		}
		if len(rows) > 200 {
			result.Truncated = true
			rows = rows[:200]
		}
		for _, row := range rows {
			result.Items = append(result.Items, verification.SignalRead{
				ID: row.ID, CheckID: row.CheckID, AttemptID: row.AttemptID,
				RunnerKind: row.RunnerKind, RunnerID: row.RunnerID, RunnerVersion: row.RunnerVersion,
				ContractMajor: int(row.ContractMajor), ContractMinor: int(row.ContractMinor),
				Name: row.SignalName, Outcome: row.SignalOutcome,
				ReasonCodes: row.ReasonCodes, RecordedAt: row.RecordedAt.Time,
			})
		}
		return nil
	})
	return result, err
}
