//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	tinkcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto/tink"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	objectlocal "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/jackc/pgx/v5"
)

type captureAcceptanceFixture struct {
	admin, runtime *pg.Pool
	scope          tenant.Scope
	authorities    *authoritypostgres.Store
	creation       verification.SessionCreation
	registry       evidence.Registry
	catalog        evidence.Catalog
	ids            *id.Generator
	declaration    authority.Authority
	now            time.Time
}

func TestCaptureAcceptanceObservesExpiryAfterTokenLock(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		_, mutations := f.prepare(t)
		var source captureClock
		source.microseconds.Store(f.now.UnixMicro())
		store, err := evidencepostgres.NewWithClock(f.runtime, integrationProtector{}, f.catalog, &source)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := f.admin.Native().Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tx.Rollback(context.WithoutCancel(t.Context())); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error(err)
			}
		}()
		var blocker int32
		if err := tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM idenqa.capture_tokens WHERE id=$1 FOR UPDATE`, f.creation.Credential.ID().String()).Scan(&blocker); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := store.AcceptUpload(t.Context(), f.scope, mutations[0]); result <- err }()
		waitForCaptureBlock(t, f.admin, blocker, result)
		source.microseconds.Store(f.now.Add(10 * time.Minute).UnixMicro())
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, authority.ErrProcessingNotPermitted) {
			t.Fatalf("acceptance after token-lock wait: %v", err)
		}
		if _, err := store.Find(t.Context(), f.scope, mutations[0].Asset.ID()); !errors.Is(err, evidence.ErrNotFound) {
			t.Fatalf("expired evidence became available: %v", err)
		}
	})
}

type captureClock struct{ microseconds atomic.Int64 }

func (source *captureClock) Now() time.Time { return time.UnixMicro(source.microseconds.Load()).UTC() }

func waitForCaptureBlock(t *testing.T, pool *pg.Pool, blocker int32, result <-chan error) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		var blocked bool
		if err := pool.Native().QueryRow(t.Context(),
			`SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE $1 = ANY(pg_blocking_pids(pid)))`, blocker,
		).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case err := <-result:
			t.Fatalf("acceptance bypassed the lock: %v", err)
		case <-deadline.C:
			t.Fatal("acceptance never waited on lock")
		case <-tick.C:
		}
	}
}

func TestCaptureAcceptanceSerializesFinalSteps(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		store, mutations := f.prepare(t)
		barrier := &capturePublicationBarrier{Pool: f.runtime, reached: make(chan int32, 1), release: make(chan struct{})}
		blockedStore, err := evidencepostgres.NewWithClock(barrier, integrationProtector{}, f.catalog, fixedIntegrationClock{now: f.now})
		if err != nil {
			t.Fatal(err)
		}
		var release sync.Once
		unblock := func() { release.Do(func() { close(barrier.release) }) }
		defer unblock()
		first := make(chan error, 1)
		go func() { _, err := blockedStore.AcceptUpload(t.Context(), f.scope, mutations[0]); first <- err }()
		var blocker int32
		select {
		case blocker = <-barrier.reached:
		case err := <-first:
			t.Fatalf("first upload did not reach progress: %v", err)
		case <-time.After(10 * time.Second):
			t.Fatal("first upload did not reach progress")
		}
		second := make(chan error, 1)
		go func() { _, err := store.AcceptUpload(t.Context(), f.scope, mutations[1]); second <- err }()
		// Observe the actual database lock wait, rather than assuming a sleep
		// placed both uploads in the intended overlap.
		waitForCaptureBlock(t, f.admin, blocker, second)
		unblock()
		if err := <-first; err != nil {
			t.Fatal(err)
		}
		if err := <-second; err != nil {
			t.Fatal(err)
		}
		var complete bool
		var accepted, fullProgress int
		if err := f.admin.Native().QueryRow(t.Context(),
			`SELECT capture_completed_at IS NOT NULL FROM idenqa.verification_sessions WHERE id=$1`, f.creation.Session.ID().String(),
		).Scan(&complete); err != nil {
			t.Fatal(err)
		}
		if err := f.admin.Native().QueryRow(t.Context(),
			`SELECT count(*) FROM idenqa.evidence_upload_intents WHERE verification_id=$1 AND state='accepted'`, f.creation.Session.ID().String(),
		).Scan(&accepted); err != nil {
			t.Fatal(err)
		}
		if err := f.admin.Native().QueryRow(t.Context(),
			`SELECT count(*) FROM idenqa.realtime_events WHERE verification_id=$1 AND payload->>'completed_steps'='2'`, f.creation.Session.ID().String(),
		).Scan(&fullProgress); err != nil {
			t.Fatal(err)
		}
		if !complete || accepted != 2 || fullProgress != 1 {
			t.Fatalf("completion=%t, accepted=%d, complete progress events=%d", complete, accepted, fullProgress)
		}
	})
}

func TestCaptureAcceptanceRechecksAuthorityAndExpiry(t *testing.T) {
	for _, scenario := range []string{"withdrawal", "refusal", "token_revocation", "elapsed_lease", "cancelled_session"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				store, mutations := f.prepare(t)
				switch scenario {
				case "withdrawal":
					declaration := f.declaration
					if err := declaration.Withdraw(f.now); err != nil {
						t.Fatal(err)
					}
					request := integrationIdempotencyRequest(t, f.scope.ID(), declaration.Record().CreatedBy,
						"authorities.withdraw", "capture-withdraw", []byte(`{}`), f.now)
					_, err := f.authorities.Transition(t.Context(), f.scope, authority.TransitionMutation{
						Authority: declaration, ExpectedVersion: f.declaration.Record().Version, Action: authority.StateWithdrawn,
						Actor: declaration.Record().CreatedBy, EventID: mustCaptureEvent(t, f.ids), Idempotency: request,
					})
					if err != nil {
						t.Fatal(err)
					}
				case "refusal":
					responseID, err := f.ids.NewAcknowledgement()
					if err != nil {
						t.Fatal(err)
					}
					response, err := authority.NewResponse(authority.ResponseRecord{
						ID: responseID, TenantID: f.scope.ID(), AuthorityID: f.declaration.ID(), NoticeID: f.declaration.Record().NoticeID,
						SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), CaptureTokenID: f.creation.Credential.ID(),
						Action: authority.ResponseRefuse, Locale: "en-NG", RecordedAt: f.now,
					})
					if err != nil {
						t.Fatal(err)
					}
					request, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), "authorities.respond", "capture-refuse", []byte(`{}`), f.now, time.Hour)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.authorities.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{
						Response: response, EventID: mustCaptureEvent(t, f.ids), Idempotency: request,
					}); err != nil {
						t.Fatal(err)
					}
				case "token_revocation":
					if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.capture_tokens SET revoked_at=$1 WHERE id=$2`, f.now, f.creation.Credential.ID().String()); err != nil {
						t.Fatal(err)
					}
				case "elapsed_lease":
					var err error
					store, err = evidencepostgres.NewWithClock(f.runtime, integrationProtector{}, f.catalog, fixedIntegrationClock{now: f.now.Add(10 * time.Minute)})
					if err != nil {
						t.Fatal(err)
					}
				case "cancelled_session":
					lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{
						EventID: mustCaptureEvent(t, f.ids), VerificationID: f.creation.Session.ID(), ExpectedVersion: 1,
						Target: verification.SessionStateCancelled, ActorID: f.declaration.Record().CreatedBy.String(), OccurredAt: f.now,
					}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.AcceptUpload(t.Context(), f.scope, mutations[0]); !errors.Is(err, authority.ErrProcessingNotPermitted) {
					t.Fatalf("accept after %s = %v", scenario, err)
				}
				if _, err := store.Find(t.Context(), f.scope, mutations[0].Asset.ID()); !errors.Is(err, evidence.ErrNotFound) {
					t.Fatalf("denied evidence became available: %v", err)
				}
				upload, err := store.FindUpload(t.Context(), f.scope, mutations[0].UploadID)
				if err != nil || upload.State() != evidence.UploadStateUploading {
					t.Fatalf("denial changed upload: %s, %v", upload.State(), err)
				}
			})
		})
	}
}

func (f captureAcceptanceFixture) prepare(t *testing.T) (*evidencepostgres.Store, []evidence.UploadAcceptance) {
	t.Helper()
	store, err := evidencepostgres.NewWithClock(f.runtime, integrationProtector{}, f.catalog, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := localkms.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := keyring.Close(); err != nil {
			t.Error(err)
		}
	})
	purpose, err := kms.NewPurpose("idenqa.evidence.content")
	if err != nil {
		t.Fatal(err)
	}
	streaming, err := tinkcrypto.NewStreaming(keyring, keyring, purpose)
	if err != nil {
		t.Fatal(err)
	}
	objects, err := objectlocal.Open(objectlocal.Config{Directory: t.TempDir(), MaxObjectBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := objects.Close(); err != nil {
			t.Error(err)
		}
	})
	protector, err := evidence.NewProtector(streaming, objects, store, f.registry, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := f.authorities.CaptureSnapshot(t.Context(), f.scope, f.creation.Session.ID())
	if err != nil || snapshot.Response == nil {
		t.Fatalf("authority snapshot: %v", err)
	}
	var mutations []evidence.UploadAcceptance
	body := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4}
	for _, requirement := range f.creation.Session.Requirements().Requirements {
		uploadID, err := f.ids.NewUpload()
		if err != nil {
			t.Fatal(err)
		}
		evidenceID, err := f.ids.NewEvidence()
		if err != nil {
			t.Fatal(err)
		}
		upload, err := evidence.NewUpload(evidence.UploadInput{
			ID: uploadID, TenantID: f.scope.ID(), CaptureTokenID: f.creation.Credential.ID(),
			SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), EvidenceID: evidenceID,
			AuthorityID: f.declaration.ID(), ResponseID: snapshot.Response.Record().ID,
			ProfileID: f.creation.Session.ProfileID(), ProfileRevision: f.creation.Session.ProfileRevision(), ProfileDigest: f.creation.Session.ProfileDigest(),
			RequirementKey: requirement.Key, Purpose: requirement.Purpose, EvidenceType: requirement.EvidenceType,
			Artefact: requirement.Artefacts[0], AcquisitionMethod: evidence.MethodLiveCamera,
			Assurances:        []evidence.Name{evidence.AssuranceLiveCapture, evidence.AssuranceFreshness},
			AllowedMediaTypes: []string{evidence.MediaTypeJPEG}, MaximumBytes: evidence.DefaultUploadMaximumBytes,
			ExpectedBytes: int64(len(body)), ExpectedDigest: string(platformcrypto.Sum(body)), MediaType: evidence.MediaTypeJPEG,
			Region: f.declaration.Record().Regions[0], RetentionClass: "tenant.retention.identity_v1", CreatedAt: f.now, SessionExpiresAt: f.creation.Session.ExpiresAt(),
		}, f.registry, evidence.DefaultUploadPolicy())
		if err != nil {
			t.Fatal(err)
		}
		request, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), evidence.OperationCreateUpload, requirement.Key, []byte(`{}`), f.now, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateUpload(t.Context(), f.scope, evidence.UploadCreateMutation{Upload: upload, Idempotency: request}); err != nil {
			t.Fatal(err)
		}
		claimed, err := store.ClaimUploadAttempt(t.Context(), f.scope, f.creation.Credential.ID(), uploadID, 1, f.now)
		if err != nil {
			t.Fatal(err)
		}
		record := claimed.Record()
		prepared, err := protector.Prepare(t.Context(), f.scope, evidence.ProtectionInput{
			ID: evidenceID, SubjectID: record.SubjectID, VerificationID: record.VerificationID,
			RequirementKey: record.RequirementKey, EvidenceType: record.EvidenceType, Artefact: record.Artefact,
			AcquisitionMethod: record.AcquisitionMethod, Assurances: record.Assurances, Region: record.Region,
			RetentionClass: record.RetentionClass, ContentRevision: 1, MediaType: record.MediaType,
			Plaintext: bytes.NewReader(body), CreatedAt: f.now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.CreateObjectReconciliation(t.Context(), f.scope, claimed, prepared, f.now); err != nil {
			t.Fatal(err)
		}
		mutations = append(mutations, evidence.UploadAcceptance{
			UploadID: uploadID, CaptureTokenID: record.CaptureTokenID, ExpectedVersion: claimed.Version(), Attempt: claimed.Attempt(),
			Asset: prepared.Asset(), PlaintextBytes: int64(len(body)), EventID: mustCaptureEvent(t, f.ids), OccurredAt: f.now,
		})
	}
	return store, mutations
}

func mustCaptureEvent(t *testing.T, ids *id.Generator) id.Event {
	t.Helper()
	value, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type capturePublicationBarrier struct {
	*pg.Pool
	reached chan int32
	release chan struct{}
}

func (b *capturePublicationBarrier) WithinTransaction(ctx context.Context, options pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return b.Pool.WithinTransaction(ctx, options, func(ctx context.Context, tx pg.Transaction) error {
		return work(ctx, captureBarrierTransaction{Transaction: tx, barrier: b})
	})
}

type captureBarrierTransaction struct {
	pg.Transaction
	barrier *capturePublicationBarrier
}

func (tx captureBarrierTransaction) QueryRow(ctx context.Context, sql string, args ...any) pgx.Row {
	if strings.Contains(sql, "-- name: LoadCaptureProgressPublication") {
		var pid int32
		if err := tx.Transaction.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
			return captureErrorRow{err}
		}
		tx.barrier.reached <- pid
		select {
		case <-tx.barrier.release:
		case <-ctx.Done():
			return captureErrorRow{fmt.Errorf("capture barrier: %w", ctx.Err())}
		}
	}
	return tx.Transaction.QueryRow(ctx, sql, args...)
}

type captureErrorRow struct{ err error }

func (r captureErrorRow) Scan(...any) error { return r.err }
