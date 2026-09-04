package worker

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	localobjects "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
)

// EvidenceObjects is the exact ciphertext-deletion capability owned by worker.
type EvidenceObjects interface {
	Delete(context.Context, objectstore.Object) error
}

// EvidenceLifecycle releases worker-owned provider resources.
type EvidenceLifecycle interface{ Shutdown(context.Context) error }

// EvidenceInfrastructure is the provider-neutral regional deletion input.
type EvidenceInfrastructure struct {
	objects   EvidenceObjects
	lifecycle EvidenceLifecycle
}

// NewEvidenceInfrastructure validates worker evidence deletion composition.
func NewEvidenceInfrastructure(objects EvidenceObjects, lifecycle EvidenceLifecycle) (EvidenceInfrastructure, error) {
	if objects == nil || lifecycle == nil {
		return EvidenceInfrastructure{}, errors.New("worker evidence infrastructure is incomplete")
	}
	return EvidenceInfrastructure{objects: objects, lifecycle: lifecycle}, nil
}

func configuredLocalEvidence(configuration config.Worker) (EvidenceInfrastructure, error) {
	if !configuration.LocalEvidenceEnabled() {
		return EvidenceInfrastructure{}, nil
	}
	policy, err := configuration.EvidenceUploadPolicy()
	if err != nil {
		return EvidenceInfrastructure{}, err
	}
	objects, err := localobjects.Open(localobjects.Config{Directory: configuration.EvidenceLocalDirectory, MaxObjectBytes: policy.MaximumBytes() * 2})
	if err != nil {
		return EvidenceInfrastructure{}, errors.New("open worker local evidence object store")
	}
	infrastructure, err := NewEvidenceInfrastructure(objects, objectLifecycle{objects})
	if err != nil {
		_ = objects.Close()
		return EvidenceInfrastructure{}, err
	}
	return infrastructure, nil
}

type objectLifecycle struct{ closer interface{ Close() error } }

func (lifecycle objectLifecycle) Shutdown(context.Context) error { return lifecycle.closer.Close() }

func (infrastructure EvidenceInfrastructure) enabled() bool {
	return infrastructure.objects != nil && infrastructure.lifecycle != nil
}
