package provider_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

func TestEnvelopesCannotCarryRawEvidenceOrDynamicSecretValues(t *testing.T) {
	t.Parallel()

	assertSafeShape(t, reflect.TypeFor[providerv1.Request](), map[reflect.Type]bool{})
	assertSafeShape(t, reflect.TypeFor[providerv1.Result](), map[reflect.Type]bool{})
}

func TestContractCompatibility(t *testing.T) {
	t.Parallel()

	if !providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 1}) ||
		!providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 1, Minor: 1}) {
		t.Fatal("v1.1 must accept v1.0 and v1.1")
	}
	if providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 2}) ||
		providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 1, Minor: 2}) {
		t.Fatal("v1.1 accepted an incompatible contract")
	}
}

func TestDocumentObservationIsAdditiveAndBounded(t *testing.T) {
	t.Parallel()

	plain := contractResult()
	if err := plain.Validate(); err != nil {
		t.Fatalf("result without document data rejected: %v", err)
	}
	encoded, err := json.Marshal(plain)
	if err != nil || strings.Contains(string(encoded), `"document":`) {
		t.Fatalf("absent document data changed result encoding: %s %v", encoded, err)
	}

	observed := contractResult()
	observation := validDocumentObservation()
	observed.Document = &observation
	if err := observed.Validate(); err != nil {
		t.Fatalf("valid document observation rejected: %v", err)
	}
	encoded, err = json.Marshal(observed)
	if err != nil || !strings.Contains(string(encoded), `"document"`) {
		t.Fatalf("document observation missing from result encoding: %s %v", encoded, err)
	}
}

func TestDocumentObservationFailsClosedOnMalformedContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*providerv1.DocumentObservation)
	}{
		{"empty observation", func(observation *providerv1.DocumentObservation) {
			*observation = providerv1.DocumentObservation{}
		}},
		{"too many mrz lines", func(observation *providerv1.DocumentObservation) {
			observation.MRZLines = []string{padMRZ("P<UTO"), padMRZ("P<UTO"), padMRZ("P<UTO"), padMRZ("P<UTO")}
		}},
		{"oversized mrz line", func(observation *providerv1.DocumentObservation) {
			observation.MRZLines = []string{padMRZ("P<UTO") + "AAAA"}
		}},
		{"mrz line outside charset", func(observation *providerv1.DocumentObservation) {
			observation.MRZLines = []string{strings.Repeat("A", 43) + "*"}
		}},
		{"mrz line with control character", func(observation *providerv1.DocumentObservation) {
			observation.MRZLines = []string{strings.Repeat("A", 43) + "\n"}
		}},
		{"oversized barcode payload", func(observation *providerv1.DocumentObservation) {
			observation.BarcodePayload = strings.Repeat("a", providerv1.MaximumBarcodePayloadBytes+1)
		}},
		{"barcode payload with nul", func(observation *providerv1.DocumentObservation) {
			observation.BarcodePayload = "ANSI \x00"
		}},
		{"too many fields", func(observation *providerv1.DocumentObservation) {
			observation.Fields = make([]providerv1.DocumentField, providerv1.MaximumDocumentFields+1)
		}},
		{"empty field name", func(observation *providerv1.DocumentObservation) {
			observation.Fields = []providerv1.DocumentField{{Name: "", Value: "value"}}
		}},
		{"oversized field value", func(observation *providerv1.DocumentObservation) {
			observation.Fields = []providerv1.DocumentField{{Name: "document_number", Value: strings.Repeat("x", providerv1.MaximumDocumentFieldBytes+1)}}
		}},
		{"field value with control character", func(observation *providerv1.DocumentObservation) {
			observation.Fields = []providerv1.DocumentField{{Name: "document_number", Value: "value\n"}}
		}},
		{"field value with padding", func(observation *providerv1.DocumentObservation) {
			observation.Fields = []providerv1.DocumentField{{Name: "document_number", Value: " value"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			observation := validDocumentObservation()
			test.mutate(&observation)
			if err := observation.Validate(); err == nil {
				t.Fatal("malformed document observation accepted")
			}
			result := contractResult()
			result.Document = &observation
			if err := result.Validate(); err == nil {
				t.Fatal("result accepted a malformed document observation")
			}
		})
	}
}

func TestDocumentObservationCannotAttachToFailedResult(t *testing.T) {
	t.Parallel()

	observation := validDocumentObservation()
	result := providerv1.Result{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
		Outcome: providerv1.ResultOutcomeFailed, Document: &observation,
		Failure:     &providerv1.Failure{Class: providerv1.FailureUnavailable, Code: "provider_unavailable", Retry: providerv1.RetryBackoff},
		CompletedAt: time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC),
	}
	if err := result.Validate(); err == nil {
		t.Fatal("failed result accepted document observation")
	}
}

func contractResult() providerv1.Result {
	return providerv1.Result{
		Contract: providerv1.CurrentVersion, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
		Outcome:     providerv1.ResultOutcomeCompleted,
		Signals:     []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}},
		CompletedAt: time.Date(2026, 9, 7, 10, 0, 0, 0, time.UTC),
	}
}

func validDocumentObservation() providerv1.DocumentObservation {
	return providerv1.DocumentObservation{
		MRZLines: []string{
			padMRZ("P<UTODOE<<JOHN"),
			padMRZ("X100000019UTO7408122M3001018<<<<<<<<<<<<<02"),
		},
		BarcodePayload: "@\n\x1e\rANSI 636000080002PP00410272\nDCSDOE\nDACJOHN\n",
		Fields: []providerv1.DocumentField{
			{Name: "document_number", Value: "X10000001"},
			{Name: "date_of_birth", Value: "1974-08-12"},
		},
	}
}

func padMRZ(value string) string {
	if len(value) >= providerv1.MaximumMRZLineBytes {
		return value[:providerv1.MaximumMRZLineBytes]
	}
	return value + strings.Repeat("<", providerv1.MaximumMRZLineBytes-len(value))
}

func assertSafeShape(t *testing.T, value reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	if seen[value] {
		return
	}
	seen[value] = true
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Map ||
		(value.Kind() == reflect.Slice && value.Elem().Kind() == reflect.Uint8) {
		t.Fatalf("unsafe payload type reachable from contract: %s", value)
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		if value.Elem().PkgPath() == reflect.TypeFor[providerv1.Request]().PkgPath() {
			assertSafeShape(t, value.Elem(), seen)
		}
	case reflect.Struct:
		for index := range value.NumField() {
			field := value.Field(index).Type
			if field.PkgPath() == reflect.TypeFor[providerv1.Request]().PkgPath() ||
				((field.Kind() == reflect.Pointer || field.Kind() == reflect.Slice || field.Kind() == reflect.Array) &&
					field.Elem().PkgPath() == reflect.TypeFor[providerv1.Request]().PkgPath()) {
				assertSafeShape(t, field, seen)
			}
		}
	}
}
