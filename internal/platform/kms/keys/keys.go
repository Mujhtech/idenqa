// Package keys selects the configured KMS provider for a process and composes
// the owned wrapping port with manual constructor injection. The mounted local
// file keyring remains the default; AWS KMS is selected only when the operator
// explicitly configures a key and region.
package keys

import (
	"context"
	"errors"
	"fmt"

	awskms "github.com/Mujhtech/idenqa/adapters/kms/aws"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
)

// Options is the validated process configuration for key wrapping.
type Options struct {
	Provider             string
	LocalKeyringFile     string
	AWSKeyID             string
	AWSRegion            string
	AWSMaxPlaintextBytes int
}

// Keys is the process-owned wrapping capability. Close releases provider
// resources after all in-flight work has stopped.
type Keys interface {
	platformcrypto.KeyWrapper
	platformcrypto.KeyUnwrapper
	Close() error
}

// Enabled reports whether a key provider is configured for this process.
func Enabled(options Options) bool {
	return options.Provider == "aws" || options.LocalKeyringFile != ""
}

// Open constructs the selected provider. Unknown or incomplete configuration
// fails closed and never falls back to another provider.
func Open(ctx context.Context, options Options) (Keys, error) {
	if ctx == nil {
		return nil, errors.New("kms keys: context is required")
	}
	provider := options.Provider
	if provider == "" {
		// Programmatic construction without the documented environment default
		// keeps the unchanged local provider.
		provider = "local"
	}
	switch provider {
	case "local":
		if options.LocalKeyringFile == "" {
			return nil, errors.New("kms keys: local keyring file is required")
		}
		keyring, err := localkms.Open(options.LocalKeyringFile)
		if err != nil {
			return nil, errors.New("kms keys: open local keyring")
		}
		return keyring, nil
	case "aws":
		if options.AWSKeyID == "" || options.AWSRegion == "" || options.AWSMaxPlaintextBytes < 1 {
			return nil, errors.New("kms keys: complete AWS KMS configuration is required")
		}
		keyring, err := awskms.OpenKeyring(ctx, awskms.KeyringConfig{
			KeyID:             options.AWSKeyID,
			Region:            options.AWSRegion,
			MaxPlaintextBytes: options.AWSMaxPlaintextBytes,
		})
		if err != nil {
			return nil, fmt.Errorf("kms keys: open AWS KMS keyring: %w", err)
		}
		return keyring, nil
	default:
		return nil, fmt.Errorf("kms keys: provider %q is not supported", options.Provider)
	}
}
