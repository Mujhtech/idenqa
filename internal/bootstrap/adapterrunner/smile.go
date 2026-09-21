package adapterrunner

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/egress"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

func runtimeManifest(name string) (providerv1.Manifest, error) {
	switch name {
	case "", "dojah":
		return dojah.Description(), nil
	case "smileid":
		return smileid.Description(), nil
	default:
		return providerv1.Manifest{}, errors.New("unsupported provider adapter")
	}
}

type mountedInput struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
	Value     string `json:"value"`
}
type smileAdapter struct {
	*scopedAdapter
	inputs []mountedInput
	client *smileHTTP
}

func newSmileAdapter(base *scopedAdapter) (*smileAdapter, *http.Client, error) {
	settings := base.settings
	if !settings.Fixture && settings.UploadOrigin != "https://smile-uploads-test.s3.us-west-2.amazonaws.com" {
		return nil, nil, errors.New("unreviewed Smile ID upload origin")
	}
	if base.credentials == nil && !partnerIDPattern.MatchString(base.appID.Load()) {
		return nil, nil, smileid.ErrConfiguration
	}
	upload, err := egress.NewClient(settings.UploadOrigin, settings.UploadCAFile, settings.Fixture)
	if err != nil {
		return nil, nil, err
	}
	var inputs []mountedInput
	if err := config.ReadClosedFile(settings.InputsFile, &inputs, 4096); err != nil {
		return nil, nil, err
	}
	if len(inputs) != 2 {
		return nil, nil, smileid.ErrConfiguration
	}
	names := map[string]bool{}
	refs := map[string]bool{}
	for _, input := range inputs {
		if (input.Name != "idenqa.input.country" && input.Name != "idenqa.input.id_type") || names[input.Name] || input.Reference == "" || refs[input.Reference] || !regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`).MatchString(input.Value) {
			return nil, nil, smileid.ErrConfiguration
		}
		if input.Name == "idenqa.input.country" && !slices.Contains([]string{"NG", "GH", "KE", "ZA"}, input.Value) {
			return nil, nil, smileid.ErrConfiguration
		}
		names[input.Name] = true
		refs[input.Reference] = true
	}
	adapter := &smileAdapter{base, inputs, &smileHTTP{base.providerHTTP, upload, settings.BaseURL, settings.UploadOrigin}}
	implementation, err := smileid.New(adapter, adapter, &gatewayReader{adapter: base}, adapter.client, nil, time.Now)
	if err != nil {
		return nil, nil, err
	}
	if err := implementation.ValidateConfiguration(context.Background(), settings.Configuration); err != nil {
		return nil, nil, err
	}
	return adapter, upload, nil
}
func (*smileAdapter) Manifest(context.Context) (providerv1.Manifest, error) {
	return smileid.Description(), nil
}

// smileConfiguration returns the requested credentials, including any value
// published by a completed reload or resolved from the selected secret
// provider. An invalid partner identifier fails closed.
func (adapter *smileAdapter) smileConfiguration(ctx context.Context, reference, version string) (smileid.Config, error) {
	if adapter.credentials != nil {
		value, err := adapter.resolveConfiguration(ctx, reference, version)
		if err != nil {
			return smileid.Config{}, smileid.ErrConfiguration
		}
		return smileConfiguration(adapter.settings, value)
	}
	partnerID := adapter.appID.Load()
	if !partnerIDPattern.MatchString(partnerID) {
		return smileid.Config{}, smileid.ErrConfiguration
	}
	return smileid.Config{BaseURL: adapter.settings.BaseURL, PartnerID: partnerID, APIKey: adapter.apiKey.Load(), CallbackURL: adapter.settings.CallbackURL, Mode: "sandbox", Region: "africa", PollInterval: 5 * time.Second}, nil
}

func (adapter *smileAdapter) ResolveSmileID(ctx context.Context, reference, version string) (smileid.Config, error) {
	return adapter.smileConfiguration(ctx, reference, version)
}

// smileConfiguration builds the runner-owned Smile ID configuration from one
// resolved tenant credential bundle. The bundled values never leave the runner.
func smileConfiguration(settings Settings, value secret.Value) (smileid.Config, error) {
	credentials, err := parseTenantCredentials(value)
	if err != nil {
		return smileid.Config{}, err
	}
	return smileid.Config{BaseURL: settings.BaseURL, PartnerID: credentials.PartnerID, APIKey: credentials.APIKey,
		CallbackURL: settings.CallbackURL, Mode: "sandbox", Region: "africa", PollInterval: 5 * time.Second}, nil
}
func (adapter *smileAdapter) ResolveProviderInput(_ context.Context, reference string) (string, error) {
	for _, input := range adapter.inputs {
		if input.Reference == reference {
			return input.Value, nil
		}
	}
	return "", smileid.ErrConfiguration
}
func (*smileAdapter) Execute(context.Context, providerv1.Request) (providerv1.Result, error) {
	return providerv1.Result{}, errors.New("smileid requires asynchronous execution")
}
func (adapter *smileAdapter) Advance(ctx context.Context, request providerv1.Request, resume bool) (providerv1.Progress, error) {
	if request.Validate() != nil || request.TenantID != adapter.settings.TenantID || !adapter.acceptsConfiguration(request.Configuration) || request.Check != "idenqa.check.document_biometric" || len(request.Inputs) != 2 {
		return providerv1.Progress{}, smileid.ErrConfiguration
	}
	for _, reference := range request.Inputs {
		matched := false
		for _, input := range adapter.inputs {
			if reference.Name == input.Name && reference.Reference == input.Reference {
				matched = true
			}
		}
		if !matched {
			return providerv1.Progress{}, smileid.ErrConfiguration
		}
	}
	implementation, err := smileid.New(adapter, adapter, &gatewayReader{adapter: adapter.scopedAdapter, request: request}, adapter.client, nil, time.Now)
	if err != nil {
		return providerv1.Progress{}, err
	}
	return implementation.Advance(ctx, request, resume)
}

func (adapter *smileAdapter) VerifyCallback(ctx context.Context, request providerv1.Request, callback providerv1.CallbackEnvelope) (providerv1.Progress, error) {
	if request.Validate() != nil || request.TenantID != adapter.settings.TenantID || !adapter.acceptsConfiguration(request.Configuration) {
		return providerv1.Progress{}, smileid.ErrConfiguration
	}
	implementation, err := smileid.New(adapter, adapter, &gatewayReader{adapter: adapter.scopedAdapter, request: request}, adapter.client, nil, time.Now)
	if err != nil {
		return providerv1.Progress{}, err
	}
	return implementation.VerifyCallback(ctx, request, callback)
}

type smileHTTP struct {
	api, upload             *http.Client
	apiOrigin, uploadOrigin string
}

func (client *smileHTTP) Do(request *http.Request) (*http.Response, error) {
	origin := request.URL.Scheme + "://" + request.URL.Host
	if request.Method == http.MethodPost && origin == client.apiOrigin && (request.URL.Path == "/v1/upload" || request.URL.Path == "/v1/job_status") && request.URL.RawQuery == "" {
		return client.api.Do(request) // #nosec G704 -- method/path/origin allowlist plus destination-bound DNS-pinned egress transport.
	}
	if request.Method == http.MethodPut && origin == client.uploadOrigin {
		return client.upload.Do(request) // #nosec G704 -- job-bound upload path is validated by adapter and reviewed origin/DNS pinned by egress transport.
	}
	return nil, errors.New("smileid egress denied")
}
