package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// BindNativeApplication atomically consumes the token's one available native
// binding transition and appends a reference-only audit event.
func (store *SessionStore) BindNativeApplication(ctx context.Context, scope tenant.Scope, tokenID id.CaptureToken, applicationID, keyDigest string, boundAt time.Time) error {
	if store == nil || scope.ID().IsZero() || tokenID.IsZero() || applicationID == "" || len(keyDigest) != sha256.Size*2 || boundAt.IsZero() || boundAt.Location() != time.UTC {
		return access.ErrInvalidCaptureToken
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var scopeValue string
		if err := tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&scopeValue); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.capture_tokens
			SET native_application_id=$3,native_proof_key_digest=$4,native_bound_at=$5
			WHERE tenant_id=$1 AND id=$2 AND native_bound_at IS NULL AND revoked_at IS NULL
			  AND issued_at <= $5 AND expires_at > $5`, scope.ID().String(), tokenID.String(), applicationID, keyDigest, boundAt)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.Join(access.ErrInvalidCaptureToken, err)
		}
		eventDigest := sha256.Sum256([]byte(fmt.Sprintf("native.capture.bound\n%s\n%s\n%s\n", tokenID.String(), applicationID, keyDigest)))
		_, err = auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
			EventID:   nativeReference("event", "native.capture.bound:"+tokenID.String()),
			EventType: "native.capture.bound", AggregateID: nativeReference("capture", tokenID.String()),
			ActorID: nativeReference("application", applicationID), EventDigest: hex.EncodeToString(eventDigest[:]), OccurredAt: boundAt,
		})
		return err
	})
}

func nativeReference(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + ":" + hex.EncodeToString(digest[:12])
}

var _ verification.NativeBindingRepository = (*SessionStore)(nil)
