package aws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/aws/aws-sdk-go-v2/aws"
	awskms "github.com/aws/aws-sdk-go-v2/service/kms"
	awskmstypes "github.com/aws/aws-sdk-go-v2/service/kms/types"
	"github.com/aws/smithy-go"
)

const testKeyARN = "arn:aws:kms:us-east-1:111122223333:key/1234abcd-12ab-34cd-56ef-1234567890ab"

type apiFailure struct {
	code string
}

func (failure apiFailure) Error() string                 { return failure.code }
func (failure apiFailure) ErrorCode() string             { return failure.code }
func (failure apiFailure) ErrorMessage() string          { return failure.code }
func (failure apiFailure) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

type fakeKeyClient struct {
	metadata    *awskmstypes.KeyMetadata
	describeErr error
	encryptErr  error
	decryptErr  error
	scheduleErr error
	deletionAt  time.Time
	encrypted   map[string]fakeCiphertext
	lastEncrypt *awskms.EncryptInput
	lastDecrypt *awskms.DecryptInput
	lastDelete  *awskms.ScheduleKeyDeletionInput
}

type fakeCiphertext struct {
	plaintext []byte
	context   map[string]string
	material  string
}

func (client *fakeKeyClient) DescribeKey(context.Context, *awskms.DescribeKeyInput, ...func(*awskms.Options)) (*awskms.DescribeKeyOutput, error) {
	if client.describeErr != nil {
		return nil, client.describeErr
	}
	return &awskms.DescribeKeyOutput{KeyMetadata: client.metadata}, nil
}

func (client *fakeKeyClient) Encrypt(_ context.Context, input *awskms.EncryptInput, _ ...func(*awskms.Options)) (*awskms.EncryptOutput, error) {
	client.lastEncrypt = input
	if client.encryptErr != nil {
		return nil, client.encryptErr
	}
	digest := sha256.Sum256(append([]byte("cipher"), input.Plaintext...))
	name := hex.EncodeToString(digest[:8])
	if client.encrypted == nil {
		client.encrypted = map[string]fakeCiphertext{}
	}
	client.encrypted[name] = fakeCiphertext{
		plaintext: append([]byte(nil), input.Plaintext...),
		context:   input.EncryptionContext,
		material:  aws.ToString(client.metadata.CurrentKeyMaterialId),
	}
	return &awskms.EncryptOutput{
		KeyId:               aws.String(aws.ToString(input.KeyId)),
		CiphertextBlob:      []byte(name),
		EncryptionAlgorithm: awskmstypes.EncryptionAlgorithmSpecSymmetricDefault,
	}, nil
}

func (client *fakeKeyClient) Decrypt(_ context.Context, input *awskms.DecryptInput, _ ...func(*awskms.Options)) (*awskms.DecryptOutput, error) {
	client.lastDecrypt = input
	if client.decryptErr != nil {
		return nil, client.decryptErr
	}
	record, ok := client.encrypted[string(input.CiphertextBlob)]
	if !ok || !sameContext(record.context, input.EncryptionContext) {
		return nil, apiFailure{code: "InvalidCiphertextException"}
	}
	return &awskms.DecryptOutput{
		KeyId:               aws.String(aws.ToString(input.KeyId)),
		KeyMaterialId:       aws.String(record.material),
		Plaintext:           append([]byte(nil), record.plaintext...),
		EncryptionAlgorithm: awskmstypes.EncryptionAlgorithmSpecSymmetricDefault,
	}, nil
}

func (client *fakeKeyClient) ScheduleKeyDeletion(_ context.Context, input *awskms.ScheduleKeyDeletionInput, _ ...func(*awskms.Options)) (*awskms.ScheduleKeyDeletionOutput, error) {
	client.lastDelete = input
	if client.scheduleErr != nil {
		return nil, client.scheduleErr
	}
	deletionAt := client.deletionAt
	if deletionAt.IsZero() {
		deletionAt = time.Now().UTC().Add(7 * 24 * time.Hour)
	}
	return &awskms.ScheduleKeyDeletionOutput{
		KeyId:        input.KeyId,
		DeletionDate: aws.Time(deletionAt),
	}, nil
}

func sameContext(first, second map[string]string) bool {
	if len(first) != len(second) {
		return false
	}
	for key, value := range first {
		if second[key] != value {
			return false
		}
	}
	return true
}

func validMetadata() *awskmstypes.KeyMetadata {
	return &awskmstypes.KeyMetadata{
		Arn:                  aws.String(testKeyARN),
		KeyId:                aws.String("1234abcd-12ab-34cd-56ef-1234567890ab"),
		KeyState:             awskmstypes.KeyStateEnabled,
		KeyUsage:             awskmstypes.KeyUsageTypeEncryptDecrypt,
		KeySpec:              awskmstypes.KeySpecSymmetricDefault,
		CurrentKeyMaterialId: aws.String("material-1"),
	}
}

func mustPurpose(t *testing.T, value string) kms.Purpose {
	t.Helper()
	purpose, err := kms.NewPurpose(value)
	if err != nil {
		t.Fatalf("NewPurpose(%q) error = %v", value, err)
	}
	return purpose
}

func TestNewKeyringSnapshotsImmutableIdentity(t *testing.T) {
	t.Parallel()

	client := &fakeKeyClient{metadata: validMetadata()}
	keyring, err := NewKeyring(context.Background(), client, KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096})
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	wrapped, err := keyring.Wrap(context.Background(), mustPurpose(t, "evidence.content.v1"), []byte("content-key"), []byte(`{"context":1}`))
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	record := wrapped.Record()
	if record.Provider != providerName || record.Reference != testKeyARN ||
		record.Version != "material-1" || record.Algorithm != algorithm || len(record.Ciphertext) == 0 {
		t.Fatalf("Wrap() record = %+v", record)
	}
	if aws.ToString(client.lastEncrypt.KeyId) != testKeyARN {
		t.Fatalf("Encrypt() KeyId = %q", aws.ToString(client.lastEncrypt.KeyId))
	}
	if client.lastEncrypt.EncryptionContext["idenqa:purpose"] != "evidence.content.v1" ||
		len(client.lastEncrypt.EncryptionContext["idenqa:context-sha256"]) != 64 ||
		client.lastEncrypt.EncryptionContext["idenqa:context-bytes"] != "13" {
		t.Fatalf("Encrypt() context = %+v", client.lastEncrypt.EncryptionContext)
	}

	plaintext, err := keyring.Unwrap(context.Background(), mustPurpose(t, "evidence.content.v1"), wrapped, []byte(`{"context":1}`))
	if err != nil || string(plaintext) != "content-key" {
		t.Fatalf("Unwrap() = %q, %v", plaintext, err)
	}

	// A different authenticated context must never release the key.
	other := mustPurpose(t, "evidence.content.v1")
	if _, err := keyring.Unwrap(context.Background(), other, wrapped, []byte(`{"context":2}`)); !errors.Is(err, ErrCiphertextRejected) {
		t.Fatalf("Unwrap(other context) error = %v, want ErrCiphertextRejected", err)
	}
	// A different purpose is bound into the encryption context as well.
	if _, err := keyring.Unwrap(context.Background(), mustPurpose(t, "delivery.webhook-secret.v1"), wrapped, []byte(`{"context":1}`)); !errors.Is(err, ErrCiphertextRejected) {
		t.Fatalf("Unwrap(other purpose) error = %v, want ErrCiphertextRejected", err)
	}
}

func TestNewKeyringRejectsUnusableMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		client  *fakeKeyClient
		config  KeyringConfig
		wantErr error
	}{
		{name: "missing key id", client: &fakeKeyClient{metadata: validMetadata()}, config: KeyringConfig{MaxPlaintextBytes: 4096}, wantErr: ErrInvalid},
		{name: "oversized configuration bound", client: &fakeKeyClient{metadata: validMetadata()}, config: KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4097}, wantErr: ErrInvalid},
		{name: "disabled", client: &fakeKeyClient{metadata: func() *awskmstypes.KeyMetadata {
			metadata := validMetadata()
			metadata.KeyState = awskmstypes.KeyStateDisabled
			return metadata
		}()}, config: KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096}, wantErr: ErrUnavailable},
		{name: "asymmetric", client: &fakeKeyClient{metadata: func() *awskmstypes.KeyMetadata {
			metadata := validMetadata()
			metadata.KeySpec = awskmstypes.KeySpecRsa2048
			return metadata
		}()}, config: KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096}, wantErr: ErrUnavailable},
		{name: "signing usage", client: &fakeKeyClient{metadata: func() *awskmstypes.KeyMetadata {
			metadata := validMetadata()
			metadata.KeyUsage = awskmstypes.KeyUsageTypeSignVerify
			return metadata
		}()}, config: KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096}, wantErr: ErrUnavailable},
		{name: "denied", client: &fakeKeyClient{describeErr: apiFailure{code: "AccessDeniedException"}}, config: KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096}, wantErr: ErrDenied},
		{name: "missing", client: &fakeKeyClient{describeErr: apiFailure{code: "NotFoundException"}}, config: KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096}, wantErr: ErrUnavailable},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewKeyring(context.Background(), test.client, test.config)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("NewKeyring() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestKeyringBoundsPayloadsAndRejectsForeignMetadata(t *testing.T) {
	t.Parallel()

	client := &fakeKeyClient{metadata: validMetadata()}
	keyring, err := NewKeyring(context.Background(), client, KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 64})
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	purpose := mustPurpose(t, "evidence.content.v1")
	contextData := []byte(`{"tenant":"ten_1"}`)
	if _, err := keyring.Wrap(context.Background(), purpose, make([]byte, 65), contextData); !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("Wrap(oversized) error = %v, want ErrPayloadTooLarge", err)
	}
	if _, err := keyring.Wrap(context.Background(), purpose, nil, contextData); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Wrap(empty) error = %v, want ErrInvalid", err)
	}

	tests := map[string]kms.WrappedKeyRecord{
		"foreign provider":   {Provider: "local.file", Reference: testKeyARN, Version: "material-1", Algorithm: algorithm, Ciphertext: []byte("x")},
		"foreign reference":  {Provider: providerName, Reference: "arn:aws:kms:us-east-1:111122223333:key/other", Version: "material-1", Algorithm: algorithm, Ciphertext: []byte("x")},
		"foreign algorithm":  {Provider: providerName, Reference: testKeyARN, Version: "material-1", Algorithm: "RSAES_OAEP_SHA_256", Ciphertext: []byte("x")},
		"unknown ciphertext": {Provider: providerName, Reference: testKeyARN, Version: "material-0", Algorithm: algorithm, Ciphertext: []byte("unknown")},
	}
	for name, record := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			wrapped, err := kms.NewWrappedKey(record)
			if err != nil {
				t.Fatalf("NewWrappedKey() error = %v", err)
			}
			if _, err := keyring.Unwrap(context.Background(), purpose, wrapped, contextData); !errors.Is(err, ErrCiphertextRejected) {
				t.Fatalf("Unwrap() error = %v, want ErrCiphertextRejected", err)
			}
		})
	}

	// A rotated material epoch remains decryptable because KMS ciphertext
	// selects the material, while the key reference stays exact.
	rotated := *validMetadata()
	rotated.CurrentKeyMaterialId = aws.String("material-2")
	rotatedClient := &fakeKeyClient{metadata: &rotated}
	rotatedKeyring, err := NewKeyring(context.Background(), rotatedClient, KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096})
	if err != nil {
		t.Fatalf("NewKeyring(rotated) error = %v", err)
	}
	oldKeyring, err := NewKeyring(context.Background(), client, KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096})
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	wrapped, err := oldKeyring.Wrap(context.Background(), purpose, []byte("key-material"), contextData)
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	rotatedClient.encrypted = client.encrypted
	if wrapped.Record().Version != "material-1" {
		t.Fatalf("Wrap() version = %q", wrapped.Record().Version)
	}
	released, err := rotatedKeyring.Unwrap(context.Background(), purpose, wrapped, contextData)
	if err != nil || string(released) != "key-material" {
		t.Fatalf("Unwrap(rotated) = %q, %v", released, err)
	}
}

func TestKeyringMapsProviderFailures(t *testing.T) {
	t.Parallel()

	client := &fakeKeyClient{metadata: validMetadata()}
	keyring, err := NewKeyring(context.Background(), client, KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096})
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	purpose := mustPurpose(t, "evidence.content.v1")
	contextData := []byte(`{"tenant":"ten_1"}`)

	client.encryptErr = apiFailure{code: "AccessDeniedException"}
	if _, err := keyring.Wrap(context.Background(), purpose, []byte("key"), contextData); !errors.Is(err, ErrDenied) {
		t.Fatalf("Wrap(denied) error = %v, want ErrDenied", err)
	}
	client.encryptErr = nil

	wrapped, err := keyring.Wrap(context.Background(), purpose, []byte("key"), contextData)
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	client.decryptErr = apiFailure{code: "AccessDeniedException"}
	if _, err := keyring.Unwrap(context.Background(), purpose, wrapped, contextData); !errors.Is(err, ErrDenied) {
		t.Fatalf("Unwrap(denied) error = %v, want ErrDenied", err)
	}
	client.decryptErr = apiFailure{code: "KMSInvalidStateException"}
	if _, err := keyring.Unwrap(context.Background(), purpose, wrapped, contextData); !errors.Is(err, ErrCiphertextRejected) {
		t.Fatalf("Unwrap(state) error = %v, want ErrCiphertextRejected", err)
	}
	if err := keyring.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestScheduleDestructionRequiresExactIdentity(t *testing.T) {
	t.Parallel()

	client := &fakeKeyClient{metadata: validMetadata()}
	keyring, err := NewKeyring(context.Background(), client, KeyringConfig{KeyID: testKeyARN, MaxPlaintextBytes: 4096})
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	if _, err := keyring.ScheduleDestruction(context.Background(), "arn:aws:kms:us-east-1:111122223333:key/foreign", "material-1", 7*24*time.Hour); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ScheduleDestruction(foreign) error = %v, want ErrInvalid", err)
	}
	if _, err := keyring.ScheduleDestruction(context.Background(), testKeyARN, "material-0", 7*24*time.Hour); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ScheduleDestruction(stale) error = %v, want ErrInvalid", err)
	}
	if _, err := keyring.ScheduleDestruction(context.Background(), testKeyARN, "material-1", 6*24*time.Hour); !errors.Is(err, ErrInvalid) {
		t.Fatalf("ScheduleDestruction(short window) error = %v, want ErrInvalid", err)
	}
	deletionAt, err := keyring.ScheduleDestruction(context.Background(), testKeyARN, "material-1", 7*24*time.Hour)
	if err != nil || deletionAt.IsZero() {
		t.Fatalf("ScheduleDestruction() deletionAt=%v error=%v", deletionAt, err)
	}
	if client.lastDelete == nil || aws.ToString(client.lastDelete.KeyId) != testKeyARN ||
		aws.ToInt32(client.lastDelete.PendingWindowInDays) != 7 {
		t.Fatalf("ScheduleKeyDeletion input = %+v", client.lastDelete)
	}
	client.scheduleErr = apiFailure{code: "AccessDeniedException"}
	if _, err := keyring.ScheduleDestruction(context.Background(), testKeyARN, "material-1", 7*24*time.Hour); !errors.Is(err, ErrDenied) {
		t.Fatalf("ScheduleDestruction(denied) error = %v, want ErrDenied", err)
	}
}
