package verification

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/document/fields"
	"github.com/Mujhtech/idenqa/internal/document/mrz"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
)

func TestDocumentSignalsValidPassport(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 7)
	completedAt := now.Add(time.Second)
	lines := buildTestPassport(t, "123456789", "300101")
	barcodePayload := "@\n\x1e\rANSI 636000080002PP00410272\nDCSDOE\nDACJOHN\nDBB08121974\nDAQ123456789\nDCGUTO\n"
	front := analyseSide(t, fields.Input{
		Side:     document.SideFront,
		MRZLines: lines,
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "123456789"},
			{Name: "first_name", Value: "John"},
			{Name: "last_name", Value: "Doe"},
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
		},
		Barcodes:  []string{barcodePayload},
		PlanSides: []document.Side{document.SideFront},
		Reference: completedAt,
	})
	result := providerResult(t, attempt, completedAt, "idenqa.signal.document_quality", providerv1.SignalOutcomeSatisfied)
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(front)); err != nil {
		t.Fatal(err)
	}
	if check.State != CheckCompleted || check.Outcome != CheckPassed {
		t.Fatalf("state = %s, outcome = %s", check.State, check.Outcome)
	}
	observations := check.Attempts()[0].Observations
	assertSignal(t, observations, SignalDocumentMRZ, SignalSatisfied, nil)
	assertSignal(t, observations, SignalDocumentExpiry, SignalSatisfied, nil)
	assertSignal(t, observations, SignalDocumentConsistency, SignalSatisfied, nil)
	assertSignal(t, observations, SignalDocumentSides, SignalSatisfied, nil)
	assertSignal(t, observations, SignalDocumentClassification, SignalSatisfied,
		[]string{"document_country_uto", "document_type_passport"})
	assertNoRawValue(t, observations, "123456789", "DOE", "JOHN", "ERIKSSON", strings.Join(lines, ""), "1974-08-12", barcodePayload)
}

func TestDocumentSignalsTamperedCheckDigit(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 8)
	completedAt := now.Add(time.Second)
	lines := buildTestPassport(t, "123456789", "300101")
	lines[1] = tamperCheckDigit(lines[1])
	front := analyseSide(t, fields.Input{
		Side:      document.SideFront,
		MRZLines:  lines,
		PlanSides: []document.Side{document.SideFront},
		Reference: completedAt,
	})
	result := providerResult(t, attempt, completedAt, "idenqa.signal.document_quality", providerv1.SignalOutcomeSatisfied)
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(front)); err != nil {
		t.Fatal(err)
	}
	if check.Outcome != CheckNotPassed {
		t.Fatalf("outcome = %s", check.Outcome)
	}
	assertSignal(t, check.Attempts()[0].Observations, SignalDocumentMRZ, SignalNotSatisfied,
		[]string{"mrz_check_digit_invalid"})
}

func TestDocumentSignalsExpiredDocument(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 9)
	completedAt := now.Add(time.Second)
	lines := buildTestPassport(t, "123456789", "120415")
	front := analyseSide(t, fields.Input{
		Side:      document.SideFront,
		MRZLines:  lines,
		PlanSides: []document.Side{document.SideFront},
		Reference: completedAt,
	})
	result := providerResult(t, attempt, completedAt, "idenqa.signal.document_quality", providerv1.SignalOutcomeSatisfied)
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(front)); err != nil {
		t.Fatal(err)
	}
	if check.Outcome != CheckNotPassed {
		t.Fatalf("outcome = %s", check.Outcome)
	}
	assertSignal(t, check.Attempts()[0].Observations, SignalDocumentExpiry, SignalNotSatisfied,
		[]string{"document_expired"})
}

func TestDocumentSignalsMismatchedFrontAndBack(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 10)
	completedAt := now.Add(time.Second)
	front := analyseSide(t, fields.Input{
		Side:     document.SideFront,
		MRZLines: buildTestPassport(t, "X10000001", "300101"),
		ProviderFields: []fields.RawField{
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
		},
		PlanSides: []document.Side{document.SideFront, document.SideBack},
		Reference: completedAt,
	})
	back := analyseSide(t, fields.Input{
		Side: document.SideBack,
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "X20000002"},
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
		},
		Reference: completedAt,
	})
	result := providerResult(t, attempt, completedAt, "idenqa.signal.document_quality", providerv1.SignalOutcomeSatisfied)
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(front, back)); err != nil {
		t.Fatal(err)
	}
	if check.Outcome != CheckNotPassed {
		t.Fatalf("outcome = %s", check.Outcome)
	}
	observations := check.Attempts()[0].Observations
	assertSignal(t, observations, SignalDocumentSides, SignalNotSatisfied, []string{"document_number_mismatch"})
	assertSignal(t, observations, SignalDocumentConsistency, SignalNotSatisfied, []string{"document_number_mismatch"})
	assertNoRawValue(t, observations, "X10000001", "X20000002")
}

func TestDocumentSignalsUnknownTypeStaysInconclusive(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 11)
	completedAt := now.Add(time.Second)
	front := analyseSide(t, fields.Input{
		Side:           document.SideFront,
		ProviderFields: []fields.RawField{{Name: "nationality", Value: "NGA"}},
		PlanSides:      []document.Side{document.SideFront},
		Reference:      completedAt,
	})
	result := providerResult(t, attempt, completedAt, "idenqa.signal.document_quality", providerv1.SignalOutcomeSatisfied)
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(front)); err != nil {
		t.Fatal(err)
	}
	if check.Outcome != CheckInconclusive {
		t.Fatalf("outcome = %s", check.Outcome)
	}
	observations := check.Attempts()[0].Observations
	assertSignal(t, observations, SignalDocumentClassification, SignalInconclusive,
		[]string{"document_classification_unknown"})
	assertSignal(t, observations, SignalDocumentExpiry, SignalInconclusive,
		[]string{"document_expiry_unavailable"})
}

func TestDocumentSignalsProviderNameIsAuthoritative(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 12)
	completedAt := now.Add(time.Second)
	front := analyseSide(t, fields.Input{
		Side:      document.SideFront,
		MRZLines:  buildTestPassport(t, "123456789", "300101"),
		Reference: completedAt,
	})
	result := providerResult(t, attempt, completedAt, SignalDocumentMRZ, providerv1.SignalOutcomeNotSatisfied)
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(front)); err != nil {
		t.Fatal(err)
	}
	observations := check.Attempts()[0].Observations
	count := 0
	for _, observation := range observations {
		if observation.Signal.Name == SignalDocumentMRZ {
			count++
			if observation.Signal.Outcome != SignalNotSatisfied {
				t.Fatalf("provider meaning changed: %+v", observation.Signal)
			}
		}
	}
	if count != 1 {
		t.Fatalf("document_mrz observations = %d", count)
	}
}

func TestDocumentSignalsRejectInvalidOptions(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 13)
	completedAt := now.Add(time.Second)
	result := providerResult(t, attempt, completedAt, "synthetic.document", providerv1.SignalOutcomeSatisfied)

	invalid := document.Analysis{Side: "sideways"}
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(invalid)); !errors.Is(err, ErrInvalidCheck) {
		t.Fatalf("invalid analysis = %v", err)
	}

	valid := analyseSide(t, fields.Input{Side: document.SideFront, Reference: completedAt})
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence,
		WithDocumentAnalyses(valid, valid)); !errors.Is(err, ErrInvalidCheck) {
		t.Fatalf("duplicate side = %v", err)
	}
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence,
		WithDocumentAnalyses(valid, valid, valid)); !errors.Is(err, ErrInvalidCheck) {
		t.Fatalf("too many analyses = %v", err)
	}
}

func TestDocumentSignalsAbsentByDefault(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 14)
	completedAt := now.Add(time.Second)
	result := providerResult(t, attempt, completedAt, "synthetic.document", providerv1.SignalOutcomeSatisfied)
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence); err != nil {
		t.Fatal(err)
	}
	for _, observation := range check.Attempts()[0].Observations {
		if strings.HasPrefix(observation.Signal.Name, "idenqa.signal.document_") {
			t.Fatalf("unexpected derived signal: %s", observation.Signal.Name)
		}
	}
}

func TestDocumentSignalsFromSyntheticProvider(t *testing.T) {
	t.Parallel()
	check, attempt, now := runningCheck(t, RunnerProvider, 15)
	completedAt := now.Add(time.Second)
	request := providerv1.Request{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String()}
	result, err := (synthetic.Provider{Scenario: synthetic.Success, Now: func() time.Time { return completedAt }}).Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	front := analyseSide(t, fields.Input{
		Side:     document.SideFront,
		MRZLines: buildTestPassport(t, "123456789", "300101"),
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "123456789"},
			{Name: "first_name", Value: "John"},
			{Name: "last_name", Value: "Doe"},
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
		},
		PlanSides: []document.Side{document.SideFront},
		Reference: completedAt,
	})
	if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence, WithDocumentAnalyses(front)); err != nil {
		t.Fatal(err)
	}
	if check.Outcome != CheckPassed {
		t.Fatalf("outcome = %s", check.Outcome)
	}
}

func TestConsumeProviderDocumentDerivesSignalsAndClearsObservation(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	lines := buildTestPassport(t, "123456789", "300101")
	barcodePayload := "@\n\x1e\rANSI 636000080002PP00410272\nDCSDOE\nDACJOHN\nDBB08121974\nDAQ123456789\nDCGUTO\n"
	result := providerDocumentResult(t, completedAt, providerv1.DocumentObservation{
		MRZLines:       lines,
		BarcodePayload: barcodePayload,
		Fields: []providerv1.DocumentField{
			{Name: "document_number", Value: "123456789"},
			{Name: "first_name", Value: "John"},
			{Name: "last_name", Value: "Doe"},
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
		},
	})
	consumed, err := ConsumeProviderDocument(result)
	if err != nil {
		t.Fatal(err)
	}
	if consumed.Document != nil {
		t.Fatal("document observation survived consumption")
	}
	assertProviderSignal(t, consumed, SignalDocumentMRZ, providerv1.SignalOutcomeSatisfied)
	assertProviderSignal(t, consumed, SignalDocumentExpiry, providerv1.SignalOutcomeSatisfied)
	assertProviderSignal(t, consumed, SignalDocumentConsistency, providerv1.SignalOutcomeSatisfied)
	assertProviderSignal(t, consumed, SignalDocumentClassification, providerv1.SignalOutcomeSatisfied)
	assertProviderSignal(t, consumed, "idenqa.signal.document_quality", providerv1.SignalOutcomeSatisfied)
	encoded, err := json.Marshal(consumed)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"123456789", "DOE", "JOHN", barcodePayload, strings.Join(lines, "")} {
		if strings.Contains(string(encoded), value) {
			t.Fatalf("consumed result retained raw observation value %q", value)
		}
	}
}

func TestConsumeProviderDocumentFailsClosedOnMalformedObservation(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		observation providerv1.DocumentObservation
	}{
		{"empty", providerv1.DocumentObservation{}},
		{"oversized line", providerv1.DocumentObservation{MRZLines: []string{strings.Repeat("A", 45)}}},
		{"unbounded fields", providerv1.DocumentObservation{Fields: make([]providerv1.DocumentField, 33)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation := test.observation
			result := providerDocumentResult(t, completedAt, observation)
			if _, err := ConsumeProviderDocument(result); !errors.Is(err, ErrInvalidCheck) {
				t.Fatalf("malformed observation = %v", err)
			}
		})
	}
	observation := validTestObservation(t)
	failed := providerv1.Result{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_" + testULID, Outcome: providerv1.ResultOutcomeFailed,
		Failure:  &providerv1.Failure{Class: providerv1.FailureUnavailable, Code: "unavailable", Retry: providerv1.RetryNever},
		Document: &observation, CompletedAt: completedAt,
	}
	if _, err := ConsumeProviderDocument(failed); !errors.Is(err, ErrInvalidCheck) {
		t.Fatalf("failed result with observation = %v", err)
	}
}

func TestProviderResultFingerprintIgnoresDocumentObservation(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC)
	observation := validTestObservation(t)
	withObservation := providerDocumentResult(t, completedAt, observation)
	withoutObservation := withObservation
	withoutObservation.Document = nil
	withFingerprint, err := ProviderResultFingerprint(withObservation)
	if err != nil {
		t.Fatal(err)
	}
	withoutFingerprint, err := ProviderResultFingerprint(withoutObservation)
	if err != nil {
		t.Fatal(err)
	}
	if withFingerprint != withoutFingerprint {
		t.Fatalf("fingerprints differ: %s != %s", withFingerprint, withoutFingerprint)
	}
}

func TestConsumeProviderDocumentTamperedDataNeverPasses(t *testing.T) {
	t.Parallel()

	t.Run("tampered check digit", func(t *testing.T) {
		t.Parallel()
		check, attempt, now := runningCheck(t, RunnerProvider, 21)
		lines := buildTestPassport(t, "123456789", "300101")
		lines[1] = tamperCheckDigit(lines[1])
		result := providerDocumentResult(t, now.Add(time.Second), providerv1.DocumentObservation{MRZLines: lines})
		result.AttemptID = attempt.ID.String()
		consumed, err := ConsumeProviderDocument(result)
		if err != nil {
			t.Fatal(err)
		}
		assertProviderSignal(t, consumed, SignalDocumentMRZ, providerv1.SignalOutcomeNotSatisfied)
		if _, err := ApplyProviderResult(&check, consumed, &observationIDs{}, attempt.Fence); err != nil {
			t.Fatal(err)
		}
		if check.Outcome == CheckPassed {
			t.Fatal("tampered machine-readable zone passed the check")
		}
		assertSignal(t, check.Attempts()[0].Observations, SignalDocumentMRZ, SignalNotSatisfied, []string{"mrz_check_digit_invalid"})
	})

	t.Run("unparsable barcode stays inconclusive", func(t *testing.T) {
		t.Parallel()
		check, attempt, now := runningCheck(t, RunnerProvider, 22)
		result := providerDocumentResult(t, now.Add(time.Second), providerv1.DocumentObservation{BarcodePayload: "not-a-reviewed-barcode"})
		result.AttemptID = attempt.ID.String()
		if _, err := ApplyProviderResult(&check, result, &observationIDs{}, attempt.Fence); err != nil {
			t.Fatal(err)
		}
		if check.Outcome != CheckInconclusive {
			t.Fatalf("outcome = %s", check.Outcome)
		}
	})
}

func validTestObservation(t *testing.T) providerv1.DocumentObservation {
	t.Helper()
	return providerv1.DocumentObservation{
		MRZLines: buildTestPassport(t, "123456789", "300101"),
		Fields:   []providerv1.DocumentField{{Name: "document_number", Value: "123456789"}},
	}
}

func providerDocumentResult(t *testing.T, completedAt time.Time, observation providerv1.DocumentObservation) providerv1.Result {
	t.Helper()
	return providerv1.Result{
		Contract:    providerv1.CurrentVersion,
		AttemptID:   "atm_" + testULID,
		Outcome:     providerv1.ResultOutcomeCompleted,
		Signals:     []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}},
		Document:    &observation,
		CompletedAt: completedAt,
	}
}

func assertProviderSignal(t *testing.T, result providerv1.Result, name string, outcome providerv1.SignalOutcome) {
	t.Helper()
	for _, signal := range result.Signals {
		if signal.Name != name {
			continue
		}
		if signal.Outcome != outcome {
			t.Fatalf("%s outcome = %s", name, signal.Outcome)
		}
		return
	}
	t.Fatalf("missing signal %s", name)
}

func analyseSide(t *testing.T, input fields.Input) document.Analysis {
	t.Helper()
	analysis, err := fields.Analyse(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := analysis.Validate(); err != nil {
		t.Fatal(err)
	}
	return analysis
}

func providerResult(t *testing.T, attempt Attempt, completedAt time.Time, name string, outcome providerv1.SignalOutcome) providerv1.Result {
	t.Helper()
	return providerv1.Result{
		Contract:    providerv1.CurrentVersion,
		AttemptID:   attempt.ID.String(),
		Outcome:     providerv1.ResultOutcomeCompleted,
		Signals:     []providerv1.Signal{{Name: name, Outcome: outcome}},
		CompletedAt: completedAt,
	}
}

func assertSignal(t *testing.T, observations []Observation, name string, outcome SignalOutcome, reasons []string) {
	t.Helper()
	for _, observation := range observations {
		if observation.Signal.Name != name {
			continue
		}
		if observation.Signal.Outcome != outcome {
			t.Fatalf("%s outcome = %s", name, observation.Signal.Outcome)
		}
		for _, reason := range reasons {
			if !containsString(observation.Signal.ReasonCodes, reason) {
				t.Fatalf("%s reasons = %v", name, observation.Signal.ReasonCodes)
			}
		}
		return
	}
	t.Fatalf("missing signal %s", name)
}

func assertNoRawValue(t *testing.T, observations []Observation, values ...string) {
	t.Helper()
	encoded, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	rendered := string(encoded)
	for _, value := range values {
		if value == "" {
			continue
		}
		if strings.Contains(rendered, value) {
			t.Fatalf("observation payload contains raw value %q", value)
		}
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func buildTestPassport(t *testing.T, documentNumber, expiry string) []string {
	t.Helper()
	document := padTestFiller(documentNumber, 9)
	documentCheck, ok := mrz.CheckDigit(document)
	if !ok {
		t.Fatal("document check digit")
	}
	birthRaw := padTestFiller("740812", 6)
	birthCheck, ok := mrz.CheckDigit(birthRaw)
	if !ok {
		t.Fatal("birth check digit")
	}
	expiryRaw := padTestFiller(expiry, 6)
	expiryCheck, ok := mrz.CheckDigit(expiryRaw)
	if !ok {
		t.Fatal("expiry check digit")
	}
	personal := strings.Repeat("<", 14)
	personalCheck, ok := mrz.CheckDigit(personal)
	if !ok {
		t.Fatal("personal check digit")
	}
	line2 := document + string(documentCheck) + "UTO" + birthRaw + string(birthCheck) + "M" +
		expiryRaw + string(expiryCheck) + personal + string(personalCheck)
	composite := line2[0:10] + line2[13:20] + line2[21:28] + line2[28:43]
	compositeCheck, ok := mrz.CheckDigit(composite)
	if !ok {
		t.Fatal("composite check digit")
	}
	line2 += string(compositeCheck)
	line1 := padTestFiller("P<UTODOE<<JOHN", 44)
	return []string{line1, line2}
}

func tamperCheckDigit(line string) string {
	replacement := byte('0')
	if line[9] == '0' {
		replacement = '1'
	}
	return line[:9] + string(replacement) + line[10:]
}

func padTestFiller(value string, length int) string {
	if len(value) >= length {
		return value[:length]
	}
	return value + strings.Repeat("<", length-len(value))
}
