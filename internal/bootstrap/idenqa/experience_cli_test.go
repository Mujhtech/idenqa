package idenqa_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

const experienceFixtureID = "exp_01J00000000000000000000000"

func experienceFixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "..", "contracts", "experience", "v1", "testdata", name)
}

func TestExperienceCLIHTTPCommands(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-experience-credential")
	for _, test := range []struct {
		name      string
		arguments []string
		method    string
		path      string
		body      map[string]any
		status    int
	}{
		{
			name: "list", arguments: []string{"experience", "list"}, method: http.MethodGet,
			path: "/v1/experiences", status: 200,
		},
		{
			name: "get", arguments: []string{"experience", "get", experienceFixtureID}, method: http.MethodGet,
			path: "/v1/experiences/" + experienceFixtureID, status: 200,
		},
		{
			name: "approve", arguments: []string{"experience", "approve", experienceFixtureID, "--expected-version", "2", "--reason", "compliance"},
			method: http.MethodPost, path: "/v1/experiences/" + experienceFixtureID + "/approve",
			body: map[string]any{"expected_version": float64(2), "reason": "compliance"}, status: 200,
		},
		{
			name: "publish", arguments: []string{"experience", "publish", experienceFixtureID, "--expected-version", "3"},
			method: http.MethodPost, path: "/v1/experiences/" + experienceFixtureID + "/publish",
			body: map[string]any{"expected_version": float64(3)}, status: 200,
		},
		{
			name: "revoke", arguments: []string{"experience", "revoke", experienceFixtureID, "--expected-version", "4", "--reason", "incident"},
			method: http.MethodPost, path: "/v1/experiences/" + experienceFixtureID + "/revoke",
			body: map[string]any{"expected_version": float64(4), "reason": "incident"}, status: 200,
		},
		{
			name: "rollback", arguments: []string{"experience", "rollback", experienceFixtureID, "--expected-version", "5", "--target-version", "1", "--reason", "restore"},
			method: http.MethodPost, path: "/v1/experiences/" + experienceFixtureID + "/rollback",
			body: map[string]any{"expected_version": float64(5), "target_version": float64(1), "reason": "restore"}, status: 200,
		},
		{
			name: "export", arguments: []string{"experience", "export", experienceFixtureID}, method: http.MethodGet,
			path: "/v1/experiences/" + experienceFixtureID + "/export", status: 200,
		},
		{
			name: "import", arguments: []string{"experience", "import", "--file", experienceFixturePath(t, "exported-manifest.json")},
			method: http.MethodPost, path: "/v1/experiences/import", status: 201,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requestBody map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s, want %s %s", r.Method, r.URL.Path, test.method, test.path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-experience-credential" {
					t.Errorf("authorization = %q", got)
				}
				requestBody = nil
				if r.Body != nil {
					_ = json.NewDecoder(r.Body).Decode(&requestBody)
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(`{"id":"` + experienceFixtureID + `","state":"draft","revision":1,"latest_version":1,"approved_version":0,"published_version":0}`))
			}))
			defer server.Close()
			arguments := append(append([]string(nil), test.arguments...), "--api-url", server.URL)
			var out, diagnostics bytes.Buffer
			if code := bootstrap.Run(arguments, &out, &diagnostics, buildinfo.Info{}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			if test.body != nil && !jsonEqual(requestBody, test.body) {
				t.Fatalf("request body = %#v, want %#v", requestBody, test.body)
			}
		})
	}
}

func jsonEqual(left, right map[string]any) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func TestExperienceCLILocalValidateAndResolve(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		want      []string
	}{
		{
			name: "validate document", arguments: []string{"experience", "validate", "--file", experienceFixturePath(t, "document.json")},
			want: []string{`"valid":true`, `"experience_id":"` + experienceFixtureID + `"`, `"signed":false`},
		},
		{
			name: "validate manifest", arguments: []string{"experience", "validate", "--file", experienceFixturePath(t, "exported-manifest.json")},
			want: []string{`"valid":true`, `"signed":true`, `"key_id":"expkey_test_1"`, `"digest_matches":true`},
		},
		{
			name: "resolve match", arguments: []string{"experience", "resolve", "--file", experienceFixturePath(t, "exported-manifest.json"), "--workflow", "capture.identity", "--country", "NG", "--application-id", "dev.acme.app", "--sdk-version", "1.4.0"},
			want: []string{`"matched":true`, `"specificity":4`, `"experience_id":"` + experienceFixtureID + `"`},
		},
		{
			name: "resolve no match", arguments: []string{"experience", "resolve", "--file", experienceFixturePath(t, "document.json"), "--workflow", "capture.identity", "--country", "ZA"},
			want: []string{`"matched":false`, `"fallback":true`},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			if code := bootstrap.Run(test.arguments, &out, &diagnostics, buildinfo.Info{}); code != 0 {
				t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
			}
			for _, expected := range test.want {
				if !bytes.Contains(out.Bytes(), []byte(expected)) {
					t.Fatalf("output = %s, want %s", out.String(), expected)
				}
			}
		})
	}
}

func TestExperienceCLIRejectsInvalidInput(t *testing.T) {
	for _, test := range []struct {
		name      string
		arguments []string
		code      int
	}{
		{name: "invalid id", arguments: []string{"experience", "get", "not-an-experience"}, code: 2},
		{name: "missing version", arguments: []string{"experience", "publish", experienceFixtureID}, code: 2},
		{name: "missing reason", arguments: []string{"experience", "revoke", experienceFixtureID, "--expected-version", "1"}, code: 2},
		{name: "missing file", arguments: []string{"experience", "validate"}, code: 1},
		{name: "missing rollback target", arguments: []string{"experience", "rollback", experienceFixtureID, "--expected-version", "1"}, code: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out, diagnostics bytes.Buffer
			if code := bootstrap.Run(test.arguments, &out, &diagnostics, buildinfo.Info{}); code != test.code {
				t.Fatalf("exit = %d, want %d (stderr = %s)", code, test.code, diagnostics.String())
			}
		})
	}
}
