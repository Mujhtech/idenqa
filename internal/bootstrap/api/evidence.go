package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/kms/keys"
	localobjects "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
)

// EvidenceObjects is the exact immutable-ciphertext capability consumed by
// evidence ingress. Provider SDK types remain behind adapters implementing it.
type EvidenceObjects interface {
	evidence.ObjectWriter
	evidence.ObjectDeleter
}

// EvidenceKeys is the exact wrapping capability consumed by streaming content
// protection. Implementations never expose reusable plaintext key material.
type EvidenceKeys interface {
	platformcrypto.KeyWrapper
	platformcrypto.KeyUnwrapper
}

// EvidenceLifecycle releases provider resources owned by the API process.
type EvidenceLifecycle interface {
	Shutdown(context.Context) error
}

// EvidenceInfrastructure is the provider-neutral composition input used by
// the root-local API and independently versioned production distributions.
type EvidenceInfrastructure struct {
	objects   EvidenceObjects
	keys      EvidenceKeys
	lifecycle EvidenceLifecycle
}

// NewEvidenceInfrastructure validates a provider composition before process
// construction transfers lifecycle ownership to the API.
func NewEvidenceInfrastructure(
	objects EvidenceObjects,
	keys EvidenceKeys,
	lifecycle EvidenceLifecycle,
) (EvidenceInfrastructure, error) {
	if objects == nil || keys == nil || lifecycle == nil {
		return EvidenceInfrastructure{}, errors.New("api evidence infrastructure is incomplete")
	}

	return EvidenceInfrastructure{objects: objects, keys: keys, lifecycle: lifecycle}, nil
}

func configuredEvidence(ctx context.Context, configuration config.API) (EvidenceInfrastructure, error) {
	keyOptions := keys.Options{
		Provider:             configuration.KMSProvider,
		LocalKeyringFile:     configuration.EvidenceLocalKeyringFile,
		AWSKeyID:             configuration.KMSAWSKeyID,
		AWSRegion:            configuration.KMSAWSRegion,
		AWSMaxPlaintextBytes: configuration.KMSAWSMaxPlaintextBytes,
	}
	if !configuration.EvidenceLocalObjectsEnabled() {
		if keys.Enabled(keyOptions) {
			return EvidenceInfrastructure{}, errors.New("selected KMS provider requires a local evidence directory in the root API")
		}

		return EvidenceInfrastructure{}, nil
	}
	policy, err := configuration.EvidenceUploadPolicy()
	if err != nil {
		return EvidenceInfrastructure{}, err
	}
	// The v1 plaintext ceiling is bounded at 64 MiB. Twice that limit leaves a
	// conservative, deterministic allowance for streaming-AEAD framing.
	objects, err := localobjects.Open(localobjects.Config{
		Directory:      configuration.EvidenceLocalDirectory,
		MaxObjectBytes: policy.MaximumBytes() * 2,
	})
	if err != nil {
		return EvidenceInfrastructure{}, errors.New("open local evidence object store")
	}
	keyring, err := keys.Open(ctx, keyOptions)
	if err != nil {
		_ = objects.Close()

		return EvidenceInfrastructure{}, err
	}
	lifecycle := &localEvidenceLifecycle{objects: objects, keys: keyring}
	infrastructure, err := NewEvidenceInfrastructure(objects, keyring, lifecycle)
	if err != nil {
		_ = lifecycle.Shutdown(context.Background())

		return EvidenceInfrastructure{}, fmt.Errorf("compose local evidence infrastructure: %w", err)
	}

	return infrastructure, nil
}

type localEvidenceLifecycle struct {
	objects interface{ Close() error }
	keys    interface{ Close() error }
}

func (lifecycle *localEvidenceLifecycle) Shutdown(ctx context.Context) error {
	_ = ctx
	return errors.Join(lifecycle.keys.Close(), lifecycle.objects.Close())
}

func (infrastructure EvidenceInfrastructure) enabled() bool {
	return infrastructure.objects != nil && infrastructure.keys != nil && infrastructure.lifecycle != nil
}
