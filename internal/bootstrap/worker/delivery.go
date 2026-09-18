package worker

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/transport/callback"
)

// DeliveryInfrastructure supplies the owned key-wrapping, signing and callback
// boundaries. Alternate distributions may inject a regional KMS without changing
// delivery contracts.
type DeliveryInfrastructure struct {
	wrapper   platformcrypto.KeyWrapper
	unwrapper platformcrypto.KeyUnwrapper
	sender    deliverytask.Sender
	lifecycle EvidenceLifecycle
}

// NewDeliveryInfrastructure validates explicit webhook runtime dependencies.
func NewDeliveryInfrastructure(wrapper platformcrypto.KeyWrapper, unwrapper platformcrypto.KeyUnwrapper, sender deliverytask.Sender, lifecycle EvidenceLifecycle) (DeliveryInfrastructure, error) {
	if wrapper == nil || unwrapper == nil || sender == nil || lifecycle == nil {
		return DeliveryInfrastructure{}, errors.New("worker delivery infrastructure is incomplete")
	}
	return DeliveryInfrastructure{wrapper: wrapper, unwrapper: unwrapper, sender: sender, lifecycle: lifecycle}, nil
}

func configuredLocalDelivery(configuration config.Worker) (DeliveryInfrastructure, error) {
	if configuration.EvidenceLocalKeyringFile == "" {
		return DeliveryInfrastructure{}, nil
	}
	keys, err := localkms.Open(configuration.EvidenceLocalKeyringFile)
	if err != nil {
		return DeliveryInfrastructure{}, errors.New("open worker webhook signing keyring")
	}
	sender := callback.Client{Resolver: net.DefaultResolver, Dialer: &net.Dialer{Timeout: 10 * time.Second}, Timeout: 15 * time.Second}
	return NewDeliveryInfrastructure(keys, keys, sender, keyringLifecycle{keys})
}

type keyringLifecycle struct{ keys interface{ Close() error } }

func (lifecycle keyringLifecycle) Shutdown(context.Context) error { return lifecycle.keys.Close() }
func (infrastructure DeliveryInfrastructure) enabled() bool {
	return infrastructure.unwrapper != nil && infrastructure.sender != nil && infrastructure.lifecycle != nil
}
