package acceptance_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/acceptance"
)

func TestProviderRecordAccepted(t *testing.T) {
	record := acceptedProviderRecord()
	if err := record.Validate(); err != nil {
		t.Fatalf("validate accepted record: %v", err)
	}
	if !record.Passed() {
		t.Fatal("complete accepted record must pass")
	}
	digest, err := record.Digest()
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("digest accepted record: %q, %v", digest, err)
	}
}

func TestProviderRecordIncompleteAcceptanceFailsClosed(t *testing.T) {
	record := acceptedProviderRecord()
	record.Checks = record.Checks[:len(record.Checks)-1]
	if err := record.Validate(); !errors.Is(err, acceptance.ErrIncomplete) {
		t.Fatalf("validate incomplete acceptance = %v, want ErrIncomplete", err)
	}
	if record.Passed() {
		t.Fatal("incomplete acceptance must not pass")
	}

	record = acceptedProviderRecord()
	record.Approvals[0].Decision = "pending"
	if err := record.Validate(); !errors.Is(err, acceptance.ErrIncomplete) {
		t.Fatalf("validate pending approval = %v, want ErrIncomplete", err)
	}
}

func TestProviderRecordStrictlyRejectsProhibitedFields(t *testing.T) {
	encoded, err := json.Marshal(acceptedProviderRecord())
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded[:len(encoded)-1], []byte(`,"provider_payload":{"document_number":"secret"}}`)...)
	if _, err := acceptance.DecodeProvider(encoded); !errors.Is(err, acceptance.ErrInvalid) {
		t.Fatalf("decode prohibited field = %v, want ErrInvalid", err)
	}

	record := acceptedProviderRecord()
	record.Evidence.RawReportRef = "https://logs.example/raw?token=secret"
	if err := record.Validate(); !errors.Is(err, acceptance.ErrInvalid) {
		t.Fatalf("validate non-opaque reference = %v, want ErrInvalid", err)
	}
}

func TestProviderRecordDigestIsSemanticAndMeaningSensitive(t *testing.T) {
	left := acceptedProviderRecord()
	right := acceptedProviderRecord()
	right.Checks[0], right.Checks[1] = right.Checks[1], right.Checks[0]
	right.Approvals[0], right.Approvals[1] = right.Approvals[1], right.Approvals[0]

	leftDigest, err := left.Digest()
	if err != nil {
		t.Fatal(err)
	}
	rightDigest, err := right.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if leftDigest != rightDigest {
		t.Fatalf("order changed semantic digest: %s != %s", leftDigest, rightDigest)
	}

	right.Tuple.Country = "gh"
	changedDigest, err := right.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if changedDigest == leftDigest {
		t.Fatal("changed tuple meaning must change digest")
	}
}

func TestProviderRecordOpenMayRemainIncomplete(t *testing.T) {
	record := acceptedProviderRecord()
	record.Decision = "open"
	record.Run.OperatorRef = ""
	record.Run.EndedAt = time.Time{}
	record.Run.RevalidateAt = time.Time{}
	record.Build = acceptance.Build{}
	record.Evidence = acceptance.Evidence{}
	record.Checks = nil
	record.Approvals = nil
	if err := record.Validate(); err != nil {
		t.Fatalf("validate open record: %v", err)
	}
	if record.Passed() {
		t.Fatal("open record must not pass")
	}
}

func TestProviderRecordExampleIsValidAndOpen(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("..", "..", "docs", "examples", "provider-acceptance-record-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	record, err := acceptance.DecodeProvider(encoded)
	if err != nil {
		t.Fatalf("decode documented example: %v", err)
	}
	if record.Passed() {
		t.Fatal("open documented example must not pass")
	}
}

func acceptedProviderRecord() acceptance.ProviderRecord {
	started := time.Date(2026, time.October, 2, 10, 0, 0, 0, time.UTC)
	ended := started.Add(time.Hour)
	digestA := "sha256:" + strings.Repeat("a", 64)
	digestB := "sha256:" + strings.Repeat("b", 64)
	reference := func(kind, id string) string { return "ref:" + kind + ":" + id }
	checks := make([]acceptance.Check, 0, 7)
	for _, id := range []string{
		"PRV-01-authentication", "PRV-02-representative-samples", "PRV-03-outcome-mapping",
		"PRV-04-failure-paths", "PRV-05-operational-review", "PRV-06-regional-journey",
		"PRV-07-content-exclusion",
	} {
		checks = append(checks, acceptance.Check{ID: id, Result: "pass", EvidenceRefs: []string{reference("evidence", id)}})
	}
	approvals := make([]acceptance.Approval, 0, 5)
	for _, role := range []string{"operations", "privacy_legal", "product", "provider_model", "security"} {
		approvals = append(approvals, acceptance.Approval{
			Role: role, ActorRef: reference("principal", role), Decision: "approve", At: ended,
			EvidenceRef: reference("approval", role),
		})
	}
	return acceptance.ProviderRecord{
		SchemaVersion: acceptance.ProviderSchemaVersion,
		RecordID:      "provider-run-20261002",
		GateSet:       "provider",
		Decision:      "accepted",
		Tuple: acceptance.ProviderTuple{
			AdapterModel: "dojah", ArtifactDigest: digestA, Capability: "document_analysis",
			Country: "ng", EvidenceClass: "identity_document", AcquisitionMethod: "live_camera",
			Region: "eu-west-1", RuntimeClass: "linux_amd64", ConfigurationDigest: digestB,
		},
		Run: acceptance.Run{
			RunID: "run-20261002", OperatorRef: reference("principal", "operator"), StartedAt: started,
			EndedAt: ended, RevalidateAt: ended.Add(90 * 24 * time.Hour),
		},
		Build: acceptance.Build{
			RepositoryCommit: strings.Repeat("c", 40), BuildDigest: digestA,
			SignatureRef: reference("signature", "build"), SBOMRef: reference("sbom", "build"), ContractVersion: "v1.1",
		},
		Evidence: acceptance.Evidence{
			CredentialOwnership: "tenant_owned", AccountEnvironment: "production", ProcessingRegion: "eu-west-1",
			DatasetRef: reference("dataset", "approved"), RightsBasisRef: reference("rights", "approved"),
			ConsentBasisRef: reference("consent", "approved"), HardwareBoundsRef: reference("hardware", "bounds"),
			SampleExclusionsRef: reference("samples", "exclusions"), ThresholdsRef: reference("thresholds", "declared"),
			RawReportRef: reference("report", "raw"), FailuresRef: reference("report", "failures"),
			IncidentsRef: reference("report", "incidents"), Metrics: []acceptance.Metric{{
				Name: "normalized_success_rate", Value: 0.97, Unit: "ratio", Population: 100,
				EvidenceRef: reference("metric", "success-rate"),
			}},
		},
		Checks: checks, Approvals: approvals,
	}
}
