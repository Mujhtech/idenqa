package api

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
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

func configuredLocalEvidence(configuration config.API) (EvidenceInfrastructure, error) {
	if !configuration.LocalEvidenceEnabled() {
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
	keys, err := localkms.Open(configuration.EvidenceLocalKeyringFile)
	if err != nil {
		_ = objects.Close()

		return EvidenceInfrastructure{}, errors.New("open local evidence keyring")
	}
	lifecycle := &localEvidenceLifecycle{objects: objects, keys: keys}
	infrastructure, err := NewEvidenceInfrastructure(objects, keys, lifecycle)
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
