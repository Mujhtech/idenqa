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

// FindHistory returns the authoritative creation state and immutable
// post-creation transitions in aggregate-version order.
func (store *SessionStore) FindHistory(ctx context.Context, scope tenant.Scope, identifier id.Verification) (verification.LifecycleHistory, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return verification.LifecycleHistory{}, verification.ErrSessionNotFound
	}
	history := verification.LifecycleHistory{Transitions: []verification.LifecycleTransition{}}
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		params := sqlgen.FindVerificationLifecycleOriginParams{
			TenantID:       scope.ID().String(),
			VerificationID: identifier.String(),
		}
		origin, err := queries.FindVerificationLifecycleOrigin(ctx, params)
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("find verification lifecycle origin: %w", err)
		}
		originState := verification.SessionState(origin.State)
		if !originState.Valid() || origin.Version != 1 || origin.OccurredAt.Time.IsZero() {
			return errors.New("verification postgres: stored lifecycle origin is invalid")
		}
		history.Origin = verification.LifecycleOrigin{
			State: originState, Version: origin.Version, OccurredAt: origin.OccurredAt.Time,
		}

		rows, err := queries.ListVerificationLifecycleTransitions(ctx, sqlgen.ListVerificationLifecycleTransitionsParams{
			TenantID:       scope.ID().String(),
			VerificationID: identifier.String(),
		})
		if err != nil {
			return fmt.Errorf("list verification lifecycle transitions: %w", err)
		}
		if len(rows) > 100 {
			history.Truncated = true
			rows = rows[:100]
		}
		for _, row := range rows {
			eventID, parseErr := id.ParseEvent(row.EventID)
			if parseErr != nil {
				return errors.New("verification postgres: stored lifecycle event id is invalid")
			}
			from, to := verification.SessionState(row.FromState), verification.SessionState(row.ToState)
			if !from.Valid() || !to.Valid() || row.ResultingVersion < 2 || row.OccurredAt.Time.IsZero() {
				return errors.New("verification postgres: stored lifecycle transition is invalid")
			}
			var decisionID id.Decision
			if row.DecisionID != nil {
				decisionID, parseErr = id.ParseDecision(*row.DecisionID)
				if parseErr != nil {
					return errors.New("verification postgres: stored lifecycle decision id is invalid")
				}
			}
			history.Transitions = append(history.Transitions, verification.LifecycleTransition{
				EventID: eventID, From: from, To: to, Version: row.ResultingVersion,
				DecisionID: decisionID, OccurredAt: row.OccurredAt.Time,
			})
		}
		return nil
	})
	return history, err
}
