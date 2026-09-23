package idenqa

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestRegistryCredentialRotationPreservesVersionAndReference(t *testing.T) {
	client, err := NewClient("https://core.example.test", "fixture", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		var body ProviderCredentialRotationRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.ExpectedVersion != 7 || body.Credential.SecretReference != "secret://fixture/provider" || body.Credential.CredentialVersion != "v2" {
			t.Fatalf("rotation body = %+v", body)
		}
		if r.Header.Get("Idempotency-Key") != "" {
			t.Error("rotation has no published retry-key contract")
		}
		return &http.Response{StatusCode: 409, Header: http.Header{"Content-Type": {"application/problem+json"}, "X-Request-Id": {"conflict-request"}}, Body: io.NopCloser(strings.NewReader(`{"code":"version_conflict","status":409}`))}, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.RotateProviderRegistrationCredential(context.Background(), "pvr_01M11HEQG00000000000000000", ProviderCredentialRotationRequest{
		ExpectedVersion: 7, Reason: "rotate fixture",
		Credential: ProviderCredentialRotation{SecretReference: "secret://fixture/provider", CredentialVersion: "v2"},
	})
	var problem *APIError
	if !errors.As(err, &problem) || problem.Problem.Code != "version_conflict" {
		t.Fatalf("error = %v", err)
	}
}

func TestRegistryOperations(t *testing.T) {
	tests := []struct {
		name, method, path, query string
		keyed, body, array        bool
		call                      func(context.Context, *Client) (ResponseMetadata, error)
	}{
		{name: "GetModel", method: "GET", path: "/mount/v1/models/a..b", keyed: false, body: false, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.GetModel(ctx, "a..b")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "GetModelRevision", method: "GET", path: "/mount/v1/models/a..b/revisions/threshold/2", keyed: false, body: false, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.GetModelRevision(ctx, "a..b", ThresholdRevision, 2)
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "ListModelHistory", method: "GET", path: "/mount/v1/models/a..b/history", keyed: false, body: false, array: true, query: "before=7&limit=3", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.ListModelHistory(ctx, "a..b", ModelHistoryOptions{Limit: 3, Before: 7})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "RegisterModel", method: "POST", path: "/mount/v1/models/a..b/register", keyed: true, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.RegisterModel(ctx, "a..b", ModelRegistrationWrite{}, MutationOptions{IdempotencyKey: "registry-retry"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "SetModelThreshold", method: "POST", path: "/mount/v1/models/a..b/threshold", keyed: true, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.SetModelThreshold(ctx, "a..b", ModelThresholdWrite{}, MutationOptions{IdempotencyKey: "registry-retry"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "ActivateModel", method: "POST", path: "/mount/v1/models/a..b/activate", keyed: true, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.ActivateModel(ctx, "a..b", ModelDeploymentWrite{}, MutationOptions{IdempotencyKey: "registry-retry"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "RollbackModel", method: "POST", path: "/mount/v1/models/a..b/rollback", keyed: true, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.RollbackModel(ctx, "a..b", ModelDeploymentWrite{}, MutationOptions{IdempotencyKey: "registry-retry"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "RetireModel", method: "POST", path: "/mount/v1/models/a..b/retire", keyed: true, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.RetireModel(ctx, "a..b", ModelRetirementWrite{}, MutationOptions{IdempotencyKey: "registry-retry"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "ValidateModel", method: "POST", path: "/mount/v1/models/a..b/validate", keyed: false, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.ValidateModel(ctx, "a..b", ModelValidationRequest{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "ListProviderRegistrations", method: "GET", path: "/mount/v1/providers", keyed: false, body: false, array: false, query: "cursor=next+cursor&limit=3", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.ListProviderRegistrations(ctx, PaginationOptions{Limit: 3, Cursor: "next cursor"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "CreateProviderRegistration", method: "POST", path: "/mount/v1/providers", keyed: true, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.CreateProviderRegistration(ctx, ProviderRegistrationCreate{}, MutationOptions{IdempotencyKey: "registry-retry"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "GetProviderRegistration", method: "GET", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000", keyed: false, body: false, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.GetProviderRegistration(ctx, "pvr_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "UpdateProviderRegistration", method: "PUT", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000", keyed: false, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.UpdateProviderRegistration(ctx, "pvr_01M11HEQG00000000000000000", ProviderRegistrationUpdate{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "ValidateProviderRegistration", method: "POST", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000/validate", keyed: false, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.ValidateProviderRegistration(ctx, "pvr_01M11HEQG00000000000000000", ProviderRegistrationWrite{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "EnableProviderRegistration", method: "POST", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000/enable", keyed: false, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.EnableProviderRegistration(ctx, "pvr_01M11HEQG00000000000000000", ProviderRegistrationToggle{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "DisableProviderRegistration", method: "POST", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000/disable", keyed: false, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.DisableProviderRegistration(ctx, "pvr_01M11HEQG00000000000000000", ProviderRegistrationToggle{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "RotateProviderRegistrationCredential", method: "POST", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000/rotate-credential", keyed: false, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.RotateProviderRegistrationCredential(ctx, "pvr_01M11HEQG00000000000000000", ProviderCredentialRotationRequest{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "GetProviderRegistrationHealth", method: "GET", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000/health", keyed: false, body: false, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.GetProviderRegistrationHealth(ctx, "pvr_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
		{name: "SimulateProviderFailure", method: "POST", path: "/mount/v1/providers/pvr_01M11HEQG00000000000000000/failure-simulations", keyed: false, body: true, array: false, query: "", call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			r, err := client.SimulateProviderFailure(ctx, "pvr_01M11HEQG00000000000000000", ProviderFailureSimulationRequest{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return r.ResponseMetadata, nil
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			client, err := NewClient("https://core.example.test/mount", "fixture", &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Method != tt.method || r.URL.Path != tt.path || r.URL.RawQuery != tt.query {
					t.Errorf("request = %s %s", r.Method, r.URL)
				}
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing authorization")
				}
				wantKey := ""
				if tt.keyed {
					wantKey = `"registry-retry"`
				}
				if r.Header.Get("Idempotency-Key") != wantKey {
					t.Errorf("key = %q", r.Header.Get("Idempotency-Key"))
				}
				if tt.body {
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Fatal(err)
					}
					if len(body) == 0 {
						t.Error("missing typed request body")
					}
				} else if r.Body != nil {
					t.Error("unexpected request body")
				}
				payload := "{}"
				if tt.array {
					payload = "[]"
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"X-Request-Id": {"registry-request"}, "Etag": {`"v2"`}}, Body: io.NopCloser(strings.NewReader(payload))}, nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := tt.call(context.Background(), client)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || metadata.RequestID != "registry-request" || metadata.ETag != `"v2"` {
				t.Fatalf("calls=%d metadata=%+v", calls, metadata)
			}
		})
	}
}

func TestRegistryRejectsInvalidInputsBeforeTransport(t *testing.T) {
	client, err := NewClient("https://core.example.test", "fixture", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("invalid request reached transport")
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, name := range []string{"", "../escape", "Upper", "a/b", "a?query", "a%2fb", strings.Repeat("a", 65)} {
		if _, err := client.GetModel(ctx, name); err == nil {
			t.Errorf("accepted model name %q", name)
		}
	}
	for _, revision := range []int64{0, -1, 9007199254740992} {
		if _, err := client.GetModelRevision(ctx, "fixture", ModelRevision, revision); err == nil {
			t.Errorf("accepted revision %d", revision)
		}
	}
	if _, err := client.GetModelRevision(ctx, "fixture", "unknown", 1); err == nil {
		t.Error("accepted revision kind")
	}
	for _, options := range []ModelHistoryOptions{{Before: -1}, {Before: 9007199254740991}, {Limit: -1}, {Limit: 101}} {
		if _, err := client.ListModelHistory(ctx, "fixture", options); err == nil {
			t.Errorf("accepted options %+v", options)
		}
	}
	if _, err := client.GetProviderRegistration(ctx, "ver_01M11HEQG00000000000000000"); err == nil {
		t.Error("accepted wrong identifier prefix")
	}
	if _, err := client.RegisterModel(ctx, "fixture", ModelRegistrationWrite{}, MutationOptions{}); err == nil {
		t.Error("accepted missing retry key")
	}
	if _, err := client.CreateProviderRegistration(ctx, ProviderRegistrationCreate{}, MutationOptions{}); err == nil {
		t.Error("accepted missing retry key")
	}
	for _, path := range []string{"../escape", "v1/%2e%2e/escape", "v1/../escape"} {
		if err := client.DoJSON(ctx, "GET", path, nil, nil); err == nil {
			t.Errorf("accepted traversal %q", path)
		}
	}
}
