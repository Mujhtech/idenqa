package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/outbox"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	checkAggregateType         = "verification_check"
	checkProgressEvent         = "verification.check.progress.v1"
	checkEventSchema           = 1
	maximumReconciliationLease = 10 * time.Minute
)

// CheckStore persists verification execution state under forced tenant RLS.
type CheckStore struct {
	pool  transactionRunner
	clock clock.Clock
}

// NewCheckStore constructs the persistence primitive. Callers must provide
// lifecycle and authority validation; runnable workers use NewGuardedCheckStore.
func NewCheckStore(pool transactionRunner) (*CheckStore, error) {
	if pool == nil {
		return nil, errors.New("verification postgres: check pool is required")
	}
	return &CheckStore{pool: pool}, nil
}

// NewGuardedCheckStore rechecks lifecycle and authority before dispatch and
// inside every consequential check commit.
func NewGuardedCheckStore(pool transactionRunner, source clock.Clock) (*CheckStore, error) {
	store, err := NewCheckStore(pool)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, errors.New("verification postgres: processing clock is required")
	}
	store.clock = source
	return store, nil
}

// CreateCheck atomically creates a queued check and its safe progress intent.
func (store *CheckStore) CreateCheck(ctx context.Context, scope tenant.Scope, check verification.Check, eventID id.Event) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		return store.CreateCheckWithin(ctx, scope, tx, check, eventID)
	})
}

// CreateCheckWithin creates the queued aggregate and its progress intent in
// the planner's transaction. The planner owns the parent lock and authority
// validation before creating checks, attempts, tasks, and the processing state.
func (store *CheckStore) CreateCheckWithin(
	ctx context.Context,
	scope tenant.Scope,
	transaction platformpostgres.Transaction,
	check verification.Check,
	eventID id.Event,
) error {
	if transaction == nil {
		return verification.ErrInvalidCheck
	}
	if scope.ID().IsZero() || check.Validate() != nil || check.TenantID.String() != scope.ID().String() || eventID.IsZero() ||
		check.State != verification.CheckQueued || check.Version != 1 || len(check.Attempts()) != 0 {
		return verification.ErrInvalidCheck
	}
	queries := sqlgen.New(transaction)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return fmt.Errorf("set verification check tenant scope: %w", err)
	}
	if err := queries.InsertVerificationCheck(ctx, checkInsertParams(check)); err != nil {
		return fmt.Errorf("insert verification check: %w", err)
	}
	return insertCheckProgress(ctx, queries, check, eventID)
}

// FindCheck restores one complete aggregate inside an exact tenant scope.
func (store *CheckStore) FindCheck(ctx context.Context, scope tenant.Scope, checkID id.Check) (verification.Check, error) {
	if scope.ID().IsZero() || checkID.IsZero() {
		return verification.Check{}, verification.ErrCheckNotFound
	}
	var check verification.Check
	if store.clock != nil {
		err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
			var err error
			check, err = store.FindCheckWithin(ctx, scope, tx, checkID)
			if err != nil {
				return err
			}
			return authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, check.VerificationID,
				store.clock.Now().UTC(), store.clock, verification.SessionStateProcessing)
		})
		return check, err
	}
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		var err error
		check, err = loadCheck(ctx, queries, scope.ID(), checkID)
		return err
	})
	return check, err
}

// FindCheckWithin restores a check using a caller-owned transaction. It is used
// by fenced task effects whose application write and queue completion are one commit.
func (store *CheckStore) FindCheckWithin(
	ctx context.Context,
	scope tenant.Scope,
	transaction platformpostgres.Transaction,
	checkID id.Check,
) (verification.Check, error) {
	if transaction == nil || scope.ID().IsZero() || checkID.IsZero() {
		return verification.Check{}, verification.ErrCheckNotFound
	}
	queries := sqlgen.New(transaction)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return verification.Check{}, fmt.Errorf("set verification check tenant scope: %w", err)
	}
	return loadCheck(ctx, queries, scope.ID(), checkID)
}

// SaveCheck commits inbox claim, aggregate transition, immutable history,
// reconciliation intent, and safe outbox intent in one transaction.
func (store *CheckStore) SaveCheck(ctx context.Context, scope tenant.Scope, commit verification.CheckCommit) (bool, error) {
	var duplicate bool
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		duplicate, err = store.SaveCheckWithin(ctx, scope, tx, commit)
		return err
	})
	return duplicate, err
}

// SaveCheckWithin applies one check commit using a caller-owned transaction.
// The caller remains responsible for committing or rolling back that transaction.
func (store *CheckStore) SaveCheckWithin(
	ctx context.Context,
	scope tenant.Scope,
	transaction platformpostgres.Transaction,
	commit verification.CheckCommit,
) (bool, error) {
	if transaction == nil {
		return false, verification.ErrInvalidCheck
	}
	queries := sqlgen.New(transaction)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return false, fmt.Errorf("set verification check tenant scope: %w", err)
	}
	if store.clock != nil {
		// An already committed immutable receipt remains replayable after the
		// parent has completed or authority has changed. No effects are added.
		duplicate, err := findResultReplay(ctx, transaction, scope, commit)
		if err != nil || duplicate {
			return duplicate, err
		}
		if err := authoritypostgres.ValidateProcessingWithin(ctx, transaction, scope, commit.Check.VerificationID,
			commit.Check.UpdatedAt, store.clock, verification.SessionStateProcessing); err != nil {
			return false, err
		}
	}
	return saveCheck(ctx, queries, scope, commit)
}

func findResultReplay(ctx context.Context, transaction platformpostgres.Transaction, scope tenant.Scope, commit verification.CheckCommit) (bool, error) {
	if commit.Receipt == nil {
		return false, nil
	}
	if commit.Check.Validate() != nil || commit.Check.TenantID != scope.ID() || commit.Receipt.Validate() != nil {
		return false, verification.ErrInvalidCheck
	}
	var exists bool
	if err := transaction.QueryRow(ctx, `SELECT EXISTS (
SELECT 1 FROM idenqa.verification_result_inbox
WHERE tenant_id = $1 AND verification_id = $2 AND check_id = $3 AND attempt_id = $4 AND result_digest = $5
)`, scope.ID().String(), commit.Check.VerificationID.String(), commit.Check.ID.String(),
		commit.Receipt.AttemptID.String(), commit.Receipt.Fingerprint).Scan(&exists); err != nil {
		return false, fmt.Errorf("find committed verification result receipt: %w", err)
	}
	return exists, nil
}

func saveCheck(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	commit verification.CheckCommit,
) (bool, error) {
	check := commit.Check
	if scope.ID().IsZero() || check.Validate() != nil || check.ID.IsZero() || check.TenantID.String() != scope.ID().String() ||
		commit.ExpectedVersion < 1 || check.Version <= commit.ExpectedVersion || commit.EventID.IsZero() {
		return false, verification.ErrInvalidCheck
	}
	duplicate := false
	if commit.Receipt != nil && commit.Receipt.Validate() != nil {
		return false, verification.ErrInvalidCheck
	}
	err := func() error {
		if commit.Receipt != nil {
			rows, err := queries.ClaimVerificationResultInbox(ctx, sqlgen.ClaimVerificationResultInboxParams{
				TenantID: scope.ID().String(), VerificationID: check.VerificationID.String(), CheckID: check.ID.String(),
				AttemptID: commit.Receipt.AttemptID.String(), ResultDigest: commit.Receipt.Fingerprint,
				ReceivedAt: timestamp(commit.Receipt.ReceivedAt),
			})
			if err != nil {
				return fmt.Errorf("claim verification result inbox: %w", err)
			}
			if rows == 0 {
				duplicate = true
				if _, err := queries.InsertVerificationAttemptDiagnostic(ctx, sqlgen.InsertVerificationAttemptDiagnosticParams{
					TenantID: scope.ID().String(), VerificationID: check.VerificationID.String(), CheckID: check.ID.String(),
					AttemptID: commit.Receipt.AttemptID.String(), Kind: "duplicate", Code: "result_inbox_replay",
					RecordedAt: timestamp(commit.Receipt.ReceivedAt),
				}); err != nil {
					return fmt.Errorf("record duplicate result receipt: %w", err)
				}
				return nil
			}
		}
		locked, err := queries.LockVerificationCheck(ctx, sqlgen.LockVerificationCheckParams{
			TenantID: scope.ID().String(), ID: check.ID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrCheckNotFound
		}
		if err != nil {
			return fmt.Errorf("lock verification check: %w", err)
		}
		if locked.Version != commit.ExpectedVersion {
			return verification.ErrCheckVersion
		}
		rows, err := queries.UpdateVerificationCheck(ctx, checkUpdateParams(check, commit.ExpectedVersion))
		if err != nil {
			return fmt.Errorf("update verification check: %w", err)
		}
		if rows != 1 {
			return verification.ErrCheckVersion
		}
		if err := persistAttempts(ctx, queries, check, verification.CheckState(locked.State)); err != nil {
			return err
		}
		if err := persistDiagnostics(ctx, queries, check); err != nil {
			return err
		}
		return insertCheckProgress(ctx, queries, check, commit.EventID)
	}()
	return duplicate, err
}

// ClaimReconciliation leases the oldest available tenant item with SKIP LOCKED.
func (store *CheckStore) ClaimReconciliation(
	ctx context.Context,
	scope tenant.Scope,
	claimToken id.Task,
	claimedAt time.Time,
	lease time.Duration,
) (verification.ReconciliationClaim, error) {
	if scope.ID().IsZero() || claimToken.IsZero() || !utcTime(claimedAt) || lease <= 0 || lease > maximumReconciliationLease {
		return verification.ReconciliationClaim{}, verification.ErrInvalidCheck
	}
	var claim verification.ReconciliationClaim
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		token := claimToken.String()
		row, err := queries.ClaimVerificationReconciliation(ctx, sqlgen.ClaimVerificationReconciliationParams{
			ClaimToken: &token, LeaseExpiresAt: timestamp(claimedAt.Add(lease)),
			ClaimedAt: timestamp(claimedAt), TenantID: scope.ID().String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrCheckNotFound
		}
		if err != nil {
			return fmt.Errorf("claim verification reconciliation: %w", err)
		}
		claim, err = restoreReconciliation(row, scope.ID(), claimToken, claimedAt)
		return err
	})
	return claim, err
}

// ClaimReconciliationForAttempt leases only the task payload's exact durable
// target. A task can never consume a different reconciliation item.
func (store *CheckStore) ClaimReconciliationForAttempt(
	ctx context.Context,
	scope tenant.Scope,
	checkID id.Check,
	attemptID id.Attempt,
	claimToken id.Task,
	claimedAt time.Time,
	lease time.Duration,
) (verification.ReconciliationClaim, error) {
	if scope.ID().IsZero() || checkID.IsZero() || attemptID.IsZero() || claimToken.IsZero() ||
		!utcTime(claimedAt) || lease <= 0 || lease > maximumReconciliationLease {
		return verification.ReconciliationClaim{}, verification.ErrInvalidCheck
	}
	var claim verification.ReconciliationClaim
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		token := claimToken.String()
		row, err := queries.ClaimVerificationReconciliationForAttempt(
			ctx,
			sqlgen.ClaimVerificationReconciliationForAttemptParams{
				ClaimToken: &token, LeaseExpiresAt: timestamp(claimedAt.Add(lease)),
				ClaimedAt: timestamp(claimedAt), TenantID: scope.ID().String(),
				CheckID: checkID.String(), AttemptID: attemptID.String(),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			stored, findErr := queries.FindVerificationReconciliationForAttempt(
				ctx,
				sqlgen.FindVerificationReconciliationForAttemptParams{
					TenantID: scope.ID().String(), CheckID: checkID.String(), AttemptID: attemptID.String(),
				},
			)
			if errors.Is(findErr, pgx.ErrNoRows) || (findErr == nil && stored.Status == "resolved") {
				return verification.ErrCheckNotFound
			}
			if findErr != nil {
				return fmt.Errorf("find exact verification reconciliation: %w", findErr)
			}
			return verification.ErrStaleAttempt
		}
		if err != nil {
			return fmt.Errorf("claim exact verification reconciliation: %w", err)
		}
		claim, err = restoreReconciliation(row, scope.ID(), claimToken, claimedAt)
		return err
	})
	return claim, err
}

// ResolveReconciliation accepts only the exact unexpired claim token.
func (store *CheckStore) ResolveReconciliation(ctx context.Context, scope tenant.Scope, claim verification.ReconciliationClaim, resolvedAt time.Time) error {
	if scope.ID().IsZero() || claim.TenantID.String() != scope.ID().String() || claim.CheckID.IsZero() ||
		claim.AttemptID.IsZero() || claim.ClaimToken.IsZero() || !utcTime(resolvedAt) {
		return verification.ErrInvalidCheck
	}
	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		return resolveReconciliation(ctx, queries, scope, claim, resolvedAt)
	})
}

// ResolveReconciliationWithin fences application resolution with Headgate's
// task completion in the caller-owned transaction.
func (store *CheckStore) ResolveReconciliationWithin(
	ctx context.Context,
	scope tenant.Scope,
	transaction platformpostgres.Transaction,
	claim verification.ReconciliationClaim,
	resolvedAt time.Time,
) error {
	if transaction == nil || scope.ID().IsZero() || claim.TenantID.String() != scope.ID().String() ||
		claim.CheckID.IsZero() || claim.AttemptID.IsZero() || claim.ClaimToken.IsZero() || !utcTime(resolvedAt) {
		return verification.ErrInvalidCheck
	}
	queries := sqlgen.New(transaction)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return fmt.Errorf("set verification reconciliation tenant scope: %w", err)
	}
	return resolveReconciliation(ctx, queries, scope, claim, resolvedAt)
}

func resolveReconciliation(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	claim verification.ReconciliationClaim,
	resolvedAt time.Time,
) error {
	token := claim.ClaimToken.String()
	rows, err := queries.ResolveVerificationReconciliation(ctx, sqlgen.ResolveVerificationReconciliationParams{
		ResolvedAt: timestamp(resolvedAt), TenantID: scope.ID().String(), CheckID: claim.CheckID.String(),
		AttemptID: claim.AttemptID.String(), Reason: claim.Reason, ClaimToken: &token,
	})
	if err != nil {
		return fmt.Errorf("resolve verification reconciliation: %w", err)
	}
	if rows != 1 {
		return verification.ErrStaleAttempt
	}
	return nil
}

func (store *CheckStore) read(ctx context.Context, scope tenant.Scope, work func(context.Context, *sqlgen.Queries) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set verification check tenant scope: %w", err)
		}
		return work(ctx, queries)
	})
}

func (store *CheckStore) write(ctx context.Context, scope tenant.Scope, work func(context.Context, *sqlgen.Queries) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationReadCommitted}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set verification check tenant scope: %w", err)
		}
		return work(ctx, queries)
	})
}

func persistAttempts(ctx context.Context, queries *sqlgen.Queries, check verification.Check, previousState verification.CheckState) error {
	attempts := check.Attempts()
	for index, attempt := range attempts {
		parameters, err := attemptInsertParams(check, attempt)
		if err != nil {
			return err
		}
		rows, err := queries.InsertVerificationAttempt(ctx, parameters)
		if err != nil {
			return fmt.Errorf("insert verification attempt: %w", err)
		}
		isLatest := index == len(attempts)-1
		persistObservations := rows == 1 && attempt.State == verification.AttemptCompleted
		if rows == 0 && isLatest && attempt.State != verification.AttemptRunning &&
			(previousState == verification.CheckRunning || previousState == verification.CheckAwaitingProvider) {
			completeRows, err := queries.CompleteVerificationAttempt(ctx, attemptCompletionParams(check, attempt))
			if err != nil {
				return fmt.Errorf("complete verification attempt: %w", err)
			}
			if completeRows != 1 {
				return verification.ErrStaleAttempt
			}
			persistObservations = true
		}
		if isLatest && persistObservations {
			for _, observation := range attempt.Observations {
				if err := queries.InsertVerificationObservation(ctx, observationInsertParams(check, observation)); err != nil {
					return fmt.Errorf("insert verification observation: %w", err)
				}
			}
		}
	}
	return nil
}

func persistDiagnostics(ctx context.Context, queries *sqlgen.Queries, check verification.Check) error {
	diagnostics := check.Diagnostics()
	for _, diagnostic := range diagnostics {
		rows, err := queries.InsertVerificationAttemptDiagnostic(ctx, sqlgen.InsertVerificationAttemptDiagnosticParams{
			TenantID: check.TenantID.String(), VerificationID: check.VerificationID.String(), CheckID: check.ID.String(),
			AttemptID: diagnostic.AttemptID.String(), Kind: diagnostic.Kind, Code: diagnostic.Code,
			RecordedAt: timestamp(diagnostic.RecordedAt),
		})
		if err != nil {
			return fmt.Errorf("insert verification attempt diagnostic: %w", err)
		}
		if rows == 1 && (diagnostic.Kind == "stale" || diagnostic.Kind == "conflict") {
			if _, err := queries.InsertVerificationReconciliation(ctx, sqlgen.InsertVerificationReconciliationParams{
				TenantID: check.TenantID.String(), VerificationID: check.VerificationID.String(), CheckID: check.ID.String(),
				AttemptID: diagnostic.AttemptID.String(), Reason: diagnostic.Kind,
				AvailableAt: timestamp(diagnostic.RecordedAt), CreatedAt: timestamp(diagnostic.RecordedAt),
				UpdatedAt: timestamp(diagnostic.RecordedAt),
			}); err != nil {
				return fmt.Errorf("insert verification reconciliation: %w", err)
			}
		}
	}
	return nil
}

func loadCheck(ctx context.Context, queries *sqlgen.Queries, tenantID id.Tenant, checkID id.Check) (verification.Check, error) {
	row, err := queries.FindVerificationCheck(ctx, sqlgen.FindVerificationCheckParams{TenantID: tenantID.String(), ID: checkID.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return verification.Check{}, verification.ErrCheckNotFound
	}
	if err != nil {
		return verification.Check{}, fmt.Errorf("find verification check: %w", err)
	}
	attemptRows, err := queries.ListVerificationAttempts(ctx, sqlgen.ListVerificationAttemptsParams{TenantID: tenantID.String(), CheckID: checkID.String()})
	if err != nil {
		return verification.Check{}, fmt.Errorf("list verification attempts: %w", err)
	}
	observationRows, err := queries.ListVerificationObservations(ctx, sqlgen.ListVerificationObservationsParams{TenantID: tenantID.String(), CheckID: checkID.String()})
	if err != nil {
		return verification.Check{}, fmt.Errorf("list verification observations: %w", err)
	}
	diagnosticRows, err := queries.ListVerificationAttemptDiagnostics(ctx, sqlgen.ListVerificationAttemptDiagnosticsParams{TenantID: tenantID.String(), CheckID: checkID.String()})
	if err != nil {
		return verification.Check{}, fmt.Errorf("list verification attempt diagnostics: %w", err)
	}
	observations := make(map[string][]verification.Observation)
	for _, stored := range observationRows {
		observation, restoreErr := restoreObservation(stored)
		if restoreErr != nil {
			return verification.Check{}, restoreErr
		}
		observations[stored.AttemptID] = append(observations[stored.AttemptID], observation)
	}
	attempts := make([]verification.Attempt, len(attemptRows))
	for index, stored := range attemptRows {
		attempts[index], err = restoreAttempt(stored, observations[stored.ID])
		if err != nil {
			return verification.Check{}, err
		}
	}
	diagnostics := make([]verification.Diagnostic, len(diagnosticRows))
	for index, stored := range diagnosticRows {
		attemptID, parseErr := id.ParseAttempt(stored.AttemptID)
		if parseErr != nil || !stored.RecordedAt.Valid {
			return verification.Check{}, verification.ErrInvalidCheck
		}
		diagnostics[index] = verification.Diagnostic{AttemptID: attemptID, Kind: stored.Kind, Code: stored.Code, RecordedAt: stored.RecordedAt.Time.UTC()}
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil || !row.CreatedAt.Valid || !row.UpdatedAt.Valid {
		return verification.Check{}, verification.ErrInvalidCheck
	}
	outcome := verification.CheckOutcome("")
	if row.Outcome != nil {
		outcome = verification.CheckOutcome(*row.Outcome)
	}
	return verification.RestoreCheck(checkID, tenantID, verificationID, row.Name, verification.CheckState(row.State), outcome,
		row.Version, row.CreatedAt.Time.UTC(), row.UpdatedAt.Time.UTC(), attempts, diagnostics)
}

func restoreAttempt(row sqlgen.IdenqaVerificationAttempt, observations []verification.Observation) (verification.Attempt, error) {
	attemptID, err := id.ParseAttempt(row.ID)
	if err != nil || row.AttemptNumber < 1 || row.Fence < 1 || row.ContractMajor < 1 || row.ContractMajor > math.MaxUint16 ||
		row.ContractMinor < 0 || row.ContractMinor > math.MaxUint16 || !row.StartedAt.Valid || !row.Deadline.Valid {
		return verification.Attempt{}, verification.ErrInvalidCheck
	}
	attempt := verification.Attempt{ID: attemptID, Number: uint32(row.AttemptNumber), Fence: uint64(row.Fence),
		RunnerKind: verification.RunnerKind(row.RunnerKind), Provenance: verification.Provenance{
			RunnerID: row.RunnerID, RunnerVersion: row.RunnerVersion, PackageDigest: row.PackageDigest,
			ContractMajor: uint16(row.ContractMajor), ContractMinor: uint16(row.ContractMinor),
			RequestDigest: row.RequestDigest, Configuration: row.ConfigurationDigest,
		}, State: verification.AttemptState(row.State), StartedAt: row.StartedAt.Time.UTC(), Deadline: row.Deadline.Time.UTC(),
		Observations: observations}
	if row.FinishedAt.Valid {
		attempt.FinishedAt = row.FinishedAt.Time.UTC()
	}
	if row.FailureClass != nil && row.FailureCode != nil && row.RetryDisposition != nil && row.RetryAfterMilliseconds != nil {
		if *row.RetryAfterMilliseconds < 0 || *row.RetryAfterMilliseconds > math.MaxInt64/int64(time.Millisecond) {
			return verification.Attempt{}, verification.ErrInvalidCheck
		}
		attempt.Failure = &verification.Failure{Class: *row.FailureClass, Code: *row.FailureCode,
			Retry: verification.RetryDisposition(*row.RetryDisposition), RetryAfter: time.Duration(*row.RetryAfterMilliseconds) * time.Millisecond}
	}
	resultDigest := ""
	if row.ResultDigest != nil {
		resultDigest = *row.ResultDigest
	}
	return verification.RestoreAttempt(attempt, resultDigest)
}

func restoreObservation(row sqlgen.IdenqaVerificationObservation) (verification.Observation, error) {
	observationID, err := id.ParseObservation(row.ID)
	if err != nil {
		return verification.Observation{}, verification.ErrInvalidCheck
	}
	attemptID, err := id.ParseAttempt(row.AttemptID)
	if err != nil || row.ContractMajor < 1 || row.ContractMajor > math.MaxUint16 || row.ContractMinor < 0 || row.ContractMinor > math.MaxUint16 || !row.RecordedAt.Valid {
		return verification.Observation{}, verification.ErrInvalidCheck
	}
	return verification.Observation{ID: observationID, AttemptID: attemptID, RunnerKind: verification.RunnerKind(row.RunnerKind),
		Provenance: verification.Provenance{RunnerID: row.RunnerID, RunnerVersion: row.RunnerVersion,
			PackageDigest: row.PackageDigest, ContractMajor: uint16(row.ContractMajor), ContractMinor: uint16(row.ContractMinor),
			RequestDigest: row.RequestDigest, Configuration: row.ConfigurationDigest},
		Signal:     verification.Signal{Name: row.SignalName, Outcome: verification.SignalOutcome(row.SignalOutcome), ReasonCodes: row.ReasonCodes},
		RecordedAt: row.RecordedAt.Time.UTC()}, nil
}

func restoreReconciliation(row sqlgen.IdenqaVerificationReconciliation, tenantID id.Tenant, claimToken id.Task, claimedAt time.Time) (verification.ReconciliationClaim, error) {
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return verification.ReconciliationClaim{}, verification.ErrInvalidCheck
	}
	checkID, err := id.ParseCheck(row.CheckID)
	if err != nil {
		return verification.ReconciliationClaim{}, verification.ErrInvalidCheck
	}
	attemptID, err := id.ParseAttempt(row.AttemptID)
	if err != nil || row.ClaimToken == nil || *row.ClaimToken != claimToken.String() || !row.LeaseExpiresAt.Valid || row.ClaimCount < 1 {
		return verification.ReconciliationClaim{}, verification.ErrInvalidCheck
	}
	return verification.ReconciliationClaim{TenantID: tenantID, VerificationID: verificationID, CheckID: checkID,
		AttemptID: attemptID, Reason: row.Reason, ClaimToken: claimToken, ClaimCount: uint32(row.ClaimCount),
		ClaimedAt: claimedAt, LeaseExpiresAt: row.LeaseExpiresAt.Time.UTC()}, nil
}

func checkInsertParams(check verification.Check) sqlgen.InsertVerificationCheckParams {
	return sqlgen.InsertVerificationCheckParams{ID: check.ID.String(), TenantID: check.TenantID.String(),
		VerificationID: check.VerificationID.String(), Name: check.Name, State: string(check.State), Outcome: optionalOutcome(check.Outcome),
		Version: check.Version, CreatedAt: timestamp(check.CreatedAt), UpdatedAt: timestamp(check.UpdatedAt)}
}

func checkUpdateParams(check verification.Check, expected int64) sqlgen.UpdateVerificationCheckParams {
	return sqlgen.UpdateVerificationCheckParams{State: string(check.State), Outcome: optionalOutcome(check.Outcome), Version: check.Version,
		UpdatedAt: timestamp(check.UpdatedAt), TenantID: check.TenantID.String(), ID: check.ID.String(), ExpectedVersion: expected}
}

func attemptInsertParams(check verification.Check, attempt verification.Attempt) (sqlgen.InsertVerificationAttemptParams, error) {
	if attempt.Number > math.MaxInt32 || attempt.Fence > math.MaxInt64 {
		return sqlgen.InsertVerificationAttemptParams{}, verification.ErrInvalidCheck
	}
	parameters := sqlgen.InsertVerificationAttemptParams{ID: attempt.ID.String(), TenantID: check.TenantID.String(), VerificationID: check.VerificationID.String(),
		CheckID: check.ID.String(), AttemptNumber: int32(attempt.Number), Fence: int64(attempt.Fence),
		RunnerKind: string(attempt.RunnerKind), RunnerID: attempt.Provenance.RunnerID, RunnerVersion: attempt.Provenance.RunnerVersion,
		PackageDigest: attempt.Provenance.PackageDigest, ContractMajor: int32(attempt.Provenance.ContractMajor), ContractMinor: int32(attempt.Provenance.ContractMinor),
		RequestDigest: attempt.Provenance.RequestDigest, ConfigurationDigest: attempt.Provenance.Configuration, State: string(attempt.State),
		StartedAt: timestamp(attempt.StartedAt), Deadline: timestamp(attempt.Deadline)}
	terminalAttemptParams(attempt, &parameters.FinishedAt, &parameters.FailureClass, &parameters.FailureCode,
		&parameters.RetryDisposition, &parameters.RetryAfterMilliseconds, &parameters.ResultDigest)
	return parameters, nil
}

func attemptCompletionParams(check verification.Check, attempt verification.Attempt) sqlgen.CompleteVerificationAttemptParams {
	parameters := sqlgen.CompleteVerificationAttemptParams{State: string(attempt.State), FinishedAt: timestamp(attempt.FinishedAt),
		TenantID: check.TenantID.String(), ID: attempt.ID.String(), CheckID: check.ID.String(), Fence: int64(attempt.Fence)} //nolint:gosec // fence was bounded for insertion.
	parameters.ResultDigest = optionalString(attempt.ResultDigest())
	if attempt.Failure != nil {
		parameters.FailureClass, parameters.FailureCode = optionalString(attempt.Failure.Class), optionalString(attempt.Failure.Code)
		parameters.RetryDisposition = optionalString(string(attempt.Failure.Retry))
		retryMilliseconds := attempt.Failure.RetryAfter.Milliseconds()
		parameters.RetryAfterMilliseconds = &retryMilliseconds
	}
	return parameters
}

func observationInsertParams(check verification.Check, observation verification.Observation) sqlgen.InsertVerificationObservationParams {
	reasonCodes := observation.Signal.ReasonCodes
	if reasonCodes == nil {
		reasonCodes = []string{}
	}
	return sqlgen.InsertVerificationObservationParams{ID: observation.ID.String(), TenantID: check.TenantID.String(), VerificationID: check.VerificationID.String(),
		CheckID: check.ID.String(), AttemptID: observation.AttemptID.String(), RunnerKind: string(observation.RunnerKind),
		RunnerID: observation.Provenance.RunnerID, RunnerVersion: observation.Provenance.RunnerVersion,
		PackageDigest: observation.Provenance.PackageDigest, ContractMajor: int32(observation.Provenance.ContractMajor), ContractMinor: int32(observation.Provenance.ContractMinor),
		RequestDigest: observation.Provenance.RequestDigest, ConfigurationDigest: observation.Provenance.Configuration,
		SignalName: observation.Signal.Name, SignalOutcome: string(observation.Signal.Outcome), ReasonCodes: reasonCodes,
		RecordedAt: timestamp(observation.RecordedAt)}
}

func terminalAttemptParams(attempt verification.Attempt, finishedAt *pgtype.Timestamptz, failureClass **string, failureCode **string, retry **string, retryAfter **int64, resultDigest **string) {
	if attempt.State == verification.AttemptRunning {
		return
	}
	*finishedAt = timestamp(attempt.FinishedAt)
	*resultDigest = optionalString(attempt.ResultDigest())
	if attempt.Failure == nil {
		return
	}
	*failureClass, *failureCode, *retry = optionalString(attempt.Failure.Class), optionalString(attempt.Failure.Code), optionalString(string(attempt.Failure.Retry))
	milliseconds := attempt.Failure.RetryAfter.Milliseconds()
	*retryAfter = &milliseconds
}

func insertCheckProgress(ctx context.Context, queries *sqlgen.Queries, check verification.Check, eventID id.Event) error {
	intent, err := outbox.NewIntent(eventID, checkAggregateType, check.ID.String(), check.Version, checkProgressEvent, checkEventSchema,
		map[string]any{"verification_id": check.VerificationID.String(), "check_id": check.ID.String(), "state": check.State,
			"version": check.Version, "occurred_at": check.UpdatedAt}, check.UpdatedAt)
	if err != nil {
		return err
	}
	if err := queries.InsertOutboxEvent(ctx, sqlgen.InsertOutboxEventParams{ID: intent.ID.String(), TenantID: check.TenantID.String(),
		AggregateType: intent.AggregateType, AggregateID: intent.AggregateID, AggregateVersion: intent.AggregateVersion,
		EventType: intent.EventType, SchemaVersion: checkEventSchema, Payload: intent.Payload,
		OccurredAt: timestamp(intent.OccurredAt), CreatedAt: timestamp(intent.OccurredAt)}); err != nil {
		return fmt.Errorf("insert check progress outbox intent: %w", err)
	}
	if err := queries.NotifyCheckProgress(ctx, check.TenantID.String()); err != nil {
		return fmt.Errorf("notify check progress projection: %w", err)
	}
	return nil
}

func optionalOutcome(value verification.CheckOutcome) *string {
	if value == "" {
		return nil
	}
	encoded := string(value)
	return &encoded
}
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func utcTime(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

var _ verification.CheckRepository = (*CheckStore)(nil)
var _ verification.ReconciliationRepository = (*CheckStore)(nil)
