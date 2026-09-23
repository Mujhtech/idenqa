package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
	"github.com/jackc/pgx/v5/pgconn"
)

// PlanIdentifiers supplies fresh identities only for a new atomic processing start.
type PlanIdentifiers interface {
	NewCheck() (id.Check, error)
	NewAttempt() (id.Attempt, error)
	NewEvent() (id.Event, error)
	NewTask() (id.Task, error)
}

// ProcessingEnqueuer keeps queue insertion inside the owning transaction.
type ProcessingEnqueuer interface {
	EnqueueTx(context.Context, platformpostgres.Transaction, ...platformtask.Intent) error
}

// ProcessingStore discovers durable capture markers and atomically starts checks.
type ProcessingStore struct {
	pool        transactionRunner
	planner     verification.CheckPlanner
	identifiers PlanIdentifiers
	enqueuer    ProcessingEnqueuer
	clock       clock.Clock
	lifecycle   *LifecycleStore
	preparation PlannedCheckPreparation
}

// PlannedCheckPreparation joins provider request and grant persistence to planning.
type PlannedCheckPreparation interface {
	Prepare(context.Context, platformpostgres.Transaction, tenant.Scope, id.Verification, id.Check, id.Attempt, verification.PlannedCheck, time.Time, time.Time) (verification.Provenance, func(context.Context, verification.Check) error, error)
}

// WithPreparation enables immutable real-provider request preparation during planning.
func (store *ProcessingStore) WithPreparation(preparation PlannedCheckPreparation) error {
	if preparation == nil {
		return verification.ErrInvalidCheck
	}
	store.preparation = preparation
	return nil
}

// NewProcessingStore constructs the owned atomic processing adapter.
func NewProcessingStore(pool transactionRunner, wrapper platformcrypto.KeyWrapper, planner verification.CheckPlanner, identifiers PlanIdentifiers, enqueuer ProcessingEnqueuer, source clock.Clock) (*ProcessingStore, error) {
	if pool == nil || planner == nil || identifiers == nil || enqueuer == nil || source == nil {
		return nil, errors.New("verification postgres: processing dependencies are required")
	}
	lifecycle, err := NewLifecycleStore(pool, wrapper, source)
	if err != nil {
		return nil, err
	}
	return &ProcessingStore{pool: pool, planner: planner, identifiers: identifiers, enqueuer: enqueuer, clock: source, lifecycle: lifecycle}, nil
}

// ListReadyCaptures returns a bounded installation-wide identifier-only batch.
func (store *ProcessingStore) ListReadyCaptures(ctx context.Context, observedAt time.Time, limit int) ([]verification.CaptureTarget, error) {
	if !utcTime(observedAt) || limit < 1 || limit > verificationtask.CoordinationBatch {
		return nil, verification.ErrInvalidCheck
	}
	var targets []verification.CaptureTarget
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		query := `SELECT tenant_id, verification_id FROM idenqa.list_ready_verification_captures($1,$2)`
		args := []any{observedAt, limit}
		if route, ok := store.planner.(verification.CaptureRoute); ok {
			tenantID, policyID, profileDigest := route.CaptureRoute()
			if _, err := sqlgen.New(tx).SetTenantScope(ctx, tenantID); err != nil {
				return err
			}
			query = `SELECT tenant_id, verification_id FROM idenqa.list_ready_provider_captures($1,$2,$3,$4,$5)`
			args = append(args, tenantID, policyID, profileDigest)
		}
		rows, err := tx.Query(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("discover completed captures: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var tenantValue, verificationValue string
			if err := rows.Scan(&tenantValue, &verificationValue); err != nil {
				return fmt.Errorf("scan completed capture: %w", err)
			}
			tenantID, err := id.ParseTenant(tenantValue)
			if err != nil {
				return verification.ErrInvalidCheck
			}
			verificationID, err := id.ParseVerification(verificationValue)
			if err != nil {
				return verification.ErrInvalidCheck
			}
			targets = append(targets, verification.CaptureTarget{TenantID: tenantID, VerificationID: verificationID})
		}
		return rows.Err()
	})
	return targets, err
}

// StartProcessing retries only the database unit of work on serialization races.
// Its pure planner and identifier allocation perform no external side effects.
// A committed start is identified by the locked lifecycle, so restart/replay
// never creates another set of checks or extends attempt deadlines.
func (store *ProcessingStore) StartProcessing(ctx context.Context, scope tenant.Scope, verificationID id.Verification) (bool, error) {
	if scope.ID().IsZero() || verificationID.IsZero() {
		return false, verification.ErrInvalidCheck
	}
	for attempt := 0; attempt < 3; attempt++ {
		started := false
		err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
			var err error
			started, err = store.startWithin(ctx, tx, scope, verificationID)
			return err
		})
		if err == nil {
			return started, nil
		}
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || (databaseError.Code != "40001" && databaseError.Code != "40P01") {
			return false, err
		}
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
	}
	return false, verification.ErrSessionConflict
}

func (store *ProcessingStore) startWithin(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification) (bool, error) {
	queries := sqlgen.New(tx)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return false, fmt.Errorf("scope processing plan: %w", err)
	}
	session, err := queries.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{TenantID: scope.ID().String(), ID: verificationID.String()})
	if err != nil {
		return false, fmt.Errorf("lock processing session: %w", err)
	}
	if session.State != string(verification.SessionStateCollecting) {
		return false, nil
	}
	now := store.clock.Now().UTC().Truncate(time.Microsecond)
	if !session.CaptureCompletedAt.Valid || session.CaptureCompletedAt.Time.After(now) || session.PolicyID == nil || session.DecisionID == nil {
		return false, nil
	}
	if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, verificationID, now, store.clock, verification.SessionStateCollecting); err != nil {
		return false, err
	}
	var existing bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2)`, scope.ID().String(), verificationID.String()).Scan(&existing); err != nil {
		return false, fmt.Errorf("check existing plan: %w", err)
	}
	if existing {
		return false, verification.ErrSessionConflict
	}
	policyID, err := id.ParsePolicy(*session.PolicyID)
	if err != nil {
		return false, verification.ErrInvalidCheck
	}
	plan, err := store.planner.Plan(ctx, verification.PlanInput{TenantID: scope.ID(), VerificationID: verificationID, ProfileDigest: session.SourceProfileDigest, PolicyID: policyID})
	if err != nil {
		return false, err
	}
	if len(plan) == 0 || len(plan) > 100 {
		return false, verification.ErrInvalidCheck
	}
	deadline := minTime(now.Add(verificationtask.MaximumExecuteDuration), session.ExpiresAt.Time.UTC())
	intents := make([]platformtask.Intent, 0, len(plan))
	names := make(map[string]bool, len(plan))
	for _, definition := range plan {
		if names[definition.Name] {
			return false, verification.ErrInvalidCheck
		}
		names[definition.Name] = true
		intent, err := store.persistPlannedCheck(ctx, tx, queries, scope, verificationID, definition, now, deadline)
		if err != nil {
			return false, err
		}
		intents = append(intents, intent)
	}
	eventID, err := store.identifiers.NewEvent()
	if err != nil {
		return false, fmt.Errorf("generate processing event: %w", err)
	}
	actorID, err := store.identifiers.NewTask()
	if err != nil {
		return false, fmt.Errorf("generate processing actor: %w", err)
	}
	if _, err := store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{EventID: eventID, VerificationID: verificationID,
		ExpectedVersion: session.Version, Target: verification.SessionStateProcessing, ActorID: actorID.String(), OccurredAt: now}); err != nil {
		return false, err
	}
	if err := store.enqueuer.EnqueueTx(ctx, tx, intents...); err != nil {
		return false, fmt.Errorf("enqueue planned checks: %w", err)
	}
	return true, nil
}

func (store *ProcessingStore) persistPlannedCheck(ctx context.Context, tx platformpostgres.Transaction, queries *sqlgen.Queries, scope tenant.Scope, verificationID id.Verification, definition verification.PlannedCheck, now, deadline time.Time) (platformtask.Intent, error) {
	if definition.MaximumDuration > 0 {
		deadline = minTime(deadline, now.Add(definition.MaximumDuration))
	}
	checkID, err := store.identifiers.NewCheck()
	if err != nil {
		return platformtask.Intent{}, err
	}
	attemptID, err := store.identifiers.NewAttempt()
	if err != nil {
		return platformtask.Intent{}, err
	}
	eventID, err := store.identifiers.NewEvent()
	if err != nil {
		return platformtask.Intent{}, err
	}
	var savePrepared func(context.Context, verification.Check) error
	if store.preparation != nil {
		definition.Provenance, savePrepared, err = store.preparation.Prepare(ctx, tx, scope, verificationID, checkID, attemptID, definition, now, deadline)
		if err != nil {
			return platformtask.Intent{}, err
		}
	}
	check, err := verification.NewCheck(checkID, scope.ID(), verificationID, definition.Name, now)
	if err != nil {
		return platformtask.Intent{}, err
	}
	if err := check.BeginAttempt(verification.Attempt{ID: attemptID, Number: 1, Fence: 1, RunnerKind: definition.RunnerKind,
		Provenance: definition.Provenance, State: verification.AttemptRunning, StartedAt: now, Deadline: deadline}); err != nil {
		return platformtask.Intent{}, err
	}
	if err := queries.InsertVerificationCheck(ctx, checkInsertParams(check)); err != nil {
		return platformtask.Intent{}, fmt.Errorf("insert planned check: %w", err)
	}
	dependencies := definition.Route.DependsOn
	if dependencies == nil {
		// A route without predecessors is an empty PostgreSQL array, not NULL.
		dependencies = []string{}
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.verification_checks SET route_priority=$3, route_depends_on=$4, route_fallback_for=NULLIF($5,''), route_correlation_group=NULLIF($6,'') WHERE tenant_id=$1 AND id=$2`,
		scope.ID().String(), check.ID.String(), definition.Route.Priority, dependencies,
		definition.Route.FallbackFor, definition.Route.CorrelationGroup); err != nil {
		return platformtask.Intent{}, fmt.Errorf("persist planned check route: %w", err)
	}
	if err := persistAttempts(ctx, queries, check, verification.CheckQueued, ""); err != nil {
		return platformtask.Intent{}, err
	}
	if savePrepared != nil {
		if err := savePrepared(ctx, check); err != nil {
			return platformtask.Intent{}, err
		}
	}
	if err := insertCheckProgress(ctx, queries, check, eventID); err != nil {
		return platformtask.Intent{}, err
	}
	factory := verificationtask.NewExecuteIntent
	if definition.Asynchronous {
		factory = verificationtask.NewAsyncExecuteIntent
	}
	return factory(store.identifiers, scope, verificationtask.ExecutePayload{CheckID: checkID, AttemptID: attemptID}, verificationtask.IntentMetadata{ScheduledAt: now, Deadline: deadline})
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Sweep uses bounded discovery; one invalidated authority does not suppress
// unrelated ready sessions. Durable markers remain retryable after process loss.
func (store *ProcessingStore) Sweep(ctx context.Context, limit int) (int, error) {
	targets, err := store.ListReadyCaptures(ctx, store.clock.Now().UTC(), limit)
	if err != nil {
		return 0, err
	}
	count := 0
	var failures error
	for _, target := range targets {
		scope, err := tenant.NewScope(target.TenantID)
		if err != nil {
			return count, err
		}
		started, err := store.StartProcessing(ctx, scope, target.VerificationID)
		if errors.Is(err, verification.ErrPlanUnavailable) || errors.Is(err, authority.ErrProcessingNotPermitted) || errors.Is(err, authority.ErrSubjectResponseRequired) {
			continue
		}
		if err != nil {
			failures = errors.Join(failures, err)
			continue
		}
		if started {
			count++
		}
	}
	return count, failures
}
