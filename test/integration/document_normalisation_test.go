//go:build integration

package integration_test

import (
	"strings"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/document/fields"
	"github.com/Mujhtech/idenqa/internal/document/mrz"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestDocumentNormalisationPersistsBoundedSignals(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	headgateMigrator, err := taskheadgate.OpenMigrator(ctx, database.url, "headgate", 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := headgateMigrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := headgateMigrator.Close(ctx); err != nil {
		t.Fatal(err)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	tenantID, verificationID := seedExecutionVerification(t, adminPool, generator, now)

	runtimeConfig := poolConfig(database.url)
	runtimeRole := database.createRuntimeRole(t)
	database.grantHeadgateRuntime(t, runtimeRole)
	runtimeConfig.Role = runtimeRole
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	store, err := verificationpostgres.NewCheckStore(runtimePool, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}

	checkID, err := generator.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	check, err := verification.NewCheck(checkID, tenantID, verificationID, "document.consistency", now)
	if err != nil {
		t.Fatal(err)
	}
	createEvent, err := generator.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.CreateCheck(ctx, scope, check, createEvent); err != nil {
		t.Fatal(err)
	}
	attempt := newExecutionAttempt(t, generator, 1, 7, now.Add(time.Second))
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	startEvent, err := generator.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := store.SaveCheck(ctx, scope, verification.CheckCommit{Check: check, ExpectedVersion: 1, EventID: startEvent}); err != nil || duplicate {
		t.Fatalf("start attempt = %t, %v", duplicate, err)
	}

	completedAt := now.Add(2 * time.Second)
	front := integrationSide(t, fields.Input{
		Side:     document.SideFront,
		MRZLines: integrationPassport(t, "X10000001"),
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "X10000001"},
			{Name: "first_name", Value: "John"},
			{Name: "last_name", Value: "Doe"},
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
		},
		PlanSides: []document.Side{document.SideFront, document.SideBack},
		Reference: completedAt,
	})
	back := integrationSide(t, fields.Input{
		Side: document.SideBack,
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "X20000002"},
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
		},
		Reference: completedAt,
	})
	result := providerv1.Result{
		Contract:    providerv1.CurrentVersion,
		AttemptID:   attempt.ID.String(),
		Outcome:     providerv1.ResultOutcomeCompleted,
		Signals:     []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}},
		CompletedAt: completedAt,
	}
	fingerprint, err := verification.ProviderResultFingerprint(result)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := verification.NewResultReceipt(attempt.ID, fingerprint, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if disposition, err := verification.ApplyProviderResult(&check, result, generator, attempt.Fence,
		verification.WithDocumentAnalyses(front, back)); err != nil || disposition != "applied" {
		t.Fatalf("apply result = %s, %v", disposition, err)
	}
	if check.Outcome != verification.CheckNotPassed {
		t.Fatalf("outcome = %s", check.Outcome)
	}
	saveEvent, err := generator.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := store.SaveCheck(ctx, scope, verification.CheckCommit{
		Check: check, ExpectedVersion: 2, EventID: saveEvent, Receipt: &receipt,
	}); err != nil || duplicate {
		t.Fatalf("save result = %t, %v", duplicate, err)
	}

	reloaded, err := store.FindCheck(ctx, scope, checkID)
	if err != nil {
		t.Fatal(err)
	}
	observations := reloaded.Attempts()[0].Observations
	if len(observations) != 6 {
		t.Fatalf("observations = %d", len(observations))
	}
	assertPersistedSignal(t, observations, verification.SignalDocumentMRZ, verification.SignalSatisfied)
	assertPersistedSignal(t, observations, verification.SignalDocumentSides, verification.SignalNotSatisfied)
	assertPersistedSignal(t, observations, verification.SignalDocumentConsistency, verification.SignalNotSatisfied)
	assertPersistedSignal(t, observations, verification.SignalDocumentClassification, verification.SignalSatisfied)

	var leaked int
	if err := adminPool.Native().QueryRow(ctx, `SELECT count(*) FROM idenqa.verification_observations
		WHERE tenant_id = $1 AND (
			signal_name LIKE '%X10000001%' OR signal_name LIKE '%X20000002%' OR signal_name LIKE '%DOE%' OR
			reason_codes::text LIKE '%X10000001%' OR reason_codes::text LIKE '%X20000002%' OR reason_codes::text LIKE '%DOE%'
		)`, tenantID.String()).Scan(&leaked); err != nil {
		t.Fatal(err)
	}
	if leaked != 0 {
		t.Fatalf("persisted observations leaked raw values: %d rows", leaked)
	}
}

func assertPersistedSignal(t *testing.T, observations []verification.Observation, name string, outcome verification.SignalOutcome) {
	t.Helper()
	for _, observation := range observations {
		if observation.Signal.Name == name {
			if observation.Signal.Outcome != outcome {
				t.Fatalf("%s outcome = %s", name, observation.Signal.Outcome)
			}
			return
		}
	}
	t.Fatalf("missing persisted signal %s", name)
}

func integrationSide(t *testing.T, input fields.Input) document.Analysis {
	t.Helper()
	analysis, err := fields.Analyse(input)
	if err != nil {
		t.Fatal(err)
	}
	return analysis
}

func integrationPassport(t *testing.T, documentNumber string) []string {
	t.Helper()
	document := padIntegration(documentNumber, 9)
	documentCheck, ok := mrz.CheckDigit(document)
	if !ok {
		t.Fatal("document check digit")
	}
	birthCheck, ok := mrz.CheckDigit("740812")
	if !ok {
		t.Fatal("birth check digit")
	}
	expiryCheck, ok := mrz.CheckDigit("300101")
	if !ok {
		t.Fatal("expiry check digit")
	}
	personal := strings.Repeat("<", 14)
	personalCheck, ok := mrz.CheckDigit(personal)
	if !ok {
		t.Fatal("personal check digit")
	}
	line2 := document + string(documentCheck) + "UTO" + "740812" + string(birthCheck) + "M" +
		"300101" + string(expiryCheck) + personal + string(personalCheck)
	composite := line2[0:10] + line2[13:20] + line2[21:28] + line2[28:43]
	compositeCheck, ok := mrz.CheckDigit(composite)
	if !ok {
		t.Fatal("composite check digit")
	}
	line2 += string(compositeCheck)
	line1 := padIntegration("P<UTODOE<<JOHN", 44)
	return []string{line1, line2}
}

func padIntegration(value string, length int) string {
	if len(value) >= length {
		return value[:length]
	}
	return value + strings.Repeat("<", length-len(value))
}
