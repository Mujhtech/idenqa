package aws

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/smithy-go"
)

const (
	secretsProvider = "aws"
	// secretsVersionStageSelector distinguishes an AWS version stage such as
	// AWSCURRENT from an exact version-id reference.
	versionIDLength = 36
)

var (
	// ErrSecretsInvalid identifies malformed secret-manager configuration or an
	// invalid reference that never reached AWS.
	ErrSecretsInvalid = errors.New("aws secrets: invalid configuration or reference")
	// ErrSecretsUnavailable identifies an AWS Secrets Manager availability
	// failure or an unusable returned payload.
	ErrSecretsUnavailable = errors.New("aws secrets: unavailable")
)

// SecretsClient is the narrow AWS Secrets Manager surface consumed by Secrets.
type SecretsClient interface {
	GetSecretValue(context.Context, *awssm.GetSecretValueInput, ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error)
}

// SecretsConfig contains non-secret AWS Secrets Manager configuration.
type SecretsConfig struct {
	Region string
}

// OpenSecrets loads the AWS SDK's default external credential and certificate
// configuration, then constructs the Secrets Manager resolver.
func OpenSecrets(ctx context.Context, config SecretsConfig) (*Secrets, error) {
	if ctx == nil {
		return nil, ErrSecretsInvalid
	}
	options := []func(*awsconfig.LoadOptions) error{}
	if config.Region != "" {
		options = append(options, awsconfig.WithRegion(config.Region))
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, ErrSecretsUnavailable
	}
	return NewSecrets(awssm.NewFromConfig(awsConfig))
}

// NewSecrets constructs a resolver around an already configured client. The
// caller owns the client and its retry policy; this adapter adds no retries.
func NewSecrets(client SecretsClient) (*Secrets, error) {
	if client == nil || isNilClient(client) {
		return nil, ErrSecretsInvalid
	}
	return &Secrets{client: client}, nil
}

// Secrets resolves secret://aws/<secret-id> references, optionally pinned to a
// version stage or exact version id with ?version=.
type Secrets struct{ client SecretsClient }

var _ secret.Resolver = (*Secrets)(nil)

// Resolve releases the exact bounded payload for reference. A well-formed
// 36-character version selector is treated as an AWS version id; any other
// selector names a version stage such as AWSCURRENT.
func (resolver *Secrets) Resolve(ctx context.Context, reference secret.Reference) (secret.Value, error) {
	if resolver == nil || resolver.client == nil || ctx == nil || reference.IsZero() {
		return secret.Value{}, secret.ErrInvalid
	}
	if reference.Provider() != secretsProvider {
		return secret.Value{}, secret.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return secret.Value{}, fmt.Errorf("resolve aws secret: %w", err)
	}
	name := strings.TrimPrefix(reference.Path(), "/")
	if name == "" || strings.Contains(name, "//") {
		return secret.Value{}, secret.ErrInvalid
	}
	input := &awssm.GetSecretValueInput{SecretId: aws.String(name)}
	if selector := reference.Version(); selector != "" {
		if isVersionID(selector) {
			input.VersionId = aws.String(selector)
		} else {
			input.VersionStage = aws.String(selector)
		}
	}
	output, err := resolver.client.GetSecretValue(ctx, input)
	if err != nil {
		return secret.Value{}, secretsError(ctx, err)
	}
	if output == nil {
		return secret.Value{}, secret.ErrUnavailable
	}
	payload, ok := payload(output)
	if !ok {
		return secret.Value{}, secret.ErrInvalid
	}
	version := aws.ToString(output.VersionId)
	value, err := secret.NewValue(reference, version, payload)
	clear(payload)
	if err != nil {
		return secret.Value{}, secret.ErrInvalid
	}
	return value, nil
}

func payload(output *awssm.GetSecretValueOutput) ([]byte, bool) {
	if output.SecretString != nil {
		raw := []byte(aws.ToString(output.SecretString))
		if len(raw) == 0 || len(raw) > secret.MaxPayloadBytes {
			clear(raw)
			return nil, false
		}
		return raw, true
	}
	if len(output.SecretBinary) == 0 || len(output.SecretBinary) > secret.MaxPayloadBytes {
		return nil, false
	}
	return append([]byte(nil), output.SecretBinary...), true
}

func isVersionID(value string) bool {
	if len(value) != versionIDLength {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character == '-' {
			continue
		}
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') &&
			(character < 'A' || character > 'F') {
			return false
		}
	}
	return true
}

func secretsError(ctx context.Context, err error) error {
	if err == nil {
		return secret.ErrUnavailable
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("resolve aws secret: %w", ctxErr)
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		switch apiError.ErrorCode() {
		case "ResourceNotFoundException":
			return secret.ErrNotFound
		case "AccessDeniedException":
			return secret.ErrDenied
		case "InvalidParameterException", "InvalidRequestException":
			return secret.ErrInvalid
		case "DecryptionFailureException", "InternalServiceErrorException":
			return secret.ErrUnavailable
		}
	}
	return secret.ErrUnavailable
}
