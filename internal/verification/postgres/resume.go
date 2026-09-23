package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

type resumeReplay struct {
	DocumentSelections map[string]string `json:"document_selections,omitempty"`
	TokenID            string            `json:"capture_token_id"`
	Replaced           bool              `json:"replaced"`
	Version            int64             `json:"version"`
	OccurredAt         time.Time         `json:"occurred_at"`
}

type resumeClock struct{ at time.Time }

func (source resumeClock) Now() time.Time { return source.at }

// Resume atomically validates fresh subject authority, returns an
// awaiting-input session to collecting, and replaces only an unusable capture
// credential. Exact replay restores references but never bearer material.
func (store *SessionStore) Resume(ctx context.Context, scope tenant.Scope, mutation verification.ResumeMutation) (verification.ResumeResult, error) {
	var result verification.ResumeResult
	if scope.ID().IsZero() || mutation.VerificationID.IsZero() || mutation.ExpectedVersion < 1 || mutation.ReplacementID.IsZero() || mutation.EventID.IsZero() || mutation.Actor.IsZero() || mutation.KeyVersion == 0 || mutation.At.IsZero() || !mutation.TokenExpiresAt.After(mutation.At) || mutation.Idempotency.TenantID() != scope.ID() || mutation.Idempotency.Principal().String() != mutation.Actor.String() || mutation.Idempotency.Operation() != verification.OperationResumeVerification {
		return result, verification.ErrSessionConflict
	}
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		q := sqlgen.New(tx)
		if _, err := q.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		reserved, err := idempg.Reserve(ctx, q, mutation.Idempotency)
		if err != nil {
			return err
		}
		if replay, ok := reserved.Result(); ok {
			var saved resumeReplay
			if err := json.Unmarshal(replay.Body(), &saved); err != nil {
				return err
			}
			result, err = store.restoreResumeResult(ctx, q, scope, mutation.VerificationID, saved)
			result.Replayed = err == nil
			return err
		}

		var lockedID string
		if err := tx.QueryRow(ctx, `SELECT id FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), mutation.VerificationID.String()).Scan(&lockedID); errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		} else if err != nil {
			return err
		}
		row, err := q.FindVerificationSession(ctx, sqlgen.FindVerificationSessionParams{TenantID: scope.ID().String(), ID: mutation.VerificationID.String()})
		if err != nil {
			return err
		}
		session, err := store.restoreSession(row)
		if err != nil {
			return err
		}
		if session.State() != verification.SessionStateAwaitingInput || session.Version() != mutation.ExpectedVersion || !mutation.At.Before(session.ExpiresAt()) {
			return verification.ErrSessionConflict
		}
		var authorised bool
		err = tx.QueryRow(ctx, `SELECT EXISTS(
			SELECT 1 FROM idenqa.processing_authorities a
			WHERE a.tenant_id=$1 AND a.verification_id=$2 AND a.id=s.authority_id
			AND a.state='active' AND a.valid_from<=$3 AND a.expires_at>$3
			AND (SELECT r.action FROM idenqa.subject_responses r WHERE r.tenant_id=$1 AND r.authority_id=a.id ORDER BY r.recorded_at DESC,r.id DESC LIMIT 1) IN ('consent','acknowledge')
			AND (SELECT r.recorded_at FROM idenqa.subject_responses r WHERE r.tenant_id=$1 AND r.authority_id=a.id ORDER BY r.recorded_at DESC,r.id DESC LIMIT 1) >= s.updated_at)
			FROM idenqa.verification_sessions s WHERE s.tenant_id=$1 AND s.id=$2`, scope.ID().String(), mutation.VerificationID.String(), mutation.At).Scan(&authorised)
		if err != nil {
			return err
		}
		if !authorised {
			return verification.ErrSessionConflict
		}

		credential, latest, live, err := store.latestCaptureCredential(ctx, q, tx, scope, mutation.VerificationID, mutation.At)
		if err != nil {
			return err
		}
		replaced := !live
		if replaced {
			expiresAt := mutation.TokenExpiresAt
			if expiresAt.After(session.ExpiresAt()) {
				expiresAt = session.ExpiresAt()
			}
			credential, err = access.NewCaptureCredential(mutation.ReplacementID, scope.ID(), mutation.VerificationID, mutation.KeyVersion, mutation.At, expiresAt)
			if err != nil || !credential.ExpiresAt().After(mutation.At) {
				return verification.ErrSessionConflict
			}
			if latest != "" {
				if _, err := tx.Exec(ctx, `UPDATE idenqa.capture_tokens SET revoked_at=$3 WHERE tenant_id=$1 AND id=$2 AND revoked_at IS NULL`, scope.ID().String(), latest, mutation.At); err != nil {
					return err
				}
			}
			if err := q.CreateCaptureToken(ctx, sqlgen.CreateCaptureTokenParams{ID: credential.ID().String(), TenantID: scope.ID().String(), VerificationID: mutation.VerificationID.String(), KeyVersion: int32(credential.KeyVersion()), IssuedAt: timestamp(credential.IssuedAt()), ExpiresAt: timestamp(credential.ExpiresAt())}); err != nil {
				return err
			}
			if latest != "" {
				if err := store.preserveResumeProgress(ctx, tx, scope, mutation.VerificationID, latest, credential.ID(), mutation.At); err != nil {
					return err
				}
			}
		}

		lifecycle, err := NewLifecycleStore(store.pool, store.wrapper, resumeClock{at: mutation.At})
		if err != nil {
			return err
		}
		receipt, err := lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{EventID: mutation.EventID, VerificationID: mutation.VerificationID, ExpectedVersion: mutation.ExpectedVersion, Target: verification.SessionStateCollecting, ActorID: mutation.Actor.String(), OccurredAt: mutation.At})
		if err != nil {
			return err
		}
		saved := resumeReplay{TokenID: credential.ID().String(), Replaced: replaced, Version: receipt.Version, OccurredAt: receipt.OccurredAt, DocumentSelections: session.DocumentSelections()}
		encoded, err := json.Marshal(saved)
		if err != nil {
			return err
		}
		stored, err := idempotency.NewResult(200, encoded)
		if err != nil {
			return err
		}
		if err := idempg.Complete(ctx, q, mutation.Idempotency, stored, mutation.At); err != nil {
			return err
		}
		result, err = store.restoreResumeResult(ctx, q, scope, mutation.VerificationID, saved)
		return err
	})
	return result, err
}

func (store *SessionStore) latestCaptureCredential(ctx context.Context, q *sqlgen.Queries, tx pg.Transaction, scope tenant.Scope, verificationID id.Verification, at time.Time) (access.CaptureCredential, string, bool, error) {
	var latest string
	err := tx.QueryRow(ctx, `SELECT id FROM idenqa.capture_tokens WHERE tenant_id=$1 AND verification_id=$2 ORDER BY issued_at DESC,id DESC LIMIT 1`, scope.ID().String(), verificationID.String()).Scan(&latest)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.CaptureCredential{}, "", false, nil
	}
	if err != nil {
		return access.CaptureCredential{}, "", false, err
	}
	row, err := q.FindCaptureToken(ctx, sqlgen.FindCaptureTokenParams{TenantID: scope.ID().String(), ID: latest, VerificationID: verificationID.String()})
	if err != nil {
		return access.CaptureCredential{}, "", false, err
	}
	credential, err := restoreCredential(row)
	if err != nil {
		return access.CaptureCredential{}, "", false, err
	}
	return credential, latest, !row.RevokedAt.Valid && credential.ExpiresAt().After(at), nil
}

func (store *SessionStore) preserveResumeProgress(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verificationID id.Verification, oldToken string, newToken id.CaptureToken, at time.Time) error {
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.capture_recoveries(tenant_id,verification_id,old_token_id,new_token_id,recorded_at) VALUES($1,$2,$3,$4,$5)`, scope.ID().String(), verificationID.String(), oldToken, newToken.String(), at); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO idenqa.capture_recovery_uploads(tenant_id,new_token_id,upload_id,disposition)
		SELECT tenant_id,$3,id,CASE WHEN state='accepted' THEN 'retained' ELSE 'abandoned' END
		FROM idenqa.evidence_upload_intents u WHERE tenant_id=$1 AND verification_id=$2 AND
		((state='accepted' AND (capture_token_id=$4 OR EXISTS(SELECT 1 FROM idenqa.capture_recovery_uploads r WHERE r.tenant_id=u.tenant_id AND r.new_token_id=$4 AND r.upload_id=u.id AND r.disposition='retained')))
		OR (state IN ('issued','uploading') AND capture_token_id=$4))`, scope.ID().String(), verificationID.String(), newToken.String(), oldToken)
	return err
}

func (store *SessionStore) restoreResumeResult(ctx context.Context, q *sqlgen.Queries, scope tenant.Scope, verificationID id.Verification, saved resumeReplay) (verification.ResumeResult, error) {
	tokenID, err := id.ParseCaptureToken(saved.TokenID)
	if err != nil || saved.Version < 2 || saved.OccurredAt.IsZero() {
		return verification.ResumeResult{}, verification.ErrSessionConflict
	}
	row, err := q.FindVerificationSession(ctx, sqlgen.FindVerificationSessionParams{TenantID: scope.ID().String(), ID: verificationID.String()})
	if err != nil {
		return verification.ResumeResult{}, err
	}
	base, err := store.restoreSession(row)
	if err != nil {
		return verification.ResumeResult{}, err
	}
	registry, err := store.catalog.Resolve(base.Requirements().Registry)
	if err != nil {
		return verification.ResumeResult{}, err
	}
	snapshot, err := verification.RestoreSession(base.ID(), scope.ID(), verification.SessionStateCollecting, saved.Version, base.ProfileID(), base.ProfileRevision(), base.ProfileDigest(), base.Requirements(), base.Region(), base.PolicyID(), base.CreatedAt(), saved.OccurredAt.UTC(), base.ExpiresAt(), registry)
	if err != nil {
		return verification.ResumeResult{}, err
	}
	snapshot, err = snapshot.WithDocumentSelections(saved.DocumentSelections)
	if err != nil {
		return verification.ResumeResult{}, err
	}
	token, err := q.FindCaptureToken(ctx, sqlgen.FindCaptureTokenParams{TenantID: scope.ID().String(), ID: tokenID.String(), VerificationID: verificationID.String()})
	if err != nil {
		return verification.ResumeResult{}, err
	}
	credential, err := restoreCredential(token)
	if err != nil {
		return verification.ResumeResult{}, fmt.Errorf("restore resume credential: %w", err)
	}
	return verification.ResumeResult{Session: snapshot, Credential: credential, Replaced: saved.Replaced}, nil
}
