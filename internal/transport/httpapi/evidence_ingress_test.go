package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
)

func TestParseUploadMetadataValidatesHeadersWithoutReadingBody(t *testing.T) {
	t.Parallel()

	digest := sha256.Sum256([]byte("plaintext"))
	canonicalDigest := "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"

	tests := []struct {
		name   string
		mutate func(*requestFixture)
		valid  bool
	}{
		{name: "canonical jpeg", valid: true},
		{name: "canonical png", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Type", evidence.MediaTypePNG)
		}, valid: true},
		{name: "unknown content length", mutate: func(fixture *requestFixture) {
			fixture.request.ContentLength = -1
		}},
		{name: "zero content length", mutate: func(fixture *requestFixture) {
			fixture.request.ContentLength = 0
		}},
		{name: "chunked transfer", mutate: func(fixture *requestFixture) {
			fixture.request.TransferEncoding = []string{"chunked"}
		}},
		{name: "content encoding", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Encoding", "identity")
		}},
		{name: "partial content", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Range", "bytes 0-8/9")
		}},
		{name: "trailer", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Trailer", "Content-Digest")
		}},
		{name: "missing content type", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Del("Content-Type")
		}},
		{name: "content type parameters", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Type", "image/jpeg; charset=binary")
		}},
		{name: "unsupported content type", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Type", "image/webp")
		}},
		{name: "duplicate content type", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Add("Content-Type", evidence.MediaTypeJPEG)
		}},
		{name: "missing if match", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Del("If-Match")
		}},
		{name: "weak if match", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("If-Match", `W/"1"`)
		}},
		{name: "missing digest", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Del("Content-Digest")
		}},
		{name: "duplicate digest", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Add("Content-Digest", canonicalDigest)
		}},
		{name: "digest with another algorithm", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Digest", canonicalDigest+", sha-512=:AA==:")
		}},
		{name: "digest whitespace", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Digest", " "+canonicalDigest)
		}},
		{name: "digest malformed base64", mutate: func(fixture *requestFixture) {
			fixture.request.Header.Set("Content-Digest", "sha-256=:!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!:")
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newRequestFixture(t.Context(), canonicalDigest)
			if test.mutate != nil {
				test.mutate(&fixture)
			}
			metadata, err := parseUploadMetadata(fixture.request)
			if test.valid && err != nil {
				t.Fatalf("parseUploadMetadata() error = %v", err)
			}
			if !test.valid && err == nil {
				t.Fatal("parseUploadMetadata() error = nil")
			}
			if fixture.body.reads != 0 {
				t.Fatalf("body reads = %d, want 0", fixture.body.reads)
			}
			if test.valid && (metadata.ExpectedVersion != 1 || metadata.ContentLength != 9 ||
				metadata.MediaType != fixture.request.Header.Get("Content-Type") ||
				metadata.Digest != platformcrypto.Sum([]byte("plaintext"))) {
				t.Fatalf("metadata = %+v", metadata)
			}
		})
	}
}

func FuzzParseContentDigest(f *testing.F) {
	digest := sha256.Sum256([]byte("plaintext"))
	f.Add("sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":")
	f.Add("")
	f.Add("sha-256=:AA==:")
	f.Add("sha-512=:" + strings.Repeat("A", 44) + ":")

	f.Fuzz(func(t *testing.T, value string) {
		digest, err := parseContentDigest([]string{value})
		if err == nil {
			if len(digest) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(string(digest), "sha256:") {
				t.Fatalf("parseContentDigest(%q) = %q", value, digest)
			}
		}
	})
}

type requestFixture struct {
	request *http.Request
	body    *bodyProbe
}

func newRequestFixture(ctx context.Context, contentDigest string) requestFixture {
	body := &bodyProbe{}
	request := httptest.NewRequestWithContext(ctx, "PUT", "/v1/evidence-uploads/upl_example", body)
	request.ContentLength = 9
	request.Header.Set("Content-Type", evidence.MediaTypeJPEG)
	request.Header.Set("Content-Digest", contentDigest)
	request.Header.Set("If-Match", `"1"`)

	return requestFixture{request: request, body: body}
}

type bodyProbe struct{ reads int }

func (body *bodyProbe) Read([]byte) (int, error) {
	body.reads++

	return 0, io.EOF
}
