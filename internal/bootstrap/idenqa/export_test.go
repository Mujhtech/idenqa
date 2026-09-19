package idenqa_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestExportTenantCLIStreamsAndVerifies(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "export-credential")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("export-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	stream := tenantExportStream(
		`{"record":"tenant","id":"ten_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`,
		`{"record":"capture_profiles","id":"prf_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`,
	)
	var query string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/tenant/export" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer export-credential" {
			t.Errorf("authorization = %q", got)
		}
		if got := request.Header.Get("Accept"); got != "application/x-ndjson" {
			t.Errorf("accept = %q", got)
		}
		query = request.URL.RawQuery
		writer.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = writer.Write(stream)
	}))
	defer server.Close()

	var out, diagnostics bytes.Buffer
	code := bootstrap.Run([]string{
		"export", "tenant", "--output", "-",
		"--collections", "tenant,capture_profiles",
		"--api-url", server.URL, "--api-key-file", keyFile,
	}, &out, &diagnostics, buildinfo.Info{})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
	}
	if out.String() != string(stream) {
		t.Fatalf("stdout = %q, want %q", out.String(), stream)
	}
	if query != "collections=tenant%2Ccapture_profiles" {
		t.Fatalf("query = %q", query)
	}
	if strings.Contains(out.String()+diagnostics.String(), "export-credential") {
		t.Fatal("credential leaked")
	}
}

func TestExportTenantCLIWritesOwnerOnlyFile(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "export-credential")
	stream := tenantExportStream(`{"record":"tenant","id":"ten_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(stream)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "tenant.ndjson")

	var out, diagnostics bytes.Buffer
	code := bootstrap.Run([]string{
		"export", "tenant", "--output", output,
		"--api-url", server.URL,
	}, &out, &diagnostics, buildinfo.Info{})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
	}
	material, err := os.ReadFile(output) //nolint:gosec // Test-owned temporary output path.
	if err != nil {
		t.Fatal(err)
	}
	if string(material) != string(stream) {
		t.Fatalf("file = %q, want %q", material, stream)
	}
	info, err := os.Stat(output)
	if err != nil {
		t.Fatal(err)
	}
	if permission := info.Mode().Perm(); permission != 0600 {
		t.Fatalf("file mode = %o, want 0600", permission)
	}
}

func TestExportTenantCLIFailsClosed(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "export-credential")
	tests := []struct {
		name      string
		response  func(writer http.ResponseWriter)
		prepare   func(t *testing.T, directory string) string
		arguments func(output string) []string
		wantCode  int
		wantErr   string
	}{
		{
			name: "digest mismatch",
			response: func(writer http.ResponseWriter) {
				_, _ = writer.Write([]byte("{\"record\":\"header\"}\n{\"record\":\"footer\",\"counts\":{},\"digest\":\"sha256:" +
					strings.Repeat("0", 64) + "\"}\n"))
			},
			arguments: func(string) []string { return []string{"--output", "-"} },
			wantCode:  1,
			wantErr:   "digest",
		},
		{
			name: "missing footer",
			response: func(writer http.ResponseWriter) {
				_, _ = writer.Write([]byte("{\"record\":\"header\"}\n"))
			},
			arguments: func(string) []string { return []string{"--output", "-"} },
			wantCode:  1,
			wantErr:   "footer is missing",
		},
		{
			name: "trailing record",
			response: func(writer http.ResponseWriter) {
				stream := tenantExportStream()
				_, _ = writer.Write(append(stream, []byte("{\"record\":\"tenant\"}\n")...))
			},
			arguments: func(string) []string { return []string{"--output", "-"} },
			wantCode:  1,
			wantErr:   "after its footer",
		},
		{
			name: "overwrite refused",
			response: func(writer http.ResponseWriter) {
				_, _ = writer.Write(tenantExportStream())
			},
			prepare: func(t *testing.T, directory string) string {
				t.Helper()
				path := filepath.Join(directory, "existing.ndjson")
				if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
				return path
			},
			arguments: func(output string) []string { return []string{"--output", output} },
			wantCode:  1,
			wantErr:   "exclusive",
		},
		{
			name: "unknown collection",
			response: func(writer http.ResponseWriter) {
				_, _ = writer.Write(tenantExportStream())
			},
			arguments: func(string) []string {
				return []string{"--output", "-", "--collections", "secrets"}
			},
			wantCode: 2,
			wantErr:  "collections",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				test.response(writer)
			}))
			defer server.Close()
			output := ""
			if test.prepare != nil {
				output = test.prepare(t, t.TempDir())
			}
			arguments := append(test.arguments(output), "--api-url", server.URL)
			var out, diagnostics bytes.Buffer
			code := bootstrap.Run(
				append([]string{"export", "tenant"}, arguments...),
				&out, &diagnostics, buildinfo.Info{},
			)
			if code != test.wantCode {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, test.wantCode, diagnostics.String())
			}
			if !strings.Contains(diagnostics.String(), test.wantErr) {
				t.Fatalf("stderr = %q, want %q", diagnostics.String(), test.wantErr)
			}
			if output != "" && test.wantCode != 0 {
				material, err := os.ReadFile(output)
				if err != nil {
					t.Fatalf("overwrite-refused output was removed: %v", err)
				}
				if string(material) != "original" {
					t.Fatalf("output = %q, want original content", material)
				}
			}
		})
	}
}

func TestExportTenantCLIForceOverwrites(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "export-credential")
	stream := tenantExportStream()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write(stream)
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "existing.ndjson")
	if err := os.WriteFile(output, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}

	var out, diagnostics bytes.Buffer
	code := bootstrap.Run([]string{
		"export", "tenant", "--output", output, "--force",
		"--api-url", server.URL,
	}, &out, &diagnostics, buildinfo.Info{})
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, diagnostics.String())
	}
	material, err := os.ReadFile(output) //nolint:gosec // Test-owned temporary output path.
	if err != nil {
		t.Fatal(err)
	}
	if string(material) != string(stream) {
		t.Fatalf("output = %q, want replaced export", material)
	}
}

func TestExportTenantCLIRemovesFailedFile(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "export-credential")
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("{\"record\":\"header\"}\n"))
	}))
	defer server.Close()
	output := filepath.Join(t.TempDir(), "partial.ndjson")

	var out, diagnostics bytes.Buffer
	code := bootstrap.Run([]string{
		"export", "tenant", "--output", output,
		"--api-url", server.URL,
	}, &out, &diagnostics, buildinfo.Info{})
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if _, err := os.Stat(output); !os.IsNotExist(err) {
		t.Fatalf("failed export left %s behind", output)
	}
}

func tenantExportStream(records ...string) []byte {
	body := &bytes.Buffer{}
	digest := sha256.New()
	write := func(line string) {
		body.WriteString(line)
		body.WriteString("\n")
		_, _ = digest.Write([]byte(line))
		_, _ = digest.Write([]byte{'\n'})
	}
	write(`{"record":"header","schema":"idenqa.tenant-export","schema_version":1}`)
	for _, record := range records {
		write(record)
	}
	fmt.Fprintf(body, "{\"record\":\"footer\",\"counts\":{},\"digest\":\"sha256:%s\"}\n", hex.EncodeToString(digest.Sum(nil)))
	return body.Bytes()
}
