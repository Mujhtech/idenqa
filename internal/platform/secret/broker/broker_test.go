package broker

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

func TestBrokerPinsTenantReferenceAndConcreteCredentialVersion(t *testing.T) {
	current := "pvc_generation_one"
	denied := false
	reads := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct{ AccountID, Version, CoreTenantID string }
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if input.AccountID != "pva_fixture" || input.CoreTenantID != "ten_fixture" {
			t.Error("incorrect authority scope")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if denied {
			w.WriteHeader(403)
			return
		}
		if r.URL.Path == "/regional/v1/provider-credentials/current" {
			_ = json.NewEncoder(w).Encode(map[string]string{"version": current})
			return
		}
		reads++
		if input.Version != "pvc_generation_one" && input.Version != "pvc_generation_two" {
			t.Error("unresolved symbolic version reached credential delivery")
			w.WriteHeader(403)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": input.Version, "value": map[string]string{"api_key": "fixture_key"}})
	}))
	defer server.Close()
	client := server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resolver := &Resolver{origin: server.URL, tenant: "ten_fixture", client: client}
	base := "secret://broker/pva_fixture"
	version, err := resolver.CurrentVersion(t.Context(), base)
	if err != nil || version != current {
		t.Fatal("current version unavailable", err)
	}
	reference, err := secret.ParseReference(base + "?version=regional-current")
	if err != nil {
		t.Fatal(err)
	}
	value, err := resolver.Resolve(t.Context(), reference)
	if err != nil || value.Version() != current {
		t.Fatal("initial resolution failed", err)
	}
	current = "pvc_generation_two"
	pinned, err := secret.ParseReference(base + "?version=pvc_generation_one")
	if err != nil {
		t.Fatal(err)
	}
	value, err = resolver.Resolve(t.Context(), pinned)
	if err != nil || value.Version() != "pvc_generation_one" {
		t.Fatal("rotation changed an in-flight version pin", err)
	}
	value, err = resolver.Resolve(t.Context(), reference)
	if err != nil || value.Version() != current {
		t.Fatal("replacement not adopted", err)
	}
	denied = true
	if _, err = resolver.Resolve(t.Context(), pinned); !errors.Is(err, secret.ErrDenied) {
		t.Fatal("broker denial bypassed", err)
	}
	if reads != 3 {
		t.Fatal("unexpected credential reads", reads)
	}
}

func TestBrokerRejectsUnversionedAndForeignReferences(t *testing.T) {
	resolver := &Resolver{}
	for _, raw := range []string{"secret://broker/pva_fixture", "secret://file/private/key?version=v1", "secret://broker/other/account?version=v1"} {
		reference, err := secret.ParseReference(raw)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = resolver.Resolve(t.Context(), reference); !errors.Is(err, secret.ErrInvalid) {
			t.Fatal("invalid reference accepted", raw, err)
		}
	}
}
