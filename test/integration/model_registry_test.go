//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/model"
	modelpostgres "github.com/Mujhtech/idenqa/internal/model/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestModelRegistryAtomicHistoryIsolationAndRetirement(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := pg.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := pg.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, verificationID := seedExecutionVerification(t, admin, ids, now)
	otherID, _ := seedExecutionVerification(t, admin, ids, now)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := tenant.NewScope(otherID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := poolConfig(database.url)
	cfg.Role = database.createRuntimeRole(t)
	runtime, err := pg.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	keys, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	actor := newIntegrationKey(t, ids, tenantID, now, id.APIKey{})
	if err := keys.Create(ctx, scope, actor); err != nil {
		t.Fatal(err)
	}
	store, err := modelpostgres.NewRegistryStore(runtime, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	hash := "sha256:" + strings.Repeat("a", 64)
	ref := modelv1.ConfigurationReference{ModelID: "mdl_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ConfigurationRef: "configuration://model/test", ConfigurationDigest: hash}
	provenance := modelv1.Provenance{ModelID: ref.ModelID, ModelVersion: "0.1.0", ModelDigest: hash, RuntimeDigest: hash, PreprocessingDigest: hash, OutputSchemaDigest: hash, Contract: modelv1.CurrentVersion}
	registration := model.Registration{Manifest: modelv1.Manifest{Provenance: provenance, Capabilities: []modelv1.Capability{{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}}, Restrictions: modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 1024, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second}}, Configuration: ref, Owner: "fixture", License: "synthetic-only", TrainingProvenance: "synthetic", IntendedUse: "evaluation", ProhibitedUse: "production", Regions: []string{"ng"}, HardwareClass: "cpu", EvaluationOnly: true}
	threshold := model.ThresholdSet{Configuration: ref, Provenance: provenance, ScoreName: "real_score", Minimum: 0, Maximum: 1, Cutoff: 0.5, HigherIsGenuine: true, EvaluationReportDigest: hash, EvaluationOnly: true}
	apply := func(key string, command model.RegistryCommand) (model.RegistryReceipt, error) {
		t.Helper()
		raw, e := json.Marshal(command)
		if e != nil {
			t.Fatal(e)
		}
		request, e := idempotency.NewRequest(tenantID, actor.ID(), "models."+command.Operation, key, raw, now, 24*time.Hour)
		if e != nil {
			t.Fatal(e)
		}
		event, e := ids.NewEvent()
		if e != nil {
			t.Fatal(e)
		}
		return store.Apply(ctx, scope, request, event, command)
	}
	command := model.RegistryCommand{Name: "pad", Operation: "register", Reason: "fixture", Registration: &registration}
	first, err := apply("register", command)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := apply("register", command)
	if err != nil || !replay.Replayed || replay.State.Version != first.State.Version {
		t.Fatal("replay changed registration", err)
	}
	if _, err := store.Get(ctx, other, "pad"); !errors.Is(err, model.ErrRegistryNotFound) {
		t.Fatal("cross tenant visible", err)
	}
	if _, err := apply("stale", command); !errors.Is(err, model.ErrRegistryConflict) {
		t.Fatal("stale mutation accepted", err)
	}
	command = model.RegistryCommand{Name: "pad", Operation: "threshold", Reason: "fixture", ExpectedVersion: 1, Thresholds: &threshold}
	second, err := apply("threshold", command)
	if err != nil {
		t.Fatal(err)
	}
	deployment := model.Deployment{ModelRevision: 1, ThresholdRevision: 1, Region: "ng"}
	command = model.RegistryCommand{Name: "pad", Operation: "rollback", Reason: "fixture", ExpectedVersion: 2, Deployment: &deployment}
	if _, err := apply("never-active", command); !errors.Is(err, model.ErrRegistryConflict) {
		t.Fatal("rollback to never active pair", err)
	}
	command.Operation = "activate"
	if _, err := apply("activate", command); err != nil {
		t.Fatal(err)
	}
	plan, err := model.NewPlan(model.Binding{Registry: &model.RegistrySelection{Name: "pad", ModelRevision: 1, ThresholdRevision: 1, ModelDigest: first.Revision.Digest, ThresholdDigest: second.Revision.Digest}, TenantID: tenantID.String(), PolicyID: "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ProfileDigest: hash, Requirement: "selfie", Region: "ng", Purpose: "idenqa.purpose.identity_verification", Recipient: "tenant.recipient.primary", Configuration: ref}, registration.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	validate := func() error {
		return runtime.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
			if _, err := sqlgen.New(tx).SetTenantScope(ctx, tenantID.String()); err != nil {
				return err
			}
			return modelpostgres.ValidateSelection(ctx, tx, scope, plan)
		})
	}
	if err := validate(); err != nil {
		t.Fatal(err)
	}
	// Saving a registered request must retain threshold pins independently of the wire envelope.
	checkID, err := ids.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	check, err := verification.NewCheck(checkID, tenantID, verificationID, "idenqa.check.passive_pad", now)
	if err != nil {
		t.Fatal(err)
	}
	checkStore, err := verificationpostgres.NewCheckStore(runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	event, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkStore.CreateCheck(ctx, scope, check, event); err != nil {
		t.Fatal(err)
	}
	attempt := newExecutionAttempt(t, ids, 1, 1, now)
	request := modelv1.Request{Contract: modelv1.CurrentVersion, AttemptID: attempt.ID.String(), ModelID: ref.ModelID, TenantID: tenantID.String(), VerificationID: verificationID.String(), Evaluation: "idenqa.check.passive_pad", IdempotencyKey: "registry-fixture", Provenance: provenance, Capability: registration.Manifest.Capabilities[0], Restrictions: registration.Manifest.Restrictions, Configuration: ref, Deadline: attempt.Deadline, Evidence: []modelv1.EvidenceGrantReference{{GrantID: "grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH", RedemptionID: "rdm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", EvidenceID: "evd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Purpose: "idenqa.purpose.identity_verification", Variant: "selfie", ExpiresAt: attempt.Deadline}}}
	digest, err := model.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	attempt.RunnerKind = verification.RunnerModel
	attempt.Provenance.RequestDigest = digest
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	event, err = ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStore.SaveCheck(ctx, scope, verification.CheckCommit{Check: check, ExpectedVersion: 1, EventID: event}); err != nil {
		t.Fatal(err)
	}
	requests, err := modelpostgres.NewRequestStore(runtime, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, tenantID.String()); err != nil {
			return err
		}
		return requests.SaveRegisteredWithin(ctx, tx, check, request, plan)
	}); err != nil {
		t.Fatal(err)
	}
	var recordedVersion int64
	var recordedSelection []byte
	if err := admin.Native().QueryRow(ctx, `SELECT registry_version,registry_selection FROM idenqa.model_requests WHERE tenant_id=$1 AND attempt_id=$2`, tenantID.String(), attempt.ID.String()).Scan(&recordedVersion, &recordedSelection); err != nil {
		t.Fatal(err)
	}
	var recordedPin model.RegistrySelection
	if json.Unmarshal(recordedSelection, &recordedPin) != nil || recordedPin != *plan.Binding.Registry || recordedVersion != 3 {
		t.Fatal("request lost registry selection pins")
	}

	if _, err := apply("retire", model.RegistryCommand{Name: "pad", Operation: "retire", Reason: "fixture", ExpectedVersion: 3}); err != nil {
		t.Fatal(err)
	}
	if err := validate(); !errors.Is(err, model.ErrRegistryConflict) {
		t.Fatal("retirement did not fence new planning", err)
	}
	command.Operation = "rollback"
	command.ExpectedVersion = 4
	if _, err := apply("rollback", command); err != nil {
		t.Fatal(err)
	}
	if err := validate(); err != nil {
		t.Fatal(err)
	}
	history, err := store.History(ctx, scope, "pad", 0, 100)
	if err != nil || len(history) != 5 || history[0].State.Version != 5 {
		t.Fatal("history invalid", err)
	}
	if _, err := store.Revision(ctx, scope, "pad", "model", 1); err != nil {
		t.Fatal(err)
	}
	// An outbox rejection must roll back the state, immutable history and idempotency reservation.
	if err := admin.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `ALTER TABLE idenqa.outbox_events ADD CONSTRAINT reject_model_retire CHECK (event_type <> 'model.retire.v1') NOT VALID`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	retirement := model.RegistryCommand{Name: "pad", Operation: "retire", Reason: "fixture", ExpectedVersion: 5}
	if _, err := apply("atomic-retire", retirement); err == nil {
		t.Fatal("outbox rejection ignored")
	}
	current, err := store.Get(ctx, scope, "pad")
	if err != nil || current.Version != 5 || current.Active == nil {
		t.Fatal("failed command changed state", err)
	}
	if err := admin.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `ALTER TABLE idenqa.outbox_events DROP CONSTRAINT reject_model_retire`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := apply("atomic-retire", retirement); err != nil {
		t.Fatal("failed command left reservation", err)
	}
	// Competing deployments at the same root version must have a single winner.
	outcomes := make(chan error, 2)
	for _, key := range []string{"concurrent-a", "concurrent-b"} {
		go func() {
			_, err := apply(key, model.RegistryCommand{Name: "pad", Operation: "activate", Reason: "fixture", ExpectedVersion: 6, Deployment: &deployment})
			outcomes <- err
		}()
	}
	successes, conflicts := 0, 0
	for range 2 {
		err := <-outcomes
		if err == nil {
			successes++
		} else if errors.Is(err, model.ErrRegistryConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent outcomes: %d successes, %d conflicts", successes, conflicts)
	}
	// RLS must hide rows even when a query omits its tenant predicate.
	if err := runtime.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.model_registries`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("unscoped registry rows visible")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := admin.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `UPDATE idenqa.model_registry_revisions SET digest=digest WHERE tenant_id=$1`, tenantID.String())
		return err
	}); err == nil {
		t.Fatal("immutable registry revision changed")
	}

}

func TestModelRegistryValidationRoundTripIsolationAndReplay(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := pg.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := pg.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, _ := seedExecutionVerification(t, admin, ids, now)
	otherID, _ := seedExecutionVerification(t, admin, ids, now)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := tenant.NewScope(otherID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := poolConfig(database.url)
	cfg.Role = database.createRuntimeRole(t)
	runtime, err := pg.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x64}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	key, presented := newFullScopeIntegrationCredential(t, ids, tenantID, now, peppers)
	if err := accessStore.Create(ctx, scope, key); err != nil {
		t.Fatal(err)
	}
	actor, err := authenticator.Authenticate(ctx, presented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	otherKey, otherPresented := newFullScopeIntegrationCredential(t, ids, otherID, now, peppers)
	if err := accessStore.Create(ctx, other, otherKey); err != nil {
		t.Fatal(err)
	}
	otherActor, err := authenticator.Authenticate(ctx, otherPresented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	store, err := modelpostgres.NewRegistryStore(runtime, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	service, err := model.NewManagement(store, ids, func() time.Time { return now }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	hash := "sha256:" + strings.Repeat("a", 64)
	ref := modelv1.ConfigurationReference{ModelID: "mdl_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ConfigurationRef: "configuration://model/test", ConfigurationDigest: hash}
	provenance := modelv1.Provenance{ModelID: ref.ModelID, ModelVersion: "0.1.0", ModelDigest: hash, RuntimeDigest: hash, PreprocessingDigest: hash, OutputSchemaDigest: hash, Contract: modelv1.CurrentVersion}
	registration := model.Registration{Manifest: modelv1.Manifest{Provenance: provenance, Capabilities: []modelv1.Capability{{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}}, Restrictions: modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 1024, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second}}, Configuration: ref, Owner: "fixture", License: "synthetic-only", TrainingProvenance: "synthetic", IntendedUse: "evaluation", ProhibitedUse: "production", Regions: []string{"ng"}, HardwareClass: "cpu", EvaluationOnly: true}
	threshold := model.ThresholdSet{Configuration: ref, Provenance: provenance, ScoreName: "real_score", Minimum: 0, Maximum: 1, Cutoff: 0.5, HigherIsGenuine: true, EvaluationReportDigest: hash, EvaluationOnly: true}
	second := threshold
	second.Cutoff = 0.6
	execute := func(key string, command model.RegistryCommand) model.RegistryReceipt {
		t.Helper()
		receipt, err := service.Execute(ctx, actor, key, command)
		if err != nil {
			t.Fatalf("execute %s: %v", command.Operation, err)
		}
		return receipt
	}
	validate := func(request model.ValidationRequest) model.ValidationReport {
		t.Helper()
		report, err := service.Validate(ctx, actor, "pad", request)
		if err != nil {
			t.Fatalf("validate %s: %v", request.Operation, err)
		}
		return report
	}
	if report := validate(model.ValidationRequest{Operation: "register", Reason: "evaluation", Registration: &registration}); !report.Accepted || len(report.ReasonCodes) != 0 {
		t.Fatalf("new registration report = %+v", report)
	}
	registered := execute("round-register", model.RegistryCommand{Name: "pad", Operation: "register", Reason: "evaluation", Registration: &registration})
	if registered.State.Version != 1 || registered.Revision == nil || registered.State.ModelRevision != 1 {
		t.Fatalf("registration receipt = %+v", registered)
	}
	replay := execute("round-register", model.RegistryCommand{Name: "pad", Operation: "register", Reason: "evaluation", Registration: &registration})
	if !replay.Replayed || replay.State.Version != 1 {
		t.Fatalf("registration replay = %+v", replay)
	}
	if report := validate(model.ValidationRequest{Operation: "threshold", Reason: "evaluation", ExpectedVersion: 1, Thresholds: &threshold}); !report.Accepted {
		t.Fatalf("threshold report = %+v", report)
	}
	first := execute("round-threshold-1", model.RegistryCommand{Name: "pad", Operation: "threshold", Reason: "evaluation", ExpectedVersion: 1, Thresholds: &threshold})
	if first.State.Version != 2 || first.Revision == nil || first.State.ThresholdRevision != 1 {
		t.Fatalf("threshold receipt = %+v", first)
	}
	if report := validate(model.ValidationRequest{Operation: "threshold", Reason: "evaluation", ExpectedVersion: 2, Thresholds: &second}); !report.Accepted {
		t.Fatalf("second threshold report = %+v", report)
	}
	secondReceipt := execute("round-threshold-2", model.RegistryCommand{Name: "pad", Operation: "threshold", Reason: "evaluation", ExpectedVersion: 2, Thresholds: &second})
	if secondReceipt.State.Version != 3 || secondReceipt.State.ThresholdRevision != 2 {
		t.Fatalf("second threshold receipt = %+v", secondReceipt)
	}
	firstDeployment := model.Deployment{ModelRevision: 1, ThresholdRevision: 1, Region: "ng"}
	if report := validate(model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 3, Deployment: &firstDeployment}); !report.Accepted {
		t.Fatalf("first activation report = %+v", report)
	}
	execute("round-activate-1", model.RegistryCommand{Name: "pad", Operation: "activate", Reason: "evaluation", ExpectedVersion: 3, Deployment: &firstDeployment})
	secondDeployment := model.Deployment{ModelRevision: 1, ThresholdRevision: 2, Region: "ng"}
	if report := validate(model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 4, Deployment: &secondDeployment}); !report.Accepted {
		t.Fatalf("second activation report = %+v", report)
	}
	execute("round-activate-2", model.RegistryCommand{Name: "pad", Operation: "activate", Reason: "evaluation", ExpectedVersion: 4, Deployment: &secondDeployment})
	if report := validate(model.ValidationRequest{Operation: "rollback", Reason: "evaluation", ExpectedVersion: 5, Deployment: &firstDeployment}); !report.Accepted {
		t.Fatalf("rollback report = %+v", report)
	}
	missing := model.Deployment{ModelRevision: 1, ThresholdRevision: 9, Region: "ng"}
	if report := validate(model.ValidationRequest{Operation: "rollback", Reason: "evaluation", ExpectedVersion: 5, Deployment: &missing}); report.Accepted || len(report.ReasonCodes) != 1 || report.ReasonCodes[0] != model.ValidationRevisionNotFound {
		t.Fatalf("missing revision report = %+v", report)
	}
	rollback := execute("round-rollback", model.RegistryCommand{Name: "pad", Operation: "rollback", Reason: "evaluation", ExpectedVersion: 5, Deployment: &firstDeployment})
	if rollback.State.Version != 6 || rollback.State.Active == nil || *rollback.State.Active != firstDeployment {
		t.Fatalf("rollback receipt = %+v", rollback)
	}
	rollbackReplay := execute("round-rollback", model.RegistryCommand{Name: "pad", Operation: "rollback", Reason: "evaluation", ExpectedVersion: 5, Deployment: &firstDeployment})
	if !rollbackReplay.Replayed || rollbackReplay.State.Version != 6 {
		t.Fatalf("rollback replay = %+v", rollbackReplay)
	}
	if report := validate(model.ValidationRequest{Operation: "retire", Reason: "evaluation", ExpectedVersion: 6}); !report.Accepted {
		t.Fatalf("retirement report = %+v", report)
	}
	retired := execute("round-retire", model.RegistryCommand{Name: "pad", Operation: "retire", Reason: "evaluation", ExpectedVersion: 6})
	if retired.State.Version != 7 || retired.State.Active != nil {
		t.Fatalf("retirement receipt = %+v", retired)
	}
	if report := validate(model.ValidationRequest{Operation: "retire", Reason: "evaluation", ExpectedVersion: 7}); report.Accepted || len(report.ReasonCodes) != 1 || report.ReasonCodes[0] != model.ValidationStateConflict {
		t.Fatalf("second retirement report = %+v", report)
	}
	production := registration
	production.EvaluationOnly = false
	if report := validate(model.ValidationRequest{Operation: "register", Reason: "evaluation", ExpectedVersion: 7, Registration: &production}); report.Accepted || len(report.ReasonCodes) != 1 || report.ReasonCodes[0] != model.ValidationRegistrationInvalid {
		t.Fatalf("production report = %+v", report)
	}
	if report := validate(model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 99, Deployment: &firstDeployment}); report.Accepted || len(report.ReasonCodes) != 1 || report.ReasonCodes[0] != model.ValidationVersionConflict {
		t.Fatalf("stale version report = %+v", report)
	}
	history, err := store.History(ctx, scope, "pad", 0, 100)
	if err != nil || len(history) != 7 {
		t.Fatalf("history = %d receipts, err = %v", len(history), err)
	}
	if _, err := store.Revision(ctx, scope, "pad", "model", 1); err != nil {
		t.Fatalf("immutable model revision after retirement: %v", err)
	}
	if _, err := store.Get(ctx, other, "pad"); !errors.Is(err, model.ErrRegistryNotFound) {
		t.Fatalf("cross tenant visible: %v", err)
	}
	if _, err := store.Revision(ctx, other, "pad", "model", 1); !errors.Is(err, model.ErrRegistryNotFound) {
		t.Fatalf("cross tenant revision visible: %v", err)
	}
	if report, err := service.Validate(ctx, otherActor, "pad", model.ValidationRequest{Operation: "activate", Reason: "evaluation", Deployment: &firstDeployment}); err != nil || report.Accepted || len(report.ReasonCodes) != 1 || report.ReasonCodes[0] != model.ValidationRegistryNotFound {
		t.Fatalf("cross tenant validation = %+v, %v", report, err)
	}
	// RLS must hide rows even when a query omits its tenant predicate.
	if err := runtime.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.model_registries`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("unscoped registry rows visible")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
