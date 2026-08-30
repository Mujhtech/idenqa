package evidence

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
)

var (
	jpegSignature = []byte{0xff, 0xd8, 0xff}
	pngSignature  = []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
)

// ValidatedBody contains only independently observed, non-byte upload facts.
type ValidatedBody struct {
	Bytes  int64
	Digest platformcrypto.Digest
}

// UploadReader bounds and validates one claimed whole-body plaintext stream.
// It implements io.Reader so encryption can pull bytes without buffering the
// complete artefact or introducing a pipe and goroutine.
type UploadReader struct {
	source         *bufio.Reader
	digester       hash.Hash
	expectedBytes  int64
	expectedDigest string
	observedBytes  int64
	result         ValidatedBody
	terminal       error
	isValidated    bool
}

// NewUploadReader checks the declared file signature before any plaintext can
// reach a consumer and prepares independent length and SHA-256 validation.
func NewUploadReader(upload Upload, source io.Reader) (*UploadReader, error) {
	if source == nil {
		return nil, errors.New("evidence: upload body source is required")
	}
	record := upload.Record()
	if record.State != UploadStateUploading || record.ExpectedBytes < 1 {
		return nil, ErrUploadConflict
	}
	buffered := bufio.NewReaderSize(source, len(pngSignature))
	if err := validateUploadSignature(buffered, record.MediaType); err != nil {
		return nil, err
	}

	return &UploadReader{
		source: buffered, digester: sha256.New(), expectedBytes: record.ExpectedBytes,
		expectedDigest: record.ExpectedDigest,
	}, nil
}

// Read streams no more than the intended length. The read that reaches the
// boundary also verifies absence of trailing bytes and the independent digest;
// validation failures therefore abort the downstream encryption stream.
func (reader *UploadReader) Read(destination []byte) (int, error) {
	if reader == nil {
		return 0, errors.New("evidence: upload reader is not initialised")
	}
	if len(destination) == 0 {
		return 0, nil
	}
	if reader.terminal != nil {
		return 0, reader.terminal
	}
	if reader.isValidated {
		return 0, io.EOF
	}

	remaining := reader.expectedBytes - reader.observedBytes
	if int64(len(destination)) > remaining {
		destination = destination[:remaining]
	}
	read, readErr := reader.source.Read(destination)
	if read > 0 {
		if _, err := reader.digester.Write(destination[:read]); err != nil {
			reader.terminal = fmt.Errorf("hash upload body stream: %w", err)

			return read, reader.terminal
		}
		reader.observedBytes += int64(read)
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		reader.terminal = fmt.Errorf("read upload body stream: %w", readErr)

		return read, reader.terminal
	}
	if reader.observedBytes < reader.expectedBytes {
		switch {
		case errors.Is(readErr, io.EOF):
			reader.terminal = ErrUploadBodyLength
		case read == 0:
			reader.terminal = fmt.Errorf("read upload body stream: %w", io.ErrNoProgress)
		}
		if reader.terminal != nil {
			return read, reader.terminal
		}

		return read, nil
	}
	if err := reader.finalise(); err != nil {
		reader.terminal = err

		return read, err
	}

	return read, nil
}

// Result returns the independently observed facts only after a consumer has
// read and validated the complete stream.
func (reader *UploadReader) Result() (ValidatedBody, error) {
	if reader == nil {
		return ValidatedBody{}, errors.New("evidence: upload reader is not initialised")
	}
	if reader.terminal != nil {
		return ValidatedBody{}, reader.terminal
	}
	if !reader.isValidated {
		return ValidatedBody{}, ErrUploadBodyIncomplete
	}

	return reader.result, nil
}

func (reader *UploadReader) finalise() error {
	var extra [1]byte
	read, err := reader.source.Read(extra[:])
	if read > 0 {
		return ErrUploadBodyLength
	}
	if err == nil {
		return fmt.Errorf("read upload body trailer: %w", io.ErrNoProgress)
	}
	if !errors.Is(err, io.EOF) {
		return fmt.Errorf("read upload body trailer: %w", err)
	}
	digest := platformcrypto.Digest("sha256:" + hex.EncodeToString(reader.digester.Sum(nil)))
	if subtle.ConstantTimeCompare([]byte(digest), []byte(reader.expectedDigest)) != 1 {
		return ErrUploadBodyDigest
	}
	reader.result = ValidatedBody{Bytes: reader.observedBytes, Digest: digest}
	reader.isValidated = true

	return nil
}

func validateUploadSignature(source *bufio.Reader, mediaType string) error {
	var signature []byte
	switch mediaType {
	case MediaTypeJPEG:
		signature = jpegSignature
	case MediaTypePNG:
		signature = pngSignature
	default:
		return ErrUploadSignature
	}
	observed, err := source.Peek(len(signature))
	if err != nil {
		if errors.Is(err, io.EOF) {
			return ErrUploadSignature
		}

		return fmt.Errorf("read upload file signature: %w", err)
	}
	if subtle.ConstantTimeCompare(observed, signature) != 1 {
		return ErrUploadSignature
	}

	return nil
}
