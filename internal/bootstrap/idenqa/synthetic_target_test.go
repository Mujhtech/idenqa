package idenqa

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/managedtenant"
)

func TestManagedSyntheticRunnerUsesConfiguredLocalTarget(t *testing.T) {
	t.Parallel()
	var untrustedCalls, localCalls atomic.Int32
	untrusted := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		untrustedCalls.Add(1)
	}))
	t.Cleanup(untrusted.Close)
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		localCalls.Add(1)
		if request.Header.Get("Authorization") != "Bearer local-ephemeral-credential" {
			t.Error("configured local Core did not receive its credential")
		}
		// Even the trusted local server cannot redirect this credential.
		w.Header().Set("Location", untrusted.URL+"/credential-receiver")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(local.Close)
	base, err := url.Parse(local.URL)
	if err != nil {
		t.Fatal(err)
	}
	host, portText, err := net.SplitHostPort(base.Host)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := newManagedSyntheticRunner(config.API{HTTPHost: host, HTTPPort: uint16(port), HTTPTLSMode: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := os.ReadFile("../../../examples/synthetic/capture-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile("../../../examples/synthetic/policy.json")
	if err != nil {
		t.Fatal(err)
	}
	_, err = runner.Run(t.Context(), managedtenant.SyntheticRunRequest{
		RunID: "sjr_security_regression", CoreEndpoint: untrusted.URL,
		Region: syntheticRegion, ProfileDocument: profile, PolicyDocument: policy, TimeoutSeconds: 1,
	}, "local-ephemeral-credential")
	if err == nil || !strings.Contains(err.Error(), "status 307") || localCalls.Load() != 1 || untrustedCalls.Load() != 0 {
		t.Fatalf("local calls=%d untrusted calls=%d error=%v", localCalls.Load(), untrustedCalls.Load(), err)
	}
	if strings.Contains(err.Error(), "local-ephemeral-credential") || strings.Contains(err.Error(), untrusted.URL) {
		t.Fatal("request failure disclosed credential or untrusted destination")
	}
}

func TestManagedSyntheticRunnerLocalListenerConfiguration(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		host, mode, want string
	}{
		{host: "127.0.0.1", mode: "disabled", want: "http://127.0.0.1:8080"},
		{host: "0.0.0.0", mode: "disabled", want: "http://127.0.0.1:8080"},
		{host: "::", mode: "disabled", want: "http://[::1]:8080"},
		{host: "[::]", mode: "disabled", want: "http://[::1]:8080"},
		{host: "10.0.0.8", mode: "disabled", want: "http://10.0.0.8:8080"},
		{host: "core.internal.example", mode: "file", want: "https://core.internal.example:8080"},
	} {
		t.Run(test.want, func(t *testing.T) {
			t.Parallel()
			runner, err := newManagedSyntheticRunner(config.API{HTTPHost: test.host, HTTPPort: 8080, HTTPTLSMode: test.mode})
			if err != nil || runner.base.String() != test.want {
				t.Fatalf("runner=%+v error=%v", runner, err)
			}
		})
	}
	for _, configuration := range []config.API{
		{}, {HTTPHost: "127.0.0.1", HTTPTLSMode: "disabled"},
		{HTTPHost: "127.0.0.1", HTTPPort: 8080, HTTPTLSMode: "unknown"},
	} {
		if _, err := newManagedSyntheticRunner(configuration); err == nil {
			t.Fatal("unconfigured local target was accepted")
		}
	}
}

func TestSyntheticClientNeverFollowsCredentialRedirects(t *testing.T) {
	t.Parallel()
	for _, upload := range []bool{false, true} {
		t.Run(strconv.FormatBool(upload), func(t *testing.T) {
			t.Parallel()
			var forwarded atomic.Int32
			untrusted := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { forwarded.Add(1) }))
			t.Cleanup(untrusted.Close)
			local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.Header.Get("Authorization") == "" || !strings.HasPrefix(request.URL.Path, "/self-hosted/v1/") {
					t.Errorf("operator-selected API prefix or credential lost: %s", request.URL.Path)
				}
				w.Header().Set("Location", untrusted.URL+"/receiver")
				w.WriteHeader(http.StatusTemporaryRedirect)
			}))
			t.Cleanup(local.Close)
			base, err := parsePublicAPIURL(local.URL + "/self-hosted")
			if err != nil {
				t.Fatal(err)
			}
			client := &syntheticClient{
				base: base, credential: "tenant-key", out: io.Discard,
				http: &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
			}
			if upload {
				err = client.putEvidence(context.Background(), "capture-token", "", syntheticEvidenceUpload{ID: "upl_fixture"}, []byte("synthetic evidence"))
			} else {
				_, err = client.call(context.Background(), syntheticRequest{step: "profile.create", method: http.MethodPost, path: "/v1/capture-profiles", body: struct{}{}, wantStatus: http.StatusCreated})
			}
			if err == nil || forwarded.Load() != 0 {
				t.Fatalf("redirect forwarded credential: calls=%d error=%v", forwarded.Load(), err)
			}
		})
	}
}
