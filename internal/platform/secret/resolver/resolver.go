// Package resolver selects the configured secret provider for a process. The
// mounted-file resolver remains the default; AWS Secrets Manager is selected
// only when the operator explicitly configures a region.
package resolver

import (
	"context"
	"errors"
	"fmt"
	"time"

	awskms "github.com/Mujhtech/idenqa/adapters/kms/aws"
	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/Mujhtech/idenqa/internal/platform/secret/file"
)

// Options is the validated process configuration for secret resolution.
type Options struct {
	Provider  string
	AWSRegion string
	CacheTTL  time.Duration
	Now       func() time.Time
}

// Open constructs the selected resolver. Unknown or incomplete configuration
// fails closed and never falls back to another provider.
func Open(ctx context.Context, options Options) (secret.Resolver, error) {
	if ctx == nil {
		return nil, errors.New("secret resolver: context is required")
	}
	if options.Now == nil {
		return nil, errors.New("secret resolver: clock is required")
	}
	provider := options.Provider
	if provider == "" {
		// Programmatic construction without the documented environment default
		// keeps the unchanged mounted-file resolver.
		provider = "file"
	}
	var source secret.Resolver
	switch provider {
	case "file":
		source = file.New()
	case "aws":
		if options.AWSRegion == "" {
			return nil, errors.New("secret resolver: AWS region is required")
		}
		awsResolver, err := awskms.OpenSecrets(ctx, awskms.SecretsConfig{Region: options.AWSRegion})
		if err != nil {
			return nil, fmt.Errorf("secret resolver: open AWS Secrets Manager: %w", err)
		}
		source = awsResolver
	default:
		return nil, fmt.Errorf("secret resolver: provider %q is not supported", options.Provider)
	}
	if options.CacheTTL <= 0 {
		return source, nil
	}
	cache, err := secret.NewCache(source, options.CacheTTL, options.Now)
	if err != nil {
		return nil, errors.New("secret resolver: cache configuration is invalid")
	}

	return cache, nil
}
