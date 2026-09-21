package postgres

import (
	"context"
	"encoding/json"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	policypg "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// CreateRecaptureWithin copies only the parent's immutable capture configuration.
// The caller owns authorization, idempotency and atomic review linkage.
func (store *SessionStore) CreateRecaptureWithin(ctx context.Context, scope tenant.Scope, tx pg.Transaction, parentID id.Verification, mutation verification.SessionCreateMutation) (verification.SessionCreation, error) {
	if tx == nil || parentID.IsZero() || !validSessionMutationForOperation(scope, mutation, "reviews.recapture") {
		return verification.SessionCreation{}, verification.ErrSessionConflict
	}
	q := sqlgen.New(tx)
	if _, err := q.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return verification.SessionCreation{}, err
	}
	row, err := q.FindVerificationSession(ctx, sqlgen.FindVerificationSessionParams{TenantID: scope.ID().String(), ID: parentID.String()})
	if err != nil {
		return verification.SessionCreation{}, err
	}
	parent, err := store.restoreSession(row)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	if parent.PolicyID() != mutation.PolicyID || parent.ProfileID() != mutation.ProfileID || parent.Region() != mutation.Region {
		return verification.SessionCreation{}, verification.ErrSessionConflict
	}
	registry, err := store.catalog.Resolve(parent.Requirements().Registry)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	child, err := verification.RestoreSession(mutation.SessionID, scope.ID(), verification.SessionStateCollecting, 1, parent.ProfileID(), parent.ProfileRevision(), parent.ProfileDigest(), parent.Requirements(), parent.Region(), parent.PolicyID(), mutation.CreatedAt, mutation.CreatedAt, mutation.SessionExpiresAt, registry)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	credential, err := access.NewCaptureCredential(mutation.CaptureTokenID, scope.ID(), child.ID(), mutation.CaptureKeyVersion, mutation.CreatedAt, mutation.CaptureTokenExpiry)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	outcomeCredential, err := access.NewOutcomeCredential(
		mutation.OutcomeTokenID, scope.ID(), child.ID(), mutation.OutcomeKeyVersion,
		mutation.CreatedAt, mutation.OutcomeTokenExpiry,
	)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	result := verification.SessionCreation{
		Session: child, Credential: credential, OutcomeCredential: outcomeCredential,
	}
	if err := store.insertCreation(ctx, tx, q, mutation, result, registry); err != nil {
		return verification.SessionCreation{}, err
	}
	if err := policypg.CopyAssuranceWithin(ctx, tx, scope, parentID.String(), child.ID().String()); err != nil {
		return verification.SessionCreation{}, err
	}
	if store.metrics != nil {
		store.metrics.RecordVerificationRecapture(observability.Recapture{
			Reason: recaptureReason(mutation.Idempotency.Operation()),
			Region: observability.Region(mutation.Region),
		})
	}
	return result, nil
}

// RestoreCreationWithin restores only non-secret records for an already linked child.
func (store *SessionStore) RestoreCreationWithin(ctx context.Context, scope tenant.Scope, tx pg.Transaction, child id.Verification, token id.CaptureToken) (verification.SessionCreation, error) {
	if tx == nil || scope.ID().IsZero() {
		return verification.SessionCreation{}, verification.ErrSessionConflict
	}
	if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return verification.SessionCreation{}, err
	}
	outcomeCredential, err := sqlgen.New(tx).FindOutcomeTokenByVerification(
		ctx,
		sqlgen.FindOutcomeTokenByVerificationParams{
			TenantID: scope.ID().String(), VerificationID: child.String(),
		},
	)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	encoded, err := json.Marshal(sessionReplay{
		VerificationID: child.String(), CaptureTokenID: token.String(), OutcomeTokenID: outcomeCredential.ID,
	})
	if err != nil {
		return verification.SessionCreation{}, err
	}
	result, err := idempotency.NewResult(201, encoded)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	return store.restoreReplay(ctx, sqlgen.New(tx), scope.ID(), result)
}
