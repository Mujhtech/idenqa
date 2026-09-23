package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// DocumentSelectionStore uses the same session restoration and live clock as capture.
type DocumentSelectionStore struct {
	sessions *SessionStore
	clock    clock.Clock
}

// NewDocumentSelectionStore constructs the capture document command adapter.
func NewDocumentSelectionStore(sessions *SessionStore, source clock.Clock) (*DocumentSelectionStore, error) {
	if sessions == nil || source == nil {
		return nil, errors.New("document selection store dependencies are required")
	}
	return &DocumentSelectionStore{sessions: sessions, clock: source}, nil
}

type documentSelectionReplay struct {
	Selections map[string]string `json:"document_selections"`
	Version    int64             `json:"version"`
	UpdatedAt  time.Time         `json:"updated_at"`
}

// SelectDocument serializes choices with uploads, reauthenticates the token,
// and commits selection, audit, outbox and the exact replay response together.
func (store *DocumentSelectionStore) SelectDocument(ctx context.Context, scope tenant.Scope, mutation verification.DocumentSelectionMutation) (verification.Session, error) {
	var result verification.Session
	if scope.ID().IsZero() || mutation.VerificationID.IsZero() || mutation.CaptureTokenID.IsZero() || mutation.EventID.IsZero() || mutation.Retry.TenantID() != scope.ID() || mutation.Retry.Principal().String() != mutation.CaptureTokenID.String() || mutation.Retry.Operation() != verification.OperationSelectDocument {
		return result, verification.ErrSessionConflict
	}
	err := store.sessions.write(ctx, scope, func(ctx context.Context, q *sqlgen.Queries, tx pg.Transaction) error {
		row, err := q.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{TenantID: scope.ID().String(), ID: mutation.VerificationID.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("lock document selection session: %w", err)
		}
		token, err := q.LockCaptureTokenForUpload(ctx, sqlgen.LockCaptureTokenForUploadParams{TenantID: scope.ID().String(), ID: mutation.CaptureTokenID.String(), VerificationID: mutation.VerificationID.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrInvalidCaptureToken
		}
		if err != nil {
			return fmt.Errorf("lock document selection credential: %w", err)
		}
		now := store.clock.Now().UTC().Truncate(time.Microsecond)
		if token.RevokedAt.Valid || !token.ExpiresAt.Valid || !now.Before(token.ExpiresAt.Time) || now.Before(token.IssuedAt.Time) {
			return access.ErrInvalidCaptureToken
		}
		reserved, err := idempg.Reserve(ctx, q, mutation.Retry)
		if err != nil {
			return err
		}
		if replay, ok := reserved.Result(); ok {
			var saved documentSelectionReplay
			if err := json.Unmarshal(replay.Body(), &saved); err != nil {
				return fmt.Errorf("decode document selection replay: %w", err)
			}
			row.Version, row.UpdatedAt, row.State = saved.Version, timestamp(saved.UpdatedAt), string(verification.SessionStateCollecting)
			row.FailureClass, row.FailureCode = nil, nil
			row.CaptureCompletedAt = optionalTimestamp(nil)
			row.DocumentSelections, err = json.Marshal(saved.Selections)
			if err != nil {
				return err
			}
			result, err = store.sessions.restoreSession(row)
			return err
		}
		session, err := store.sessions.restoreSession(row)
		if err != nil {
			return err
		}
		var hasIntent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.evidence_upload_intents WHERE tenant_id=$1 AND verification_id=$2 AND requirement_key=$3)`, scope.ID().String(), mutation.VerificationID.String(), mutation.Input.RequirementKey).Scan(&hasIntent); err != nil {
			return fmt.Errorf("check document upload history: %w", err)
		}
		result, err = session.SelectDocument(mutation.Input.RequirementKey, mutation.Input.DocumentType, mutation.Input.ExpectedVersion, hasIntent, now)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(result.DocumentSelections())
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.verification_sessions SET document_selections=$3, version=$4, updated_at=$5 WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), result.ID().String(), encoded, result.Version(), now); err != nil {
			return fmt.Errorf("persist document selection: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.capture_document_selection_audit(tenant_id,verification_id,aggregate_version,capture_token_id,requirement_key,document_type,occurred_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), result.ID().String(), result.Version(), mutation.CaptureTokenID.String(), mutation.Input.RequirementKey, mutation.Input.DocumentType, now); err != nil {
			return fmt.Errorf("audit document selection: %w", err)
		}
		payload, err := json.Marshal(struct {
			VerificationID string `json:"verification_id"`
			RequirementKey string `json:"requirement_key"`
			DocumentType   string `json:"document_type"`
		}{result.ID().String(), mutation.Input.RequirementKey, mutation.Input.DocumentType})
		if err != nil {
			return err
		}
		if err := q.InsertOutboxEvent(ctx, sqlgen.InsertOutboxEventParams{ID: mutation.EventID.String(), TenantID: scope.ID().String(), AggregateType: verificationAggregateType, AggregateID: result.ID().String(), AggregateVersion: result.Version(), EventType: "verification.document_selected.v1", SchemaVersion: 1, Payload: payload, OccurredAt: timestamp(now), CreatedAt: timestamp(now)}); err != nil {
			return fmt.Errorf("publish document selection intent: %w", err)
		}
		body, err := json.Marshal(documentSelectionReplay{Selections: result.DocumentSelections(), Version: result.Version(), UpdatedAt: now})
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(200, body)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, mutation.Retry, replay, now)
	})
	return result, err
}
