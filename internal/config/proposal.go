package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

const (
	// ProposalAdapterOpenAICompatible selects the shared chat-completions adapter.
	ProposalAdapterOpenAICompatible = "openai_compatible"
	// ProposalAdapterAnthropic selects the Anthropic Messages adapter.
	ProposalAdapterAnthropic = "anthropic"
)

var proposalModelToken = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,63}$`)
var proposalDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ProposalRuntime binds one logical model/version to one provider protocol.
// CredentialReference is a non-secret secret:// reference.
type ProposalRuntime struct {
	ModelID                string `json:"model_id"`
	ModelVersion           string `json:"model_version"`
	PromptVersion          string `json:"prompt_version"`
	Instructions           string `json:"instructions"`
	ModelRegistryID        string `json:"model_registry_id"`
	ModelRegistryVersion   int64  `json:"model_registry_version"`
	ModelDigest            string `json:"model_digest"`
	PromptRegistryID       string `json:"prompt_registry_id"`
	PromptRegistryVersion  int64  `json:"prompt_registry_version"`
	PromptDigest           string `json:"prompt_digest"`
	Adapter                string `json:"adapter"`
	Origin                 string `json:"origin"`
	CredentialReference    string `json:"credential_reference"`
	CAFile                 string `json:"ca_file,omitempty"`
	AnthropicVersion       string `json:"anthropic_version,omitempty"`
	MaxOutputTokens        int    `json:"max_output_tokens,omitempty"`
	MaxResponseBytes       int64  `json:"max_response_bytes,omitempty"`
	InputMicrosPerMillion  int64  `json:"input_micros_per_million,omitempty"`
	OutputMicrosPerMillion int64  `json:"output_micros_per_million,omitempty"`
	Fixture                bool   `json:"fixture,omitempty"`
}

// ProposalRuntimes is the closed, operator-mounted generative-model route set.
type ProposalRuntimes struct {
	Models []ProposalRuntime `json:"models"`
}

// LoadProposalRuntimes reads and validates up to 32 exact model routes.
func LoadProposalRuntimes(path string) (ProposalRuntimes, error) {
	var runtimes ProposalRuntimes
	if err := ReadClosedFile(path, &runtimes, 512<<10); err != nil {
		return ProposalRuntimes{}, err
	}
	if len(runtimes.Models) == 0 || len(runtimes.Models) > 32 {
		return ProposalRuntimes{}, errors.New("proposal runtime requires one to 32 models")
	}
	seen := make(map[string]struct{}, len(runtimes.Models))
	for index := range runtimes.Models {
		route := &runtimes.Models[index]
		if !proposalModelToken.MatchString(route.ModelID) || !validProposalRouteVersion(route.ModelVersion) ||
			!validProposalRouteVersion(route.PromptVersion) || route.Origin == "" {
			return ProposalRuntimes{}, errors.New("proposal runtime contains an invalid model route")
		}
		if strings.TrimSpace(route.Instructions) != route.Instructions || route.Instructions == "" || len(route.Instructions) > 16*1024 ||
			route.ModelRegistryVersion < 1 || !proposalDigest.MatchString(route.ModelDigest) ||
			route.PromptRegistryVersion < 1 || !proposalDigest.MatchString(route.PromptDigest) {
			return ProposalRuntimes{}, errors.New("proposal runtime contains invalid registry binding")
		}
		promptSum := sha256.Sum256([]byte(route.Instructions))
		if hex.EncodeToString(promptSum[:]) != route.PromptDigest {
			return ProposalRuntimes{}, errors.New("proposal runtime instructions do not match the prompt digest")
		}
		if _, err := id.ParseModel(route.ModelRegistryID); err != nil {
			return ProposalRuntimes{}, errors.New("proposal runtime contains an invalid model registry identifier")
		}
		if _, err := id.ParsePrompt(route.PromptRegistryID); err != nil {
			return ProposalRuntimes{}, errors.New("proposal runtime contains an invalid prompt registry identifier")
		}
		if route.Adapter != ProposalAdapterOpenAICompatible && route.Adapter != ProposalAdapterAnthropic {
			return ProposalRuntimes{}, errors.New("proposal runtime contains an unsupported adapter")
		}
		if _, err := secret.ParseReference(route.CredentialReference); err != nil {
			return ProposalRuntimes{}, errors.New("proposal runtime contains an invalid credential reference")
		}
		if route.Adapter != ProposalAdapterAnthropic && route.AnthropicVersion != "" {
			return ProposalRuntimes{}, errors.New("proposal runtime contains adapter-specific fields for the wrong adapter")
		}
		if route.MaxOutputTokens == 0 {
			route.MaxOutputTokens = 2048
		}
		if route.MaxOutputTokens < 1 || route.MaxOutputTokens > 8192 || route.MaxResponseBytes < 0 || route.MaxResponseBytes > 1024*1024 ||
			route.InputMicrosPerMillion < 0 || route.InputMicrosPerMillion > 1_000_000_000 ||
			route.OutputMicrosPerMillion < 0 || route.OutputMicrosPerMillion > 1_000_000_000 {
			return ProposalRuntimes{}, errors.New("proposal runtime contains invalid resource limits")
		}
		key := route.ModelID + "\x00" + route.ModelVersion + "\x00" + route.PromptVersion
		if _, duplicate := seen[key]; duplicate {
			return ProposalRuntimes{}, errors.New("proposal runtime contains duplicate model routes")
		}
		seen[key] = struct{}{}
	}
	return runtimes, nil
}

func validProposalRouteVersion(value string) bool {
	if len(value) < 1 || len(value) > 64 || strings.TrimSpace(value) != value || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}
