package provider_test

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/provider"
)

const (
	testTenant  = "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
	testPolicy  = "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
	testProfile = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
)

func registrationConfiguration(t *testing.T, manifest providerv1.Manifest, providerID string) providerv1.ConfigurationReference {
	t.Helper()
	reference := providerv1.ConfigurationReference{ProviderID: providerID, SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/tenant/account", CredentialVersion: "v1"}
	if reference.Validate() != nil {
		t.Fatal("fixture configuration is invalid")
	}
	return reference
}

func dojahWrite(t *testing.T) provider.RegistrationWrite {
	t.Helper()
	manifest := dojah.Description()
	return provider.RegistrationWrite{AdapterID: manifest.Package.AdapterID, Region: "africa", Configuration: registrationConfiguration(t, manifest, "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH")}
}

func smileidWrite(t *testing.T) provider.RegistrationWrite {
	t.Helper()
	manifest := smileid.Description()
	return provider.RegistrationWrite{AdapterID: manifest.Package.AdapterID, Region: "africa", Configuration: registrationConfiguration(t, manifest, "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH"),
		Inputs:            []providerv1.InputReference{{Name: "idenqa.input.country", Reference: "secret://input/country"}, {Name: "idenqa.input.id_type", Reference: "secret://input/id-type"}},
		SelfieRequirement: "selfie"}
}

func TestRegistrationWriteValidation(t *testing.T) {
	t.Parallel()
	dojahManifest := dojah.Description()
	smileManifest := smileid.Description()
	for _, test := range []struct {
		name    string
		write   provider.RegistrationWrite
		modify  func(*provider.RegistrationWrite)
		accept  bool
		reasons []string
	}{
		{name: "dojah accepted", write: dojahWrite(t), accept: true},
		{name: "dojah rejects inputs", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) {
			w.Inputs = []providerv1.InputReference{{Name: "idenqa.input.country", Reference: "secret://input/country"}}
		}},
		{name: "dojah rejects selfie", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) { w.SelfieRequirement = "selfie" }},
		{name: "schema mismatch", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) {
			w.Configuration.SchemaDigest = "sha256:" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		}},
		{name: "credential value rejected", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) {
			w.Configuration.SecretReference = "live-api-key-value"
		}},
		{name: "secret material rejected", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) {
			w.Configuration.CredentialVersion = "my_api_key"
		}},
		{name: "invalid region", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) { w.Region = "Africa East" }},
		{name: "unknown adapter", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) { w.AdapterID = "other" }},
		{name: "smileid accepted", write: smileidWrite(t), accept: true},
		{name: "smileid requires selfie", write: smileidWrite(t), modify: func(w *provider.RegistrationWrite) { w.SelfieRequirement = "" }},
		{name: "smileid requires two inputs", write: smileidWrite(t), modify: func(w *provider.RegistrationWrite) { w.Inputs = w.Inputs[:1] }},
		{name: "smileid rejects unknown input", write: smileidWrite(t), modify: func(w *provider.RegistrationWrite) {
			w.Inputs = []providerv1.InputReference{{Name: "idenqa.input.country", Reference: "secret://input/country"}, {Name: "idenqa.input.id_number", Reference: "secret://input/id-number"}}
		}},
		{name: "restrictions may not exceed manifest", write: dojahWrite(t), modify: func(w *provider.RegistrationWrite) {
			w.Restrictions = &providerv1.Restrictions{MaximumGrants: 32, MaximumResultSize: 32 * 1024, MaximumDuration: time.Minute}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			write := test.write
			if test.modify != nil {
				test.modify(&write)
			}
			manifest := dojahManifest
			if write.AdapterID == "smileid" {
				manifest = smileManifest
			}
			report := write.ValidateReport(manifest)
			if report.Accepted != test.accept {
				t.Fatalf("accepted = %v reasons = %v", report.Accepted, report.ReasonCodes)
			}
			if test.accept && (len(report.ReasonCodes) != 1 || report.ReasonCodes[0] != provider.RegistrationAccepted) {
				t.Fatalf("accepted report = %v", report.ReasonCodes)
			}
			if !test.accept && (len(report.ReasonCodes) == 0 || report.ReasonCodes[0] == provider.RegistrationAccepted) {
				t.Fatalf("rejected report = %v", report.ReasonCodes)
			}
		})
	}
}

func TestRegistrationSelectionIsExactAndAmbiguitySafe(t *testing.T) {
	t.Parallel()
	base := provider.Registration{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", Region: "africa", Enabled: true}
	if _, ok := provider.Selected([]provider.Registration{base}, "dojah", "africa"); !ok {
		t.Fatal("exact enabled registration not selected")
	}
	if _, ok := provider.Selected([]provider.Registration{base}, "smileid", "africa"); ok {
		t.Fatal("adapter mismatch selected")
	}
	if _, ok := provider.Selected([]provider.Registration{base}, "dojah", "europe"); ok {
		t.Fatal("region mismatch selected")
	}
	if _, ok := provider.Selected([]provider.Registration{base}, "dojah", ""); !ok {
		t.Fatal("single enabled registration without region not selected")
	}
	second := base
	second.ID = "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWJ"
	second.Region = "europe"
	if _, ok := provider.Selected([]provider.Registration{base, second}, "dojah", ""); ok {
		t.Fatal("ambiguous enabled registrations selected")
	}
	disabled := base
	disabled.Enabled = false
	if _, ok := provider.Selected([]provider.Registration{disabled}, "dojah", "africa"); ok {
		t.Fatal("disabled registration selected")
	}
}

func TestRegisteredPlanOverlaysAndPinsConfiguration(t *testing.T) {
	t.Parallel()
	manifest := dojah.Description()
	deployment := provider.Binding{TenantID: testTenant, PolicyID: testPolicy, ProfileDigest: testProfile, Requirement: "document", Region: "africa",
		Purpose: "idenqa.purpose.identity_verification", Recipient: "tenant.recipient.primary", Configuration: registrationConfiguration(t, manifest, "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH")}
	deploymentPlan, err := provider.NewPlan(deployment, manifest)
	if err != nil {
		t.Fatal(err)
	}
	registration := provider.Registration{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", Region: "africa", Enabled: true,
		Configuration: registrationConfiguration(t, manifest, "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWK"),
		Restrictions:  &providerv1.Restrictions{NetworkRequired: true, MaximumGrants: 1, MaximumResultSize: 1024, MaximumDuration: time.Second}}
	plan, err := provider.NewRegisteredPlan(registration, deployment, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if plan.ConfigurationDigest() == deploymentPlan.ConfigurationDigest() {
		t.Fatal("registered configuration did not change the pinned digest")
	}
	if plan.Binding.Configuration.ProviderID != registration.Configuration.ProviderID || plan.Manifest.Restrictions.MaximumGrants != 1 {
		t.Fatal("registered configuration or tightened restrictions were not applied")
	}
	mismatch := registration
	mismatch.Region = "europe"
	if _, err := provider.NewRegisteredPlan(mismatch, deployment, manifest); err == nil {
		t.Fatal("region mismatch accepted")
	}
	unbounded := registration
	unbounded.Restrictions = &providerv1.Restrictions{NetworkRequired: true, MaximumGrants: 32, MaximumResultSize: 1024, MaximumDuration: time.Second}
	if _, err := provider.NewRegisteredPlan(unbounded, deployment, manifest); err == nil {
		t.Fatal("registration exceeding manifest restrictions accepted")
	}
}

func TestFailureSimulationNeverProducesIdentityOutcome(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		class        providerv1.FailureClass
		retry        string
		attemptState string
		checkState   string
	}{
		{providerv1.FailureUnavailable, "backoff", "failed", "failed"},
		{providerv1.FailureRateLimited, "backoff", "failed", "failed"},
		{providerv1.FailureDeadline, "reconcile", "timed_out", "timed_out"},
		{providerv1.FailureCancelled, "reconcile", "cancelled", "cancelled"},
		{providerv1.FailureProviderRejected, "never", "failed", "failed"},
	} {
		simulation, err := provider.SimulateFailurePreview(test.class, "provider_failure")
		if err != nil {
			t.Fatal(err)
		}
		if simulation.Retry != test.retry || simulation.AttemptState != test.attemptState || simulation.CheckState != test.checkState || simulation.ProducesIdentityOutcome {
			t.Fatalf("simulation = %+v", simulation)
		}
	}
	if _, err := provider.SimulateFailurePreview("made_up", "provider_failure"); err == nil {
		t.Fatal("unknown failure class accepted")
	}
	if _, err := provider.SimulateFailurePreview(providerv1.FailureUnavailable, "Invalid Code"); err == nil {
		t.Fatal("invalid failure code accepted")
	}
}
