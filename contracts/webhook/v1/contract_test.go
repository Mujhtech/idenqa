package webhook_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
)

func TestCatalogueSchemasAndFixturesAgree(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("events")
	if err != nil {
		t.Fatalf("read event schemas: %v", err)
	}
	seen := make(map[webhookv1.Type]bool, len(entries))
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), ".schema.json")
		if name == entry.Name() {
			t.Fatalf("unexpected event schema file %q", entry.Name())
		}
		definition, exists := webhookv1.Lookup(webhookv1.Type(name))
		if !exists {
			t.Fatalf("schema %q has no catalogue definition", name)
		}
		seen[definition.Type] = true
		payload, err := os.ReadFile(filepath.Join("events", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var schema struct {
			Properties map[string]json.RawMessage `json:"properties"`
			Required   []string                   `json:"required"`
		}
		if err := json.Unmarshal(payload, &schema); err != nil {
			t.Fatalf("decode schema %q: %v", name, err)
		}
		var version struct {
			Const string `json:"const"`
		}
		if err := json.Unmarshal(schema.Properties["schema_version"], &version); err != nil || version.Const != definition.SchemaVersion {
			t.Fatalf("schema %q version = %q, %v", name, version.Const, err)
		}
		var data struct {
			Required   []string                   `json:"required"`
			Properties map[string]json.RawMessage `json:"properties"`
		}
		if err := json.Unmarshal(schema.Properties["data"], &data); err != nil {
			t.Fatalf("decode data schema %q: %v", name, err)
		}
		wantRequired := append([]string(nil), definition.RequiredData...)
		sort.Strings(wantRequired)
		gotRequired := append([]string(nil), data.Required...)
		sort.Strings(gotRequired)
		if strings.Join(wantRequired, ",") != strings.Join(gotRequired, ",") {
			t.Fatalf("schema %q required = %v, want %v", name, gotRequired, wantRequired)
		}
		accepted := definition.AcceptedData()
		gotFields := make([]string, 0, len(data.Properties))
		for field := range data.Properties {
			gotFields = append(gotFields, field)
		}
		sort.Strings(gotFields)
		if strings.Join(accepted, ",") != strings.Join(gotFields, ",") {
			t.Fatalf("schema %q fields = %v, want %v", name, gotFields, accepted)
		}
	}
	for _, definition := range webhookv1.Catalogue() {
		if !seen[definition.Type] {
			t.Fatalf("catalogue type %q has no schema", definition.Type)
		}
		fixture, err := os.ReadFile(filepath.Join("fixtures", string(definition.Type), definition.SchemaVersion+".json"))
		if err != nil {
			t.Fatalf("read fixture %q: %v", definition.Type, err)
		}
		event, err := webhookv1.Parse(fixture)
		if err != nil {
			t.Fatalf("parse fixture %q: %v", definition.Type, err)
		}
		canonical, err := event.Canonical()
		if err != nil {
			t.Fatalf("canonical fixture %q: %v", definition.Type, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, fixture); err != nil {
			t.Fatal(err)
		}
		if compact.String() != string(canonical) {
			t.Fatalf("fixture %q is not canonical: %s", definition.Type, canonical)
		}
		if _, err := event.Digest(); err != nil {
			t.Fatalf("digest fixture %q: %v", definition.Type, err)
		}
	}
}

func TestSubscriptionSelectionIsClosed(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		selection []string
		want      string
	}{
		{name: "exact", selection: []string{"verification.completed", "evidence.ready"}, want: "verification.completed"},
		{name: "wildcard", selection: []string{"*"}, want: "*"},
		{name: "unknown", selection: []string{"charge.succeeded"}},
		{name: "duplicate", selection: []string{"evidence.ready", "evidence.ready"}},
		{name: "wildcard mixed", selection: []string{"*", "evidence.ready"}},
		{name: "empty", selection: nil},
		{name: "oversized", selection: make([]string, webhookv1.MaximumSubscriptions+1)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := webhookv1.ValidateSubscriptions(test.selection)
			if test.want == "" {
				if !errors.Is(err, webhookv1.ErrInvalidSubscription) {
					t.Fatalf("ValidateSubscriptions() = %v, %v", result, err)
				}
				return
			}
			if err != nil || len(result) == 0 || result[0] != test.want {
				t.Fatalf("ValidateSubscriptions() = %v, %v", result, err)
			}
		})
	}
	if !webhookv1.Subscribes([]string{"*"}, webhookv1.CaseCreated) || webhookv1.Subscribes([]string{"case.created"}, webhookv1.AppealUpdated) {
		t.Fatal("subscription matching is wrong")
	}
}

func TestEnvelopeRejectsInvalidEvents(t *testing.T) {
	t.Parallel()

	base := `{"id":"evt_01M11HEQG00000000000000000","type":"evidence.ready","schema_version":"1.0","created_at":"2026-09-18T09:00:00Z","tenant_id":"ten_01M11HEQG00000000000000000","region":"eu-west","data":{"evidence_id":"evd_01M11HEQG00000000000000000","verification_id":"ver_01M11HEQG00000000000000000"}}`
	if _, err := webhookv1.Parse([]byte(base)); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}
	for _, test := range []struct{ name, encoded string }{
		{name: "unknown type", encoded: strings.Replace(base, "evidence.ready", "evidence.unknown", 1)},
		{name: "missing field", encoded: strings.Replace(base, `"verification_id":"ver_01M11HEQG00000000000000000"`, `"other_id":"ver_01M11HEQG00000000000000000"`, 1)},
		{name: "extra field", encoded: strings.Replace(base, `"verification_id"`, `"raw_evidence":"x","verification_id"`, 1)},
		{name: "bad identifier", encoded: strings.Replace(base, "evt_01M11HEQG00000000000000000", "evt_bad", 1)},
		{name: "trailing content", encoded: base + `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := webhookv1.Parse([]byte(test.encoded)); !errors.Is(err, webhookv1.ErrInvalidEvent) {
				t.Fatalf("Parse() error = %v", err)
			}
		})
	}
}
