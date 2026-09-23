package postgres

import (
	"context"
	"fmt"

	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// RouteAdmission evaluates persisted dependency and fallback edges without
// consulting mutable operator configuration.
func (store *CheckStore) RouteAdmission(ctx context.Context, scope tenant.Scope, check verification.Check) (verification.RouteAdmission, error) {
	var admission verification.RouteAdmission
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		admission, err = store.RouteAdmissionWithin(ctx, scope, tx, check)
		return err
	})
	return admission, err
}

// RouteAdmissionWithin repeats admission under the task completion
// transaction so a skip cannot race a predecessor transition.
func (store *CheckStore) RouteAdmissionWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, check verification.Check) (verification.RouteAdmission, error) {
	if tx == nil || scope.ID().IsZero() || check.ID.IsZero() || check.TenantID != scope.ID() {
		return "", verification.ErrInvalidCheck
	}
	if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return "", fmt.Errorf("set route admission tenant scope: %w", err)
	}
	var dependencies []string
	var fallback string
	if err := tx.QueryRow(ctx, `SELECT route_depends_on,COALESCE(route_fallback_for,'') FROM idenqa.verification_checks WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), check.ID.String()).Scan(&dependencies, &fallback); err != nil {
		return "", fmt.Errorf("load check route: %w", err)
	}
	states := map[string]verification.CheckState{}
	for _, name := range append(dependencies, fallback) {
		if name == "" {
			continue
		}
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2 AND name=$3`, scope.ID().String(), check.VerificationID.String(), name).Scan(&state); err != nil {
			return "", fmt.Errorf("load route predecessor: %w", err)
		}
		states[name] = verification.CheckState(state)
	}
	for _, dependency := range dependencies {
		switch states[dependency] {
		case verification.CheckCompleted:
		case verification.CheckFailed, verification.CheckTimedOut, verification.CheckCancelled, verification.CheckSkippedByPolicy:
			return verification.RouteSkip, nil
		default:
			return verification.RouteWait, nil
		}
	}
	if fallback != "" {
		switch states[fallback] {
		case verification.CheckFailed, verification.CheckTimedOut, verification.CheckCancelled:
			return verification.RouteRun, nil
		case verification.CheckCompleted, verification.CheckSkippedByPolicy:
			return verification.RouteSkip, nil
		default:
			return verification.RouteWait, nil
		}
	}
	return verification.RouteRun, nil
}
