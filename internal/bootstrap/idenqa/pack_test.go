package idenqa_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

const packFixtureJSON = `{"pack":{"schema_major":1,"schema_minor":0,"country":"NG","country_alpha3":"NGA","authority":"idenqa.requirement.authority","revision":1,"published_at":"2026-09-20T00:00:00Z","digest":"` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `","lifecycle_state":"active","legal_review":{"state":"not_reviewed"},"requirement_slots":[{"name":"authority","key":"idenqa.requirement.authority"}],"documents":[{"type":"driver_licence","known_versions":[],"required_sides":[],"supported_fields":[],"security_checks":[],"barcode":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"mrz":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"nfc":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"model_and_parser_requirements":[],"evaluation_coverage":{"state":"not_evaluated","references":[]},"support_level":"unsupported","known_limitations":["pending_public_structural_source"],"evidence":[],"assurance_mappings":[{"state":"not_mapped","reason":"no_pack_specific_assurance_profile"}]},{"type":"passport","known_versions":[{"version":"icao-9303-td3","reference":"ICAO Doc 9303 Part 4"}],"required_sides":["front"],"supported_fields":["document_number"],"security_checks":[],"barcode":{"state":"unknown","formats":[],"reason":"no_public_structural_source"},"mrz":{"state":"supported","formats":["td3"],"reference":"ICAO Doc 9303 Part 4"},"nfc":{"state":"unknown","formats":[],"reason":"nfc_not_implemented"},"model_and_parser_requirements":[{"kind":"parser","reference":"idenqa.document.mrz","version":"v1"}],"evaluation_coverage":{"state":"not_evaluated","references":[]},"support_level":"structurally_supported","known_limitations":["no_legal_review"],"evidence":["core_mrz_parser_v1"],"assurance_mappings":[{"state":"not_mapped","reason":"no_pack_specific_assurance_profile"}]}]},"lifecycle_state":"active","version":0}`

const packDocumentJSON = `{"type":"passport","known_versions":[{"version":"icao-9303-td3","reference":"ICAO Doc 9303 Part 4"}],"required_sides":["front"],"supported_fields":["document_number"],"security_checks":[],"barcode":{"state":"unknown","formats":[],"reason":"no_public_structural_source"},"mrz":{"state":"supported","formats":["td3"],"reference":"ICAO Doc 9303 Part 4"},"nfc":{"state":"unknown","formats":[],"reason":"nfc_not_implemented"},"model_and_parser_requirements":[{"kind":"parser","reference":"idenqa.document.mrz","version":"v1"}],"evaluation_coverage":{"state":"not_evaluated","references":[]},"support_level":"structurally_supported","known_limitations":["no_legal_review"],"evidence":["core_mrz_parser_v1"],"assurance_mappings":[{"state":"not_mapped","reason":"no_pack_specific_assurance_profile"}]}`

const packSupportJSON = `{"country":"NG","country_alpha3":"NGA","document_type":"driver_licence","pack_revision":1,"pack_digest":"` + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" + `","lifecycle_state":"active","legal_review":{"state":"not_reviewed"},"support_level":"unsupported","known_versions":[],"required_sides":[],"supported_fields":[],"security_checks":[],"barcode":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"mrz":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"nfc":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"model_and_parser_requirements":[],"evaluation_coverage":{"state":"not_evaluated","references":[]},"known_limitations":["pending_public_structural_source"],"evidence":[],"assurance_mappings":[{"state":"not_mapped","reason":"no_pack_specific_assurance_profile"}],"requirement_slots":[{"name":"authority","key":"idenqa.requirement.authority"}],"authority":"idenqa.requirement.authority"}`

func TestPackCLICommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "environment-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-pack-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		arguments []string
		method    string
		path      string
		query     url.Values
		response  string
		expected  string
	}{
		{
			name:      "pack list",
			arguments: []string{"pack", "list"},
			method:    http.MethodGet,
			path:      "/v1/packs",
			response:  `{"data":[{"country":"NG"}],"page":{"has_more":false}}`,
			expected:  `{"data":[{"country":"NG"}],"page":{"has_more":false}}`,
		},
		{
			name:      "pack country",
			arguments: []string{"pack", "country", "ng"},
			method:    http.MethodGet,
			path:      "/v1/packs/NG",
			response:  packFixtureJSON,
			expected:  packFixtureJSON,
		},
		{
			name:      "pack document synthesizes the matching entry",
			arguments: []string{"pack", "document", "NG", "passport"},
			method:    http.MethodGet,
			path:      "/v1/packs/NG",
			response:  packFixtureJSON,
			expected:  packDocumentJSON,
		},
		{
			name:      "pack document accepts the canonical spelling",
			arguments: []string{"pack", "document", "NG", "drivers_license"},
			method:    http.MethodGet,
			path:      "/v1/packs/NG",
			response:  packFixtureJSON,
			expected:  `{"type":"driver_licence","known_versions":[],"required_sides":[],"supported_fields":[],"security_checks":[],"barcode":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"mrz":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"nfc":{"state":"unknown","formats":[],"reason":"public_structural_source_not_cited"},"model_and_parser_requirements":[],"evaluation_coverage":{"state":"not_evaluated","references":[]},"support_level":"unsupported","known_limitations":["pending_public_structural_source"],"evidence":[],"assurance_mappings":[{"state":"not_mapped","reason":"no_pack_specific_assurance_profile"}]}`,
		},
		{
			name:      "pack support",
			arguments: []string{"pack", "support", "nga", "driver_licence"},
			method:    http.MethodGet,
			path:      "/v1/document-support",
			query:     url.Values{"country": {"NGA"}, "type": {"driver_licence"}},
			response:  packSupportJSON,
			expected:  packSupportJSON,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if got, want := r.URL.Query().Encode(), test.query.Encode(); got != want {
					t.Errorf("query = %q, want %q", got, want)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-pack-credential" {
					t.Errorf("authorization = %q", got)
				}
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, test.response)
			}))
			defer server.Close()
			var out, diagnostics bytes.Buffer
			args := append([]string(nil), test.arguments...)
			args = append(args, "--api-url", server.URL, "--api-key-file", keyFile)
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if strings.Contains(out.String()+diagnostics.String(), "test-pack-credential") {
				t.Fatal("credential leaked")
			}
			var got, want any
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("stdout = %q: %v", out.String(), err)
			}
			if err := json.Unmarshal([]byte(test.expected), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("stdout = %s, want %s", out.String(), test.expected)
			}
		})
	}
}

func TestPackCLIFailuresAndValidation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-pack-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, "private-server-response")
	}))
	defer server.Close()

	for _, test := range []struct {
		name string
		args []string
	}{
		{"missing country", []string{"pack", "country"}},
		{"invalid country", []string{"pack", "country", "X"}},
		{"invalid country digits", []string{"pack", "country", "12"}},
		{"invalid type", []string{"pack", "document", "NG", "permit"}},
		{"invalid support type", []string{"pack", "support", "NG", "licence"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := calls.Load()
			args := append(append([]string(nil), test.args...), "--api-url", server.URL)
			var out, diagnostics bytes.Buffer
			if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 2 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if calls.Load() != before {
				t.Fatal("invalid input reached the API")
			}
			if !strings.Contains(diagnostics.String(), "Usage:") {
				t.Fatalf("usage error did not print usage: %s", diagnostics.String())
			}
		})
	}

	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run([]string{"pack", "list", "--api-url", server.URL}, &out, &diagnostics, buildinfo.Info{}); code != 1 || calls.Load() != 1 {
		t.Fatalf("runtime failure exit = %d calls = %d stderr = %s", code, calls.Load(), diagnostics.String())
	}
	if strings.Contains(diagnostics.String(), "private-server-response") || !strings.Contains(diagnostics.String(), "status 503") {
		t.Fatalf("runtime failure = %q", diagnostics.String())
	}
}
