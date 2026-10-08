package acceptance_test

import (
	"testing"
)

func TestReceiptBindsTupleAndCredentialCustody(t *testing.T) {
	record := acceptedProviderRecord()
	tenant, err := record.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	record.RecordID = "managed-provider-run"
	record.Evidence.CredentialOwnership = "operator_managed"
	managed, err := record.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	if tenant.TupleDigest != managed.TupleDigest || tenant.Digest == managed.Digest ||
		managed.CredentialOwnership != "operator_managed" || managed.SchemaVersion != "idenqa.acceptance.receipt.v2" {
		t.Fatal("receipt did not preserve exact tuple and independent custody evidence")
	}
	record.Tuple.Country = "gh"
	other, err := record.Receipt()
	if err != nil {
		t.Fatal(err)
	}
	if other.TupleDigest == managed.TupleDigest {
		t.Fatal("different country reused accepted tuple digest")
	}
	record.Decision = "open"
	record.Checks = nil
	open, err := record.Receipt()
	if err != nil || open.Passed {
		t.Fatalf("open scoped receipt passed: %v", err)
	}
}
