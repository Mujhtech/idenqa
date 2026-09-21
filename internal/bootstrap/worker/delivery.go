package worker

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/kms/keys"
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

func configuredDelivery(ctx context.Context, configuration config.Worker) (DeliveryInfrastructure, error) {
	options := keys.Options{
		Provider:             configuration.KMSProvider,
		LocalKeyringFile:     configuration.EvidenceLocalKeyringFile,
		AWSKeyID:             configuration.KMSAWSKeyID,
		AWSRegion:            configuration.KMSAWSRegion,
		AWSMaxPlaintextBytes: configuration.KMSAWSMaxPlaintextBytes,
	}
	if !keys.Enabled(options) {
		return DeliveryInfrastructure{}, nil
	}
	keyring, err := keys.Open(ctx, options)
	if err != nil {
		return DeliveryInfrastructure{}, err
	}
	sender := callback.Client{Resolver: net.DefaultResolver, Dialer: &net.Dialer{Timeout: 10 * time.Second}, Timeout: 15 * time.Second}
	return NewDeliveryInfrastructure(keyring, keyring, sender, keyringLifecycle{keyring})
}

type keyringLifecycle struct{ keys interface{ Close() error } }

func (lifecycle keyringLifecycle) Shutdown(context.Context) error { return lifecycle.keys.Close() }
func (infrastructure DeliveryInfrastructure) enabled() bool {
	return infrastructure.unwrapper != nil && infrastructure.sender != nil && infrastructure.lifecycle != nil
}
