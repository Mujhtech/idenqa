package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type grantTransaction struct{ tx platformpostgres.Transaction }

func (bound grantTransaction) WithinTransaction(ctx context.Context, _ platformpostgres.TransactionOptions, work func(context.Context, platformpostgres.Transaction) error) error {
	return work(ctx, bound.tx)
}

const (
	grantAuditCreate int32 = 1
	grantAuditRevoke int32 = 2
)

var (
	_ evidence.GrantCreator         = (*Store)(nil)
	_ evidence.GrantFinder          = (*Store)(nil)
	_ evidence.GrantClaimer         = (*Store)(nil)
	_ evidence.GrantOutcomeRecorder = (*Store)(nil)
	_ evidence.GrantRevoker         = (*Store)(nil)
)

// ApplyGrant atomically stores one grant and its exact retry receipt.
func (store *Store) ApplyGrant(ctx context.Context, scope tenant.Scope, request idempotency.Request, issue func(evidence.GrantCommandStore) (evidence.Grant, error)) (evidence.Grant, error) {
	if scope.ID().IsZero() || request.TenantID() != scope.ID() || issue == nil {
		return evidence.Grant{}, evidence.ErrGrantDenied
	}
	var result evidence.Grant
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		reservation, err := idempotencypostgres.Reserve(ctx, queries, request)
		if err != nil {
			return err
		}
		if prior, ok := reservation.Result(); ok {
			var receipt struct {
				GrantID string `json:"grant_id"`
			}
			if err := json.Unmarshal(prior.Body(), &receipt); err != nil {
				return err
			}
			identifier, err := id.ParseGrant(receipt.GrantID)
			if err != nil {
				return err
			}
			row, err := queries.FindEvidenceProcessingGrant(ctx, sqlgen.FindEvidenceProcessingGrantParams{TenantID: scope.ID().String(), ID: identifier.String()})
			if err != nil {
				return err
			}
			result, err = restoreGrant(row, nil)
			return err
		}
		bound := &Store{pool: grantTransaction{tx}, catalog: store.catalog, clock: store.clock, wrapper: store.wrapper, metrics: store.metrics}
		result, err = issue(bound)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(map[string]string{"grant_id": result.ID().String()})
		if err != nil {
			return err
		}
		receipt, err := idempotency.NewResult(201, encoded)
		if err != nil {
			return err
		}
		return idempotencypostgres.Complete(ctx, queries, request, receipt, store.clock.Now().UTC())
	})
	return result, err
}

// FindGrant loads one tenant-scoped grant without redemption details.
func (store *Store) FindGrant(ctx context.Context, scope tenant.Scope, grantID id.Grant) (evidence.Grant, error) {
	if scope.ID().IsZero() || grantID.IsZero() {
		return evidence.Grant{}, evidence.ErrGrantDenied
	}
	var result evidence.Grant
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set tenant scope: %w", err)
		}
		row, err := queries.FindEvidenceProcessingGrant(ctx, sqlgen.FindEvidenceProcessingGrantParams{TenantID: scope.ID().String(), ID: grantID.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrGrantDenied
		}
		if err != nil {
			return fmt.Errorf("find evidence processing grant: %w", err)
		}
		result, err = restoreGrant(row, nil)
		return err
	})
	return result, err
}

// CreateGrant atomically inserts one unredeemed grant and its creation audit.
func (store *Store) CreateGrant(
	ctx context.Context,
	scope tenant.Scope,
	grant evidence.Grant,
	attribution evidence.CommandAttribution,
) error {
	record := grant.Record()
	if scope.ID().IsZero() || record.TenantID != scope.ID() || record.Uses != 0 ||
		record.RevokedAt != nil || !attribution.Valid() {
		return evidence.ErrGrantDenied
	}

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		parameters, err := createGrantParameters(record)
		if err != nil {
			return err
		}
		if err := queries.CreateEvidenceProcessingGrant(ctx, parameters); err != nil {
			return fmt.Errorf("insert evidence processing grant: %w", err)
		}
		if err := queries.InsertEvidenceProcessingGrantAudit(
			ctx,
			grantAuditParameters(record.TenantID, record.ID, grantAuditCreate,
				"create", attribution, record.CreatedAt),
		); err != nil {
			return fmt.Errorf("insert evidence processing grant audit: %w", err)
		}
		return nil
	})
}

// ClaimGrant serializes on the tenant/redemption identity, returns an existing
// attempt idempotently, or atomically increments one grant use and creates it.
func (store *Store) ClaimGrant(
	ctx context.Context,
	scope tenant.Scope,
	grantID id.Grant,
	redemptionID id.Redemption,
	runner evidence.Runner,
	now time.Time,
) (evidence.Redemption, error) {
	if scope.ID().IsZero() || grantID.IsZero() || redemptionID.IsZero() ||
		!runner.Valid() || now.IsZero() {
		return evidence.Redemption{}, evidence.ErrGrantDenied
	}
	var (
		redemption evidence.Redemption
		denied     bool
	)
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.LockEvidenceGrantRedemptionID(
			ctx,
			sqlgen.LockEvidenceGrantRedemptionIDParams{
				TenantID: scope.ID().String(), RedemptionID: redemptionID.String(),
			},
		); err != nil {
			return fmt.Errorf("lock evidence grant redemption: %w", err)
		}

		existing, err := queries.FindEvidenceGrantRedemption(
			ctx,
			sqlgen.FindEvidenceGrantRedemptionParams{
				TenantID: scope.ID().String(), ID: redemptionID.String(),
			},
		)
		if err == nil {
			redemption, denied, err = store.restoreExistingRedemption(
				ctx, queries, scope, grantID, redemptionID, runner, existing,
			)
			if err != nil {
				return err
			}
			decision := "replayed_pending"
			var reason *string
			if denied {
				decision = "denied"
				value := "redemption_mismatch"
				if existing.DenialReason != nil && existing.GrantID == grantID.String() &&
					existing.RunnerIdentity == runner.Identity &&
					existing.WorkloadVersion == runner.WorkloadVersion {
					value = *existing.DenialReason
				}
				reason = &value
			} else if _, terminal := redemption.TerminalOutcome(); terminal {
				decision = "replayed_terminal"
			}
			return insertGrantAccessAttempt(
				ctx, queries, scope, grantID, redemptionID, runner, decision, reason, now,
			)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("find evidence grant redemption: %w", err)
		}

		row, err := queries.LockEvidenceProcessingGrant(
			ctx,
			sqlgen.LockEvidenceProcessingGrantParams{
				TenantID: scope.ID().String(), ID: grantID.String(),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			denied = true
			reason := "not_found"
			return insertGrantAccessAttempt(
				ctx, queries, scope, grantID, redemptionID, runner, "denied", &reason, now,
			)
		}
		if err != nil {
			return fmt.Errorf("lock evidence processing grant: %w", err)
		}
		grant, err := restoreGrant(row, nil)
		if err != nil {
			return err
		}
		claimed, err := grant.Claim(runner, now)
		if errors.Is(err, evidence.ErrGrantDenied) {
			denied = true
			if err := recordDeniedRedemption(
				ctx, queries, scope, grant, redemptionID, runner, now,
			); err != nil {
				return err
			}
			reason := denialReason(grant.Record(), runner, now)
			return insertGrantAccessAttempt(
				ctx, queries, scope, grantID, redemptionID, runner, "denied", &reason, now,
			)
		}
		if err != nil {
			return fmt.Errorf("claim evidence processing grant: %w", err)
		}
		updated, err := queries.IncrementEvidenceProcessingGrantUse(
			ctx,
			sqlgen.IncrementEvidenceProcessingGrantUseParams{
				TenantID: scope.ID().String(), ID: grantID.String(), Uses: row.Uses,
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrGrantConflict
		}
		if err != nil {
			return fmt.Errorf("increment evidence processing grant use: %w", err)
		}
		persisted, err := restoreGrant(updated, nil)
		if err != nil {
			return err
		}
		if persisted.Uses() != claimed.Uses() {
			return errors.New("evidence postgres: claimed grant use is inconsistent")
		}
		claimedUse := updated.Uses
		if err := queries.CreatePendingEvidenceGrantRedemption(
			ctx,
			sqlgen.CreatePendingEvidenceGrantRedemptionParams{
				ID: redemptionID.String(), TenantID: scope.ID().String(),
				GrantID: grantID.String(), RunnerIdentity: runner.Identity,
				WorkloadVersion: runner.WorkloadVersion, ClaimedUse: &claimedUse,
				AttemptedAt: timestamp(now),
			},
		); err != nil {
			return fmt.Errorf("insert pending evidence grant redemption: %w", err)
		}
		redemption, err = evidence.NewRedemption(redemptionID, persisted)
		if err != nil {
			return err
		}
		return insertGrantAccessAttempt(
			ctx, queries, scope, grantID, redemptionID, runner, "claimed", nil, now,
		)
	})
	if err != nil {
		return evidence.Redemption{}, err
	}
	if denied {
		return evidence.Redemption{}, evidence.ErrGrantDenied
	}
	return redemption, nil
}

// RecordGrantOutcome appends one immutable terminal outcome idempotently.
func (store *Store) RecordGrantOutcome(
	ctx context.Context,
	scope tenant.Scope,
	redemption evidence.Redemption,
	outcome evidence.GrantOutcome,
	now time.Time,
) error {
	grant := redemption.Grant()
	if scope.ID().IsZero() || redemption.ID().IsZero() || grant.TenantID() != scope.ID() ||
		now.IsZero() {
		return evidence.ErrGrantDenied
	}
	if _, err := redemption.Complete(outcome); err != nil {
		return err
	}

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.LockEvidenceGrantRedemptionID(
			ctx,
			sqlgen.LockEvidenceGrantRedemptionIDParams{
				TenantID: scope.ID().String(), RedemptionID: redemption.ID().String(),
			},
		); err != nil {
			return fmt.Errorf("lock evidence grant redemption outcome: %w", err)
		}
		row, err := queries.FindEvidenceGrantRedemption(
			ctx,
			sqlgen.FindEvidenceGrantRedemptionParams{
				TenantID: scope.ID().String(), ID: redemption.ID().String(),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrGrantDenied
		}
		if err != nil {
			return fmt.Errorf("find evidence grant redemption for outcome: %w", err)
		}
		grantRecord := grant.Record()
		if row.ClaimedUse == nil || row.DenialReason != nil || row.GrantID != grant.ID().String() ||
			row.RunnerIdentity != grantRecord.Runner.Identity ||
			row.WorkloadVersion != grantRecord.Runner.WorkloadVersion ||
			int64(*row.ClaimedUse) != int64(grant.Uses()) {
			return evidence.ErrGrantDenied
		}
		if !row.AttemptedAt.Valid || now.UTC().Before(row.AttemptedAt.Time.UTC()) {
			return evidence.ErrGrantConflict
		}
		return store.createOrVerifyOutcome(ctx, queries, scope, redemption, outcome, now)
	})
}

// RevokeGrant atomically persists one irreversible revocation and audit row.
func (store *Store) RevokeGrant(
	ctx context.Context,
	scope tenant.Scope,
	grantID id.Grant,
	attribution evidence.CommandAttribution,
	now time.Time,
) (evidence.Grant, error) {
	if scope.ID().IsZero() || grantID.IsZero() || !attribution.Valid() || now.IsZero() {
		return evidence.Grant{}, evidence.ErrGrantDenied
	}
	var revoked evidence.Grant
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.LockEvidenceProcessingGrant(
			ctx,
			sqlgen.LockEvidenceProcessingGrantParams{
				TenantID: scope.ID().String(), ID: grantID.String(),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrGrantDenied
		}
		if err != nil {
			return fmt.Errorf("lock evidence processing grant for revocation: %w", err)
		}
		grant, err := restoreGrant(row, nil)
		if err != nil {
			return err
		}
		revoked, err = grant.Revoke(now)
		if err != nil {
			return err
		}
		updated, err := queries.RevokeEvidenceProcessingGrant(
			ctx,
			sqlgen.RevokeEvidenceProcessingGrantParams{
				TenantID: scope.ID().String(), ID: grantID.String(),
				RevokedAt: timestamp(now),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrGrantConflict
		}
		if err != nil {
			return fmt.Errorf("revoke evidence processing grant: %w", err)
		}
		revoked, err = restoreGrant(updated, nil)
		if err != nil {
			return err
		}
		if err := queries.InsertEvidenceProcessingGrantAudit(
			ctx,
			grantAuditParameters(scope.ID(), grantID, grantAuditRevoke,
				"revoke", attribution, now),
		); err != nil {
			return fmt.Errorf("insert evidence grant revocation audit: %w", err)
		}
		return nil
	})
	return revoked, err
}

func insertGrantAccessAttempt(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	grantID id.Grant,
	redemptionID id.Redemption,
	runner evidence.Runner,
	decision string,
	denialReason *string,
	now time.Time,
) error {
	if err := queries.InsertEvidenceGrantAccessAttemptAudit(
		ctx,
		sqlgen.InsertEvidenceGrantAccessAttemptAuditParams{
			TenantID: scope.ID().String(), PresentedGrantID: grantID.String(),
			RedemptionID: redemptionID.String(), RunnerIdentity: runner.Identity,
			WorkloadVersion: runner.WorkloadVersion, Decision: decision,
			DenialReason: denialReason, OccurredAt: timestamp(now),
		},
	); err != nil {
		return fmt.Errorf("insert evidence grant access-attempt audit: %w", err)
	}
	return nil
}

func grantAuditParameters(
	tenantID id.Tenant,
	grantID id.Grant,
	version int32,
	action string,
	attribution evidence.CommandAttribution,
	now time.Time,
) sqlgen.InsertEvidenceProcessingGrantAuditParams {
	return sqlgen.InsertEvidenceProcessingGrantAuditParams{
		TenantID: tenantID.String(), GrantID: grantID.String(),
		AggregateVersion: version, Action: action,
		PrincipalType: attribution.Principal.Type, PrincipalID: attribution.Principal.ID,
		TenantActorType: attribution.TenantActor.Type, TenantActorID: attribution.TenantActor.ID,
		Reason: attribution.Reason, OccurredAt: timestamp(now),
	}
}

func (store *Store) restoreExistingRedemption(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	grantID id.Grant,
	redemptionID id.Redemption,
	runner evidence.Runner,
	row sqlgen.IdenqaEvidenceGrantRedemption,
) (evidence.Redemption, bool, error) {
	if row.GrantID != grantID.String() || row.RunnerIdentity != runner.Identity ||
		row.WorkloadVersion != runner.WorkloadVersion || row.DenialReason != nil ||
		row.ClaimedUse == nil {
		return evidence.Redemption{}, true, nil
	}
	grantRow, err := queries.FindEvidenceProcessingGrant(
		ctx,
		sqlgen.FindEvidenceProcessingGrantParams{
			TenantID: scope.ID().String(), ID: grantID.String(),
		},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence.Redemption{}, true, nil
	}
	if err != nil {
		return evidence.Redemption{}, false, fmt.Errorf("find redeemed evidence grant: %w", err)
	}
	grant, err := restoreGrant(grantRow, row.ClaimedUse)
	if err != nil {
		return evidence.Redemption{}, false, err
	}
	outcome := evidence.GrantOutcome("")
	outcomeRow, err := queries.FindEvidenceGrantRedemptionOutcome(
		ctx,
		sqlgen.FindEvidenceGrantRedemptionOutcomeParams{
			TenantID: scope.ID().String(), RedemptionID: redemptionID.String(),
			GrantID: grantID.String(),
		},
	)
	if err == nil {
		outcome = evidence.GrantOutcome(outcomeRow.Outcome)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return evidence.Redemption{}, false, fmt.Errorf("find evidence grant redemption outcome: %w", err)
	}
	redemption, err := evidence.RestoreRedemption(redemptionID, grant, outcome)
	return redemption, false, err
}

func (store *Store) createOrVerifyOutcome(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	redemption evidence.Redemption,
	outcome evidence.GrantOutcome,
	now time.Time,
) error {
	denialReason := (*string)(nil)
	if outcome == evidence.GrantOutcomeDenied {
		reason := "post_claim_denied"
		denialReason = &reason
	}
	parameters := sqlgen.CreateEvidenceGrantRedemptionOutcomeParams{
		TenantID: scope.ID().String(), RedemptionID: redemption.ID().String(),
		GrantID: redemption.Grant().ID().String(), Outcome: string(outcome),
		DenialReason: denialReason, OccurredAt: timestamp(now),
	}
	inserted, err := queries.CreateEvidenceGrantRedemptionOutcome(ctx, parameters)
	if err != nil {
		return fmt.Errorf("insert evidence grant redemption outcome: %w", err)
	}
	if inserted == 1 {
		return nil
	}
	existing, err := queries.FindEvidenceGrantRedemptionOutcome(
		ctx,
		sqlgen.FindEvidenceGrantRedemptionOutcomeParams{
			TenantID: parameters.TenantID, RedemptionID: parameters.RedemptionID,
			GrantID: parameters.GrantID,
		},
	)
	if err != nil {
		return fmt.Errorf("reload evidence grant redemption outcome: %w", err)
	}
	if existing.Outcome != string(outcome) {
		return evidence.ErrGrantConflict
	}
	return nil
}

func recordDeniedRedemption(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	grant evidence.Grant,
	redemptionID id.Redemption,
	runner evidence.Runner,
	now time.Time,
) error {
	reason := denialReason(grant.Record(), runner, now)
	if err := queries.CreateDeniedEvidenceGrantRedemption(
		ctx,
		sqlgen.CreateDeniedEvidenceGrantRedemptionParams{
			ID: redemptionID.String(), TenantID: scope.ID().String(),
			GrantID: grant.ID().String(), RunnerIdentity: runner.Identity,
			WorkloadVersion: runner.WorkloadVersion, DenialReason: &reason,
			AttemptedAt: timestamp(now),
		},
	); err != nil {
		return fmt.Errorf("insert denied evidence grant redemption: %w", err)
	}
	if _, err := queries.CreateEvidenceGrantRedemptionOutcome(
		ctx,
		sqlgen.CreateEvidenceGrantRedemptionOutcomeParams{
			TenantID: scope.ID().String(), RedemptionID: redemptionID.String(),
			GrantID: grant.ID().String(), Outcome: string(evidence.GrantOutcomeDenied),
			DenialReason: &reason, OccurredAt: timestamp(now),
		},
	); err != nil {
		return fmt.Errorf("insert denied evidence grant outcome: %w", err)
	}
	return nil
}

func denialReason(record evidence.GrantRecord, runner evidence.Runner, now time.Time) string {
	switch {
	case runner != record.Runner:
		return "runner_mismatch"
	case now.UTC().Before(record.CreatedAt):
		return "not_started"
	case record.RevokedAt != nil:
		return "revoked"
	case !now.UTC().Before(record.ExpiresAt):
		return "expired"
	default:
		return "exhausted"
	}
}

func createGrantParameters(
	record evidence.GrantRecord,
) (sqlgen.CreateEvidenceProcessingGrantParams, error) {
	if record.MaximumUses > math.MaxInt32 || record.Uses > math.MaxInt32 {
		return sqlgen.CreateEvidenceProcessingGrantParams{},
			errors.New("evidence postgres: grant use count exceeds database range")
	}
	return sqlgen.CreateEvidenceProcessingGrantParams{
		ID: record.ID.String(), TenantID: record.TenantID.String(),
		SubjectID: record.SubjectID.String(), VerificationID: record.VerificationID.String(),
		EvidenceID: record.EvidenceID.String(), RequirementKey: record.RequirementKey,
		AuthorityID: record.AuthorityID.String(), ResponseID: record.ResponseID.String(),
		CheckReference: record.CheckReference, RunnerIdentity: record.Runner.Identity,
		WorkloadVersion: record.Runner.WorkloadVersion, Purpose: string(record.Purpose),
		Operation: record.Operation, PermittedVariants: record.PermittedVariants,
		Region: record.Region, RecipientReference: record.RecipientReference,
		OutputDestination: record.OutputDestination, PolicyReference: record.PolicyReference,
		MaximumUses: int32(record.MaximumUses), Uses: int32(record.Uses),
		CreatedAt: timestamp(record.CreatedAt), ExpiresAt: timestamp(record.ExpiresAt),
		RevokedAt: nullableTimestamp(record.RevokedAt),
	}, nil
}

func restoreGrant(
	row sqlgen.IdenqaEvidenceProcessingGrant,
	claimedUse *int32,
) (evidence.Grant, error) {
	if row.MaximumUses <= 0 || row.Uses < 0 || row.Uses > row.MaximumUses ||
		!row.CreatedAt.Valid || !row.ExpiresAt.Valid {
		return evidence.Grant{}, errors.New("evidence postgres: invalid durable grant bounds")
	}
	grantID, err := id.ParseGrant(row.ID)
	if err != nil {
		return evidence.Grant{}, fmt.Errorf("restore evidence grant id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return evidence.Grant{}, fmt.Errorf("restore evidence grant tenant: %w", err)
	}
	subjectID, err := id.ParseSubject(row.SubjectID)
	if err != nil {
		return evidence.Grant{}, fmt.Errorf("restore evidence grant subject: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return evidence.Grant{}, fmt.Errorf("restore evidence grant verification: %w", err)
	}
	evidenceID, err := id.ParseEvidence(row.EvidenceID)
	if err != nil {
		return evidence.Grant{}, fmt.Errorf("restore evidence grant evidence: %w", err)
	}
	authorityID, err := id.ParseAuthority(row.AuthorityID)
	if err != nil {
		return evidence.Grant{}, fmt.Errorf("restore evidence grant authority: %w", err)
	}
	responseID, err := id.ParseAcknowledgement(row.ResponseID)
	if err != nil {
		return evidence.Grant{}, fmt.Errorf("restore evidence grant response: %w", err)
	}
	uses := row.Uses
	if claimedUse != nil {
		uses = *claimedUse
	}
	revokedAt := timestampPointer(row.RevokedAt)
	return evidence.RestoreGrant(evidence.GrantRecord{
		ID: grantID, TenantID: tenantID, SubjectID: subjectID,
		VerificationID: verificationID, EvidenceID: evidenceID,
		RequirementKey: row.RequirementKey, AuthorityID: authorityID, ResponseID: responseID,
		CheckReference: row.CheckReference,
		Runner:         evidence.Runner{Identity: row.RunnerIdentity, WorkloadVersion: row.WorkloadVersion},
		Purpose:        evidence.Name(row.Purpose), Operation: row.Operation,
		PermittedVariants: row.PermittedVariants, Region: row.Region,
		RecipientReference: row.RecipientReference, OutputDestination: row.OutputDestination,
		PolicyReference: row.PolicyReference, MaximumUses: uint32(row.MaximumUses),
		Uses: uint32(uses), CreatedAt: row.CreatedAt.Time, ExpiresAt: row.ExpiresAt.Time,
		RevokedAt: revokedAt,
	})
}
