//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepg "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpg "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func documentChoiceProfile(profile *verification.Profile) {
	r := profile.Requirements[0]
	r.Key = "document"
	r.EvidenceType = evidence.EvidenceDocumentImage
	r.Artefacts = []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}
	r.DocumentOptions = []verification.DocumentOption{
		{ID: "passport", Label: "Passport", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront}},
		{ID: "driver_license", Label: "Driver licence", Artefacts: []evidence.Name{evidence.ArtefactDocumentFront, evidence.ArtefactDocumentBack}},
	}
	profile.Requirements = []verification.Requirement{r}
}

func documentChoiceMutation(t *testing.T, f captureAcceptanceFixture, choice, key string, version int64) verification.DocumentSelectionMutation {
	t.Helper()
	input := verification.DocumentSelectionInput{RequirementKey: "document", DocumentType: choice, ExpectedVersion: version}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), verification.OperationSelectDocument, key, encoded, f.now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return verification.DocumentSelectionMutation{Input: input, VerificationID: f.creation.Session.ID(), CaptureTokenID: f.creation.Credential.ID(), EventID: mustCaptureEvent(t, f.ids), Retry: retry}
}

func expectDocumentSelectionGuard(t *testing.T, err error, message string) {
	t.Helper()
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "P0001" || databaseError.Message != message {
		t.Fatalf("want database guard %q, got %v", message, err)
	}
}

func TestCaptureDocumentSelectionDatabaseGuards(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		sessions, err := verificationpg.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
		if err != nil {
			t.Fatal(err)
		}
		store, err := verificationpg.NewDocumentSelectionStore(sessions, fixedIntegrationClock{now: f.now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SelectDocument(t.Context(), f.scope, documentChoiceMutation(t, f, "driver_license", "initial", 1)); err != nil {
			t.Fatal(err)
		}
		// Each attempt owns and rolls back its own transaction. The superuser
		// connection bypasses table privileges/RLS, so failures prove the triggers.
		for _, state := range []string{"created", "awaiting_input", "processing", "awaiting_external", "manual_review", "cancelled", "expired", "failed"} {
			t.Run(state, func(t *testing.T) {
				tx, err := f.admin.Native().Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := tx.Rollback(context.WithoutCancel(t.Context())); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
						t.Error(err)
					}
				}()
				if _, err := tx.Exec(t.Context(), `UPDATE idenqa.verification_sessions SET state=$2,version=version+1,failure_class=CASE WHEN $2='failed' THEN 'infrastructure' END,failure_code=CASE WHEN $2='failed' THEN 'unavailable' END WHERE id=$1`, f.creation.Session.ID().String(), state); err != nil {
					t.Fatal(err)
				}
				// Do not touch version/time: the older lifecycle trigger would
				// otherwise mask the missing document-selection protection.
				_, err = tx.Exec(t.Context(), `UPDATE idenqa.verification_sessions SET document_selections='{"document":"passport"}' WHERE id=$1`, f.creation.Session.ID().String())
				expectDocumentSelectionGuard(t, err, "document selection requires an incomplete collecting session")
			})
		}
		for _, assignment := range []string{`'{"document":"passport"}'::jsonb`, `document_selections`} {
			t.Run("completed_"+assignment, func(t *testing.T) {
				tx, err := f.admin.Native().Begin(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := tx.Rollback(context.WithoutCancel(t.Context())); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
						t.Error(err)
					}
				}()
				if _, err := tx.Exec(t.Context(), `UPDATE idenqa.verification_sessions SET capture_completed_at=$2 WHERE id=$1`, f.creation.Session.ID().String(), f.now); err != nil {
					t.Fatal(err)
				}
				// The two SQL fragments are fixed test cases, never external input.
				_, err = tx.Exec(t.Context(), `UPDATE idenqa.verification_sessions SET document_selections=`+assignment+`,version=version+1 WHERE id=$1`, f.creation.Session.ID().String())
				expectDocumentSelectionGuard(t, err, "document selection requires an incomplete collecting session")
			})
		}
		for _, query := range []string{
			`UPDATE idenqa.capture_document_selection_audit SET document_type='passport' WHERE verification_id=$1`,
			`DELETE FROM idenqa.capture_document_selection_audit WHERE verification_id=$1`,
		} {
			_, err := f.admin.Native().Exec(t.Context(), query, f.creation.Session.ID().String())
			expectDocumentSelectionGuard(t, err, "audit history is append-only")
		}
		var count int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.capture_document_selection_audit WHERE verification_id=$1 AND document_type='driver_license'`, f.creation.Session.ID().String()).Scan(&count); err != nil || count != 1 {
			t.Fatal("audit history changed", count, err)
		}
	}, "local", "tenant.region.ng", documentChoiceProfile)
}

func TestCaptureDocumentSelectionSerializesWithUpload(t *testing.T) {
	for _, selectionFirst := range []bool{true, false} {
		name := "upload_first"
		if selectionFirst {
			name = "selection_first"
		}
		t.Run(name, func(t *testing.T) {
			runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
				sessions, err := verificationpg.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
				if err != nil {
					t.Fatal(err)
				}
				selectionStore, err := verificationpg.NewDocumentSelectionStore(sessions, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := selectionStore.SelectDocument(t.Context(), f.scope, documentChoiceMutation(t, f, "driver_license", "initial", 1)); err != nil {
					t.Fatal(err)
				}
				mutation := documentChoiceMutation(t, f, "passport", "switch", 2)
				upload := documentBackUpload(t, f)
				retry, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), evidence.OperationCreateUpload, "back-upload", []byte(`{}`), f.now, time.Hour)
				if err != nil {
					t.Fatal(err)
				}
				barrier := &documentWriteBarrier{Pool: f.runtime, reached: make(chan int32, 1), release: make(chan struct{}), query: "-- name: CreateEvidenceUploadIntent"}
				var release sync.Once
				unblock := func() { release.Do(func() { close(barrier.release) }) }
				defer unblock()
				var uploadPool interface {
					WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
				} = barrier
				if selectionFirst {
					barrier.query = "-- name: InsertOutboxEvent"
					blockedSessions, err := verificationpg.NewSessionStore(barrier, integrationProtector{}, f.catalog)
					if err != nil {
						t.Fatal(err)
					}
					selectionStore, err = verificationpg.NewDocumentSelectionStore(blockedSessions, fixedIntegrationClock{now: f.now})
					if err != nil {
						t.Fatal(err)
					}
					uploadPool = f.runtime
				}
				uploadStore, err := evidencepg.NewWithClock(uploadPool, integrationProtector{}, f.catalog, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				selectChoice := func() error { _, err := selectionStore.SelectDocument(t.Context(), f.scope, mutation); return err }
				createUpload := func() error {
					_, err := uploadStore.CreateUpload(t.Context(), f.scope, evidence.UploadCreateMutation{Upload: upload, Idempotency: retry})
					return err
				}
				firstWork, secondWork := createUpload, selectChoice
				wantSecond := verification.ErrSessionConflict
				if selectionFirst {
					firstWork, secondWork = selectChoice, createUpload
					wantSecond = evidence.ErrUploadConflict
				}
				first, second := make(chan error, 1), make(chan error, 1)
				go func() { first <- firstWork() }()
				var pid int32
				select {
				case pid = <-barrier.reached:
				case err := <-first:
					t.Fatal("first transaction failed before lock barrier", err)
				case <-time.After(10 * time.Second):
					t.Fatal("first transaction never reached lock barrier")
				}
				go func() { second <- secondWork() }()
				waitForCaptureBlock(t, f.admin, pid, second)
				unblock()
				if err := <-first; err != nil {
					t.Fatal("winning transaction", err)
				}
				if err := <-second; !errors.Is(err, wantSecond) {
					t.Fatal("losing transaction", err)
				}
				var choice string
				var version int64
				var intents, audits int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT document_selections->>'document',version,(SELECT count(*) FROM idenqa.evidence_upload_intents WHERE verification_id=s.id),(SELECT count(*) FROM idenqa.capture_document_selection_audit WHERE verification_id=s.id) FROM idenqa.verification_sessions s WHERE id=$1`, f.creation.Session.ID().String()).Scan(&choice, &version, &intents, &audits); err != nil {
					t.Fatal(err)
				}
				if selectionFirst {
					if choice != "passport" || version != 3 || intents != 0 || audits != 2 {
						t.Fatal("selection winner was not atomic", choice, version, intents, audits)
					}
				} else if choice != "driver_license" || version != 2 || intents != 1 || audits != 1 {
					t.Fatal("upload winner was not atomic", choice, version, intents, audits)
				}
				if !selectionFirst {
					claimed, err := uploadStore.ClaimUploadAttempt(t.Context(), f.scope, f.creation.Credential.ID(), upload.ID(), upload.Version(), f.now)
					if err != nil {
						t.Fatal(err)
					}
					failed, err := uploadStore.FailUploadAttempt(t.Context(), f.scope, f.creation.Credential.ID(), upload.ID(), claimed.Version(), claimed.Attempt(), f.now)
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_sessions SET document_selections='{"document":"passport"}',version=version+1 WHERE id=$1`, f.creation.Session.ID().String())
					expectDocumentSelectionGuard(t, err, "document selection is locked by upload history")
					claimed, err = uploadStore.ClaimUploadAttempt(t.Context(), f.scope, f.creation.Credential.ID(), upload.ID(), failed.Version(), f.now)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := uploadStore.RejectUploadAttempt(t.Context(), f.scope, f.creation.Credential.ID(), upload.ID(), claimed.Version(), claimed.Attempt(), "invalid_media_type", f.now); err != nil {
						t.Fatal(err)
					}
					_, err = f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_sessions SET document_selections='{}',version=version+1 WHERE id=$1`, f.creation.Session.ID().String())
					expectDocumentSelectionGuard(t, err, "document selection is locked by upload history")
				}
			}, "local", "tenant.region.ng", documentChoiceProfile)
		})
	}
}

func documentBackUpload(t *testing.T, f captureAcceptanceFixture) evidence.Upload {
	t.Helper()
	snapshot, err := f.authorities.CaptureSnapshot(t.Context(), f.scope, f.creation.Session.ID())
	if err != nil || snapshot.Response == nil {
		t.Fatal("authority", err)
	}
	uploadID, err := f.ids.NewUpload()
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, err := f.ids.NewEvidence()
	if err != nil {
		t.Fatal(err)
	}
	r := f.creation.Session.Requirements().Requirements[0]
	upload, err := evidence.NewUpload(evidence.UploadInput{
		ID: uploadID, TenantID: f.scope.ID(), CaptureTokenID: f.creation.Credential.ID(), SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), EvidenceID: evidenceID,
		AuthorityID: f.declaration.ID(), ResponseID: snapshot.Response.Record().ID, ProfileID: f.creation.Session.ProfileID(), ProfileRevision: f.creation.Session.ProfileRevision(), ProfileDigest: f.creation.Session.ProfileDigest(),
		RequirementKey: r.Key, Purpose: r.Purpose, EvidenceType: r.EvidenceType, Artefact: evidence.ArtefactDocumentBack, AcquisitionMethod: evidence.MethodLiveCamera,
		Assurances: []evidence.Name{evidence.AssuranceLiveCapture, evidence.AssuranceFreshness}, AllowedMediaTypes: []string{evidence.MediaTypeJPEG}, MaximumBytes: evidence.DefaultUploadMaximumBytes, ExpectedBytes: 8, ExpectedDigest: "sha256:" + strings.Repeat("a", 64), MediaType: evidence.MediaTypeJPEG,
		Region: f.declaration.Record().Regions[0], RetentionClass: "tenant.retention.identity_v1", CreatedAt: f.now, SessionExpiresAt: f.creation.Session.ExpiresAt(),
	}, f.registry, evidence.DefaultUploadPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return upload
}

type documentWriteBarrier struct {
	*pg.Pool
	query   string
	reached chan int32
	release chan struct{}
}

func (barrier *documentWriteBarrier) WithinTransaction(ctx context.Context, options pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return barrier.Pool.WithinTransaction(ctx, options, func(ctx context.Context, tx pg.Transaction) error {
		return work(ctx, documentBarrierTransaction{Transaction: tx, barrier: barrier})
	})
}

type documentBarrierTransaction struct {
	pg.Transaction
	barrier *documentWriteBarrier
}

func (tx documentBarrierTransaction) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	if strings.Contains(query, tx.barrier.query) {
		var pid int32
		if err := tx.Transaction.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return pgconn.CommandTag{}, err
		}
		tx.barrier.reached <- pid
		select {
		case <-tx.barrier.release:
		case <-ctx.Done():
			return pgconn.CommandTag{}, ctx.Err()
		}
	}
	return tx.Transaction.Exec(ctx, query, args...)
}
