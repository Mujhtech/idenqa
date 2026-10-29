//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
	modelpostgres "github.com/Mujhtech/idenqa/internal/model/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

// dispatchSnapshotBarrier establishes a real transaction snapshot before
// allowing its owning operation to proceed. This makes the stale-snapshot race
// deterministic without timing sleeps or substituting adapter SQL.
type dispatchSnapshotBarrier struct {
	pool         *pg.Pool
	tenant       string
	verification string
	established  chan struct{}
	proceed      chan struct{}
	calls        atomic.Int32
}

func (b *dispatchSnapshotBarrier) WithinTransaction(ctx context.Context, options pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return b.pool.WithinTransaction(ctx, options, func(ctx context.Context, tx pg.Transaction) error {
		if b.calls.Add(1) == 1 {
			var state string
			if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, b.tenant); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx, `SELECT state FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, b.tenant, b.verification).Scan(&state); err != nil {
				return err
			}
			close(b.established)
			select {
			case <-b.proceed:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return work(ctx, tx)
	})
}

func newDispatchSnapshotBarrier(f captureAcceptanceFixture) *dispatchSnapshotBarrier {
	return &dispatchSnapshotBarrier{pool: f.runtime, tenant: f.scope.ID().String(), verification: f.creation.Session.ID().String(), established: make(chan struct{}), proceed: make(chan struct{})}
}

func TestProviderDispatchCancellationSerializesExistingSnapshots(t *testing.T) {
	runDispatchCancellationSnapshots(t, "provider")
}

func TestModelDispatchCancellationSerializesExistingSnapshots(t *testing.T) {
	runDispatchCancellationSnapshots(t, "model")
}

func runDispatchCancellationSnapshots(t *testing.T, kind string) {
	t.Helper()
	for _, winner := range []string{"dispatch", "cancellation"} {
		t.Run(winner, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				fixture := seedProviderRecoveryFixture(t, f)
				attemptID := fixture.attemptID
				var modelRequest modelv1.Request
				if kind == "model" {
					modelRequest = seedCancellationModelDispatch(t, f)
					var err error
					attemptID, err = id.ParseAttempt(modelRequest.AttemptID)
					if err != nil {
						t.Fatal(err)
					}
				}
				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()
				source := fixedIntegrationClock{now: f.now.Add(time.Minute)}
				barrier := newDispatchSnapshotBarrier(f)
				var version int64
				if err := f.admin.Native().QueryRow(ctx, `SELECT version FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&version); err != nil {
					t.Fatal(err)
				}
				mutation := cancelMutation(t, f.scope, f.declaration.Record().CreatedBy, f.creation.Session.ID(), version, "cancel-dispatch-race", source.Now())
				requestsPool := interface {
					WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
				}(f.runtime)
				stopsPool := requestsPool
				if winner == "dispatch" {
					stopsPool = barrier
				} else {
					requestsPool = barrier
				}
				stops, err := verificationpostgres.NewStopStore(stopsPool, integrationProtector{}, f.ids, source)
				if err != nil {
					t.Fatal(err)
				}
				var claim func() (bool, error)
				if kind == "model" {
					requests, err := modelpostgres.NewRequestStore(requestsPool, source)
					if err != nil {
						t.Fatal(err)
					}
					claim = func() (bool, error) { claimed, _, err := requests.Claim(ctx, modelRequest); return claimed, err }
				} else {
					requests, err := providerpostgres.NewRequestStore(requestsPool, source)
					if err != nil {
						t.Fatal(err)
					}
					claim = func() (bool, error) { claimed, _, err := requests.Claim(ctx, fixture.request); return claimed, err }
				}
				completed := make(chan error, 1)
				if winner == "dispatch" {
					go func() {
						_, err := stops.Cancel(ctx, f.scope, mutation)
						completed <- err
					}()
				} else {
					go func() {
						claimed, err := claim()
						if err == nil || claimed {
							completed <- fmt.Errorf("cancelled parent accepted claim (claimed=%v): %w", claimed, err)
							return
						}
						completed <- nil
					}()
				}
				select {
				case <-barrier.established:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
				if winner == "dispatch" {
					claimed, err := claim()
					if err != nil || !claimed {
						t.Fatalf("dispatch before cancellation: claimed=%v err=%v", claimed, err)
					}
				} else if _, err := stops.Cancel(ctx, f.scope, mutation); err != nil {
					t.Fatal(err)
				}
				close(barrier.proceed)
				if err := <-completed; err != nil {
					t.Fatalf("losing operation: %v", err)
				}
				var attemptState string
				var dispatches int
				table := "provider_dispatches"
				if kind == "model" {
					table = "model_dispatches"
				}
				if err := f.admin.Native().QueryRow(ctx, `SELECT state,(SELECT count(*) FROM idenqa.`+table+` WHERE tenant_id=$1 AND attempt_id=$2) FROM idenqa.verification_attempts WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), attemptID.String()).Scan(&attemptState, &dispatches); err != nil {
					t.Fatal(err)
				}
				if winner == "dispatch" {
					if attemptState != string(verification.AttemptRunning) || dispatches != 1 || barrier.calls.Load() < 2 {
						t.Fatalf("committed dispatch lost to old cancellation snapshot: state=%s dispatches=%d cancellation transactions=%d", attemptState, dispatches, barrier.calls.Load())
					}
				} else if attemptState != string(verification.AttemptCancelled) || dispatches != 0 {
					t.Fatalf("cancelled attempt was dispatched: state=%s dispatches=%d", attemptState, dispatches)
				}
			})
		})
	}
}

// Planning is outside this test: seed an immutable, registered synthetic request
// and its history FK, then exercise the production Claim and cancellation paths.
func seedCancellationModelDispatch(t *testing.T, f captureAcceptanceFixture) modelv1.Request {
	t.Helper()
	ctx := t.Context()
	checkID, err := f.ids.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := f.ids.NewAttempt()
	if err != nil {
		t.Fatal(err)
	}
	hash := "sha256:" + strings.Repeat("a", 64)
	configuration := modelv1.ConfigurationReference{ModelID: "mdl_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ConfigurationRef: "configuration://model/cancellation", ConfigurationDigest: hash}
	provenance := modelv1.Provenance{ModelID: configuration.ModelID, ModelVersion: "0.1.0", ModelDigest: hash, RuntimeDigest: hash, PreprocessingDigest: hash, OutputSchemaDigest: hash, Contract: modelv1.CurrentVersion}
	deadline := f.now.Add(20 * time.Minute)
	request := modelv1.Request{
		Contract: modelv1.CurrentVersion, AttemptID: attemptID.String(), ModelID: configuration.ModelID,
		TenantID: f.scope.ID().String(), VerificationID: f.creation.Session.ID().String(),
		Evaluation: "idenqa.check.passive_pad", IdempotencyKey: "model-cancellation-race", Provenance: provenance,
		Capability:    modelv1.Capability{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}},
		Restrictions:  modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 1024, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second},
		Configuration: configuration, Deadline: deadline,
		Evidence: []modelv1.EvidenceGrantReference{{GrantID: "grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH", RedemptionID: "rdm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", EvidenceID: "evd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Purpose: "idenqa.purpose.identity_verification", Variant: "selfie", ExpiresAt: deadline}},
	}
	digest, err := model.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := json.Marshal(model.RegistrySelection{Name: "cancellation", ModelRevision: 1, ThresholdRevision: 1, ModelDigest: hash, ThresholdDigest: hash})
	if err != nil {
		t.Fatal(err)
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.admin.Native().Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO idenqa.verification_checks(id,tenant_id,verification_id,name,state,version,created_at,updated_at) VALUES($1,$2,$3,'idenqa.check.passive_pad','running',1,$4,$4)`, checkID.String(), request.TenantID, request.VerificationID, f.now)
	exec(`INSERT INTO idenqa.verification_attempts(id,tenant_id,verification_id,check_id,attempt_number,fence,runner_kind,runner_id,runner_version,package_digest,contract_major,contract_minor,request_digest,configuration_digest,state,started_at,deadline) VALUES($1,$2,$3,$4,1,1,'model','cancellation','0.1.0',$5,1,0,$6,$5,'running',$7,$8)`, attemptID.String(), request.TenantID, request.VerificationID, checkID.String(), strings.Repeat("a", 64), digest, f.now, deadline)
	exec(`INSERT INTO idenqa.model_registries(tenant_id,name,version,state) VALUES($1,'cancellation',1,'{}')`, request.TenantID)
	exec(`INSERT INTO idenqa.model_registry_history(tenant_id,name,version,actor_key_id,receipt) VALUES($1,'cancellation',1,$2,'{}')`, request.TenantID, f.declaration.Record().CreatedBy.String())
	exec(`INSERT INTO idenqa.model_requests(tenant_id,attempt_id,verification_id,check_id,request_digest,request_body,registry_name,registry_version,registry_selection) VALUES($1,$2,$3,$4,$5,$6,'cancellation',1,$7)`, request.TenantID, request.AttemptID, request.VerificationID, checkID.String(), digest, body, selection)
	return request
}
