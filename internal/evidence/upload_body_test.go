package evidence_test

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
)

func TestNewUploadReaderAcceptsClaimedJPEGAndPNG(t *testing.T) {
	t.Parallel()
	largeJPEG := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte{0x5a}, 70_000)...)
	largeJPEG = append(largeJPEG, 0xff, 0xd9)

	for _, test := range []struct {
		name      string
		mediaType string
		body      []byte
	}{
		{
			name: "jpeg", mediaType: evidence.MediaTypeJPEG,
			body: largeJPEG,
		},
		{
			name: "png", mediaType: evidence.MediaTypePNG,
			body: []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0, 0, 0, 0},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			upload := claimedUploadForBody(t, test.mediaType, test.body, platformcrypto.Sum(test.body))
			reader, err := evidence.NewUploadReader(upload, bytes.NewReader(test.body))
			if err != nil {
				t.Fatalf("NewUploadReader() error = %v", err)
			}
			var destination bytes.Buffer
			if _, err := io.Copy(&destination, reader); err != nil {
				t.Fatalf("Copy() error = %v", err)
			}
			result, err := reader.Result()
			if err != nil {
				t.Fatalf("Result() error = %v", err)
			}
			if result.Bytes != int64(len(test.body)) || result.Digest != platformcrypto.Sum(test.body) {
				t.Fatalf("validated body = %+v", result)
			}
			if !bytes.Equal(destination.Bytes(), test.body) {
				t.Fatalf("destination bytes = %x", destination.Bytes())
			}
		})
	}
}

func TestNewUploadReaderRejectsInvalidSignatureBeforeConsumerReads(t *testing.T) {
	t.Parallel()

	body := []byte("not a jpeg")
	upload := claimedUploadForBody(t, evidence.MediaTypeJPEG, body, platformcrypto.Sum(body))
	if _, err := evidence.NewUploadReader(
		upload, bytes.NewReader(body),
	); !errors.Is(err, evidence.ErrUploadSignature) {
		t.Fatalf("NewUploadReader() error = %v, want ErrUploadSignature", err)
	}
}

func TestUploadReaderRejectsTruncatedAndExcessBodies(t *testing.T) {
	t.Parallel()

	expected := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4, 0xff, 0xd9}
	for _, test := range []struct {
		name           string
		body           []byte
		expectedOutput int
	}{
		{name: "truncated", body: expected[:6], expectedOutput: 6},
		{
			name: "excess", body: append(append([]byte(nil), expected...), 0x00),
			expectedOutput: len(expected),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			upload := claimedUploadForBody(
				t, evidence.MediaTypeJPEG, expected, platformcrypto.Sum(expected),
			)
			reader, err := evidence.NewUploadReader(upload, bytes.NewReader(test.body))
			if err != nil {
				t.Fatalf("NewUploadReader() error = %v", err)
			}
			var destination bytes.Buffer
			if _, err := io.Copy(&destination, reader); !errors.Is(err, evidence.ErrUploadBodyLength) {
				t.Fatalf("Copy() error = %v, want ErrUploadBodyLength", err)
			}
			if destination.Len() != test.expectedOutput {
				t.Fatalf("destination length = %d, want %d", destination.Len(), test.expectedOutput)
			}
			if _, err := reader.Result(); !errors.Is(err, evidence.ErrUploadBodyLength) {
				t.Fatalf("Result() error = %v, want ErrUploadBodyLength", err)
			}
		})
	}
}

func TestUploadReaderRejectsIndependentDigestMismatch(t *testing.T) {
	t.Parallel()

	body := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4, 0xff, 0xd9}
	different := append([]byte(nil), body...)
	different[len(different)-1] = 0x00
	upload := claimedUploadForBody(
		t, evidence.MediaTypeJPEG, body, platformcrypto.Sum(different),
	)
	reader, err := evidence.NewUploadReader(upload, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewUploadReader() error = %v", err)
	}
	if _, err := io.Copy(io.Discard, reader); !errors.Is(err, evidence.ErrUploadBodyDigest) {
		t.Fatalf("Copy() error = %v, want ErrUploadBodyDigest", err)
	}
	if _, err := reader.Result(); !errors.Is(err, evidence.ErrUploadBodyDigest) {
		t.Fatalf("Result() error = %v, want ErrUploadBodyDigest", err)
	}
}

func TestUploadReaderPropagatesSourceFailuresAndNoProgress(t *testing.T) {
	t.Parallel()

	body := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4, 0xff, 0xd9}
	upload := claimedUploadForBody(t, evidence.MediaTypeJPEG, body, platformcrypto.Sum(body))
	readFailure := errors.New("source failed")

	for _, test := range []struct {
		name     string
		source   io.Reader
		expected error
	}{
		{
			name:     "source failure after signature",
			source:   io.MultiReader(bytes.NewReader(body[:5]), errorReader{err: readFailure}),
			expected: readFailure,
		},
		{
			name:     "no read progress after signature",
			source:   io.MultiReader(bytes.NewReader(body[:5]), noProgressReader{}),
			expected: io.ErrNoProgress,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader, err := evidence.NewUploadReader(upload, test.source)
			if err != nil {
				t.Fatalf("NewUploadReader() error = %v", err)
			}
			if _, err := io.Copy(io.Discard, reader); !errors.Is(err, test.expected) {
				t.Fatalf("Copy() error = %v, want %v", err, test.expected)
			}
		})
	}
}

func TestNewUploadReaderPropagatesSignatureReadFailure(t *testing.T) {
	t.Parallel()

	body := []byte{0xff, 0xd8, 0xff, 0xe0}
	upload := claimedUploadForBody(t, evidence.MediaTypeJPEG, body, platformcrypto.Sum(body))
	want := errors.New("signature source failed")
	if _, err := evidence.NewUploadReader(upload, errorReader{err: want}); !errors.Is(err, want) {
		t.Fatalf("NewUploadReader() error = %v, want %v", err, want)
	}
}

func TestUploadReaderResultRequiresCompleteConsumption(t *testing.T) {
	t.Parallel()

	body := []byte{0xff, 0xd8, 0xff, 0xd9}
	upload := claimedUploadForBody(t, evidence.MediaTypeJPEG, body, platformcrypto.Sum(body))
	reader, err := evidence.NewUploadReader(upload, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewUploadReader() error = %v", err)
	}
	if _, err := reader.Result(); !errors.Is(err, evidence.ErrUploadBodyIncomplete) {
		t.Fatalf("Result() error = %v, want ErrUploadBodyIncomplete", err)
	}
}

func TestUploadReaderAbortsProtectionBeforeInvalidBodyPersistence(t *testing.T) {
	t.Parallel()

	body := []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3, 4, 0xff, 0xd9}
	different := append([]byte(nil), body...)
	different[len(different)-1] = 0x00
	upload := claimedUploadForBody(
		t, evidence.MediaTypeJPEG, body, platformcrypto.Sum(different),
	)
	reader, err := evidence.NewUploadReader(upload, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewUploadReader() error = %v", err)
	}
	protection := newProtectionFixture(t)
	if _, err := protection.protector.Protect(
		t.Context(), protection.scope, protection.input(reader),
	); err == nil {
		t.Fatal("Protect() error = nil")
	}
	if _, err := reader.Result(); !errors.Is(err, evidence.ErrUploadBodyDigest) {
		t.Fatalf("Result() error = %v, want ErrUploadBodyDigest", err)
	}
	if !protection.repository.created.ID().IsZero() {
		t.Fatal("invalid upload body was persisted as available evidence")
	}
}

func TestNewUploadReaderRequiresClaimedUploadAndSource(t *testing.T) {
	t.Parallel()

	body := []byte{0xff, 0xd8, 0xff, 0xd9}
	fixture := newUploadFixture(t)
	fixture.input.ExpectedBytes = int64(len(body))
	fixture.input.ExpectedDigest = string(platformcrypto.Sum(body))
	fixture.input.MediaType = evidence.MediaTypeJPEG
	issued := fixture.upload(t)
	if _, err := evidence.NewUploadReader(issued, bytes.NewReader(body)); !errors.Is(err, evidence.ErrUploadConflict) {
		t.Fatalf("NewUploadReader(issued) error = %v, want ErrUploadConflict", err)
	}
	if _, err := evidence.NewUploadReader(evidence.Upload{}, nil); err == nil {
		t.Fatal("NewUploadReader(nil source) error = nil")
	}
	var reader *evidence.UploadReader
	if _, err := reader.Read(make([]byte, 1)); err == nil {
		t.Fatal("nil UploadReader.Read() error = nil")
	}
	if _, err := reader.Result(); err == nil {
		t.Fatal("nil UploadReader.Result() error = nil")
	}
}

func claimedUploadForBody(
	t *testing.T,
	mediaType string,
	body []byte,
	digest platformcrypto.Digest,
) evidence.Upload {
	t.Helper()
	fixture := newUploadFixture(t)
	fixture.input.ExpectedBytes = int64(len(body))
	fixture.input.ExpectedDigest = string(digest)
	fixture.input.MediaType = mediaType
	upload := fixture.upload(t)
	claimed, err := upload.ClaimAttempt(upload.Version(), fixture.now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}

	return claimed
}

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type noProgressReader struct{}

func (noProgressReader) Read([]byte) (int, error) { return 0, nil }
