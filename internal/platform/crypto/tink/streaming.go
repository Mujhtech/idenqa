// Package tink provides the reviewed Tink Streaming AEAD adapter behind Idenqa-owned values.
package tink

import (
	"bytes"
	"context"
	"errors"
	"io"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	"github.com/tink-crypto/tink-go/v2/keyset"
	"github.com/tink-crypto/tink-go/v2/streamingaead"
)

const (
	formatVersion    = 1
	contentAlgorithm = "AES256_GCM_HKDF_1MB"
)

var (
	// ErrSealFailed is safe to return when content encryption does not complete.
	ErrSealFailed = errors.New("crypto: content sealing failed")
	// ErrOpenFailed intentionally combines malformed envelopes, unavailable
	// keys, wrong context, truncation, and authentication failure.
	ErrOpenFailed = errors.New("crypto: content opening failed")
)

// Streaming seals and opens evidence streams with one fresh Tink keyset per object.
type Streaming struct {
	wrapper   platformcrypto.KeyWrapper
	unwrapper platformcrypto.KeyUnwrapper
	purpose   kms.Purpose
}

// NewStreaming constructs an adapter for one stable namespaced key purpose.
func NewStreaming(
	wrapper platformcrypto.KeyWrapper,
	unwrapper platformcrypto.KeyUnwrapper,
	purpose kms.Purpose,
) (*Streaming, error) {
	if wrapper == nil || unwrapper == nil {
		return nil, errors.New("crypto: streaming dependencies are required")
	}
	if _, err := kms.NewPurpose(string(purpose)); err != nil {
		return nil, errors.New("crypto: streaming purpose is invalid")
	}

	return &Streaming{wrapper: wrapper, unwrapper: unwrapper, purpose: purpose}, nil
}

// Seal writes ciphertext incrementally and returns its authenticated envelope.
func (streaming *Streaming) Seal(
	ctx context.Context,
	destination io.Writer,
	plaintext io.Reader,
	authenticatedContext platformcrypto.Context,
) (platformcrypto.Envelope, error) {
	if err := validateStreams(ctx, destination, plaintext, authenticatedContext); err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	handle, err := keyset.NewHandle(streamingaead.AES256GCMHKDF1MBKeyTemplate())
	if err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	serialized, err := serializeKeyset(handle)
	if err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	defer clear(serialized)
	contextData := authenticatedContext.Data()
	defer clear(contextData)
	wrapped, err := streaming.wrapper.Wrap(ctx, streaming.purpose, serialized, contextData)
	if err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	primitive, err := streamingaead.New(handle)
	if err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	writer, err := primitive.NewEncryptingWriter(destination, contextData)
	if err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	if err := copyWithContext(ctx, writer, plaintext); err != nil {
		_ = writer.Close()
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	if err := writer.Close(); err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}
	if err := ctx.Err(); err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}

	envelope, err := platformcrypto.NewEnvelope(platformcrypto.EnvelopeRecord{
		FormatVersion:        formatVersion,
		ContentAlgorithm:     contentAlgorithm,
		Purpose:              string(streaming.purpose),
		WrappedKey:           wrapped.Record(),
		ContextSchemaVersion: authenticatedContext.SchemaVersion(),
		ContextDigest:        string(authenticatedContext.Digest()),
	})
	if err != nil {
		return platformcrypto.Envelope{}, ErrSealFailed
	}

	return envelope, nil
}

// Open authenticates and writes plaintext incrementally. Callers must not
// commit downstream effects until this method returns nil because a later
// ciphertext segment can still report truncation or authentication failure.
func (streaming *Streaming) Open(
	ctx context.Context,
	destination io.Writer,
	ciphertext io.Reader,
	envelope platformcrypto.Envelope,
	authenticatedContext platformcrypto.Context,
) error {
	if err := validateStreams(ctx, destination, ciphertext, authenticatedContext); err != nil || envelope.IsZero() {
		return ErrOpenFailed
	}
	record := envelope.Record()
	if record.FormatVersion != formatVersion || record.ContentAlgorithm != contentAlgorithm ||
		record.Purpose != string(streaming.purpose) ||
		record.ContextSchemaVersion != authenticatedContext.SchemaVersion() ||
		record.ContextDigest != string(authenticatedContext.Digest()) {
		return ErrOpenFailed
	}
	contextData := authenticatedContext.Data()
	defer clear(contextData)
	serialized, err := streaming.unwrapper.Unwrap(ctx, streaming.purpose, envelopeWrappedKey(envelope), contextData)
	if err != nil {
		return ErrOpenFailed
	}
	defer clear(serialized)
	handle, err := insecurecleartextkeyset.Read(keyset.NewBinaryReader(bytes.NewReader(serialized)))
	if err != nil {
		return ErrOpenFailed
	}
	primitive, err := streamingaead.New(handle)
	if err != nil {
		return ErrOpenFailed
	}
	reader, err := primitive.NewDecryptingReader(ciphertext, contextData)
	if err != nil {
		if ctx.Err() == nil && !errors.Is(err, platformcrypto.ErrCiphertextUnavailable) {
			return errors.Join(ErrOpenFailed, platformcrypto.ErrCiphertextIntegrity)
		}
		return ErrOpenFailed
	}
	if err := copyDecryptedWithContext(ctx, destination, reader); err != nil {
		if errors.Is(err, platformcrypto.ErrCiphertextIntegrity) {
			return errors.Join(ErrOpenFailed, platformcrypto.ErrCiphertextIntegrity)
		}
		return ErrOpenFailed
	}
	if err := ctx.Err(); err != nil {
		return ErrOpenFailed
	}

	return nil
}

func copyDecryptedWithContext(ctx context.Context, destination io.Writer, source io.Reader) error {
	buffer := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			if writeErr != nil {
				return writeErr
			}
			if written != read {
				return io.ErrShortWrite
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			if ctx.Err() != nil || errors.Is(readErr, platformcrypto.ErrCiphertextUnavailable) {
				return readErr
			}
			return errors.Join(readErr, platformcrypto.ErrCiphertextIntegrity)
		}
		if read == 0 {
			return errors.Join(io.ErrNoProgress, platformcrypto.ErrCiphertextIntegrity)
		}
	}
}

func serializeKeyset(handle *keyset.Handle) ([]byte, error) {
	var output bytes.Buffer
	if err := insecurecleartextkeyset.Write(handle, keyset.NewBinaryWriter(&output)); err != nil {
		return nil, err
	}

	return output.Bytes(), nil
}

func envelopeWrappedKey(envelope platformcrypto.Envelope) kms.WrappedKey {
	record := envelope.Record()
	wrapped, err := kms.NewWrappedKey(record.WrappedKey)
	if err != nil {
		return kms.WrappedKey{}
	}

	return wrapped
}

func validateStreams(
	ctx context.Context,
	destination io.Writer,
	source io.Reader,
	authenticatedContext platformcrypto.Context,
) error {
	if ctx == nil || destination == nil || source == nil || authenticatedContext.SchemaVersion() == 0 {
		return errors.New("crypto: streaming input is invalid")
	}

	return ctx.Err()
}

func copyWithContext(ctx context.Context, destination io.Writer, source io.Reader) error {
	reader := readerFunc(func(buffer []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}

		return source.Read(buffer)
	})
	_, err := io.CopyBuffer(destination, reader, make([]byte, 32*1024))

	return err
}

type readerFunc func([]byte) (int, error)

func (read readerFunc) Read(buffer []byte) (int, error) { return read(buffer) }
