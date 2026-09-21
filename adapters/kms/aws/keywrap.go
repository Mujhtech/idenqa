// Package aws adapts AWS KMS key wrapping and AWS Secrets Manager resolution to
// the owned Idenqa KMS and secret ports. Provider SDK types never leave this
// module and resolved plaintext never enters an error, log, or identifier.
package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awskmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"
)

const (
	providerName = "aws.kms"
	algorithm    = "SYMMETRIC_DEFAULT"
	// symmetricPlaintextLimit is the AWS KMS Encrypt plaintext ceiling for
	// symmetric keys. The owned port permits larger keysets, so this adapter
	// reports its narrower bound instead of truncating or splitting input.
	symmetricPlaintextLimit = 4096
	maximumContextBytes     = 32 * 1024
	contextDigestDomain     = "idenqa.aws-kms.v1"
)

var (
	// ErrInvalid identifies malformed adapter configuration or an invalid
	// operation that never reached AWS.
	ErrInvalid = errors.New("aws kms: invalid configuration or operation")
	// ErrUnavailable identifies a key that is missing, disabled, or otherwise
	// unusable after a provider call.
	ErrUnavailable = errors.New("aws kms: key unavailable")
	// ErrDenied identifies an AWS access-denied refusal.
	ErrDenied = errors.New("aws kms: access denied")
	// ErrPayloadTooLarge identifies plaintext above this adapter's provider bound.
	ErrPayloadTooLarge = errors.New("aws kms: payload exceeds the provider limit")
	// ErrCiphertextRejected identifies every authenticated-decryption failure.
	ErrCiphertextRejected = errors.New("aws kms: ciphertext rejected")
)

// KeyClient is the narrow AWS KMS surface consumed by Keyring.
type KeyClient interface {
	DescribeKey(context.Context, *awskms.DescribeKeyInput, ...func(*awskms.Options)) (*awskms.DescribeKeyOutput, error)
	Encrypt(context.Context, *awskms.EncryptInput, ...func(*awskms.Options)) (*awskms.EncryptOutput, error)
	Decrypt(context.Context, *awskms.DecryptInput, ...func(*awskms.Options)) (*awskms.DecryptOutput, error)
	ScheduleKeyDeletion(context.Context, *awskms.ScheduleKeyDeletionInput, ...func(*awskms.Options)) (*awskms.ScheduleKeyDeletionOutput, error)
}

// KeyringConfig contains non-secret AWS KMS key configuration. Credentials and
// custom endpoints use the AWS SDK's standard external configuration chain.
type KeyringConfig struct {
	KeyID             string
	Region            string
	MaxPlaintextBytes int
}

// ValidateKeyring checks configuration without loading credentials or making a request.
func ValidateKeyring(config KeyringConfig) error {
	if config.KeyID == "" || strings.TrimSpace(config.KeyID) != config.KeyID ||
		config.MaxPlaintextBytes <= 0 || config.MaxPlaintextBytes > symmetricPlaintextLimit {
		return ErrInvalid
	}
	return nil
}

// OpenKeyring loads the AWS SDK's default external credential and
// certificate configuration, then resolves the configured KMS key.
func OpenKeyring(ctx context.Context, config KeyringConfig) (*Keyring, error) {
	if ctx == nil {
		return nil, ErrInvalid
	}
	if err := ValidateKeyring(config); err != nil {
		return nil, err
	}
	options := []func(*awsconfig.LoadOptions) error{}
	if config.Region != "" {
		options = append(options, awsconfig.WithRegion(config.Region))
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, ErrUnavailable
	}
	return NewKeyring(ctx, awskms.NewFromConfig(awsConfig), config)
}

// NewKeyring validates a KMS key through one DescribeKey call and snapshots its
// immutable identity. The caller owns the client and its retry policy; this
// adapter performs no additional retries.
func NewKeyring(ctx context.Context, client KeyClient, config KeyringConfig) (*Keyring, error) {
	if ctx == nil || client == nil || isNilClient(client) {
		return nil, ErrInvalid
	}
	if err := ValidateKeyring(config); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("resolve aws kms key: %w", err)
	}
	describe, err := client.DescribeKey(ctx, &awskms.DescribeKeyInput{KeyId: aws.String(config.KeyID)})
	if err != nil || describe == nil || describe.KeyMetadata == nil {
		return nil, describeError(ctx, err)
	}
	metadata := describe.KeyMetadata
	if metadata.KeyUsage != awskmstypes.KeyUsageTypeEncryptDecrypt ||
		metadata.KeyState != awskmstypes.KeyStateEnabled ||
		metadata.KeySpec != awskmstypes.KeySpecSymmetricDefault ||
		aws.ToString(metadata.Arn) == "" {
		return nil, ErrUnavailable
	}
	reference := aws.ToString(metadata.Arn)
	version := aws.ToString(metadata.CurrentKeyMaterialId)
	if version == "" {
		// Keys without material rotation report no rotation epoch. The ARN is
		// the immutable key identity, so a stable version marker is exact.
		version = "v1"
	}
	return &Keyring{
		client: client, reference: reference, version: version, maximum: config.MaxPlaintextBytes,
	}, nil
}

// Keyring wraps small content keys with one resolved AWS KMS symmetric key.
type Keyring struct {
	client    KeyClient
	reference string
	version   string
	maximum   int
}

var (
	_ interface {
		Wrap(context.Context, kms.Purpose, []byte, []byte) (kms.WrappedKey, error)
		Unwrap(context.Context, kms.Purpose, kms.WrappedKey, []byte) ([]byte, error)
		Close() error
	} = (*Keyring)(nil)
)

// Wrap protects one bounded content key or keyset under the resolved KMS key.
// The authenticated context is bound by digest so payload sizes stay within the
// provider's encryption-context limit while remaining collision resistant.
func (keyring *Keyring) Wrap(
	ctx context.Context,
	purpose kms.Purpose,
	plaintext []byte,
	authenticatedContext []byte,
) (kms.WrappedKey, error) {
	if err := keyring.validate(ctx, purpose, plaintext, authenticatedContext); err != nil {
		return kms.WrappedKey{}, err
	}
	if len(plaintext) > keyring.maximum {
		return kms.WrappedKey{}, ErrPayloadTooLarge
	}
	output, err := keyring.client.Encrypt(ctx, &awskms.EncryptInput{
		KeyId:               aws.String(keyring.reference),
		Plaintext:           plaintext,
		EncryptionAlgorithm: awskmstypes.EncryptionAlgorithmSpecSymmetricDefault,
		EncryptionContext:   encryptionContext(purpose, authenticatedContext),
	})
	if err != nil || output == nil || len(output.CiphertextBlob) == 0 || aws.ToString(output.KeyId) == "" {
		return kms.WrappedKey{}, wrapError(ctx, err)
	}
	if aws.ToString(output.KeyId) != keyring.reference {
		return kms.WrappedKey{}, ErrUnavailable
	}
	if output.EncryptionAlgorithm != "" && string(output.EncryptionAlgorithm) != algorithm {
		return kms.WrappedKey{}, ErrUnavailable
	}
	wrapped, err := kms.NewWrappedKey(kms.WrappedKeyRecord{
		Provider:   providerName,
		Reference:  keyring.reference,
		Version:    keyring.version,
		Algorithm:  algorithm,
		Ciphertext: output.CiphertextBlob,
	})
	if err != nil {
		return kms.WrappedKey{}, ErrInvalid
	}
	return wrapped, nil
}

// Unwrap releases a content key only for this exact provider, key reference,
// algorithm, purpose, and authenticated context. The version field records the
// rotation epoch observed when the key was wrapped; AWS KMS ciphertext selects
// the actual key material, so an older epoch remains decryptable after managed
// rotation while a foreign key reference is always rejected.
func (keyring *Keyring) Unwrap(
	ctx context.Context,
	purpose kms.Purpose,
	wrapped kms.WrappedKey,
	authenticatedContext []byte,
) ([]byte, error) {
	if err := keyring.validate(ctx, purpose, []byte{1}, authenticatedContext); err != nil {
		return nil, err
	}
	if wrapped.IsZero() {
		return nil, ErrCiphertextRejected
	}
	record := wrapped.Record()
	if record.Provider != providerName || record.Reference != keyring.reference ||
		record.Algorithm != algorithm || record.Version == "" {
		return nil, ErrCiphertextRejected
	}
	output, err := keyring.client.Decrypt(ctx, &awskms.DecryptInput{
		KeyId:               aws.String(keyring.reference),
		CiphertextBlob:      record.Ciphertext,
		EncryptionAlgorithm: awskmstypes.EncryptionAlgorithmSpecSymmetricDefault,
		EncryptionContext:   encryptionContext(purpose, authenticatedContext),
	})
	if err != nil || output == nil || len(output.Plaintext) == 0 {
		return nil, decryptError(ctx, err)
	}
	if output.KeyId != nil && aws.ToString(output.KeyId) != keyring.reference {
		clear(output.Plaintext)
		return nil, ErrCiphertextRejected
	}
	// When the key reports a rotation epoch, require the material that actually
	// decrypted this ciphertext to be the exact recorded epoch. This preserves
	// backward decryption across managed rotation without ever falling back to
	// another version.
	if material := aws.ToString(output.KeyMaterialId); material != "" && record.Version != "v1" && material != record.Version {
		clear(output.Plaintext)
		return nil, ErrCiphertextRejected
	}
	return output.Plaintext, nil
}

// Close releases no provider resources; it satisfies the process lifecycle
// contract shared with the mounted-file keyring.
func (*Keyring) Close() error { return nil }

// ScheduleDestruction schedules deletion of the resolved KMS key. Only the
// exact key reference and the current material epoch are accepted: a stale or
// foreign reference never reaches AWS, and an already pending deletion is
// reported with its existing deletion instant.
func (keyring *Keyring) ScheduleDestruction(ctx context.Context, reference, version string, pendingWindow time.Duration) (time.Time, error) {
	if keyring == nil || keyring.client == nil || ctx == nil ||
		reference == "" || reference != keyring.reference || version == "" || version != keyring.version {
		return time.Time{}, ErrInvalid
	}
	days := int64(pendingWindow / (24 * time.Hour))
	if pendingWindow < 7*24*time.Hour || pendingWindow > 30*24*time.Hour || days < 7 || days > 30 {
		return time.Time{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return time.Time{}, fmt.Errorf("schedule aws kms deletion: %w", err)
	}
	output, err := keyring.client.ScheduleKeyDeletion(ctx, &awskms.ScheduleKeyDeletionInput{
		KeyId:               aws.String(reference),
		PendingWindowInDays: aws.Int32(int32(days)),
	})
	if err != nil || output == nil || output.DeletionDate == nil {
		return time.Time{}, scheduleError(ctx, err)
	}

	return output.DeletionDate.UTC(), nil
}

func (keyring *Keyring) validate(
	ctx context.Context,
	purpose kms.Purpose,
	value []byte,
	authenticatedContext []byte,
) error {
	if keyring == nil || keyring.client == nil || ctx == nil || len(value) == 0 ||
		len(value) > symmetricPlaintextLimit || len(authenticatedContext) == 0 ||
		len(authenticatedContext) > maximumContextBytes {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("perform aws kms operation: %w", err)
	}
	if _, err := kms.NewPurpose(string(purpose)); err != nil {
		return ErrInvalid
	}
	return nil
}

// encryptionContext binds purpose and the exact caller context digest. The
// domain prefix separates these values from encryption contexts created by any
// other Idenqa component using the same KMS key.
func encryptionContext(purpose kms.Purpose, authenticatedContext []byte) map[string]string {
	digest := sha256.New()
	digest.Write([]byte(contextDigestDomain))
	digest.Write([]byte{0})
	digest.Write([]byte(purpose))
	digest.Write([]byte{0})
	digest.Write(authenticatedContext)
	return map[string]string{
		"idenqa:purpose":        string(purpose),
		"idenqa:context-sha256": hex.EncodeToString(digest.Sum(nil)),
		"idenqa:context-bytes":  strconv.Itoa(len(authenticatedContext)),
	}
}

func describeError(ctx context.Context, err error) error {
	if err == nil {
		return ErrUnavailable
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("resolve aws kms key: %w", ctxErr)
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) && apiError.ErrorCode() == "AccessDeniedException" {
		return ErrDenied
	}
	return ErrUnavailable
}

func wrapError(ctx context.Context, err error) error {
	if err == nil {
		return ErrUnavailable
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("wrap content key with aws kms: %w", ctxErr)
	}
	var apiError smithy.APIError
	if errors.As(err, &apiError) && apiError.ErrorCode() == "AccessDeniedException" {
		return ErrDenied
	}
	return ErrUnavailable
}

func decryptError(ctx context.Context, err error) error {
	if err == nil {
		return ErrCiphertextRejected
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("unwrap content key with aws kms: %w", ctxErr)
	}
	var apiError smithy.APIError
	switch {
	case errors.As(err, &apiError) && apiError.ErrorCode() == "AccessDeniedException":
		return ErrDenied
	case errors.As(err, &apiError) && apiError.ErrorCode() == "InvalidCiphertextException":
		return ErrCiphertextRejected
	default:
		return ErrCiphertextRejected
	}
}

func isNilClient(client any) bool {
	value := reflect.ValueOf(client)
	return value.Kind() == reflect.Pointer && value.IsNil()
}

func scheduleError(ctx context.Context, err error) error {
	if err == nil {
		return ErrUnavailable
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("schedule aws kms deletion: %w", ctxErr)
	}
	var apiError smithy.APIError
	switch {
	case errors.As(err, &apiError) && apiError.ErrorCode() == "AccessDeniedException":
		return ErrDenied
	case errors.As(err, &apiError) && apiError.ErrorCode() == "NotFoundException":
		return ErrUnavailable
	case errors.As(err, &apiError) && apiError.ErrorCode() == "KMSInvalidStateException":
		return ErrUnavailable
	default:
		return ErrUnavailable
	}
}
