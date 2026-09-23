package api

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/adapters/proposals/anthropic"
	"github.com/Mujhtech/idenqa/adapters/proposals/openaicompatible"
	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/egress"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	secretresolver "github.com/Mujhtech/idenqa/internal/platform/secret/resolver"
	"github.com/Mujhtech/idenqa/internal/proposal"
)

func configuredProposalModel(
	ctx context.Context,
	configuration config.API,
	identifiers *id.Generator,
	registry proposal.RegistryStore,
	usage proposal.GenerationUsageRecorder,
) (proposalv1.ProposalModel, proposal.GenerationBindingChecker, error) {
	if configuration.ProposalRuntimeFile == "" {
		model, err := proposal.NewReferenceModel(identifiers, clock.System{})
		return model, nil, err
	}
	if registry == nil || usage == nil {
		return nil, nil, errors.New("proposal runtime requires registry and usage persistence")
	}
	runtimes, err := config.LoadProposalRuntimes(configuration.ProposalRuntimeFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load proposal runtime: %w", err)
	}
	secrets, err := secretresolver.Open(ctx, secretresolver.Options{
		Provider: configuration.SecretsProvider, AWSRegion: configuration.SecretsAWSRegion,
		CacheTTL: configuration.SecretsCacheTTL, Now: timeNow,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("open proposal secret resolver: %w", err)
	}
	routes := make([]proposal.GenerationRoute, 0, len(runtimes.Models))
	bindings := make([]proposal.GenerationBindingRoute, 0, len(runtimes.Models))
	for _, runtime := range runtimes.Models {
		if runtime.Fixture && configuration.Environment == "production" {
			return nil, nil, errors.New("proposal runtime fixture transport is unavailable in production")
		}
		httpClient, err := egress.NewClient(runtime.Origin, runtime.CAFile, runtime.Fixture)
		if err != nil {
			return nil, nil, fmt.Errorf("construct proposal egress for %s: %w", runtime.ModelID, err)
		}
		credential, err := secret.ParseReference(runtime.CredentialReference)
		if err != nil {
			return nil, nil, errors.New("proposal runtime credential reference is invalid")
		}
		var generator proposal.Generator
		switch runtime.Adapter {
		case config.ProposalAdapterOpenAICompatible:
			generator, err = openaicompatible.New(openaicompatible.Config{
				Origin: runtime.Origin, Credential: credential, Secrets: secrets, HTTPClient: httpClient,
				MaxResponseBytes: runtime.MaxResponseBytes,
			})
		case config.ProposalAdapterAnthropic:
			generator, err = anthropic.New(anthropic.Config{
				Origin: runtime.Origin, Credential: credential, Secrets: secrets, HTTPClient: httpClient,
				APIVersion: runtime.AnthropicVersion, MaxResponseBytes: runtime.MaxResponseBytes,
			})
		default:
			err = errors.New("unsupported proposal adapter")
		}
		if err != nil {
			return nil, nil, fmt.Errorf("construct proposal adapter for %s: %w", runtime.ModelID, err)
		}
		routes = append(routes, proposal.GenerationRoute{
			ModelID: runtime.ModelID, ModelVersion: runtime.ModelVersion, PromptVersion: runtime.PromptVersion,
			Instructions: runtime.Instructions, Generator: generator, MaxOutputTokens: runtime.MaxOutputTokens,
			InputMicrosPerMillion: runtime.InputMicrosPerMillion, OutputMicrosPerMillion: runtime.OutputMicrosPerMillion,
		})
		modelRegistryID, _ := id.ParseModel(runtime.ModelRegistryID)
		promptRegistryID, _ := id.ParsePrompt(runtime.PromptRegistryID)
		bindings = append(bindings, proposal.GenerationBindingRoute{
			ModelID: runtime.ModelID, ModelVersion: runtime.ModelVersion, PromptVersion: runtime.PromptVersion,
			ModelRegistryID: modelRegistryID, ModelRegistryVersion: runtime.ModelRegistryVersion, ModelDigest: runtime.ModelDigest,
			PromptRegistryID: promptRegistryID, PromptRegistryVersion: runtime.PromptRegistryVersion,
			PromptDigest: runtime.PromptDigest, Instructions: runtime.Instructions,
		})
	}
	router, err := proposal.NewGeneratorRouter(routes)
	if err != nil {
		return nil, nil, fmt.Errorf("construct proposal model router: %w", err)
	}
	binding, err := proposal.NewGenerationBindingValidator(registry, bindings)
	if err != nil {
		return nil, nil, fmt.Errorf("construct proposal registry binding: %w", err)
	}
	model, err := proposal.NewGenerativeModel(proposal.GenerativeModelConfig{
		Generator: router, GeneratorIDs: identifiers, Clock: clock.System{}, UsageRecorder: usage,
	})
	if err != nil {
		return nil, nil, err
	}
	return model, binding, nil
}

func timeNow() time.Time { return time.Now().UTC() }
