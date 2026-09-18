package postgres

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// RenewCaptureWithin replaces an expired expected token under the session lock.
// Caller owns idempotency and the review audit. A session deadline never moves.
func (store *SessionStore) RenewCaptureWithin(ctx context.Context, scope tenant.Scope, tx pg.Transaction, child id.Verification, input verification.CaptureRenewal, source clock.Clock) (verification.SessionCreation, error) {
	var empty verification.SessionCreation
	if tx == nil || source == nil || input.ExpectedToken.IsZero() || input.Token.IsZero() || input.Actor.IsZero() || input.At.IsZero() || !input.ExpiresAt.After(input.At) {
		return empty, verification.ErrSessionConflict
	}
	q := sqlgen.New(tx)
	if _, err := q.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return empty, err
	}
	session, err := q.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{TenantID: scope.ID().String(), ID: child.String()})
	if err != nil {
		return empty, err
	}
	now := source.Now().UTC()
	if input.At.After(now) {
		return empty, verification.ErrSessionConflict
	}
	if session.State != string(verification.SessionStateCollecting) || !session.ExpiresAt.Time.After(now) {
		return empty, verification.ErrSessionConflict
	}
	// Renewal must not reactivate a credential revoked for a security or authority reason.
	var latest string
	err = tx.QueryRow(ctx, `SELECT id FROM idenqa.capture_tokens WHERE tenant_id=$1 AND verification_id=$2 ORDER BY issued_at DESC,id DESC LIMIT 1`, scope.ID().String(), child.String()).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && latest != input.ExpectedToken.String() {
		return empty, verification.ErrSessionConflict
	}
	if err != nil {
		return empty, err
	}
	old, err := q.FindCaptureToken(ctx, sqlgen.FindCaptureTokenParams{TenantID: scope.ID().String(), ID: latest, VerificationID: child.String()})
	if err != nil {
		return empty, err
	}
	if old.RevokedAt.Valid || old.ExpiresAt.Time.After(now) {
		return empty, verification.ErrSessionConflict
	}
	if session.AuthorityID != nil {
		var allowed bool
		err = tx.QueryRow(ctx, `SELECT a.state='active' AND a.valid_from<=$3 AND a.expires_at>$3 AND NOT EXISTS(SELECT 1 FROM (SELECT action FROM idenqa.subject_responses WHERE tenant_id=$1 AND authority_id=a.id ORDER BY recorded_at DESC,id DESC LIMIT 1) latest WHERE latest.action NOT IN ('consent','acknowledge')) FROM idenqa.processing_authorities a WHERE a.tenant_id=$1 AND a.id=$2`, scope.ID().String(), *session.AuthorityID, now).Scan(&allowed)
		if err != nil {
			return empty, err
		}
		if !allowed {
			return empty, verification.ErrSessionConflict
		}
	}
	expiry := input.ExpiresAt
	if expiry.After(session.ExpiresAt.Time) {
		expiry = session.ExpiresAt.Time
	}
	credential, err := access.NewCaptureCredential(input.Token, scope.ID(), child, input.KeyVersion, input.At, expiry)
	if err != nil {
		return empty, err
	}
	if !credential.ExpiresAt().After(now) {
		return empty, verification.ErrSessionConflict
	}
	if tag, err := tx.Exec(ctx, `UPDATE idenqa.capture_tokens SET revoked_at=$3 WHERE tenant_id=$1 AND id=$2 AND revoked_at IS NULL`, scope.ID().String(), latest, input.At); err != nil {
		return empty, err
	} else if tag.RowsAffected() != 1 {
		return empty, verification.ErrSessionConflict
	}
	if err := q.CreateCaptureToken(ctx, sqlgen.CreateCaptureTokenParams{ID: input.Token.String(), TenantID: scope.ID().String(), VerificationID: child.String(), KeyVersion: int32(input.KeyVersion), IssuedAt: timestamp(credential.IssuedAt()), ExpiresAt: timestamp(credential.ExpiresAt())}); err != nil {
		return empty, err
	}
	// Write the locked session to fence snapshots established before renewal.
	if _, err := tx.Exec(ctx, `UPDATE idenqa.verification_sessions SET updated_at=GREATEST(updated_at,$3) WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), child.String(), now); err != nil {
		return empty, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.capture_recoveries(tenant_id,verification_id,old_token_id,new_token_id,recorded_at) VALUES($1,$2,$3,$4,$5)`, scope.ID().String(), child.String(), latest, input.Token.String(), now); err != nil {
		return empty, err
	}
	// Preserve original upload/token/response provenance. Pending work is durably
	// abandoned and its revoked credential cannot claim or commit another attempt.
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.capture_recovery_uploads(tenant_id,new_token_id,upload_id,disposition)
 SELECT tenant_id,$3,id,CASE WHEN state='accepted' THEN 'retained' ELSE 'abandoned' END
 FROM idenqa.evidence_upload_intents u WHERE tenant_id=$1 AND verification_id=$2 AND
 ((state='accepted' AND (capture_token_id=$4 OR EXISTS(SELECT 1 FROM idenqa.capture_recovery_uploads r WHERE r.tenant_id=u.tenant_id AND r.new_token_id=$4 AND r.upload_id=u.id AND r.disposition='retained')))
 OR (state IN ('issued','uploading') AND capture_token_id=$4))`, scope.ID().String(), child.String(), input.Token.String(), latest); err != nil {
		return empty, err
	}
	return store.RestoreCreationWithin(ctx, scope, tx, child, input.Token)
}
