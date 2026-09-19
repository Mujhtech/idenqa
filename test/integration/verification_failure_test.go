//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/go-chi/chi/v5"
)

// TestVerificationFailureProjection proves the terminal operational-failure
// projection stays decision-free and bounded: policy failure carries the
// writer class and prohibited code, the session read exposes it, replay keeps
// it, and every other terminal lifecycle carries no failure at all.
func TestVerificationFailureProjection(t *testing.T) {
	wantFailure := verification.SessionFailure{Class: "policy", Code: "workflow_prohibited"}

	t.Run("policy routing persists and projects failure", func(t *testing.T) {
		runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
			base := prepareCompletion(t, f)
			previous := base.Snapshot()
			var responseValue string
			if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.subject_responses WHERE tenant_id=$1 AND authority_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 1`, f.scope.ID().String(), f.declaration.ID().String()).Scan(&responseValue); err != nil {
				t.Fatal(err)
			}
			responseID, err := id.ParseAcknowledgement(responseValue)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := policy.NewSnapshot(policy.SnapshotInput{TenantID: f.scope.ID(), VerificationID: previous.VerificationID(), AuthorityID: previous.AuthorityID(), AcknowledgementID: responseID, Region: previous.Region(), Policy: previous.Policy(), Evaluator: previous.Evaluator(), EvaluatedAt: previous.EvaluatedAt(), Facts: previous.Facts()})
			if err != nil {
				t.Fatal(err)
			}
			results := base.Evaluation().Results()
			for index := range results {
				results[index].State = policy.RequirementProhibited
				results[index].Candidate = policy.DirectiveFailWorkflow
			}
			evaluation, err := policy.Resolve(snapshot, results, "")
			if err != nil {
				t.Fatal(err)
			}
			request := policy.AuthorRequest{DecisionID: base.ID(), VerificationID: snapshot.VerificationID(), EvaluatedAt: snapshot.EvaluatedAt(), DecidedAt: base.DecidedAt()}
			routing, err := policy.NewRouting(f.scope, request, snapshot, evaluation)
			if err != nil {
				t.Fatal(err)
			}
			store, err := reviewpostgres.NewRoutingStore(f.runtime, integrationProtector{}, f.ids, fixedIntegrationClock{now: f.now}, nil)
			if err != nil {
				t.Fatal(err)
			}
			actor, err := f.ids.NewTask()
			if err != nil {
				t.Fatal(err)
			}
			route := func() error {
				return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
					return store.RouteWithin(ctx, f.scope, tx, routing, actor)
				})
			}
			if err := route(); err != nil {
				t.Fatal(err)
			}
			assertWorkflowRouting(t, f, "failed")
			assertPersistedFailure(t, f, f.creation.Session.ID(), &wantFailure)

			sessions, err := verificationpostgres.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
			if err != nil {
				t.Fatal(err)
			}
			session, err := sessions.FindSession(t.Context(), f.scope, f.creation.Session.ID())
			if err != nil || session.State() != verification.SessionStateFailed || session.Failure() != wantFailure {
				t.Fatalf("failed session read = %+v, %v", session, err)
			}
			assertPublicFailureProjection(t, f, sessions, session, &wantFailure)
			if err := route(); err != nil {
				t.Fatalf("replay: %v", err)
			}
			assertWorkflowRouting(t, f, "failed")
			assertPersistedFailure(t, f, f.creation.Session.ID(), &wantFailure)

			// A non-failed session can never persist a failure: the column
			// constraint rejects it even for the administrative role, and the
			// session read projects the zero failure.
			otherTenant, otherVerification := seedExecutionVerification(t, f.admin, f.ids, f.now)
			if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_sessions SET failure_class=$3, failure_code=$4 WHERE tenant_id=$1 AND id=$2`, otherTenant.String(), otherVerification.String(), wantFailure.Class, wantFailure.Code); err == nil {
				t.Fatal("non-failed session accepted a persisted failure")
			}
			var otherClass, otherCode *string
			if err := f.admin.Native().QueryRow(t.Context(), `SELECT failure_class, failure_code FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, otherTenant.String(), otherVerification.String()).Scan(&otherClass, &otherCode); err != nil {
				t.Fatal(err)
			}
			if otherClass != nil || otherCode != nil {
				t.Fatalf("collecting session carried failure %v/%v", otherClass, otherCode)
			}
		})
	})

	t.Run("terminal states without operational failure stay null", func(t *testing.T) {
		f := newLifecycleFixture(t)
		cancelled := f.command(t, verification.SessionStateCancelled, 1, f.now.Add(time.Second))
		if _, err := f.store.Apply(t.Context(), f.scope, cancelled); err != nil {
			t.Fatal(err)
		}
		assertNoPersistedFailure(t, f, f.scope.ID(), f.verificationID)

		expiredTenant, expiredID := seedExecutionVerification(t, f.admin, f.ids, f.now)
		expiredScope, err := tenant.NewScope(expiredTenant)
		if err != nil {
			t.Fatal(err)
		}
		expiredStore, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, lifecycleClock{f.now.Add(2 * time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		expired := f.command(t, verification.SessionStateExpired, 1, f.now.Add(time.Hour))
		expired.VerificationID = expiredID
		if _, err := expiredStore.Apply(t.Context(), expiredScope, expired); err != nil {
			t.Fatal(err)
		}
		assertNoPersistedFailure(t, f, expiredTenant, expiredID)

		completedTenant, completedID := seedExecutionVerification(t, f.admin, f.ids, f.now)
		completedScope, err := tenant.NewScope(completedTenant)
		if err != nil {
			t.Fatal(err)
		}
		policyStore, err := policypostgres.New(f.runtime, integrationProtector{})
		if err != nil {
			t.Fatal(err)
		}
		processing := f.command(t, verification.SessionStateProcessing, 1, f.now.Add(time.Second))
		processing.VerificationID = completedID
		if _, err := f.store.Apply(t.Context(), completedScope, processing); err != nil {
			t.Fatal(err)
		}
		decision := newIntegrationDecision(t, f.ids, completedTenant, completedID, id.Decision{}, f.now.Add(30*time.Second))
		if err := policyStore.Append(t.Context(), completedScope, decision); err != nil {
			t.Fatal(err)
		}
		complete := f.command(t, verification.SessionStateCompleted, 2, f.now.Add(time.Minute))
		complete.VerificationID = completedID
		complete.DecisionID = decision.ID()
		if _, err := f.store.Apply(t.Context(), completedScope, complete); err != nil {
			t.Fatal(err)
		}
		assertNoPersistedFailure(t, f, completedTenant, completedID)
	})
}

// TestVerificationFailureBackfill rolls the failure migration back, fails an
// existing session under the previous schema, and re-applies the migration to
// prove legacy failed rows receive a bounded class and code before the
// non-null constraint is added.
func TestVerificationFailureBackfill(t *testing.T) {
	f := newLifecycleFixture(t)
	migrator, err := pg.OpenMigrator(t.Context(), migrationConfig(f.database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := migrator.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}()
	// Roll back every migration applied after the failure migration, then the
	// failure migration itself, so the legacy row is written on the prior schema
	// even when later migrations have been added.
	for attempt := 0; attempt < 10; attempt++ {
		var present bool
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM information_schema.columns
WHERE table_schema='idenqa' AND table_name='verification_sessions' AND column_name='failure_class')`).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		if _, err := migrator.DownOne(t.Context(), pg.RollbackGuard{Environment: "test", Confirmed: true}); err != nil {
			t.Fatalf("roll back failure migration: %v", err)
		}
	}
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_sessions
SET state='failed', version=2, updated_at=updated_at + interval '1 second' WHERE tenant_id=$1 AND id=$2`,
		f.scope.ID().String(), f.verificationID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatalf("re-apply failure migration: %v", err)
	}
	var class, code string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT failure_class, failure_code FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.verificationID.String()).Scan(&class, &code); err != nil {
		t.Fatal(err)
	}
	if class != "legacy" || code != "unspecified" {
		t.Fatalf("backfilled failure = %q/%q, want legacy/unspecified", class, code)
	}
}

// assertPublicFailureProjection drives the real tenant GET route with a real
// access credential and the persisted failed session, proving the transport
// response carries the bounded failure rather than only the aggregate.
func assertPublicFailureProjection(t *testing.T, f captureAcceptanceFixture, sessions *verificationpostgres.SessionStore, session verification.Session, want *verification.SessionFailure) {
	t.Helper()
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x5a}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	key, presented := newIntegrationCredentialKey(t, f.ids, f.scope.ID(), f.now, peppers, access.Pattern("verification_sessions:read"))
	if err := accessStore.Create(t.Context(), f.scope, key); err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	accessMiddleware, err := httpapi.NewAccessMiddleware(authenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{1: bytes.Repeat([]byte{0x6b}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	captureSigner, err := access.NewCaptureTokenSigner(keyring, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	captureAuthenticator, err := verification.NewCaptureAuthenticator(sessions, captureSigner, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	captureMiddleware, err := httpapi.NewCaptureAccessMiddleware(captureAuthenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpapi.NewVerificationRoutes(accessMiddleware, captureMiddleware, failureSessionServiceStub{session: session}, f.catalog, nil, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route(httpapi.VersionPrefix, func(versioned chi.Router) { routes.Register(versioned) })
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/verifications/"+session.ID().String(), nil)
	request.Header.Set("Authorization", "Bearer "+presented.Reveal())
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("public failure read status = %d body=%s", recorder.Code, recorder.Body)
	}
	var response openapiv1.VerificationSession
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Failure == nil || response.Failure.Class != want.Class || response.Failure.Code != want.Code {
		t.Fatalf("public failure projection = %+v", response.Failure)
	}
}

type failureSessionServiceStub struct{ session verification.Session }

func (stub failureSessionServiceStub) Create(context.Context, access.Context, string, verification.SessionCreateInput) (verification.CreatedSession, error) {
	return verification.CreatedSession{}, verification.ErrSessionConflict
}

func (stub failureSessionServiceStub) Find(context.Context, access.Context, id.Verification) (verification.Session, error) {
	return stub.session, nil
}

func (stub failureSessionServiceStub) Resume(context.Context, access.Context, id.Verification, int64, string) (verification.ResumedSession, error) {
	return verification.ResumedSession{}, verification.ErrSessionConflict
}

func assertPersistedFailure(t *testing.T, f captureAcceptanceFixture, verificationID id.Verification, want *verification.SessionFailure) {
	t.Helper()
	var class, code string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT failure_class, failure_code FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), verificationID.String()).Scan(&class, &code); err != nil {
		t.Fatal(err)
	}
	if class != want.Class || code != want.Code {
		t.Fatalf("persisted failure = %q/%q, want %q/%q", class, code, want.Class, want.Code)
	}
}

func assertNoPersistedFailure(t *testing.T, f lifecycleFixture, tenantID id.Tenant, verificationID id.Verification) {
	t.Helper()
	var class, code *string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT failure_class, failure_code FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, tenantID.String(), verificationID.String()).Scan(&class, &code); err != nil {
		t.Fatal(err)
	}
	if class != nil || code != nil {
		t.Fatalf("terminal lifecycle carried failure %v/%v", class, code)
	}
}
