package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
)

const contentDigestPrefix = "sha-256=:"

// parseUploadMetadata validates only headers and request framing. It must not
// read the body; body hashing and file-signature validation happen after the
// durable upload attempt has been claimed.
func parseUploadMetadata(request *http.Request) (evidence.UploadMetadata, error) {
	if request == nil {
		return evidence.UploadMetadata{}, invalidRequest(errors.New("upload request is required"))
	}
	if request.ContentLength <= 0 || len(request.TransferEncoding) != 0 {
		return evidence.UploadMetadata{}, invalidRequest(errors.New("one positive Content-Length is required"))
	}
	if len(request.Header.Values("Content-Encoding")) != 0 ||
		len(request.Header.Values("Content-Range")) != 0 ||
		len(request.Header.Values("Trailer")) != 0 || len(request.Trailer) != 0 {
		return evidence.UploadMetadata{}, invalidRequest(errors.New("encoded, partial, or trailer uploads are not supported"))
	}
	mediaTypes := request.Header.Values("Content-Type")
	if len(mediaTypes) != 1 ||
		(mediaTypes[0] != evidence.MediaTypeJPEG && mediaTypes[0] != evidence.MediaTypePNG) {
		return evidence.UploadMetadata{}, invalidRequest(errors.New("one canonical supported Content-Type is required"))
	}
	version, err := parseIfMatch(request.Header.Values("If-Match"))
	if err != nil {
		return evidence.UploadMetadata{}, err
	}
	digest, err := parseContentDigest(request.Header.Values("Content-Digest"))
	if err != nil {
		return evidence.UploadMetadata{}, err
	}

	return evidence.UploadMetadata{
		ExpectedVersion: version,
		ContentLength:   request.ContentLength,
		MediaType:       mediaTypes[0],
		Digest:          digest,
	}, nil
}

func parseContentDigest(values []string) (platformcrypto.Digest, error) {
	const encodedDigestLength = 44
	if len(values) != 1 || len(values[0]) != len(contentDigestPrefix)+encodedDigestLength+1 ||
		!strings.HasPrefix(values[0], contentDigestPrefix) || !strings.HasSuffix(values[0], ":") {
		return "", invalidRequest(errors.New("one canonical SHA-256 Content-Digest is required"))
	}
	encoded := values[0][len(contentDigestPrefix) : len(values[0])-1]
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size || base64.StdEncoding.EncodeToString(decoded) != encoded {
		return "", invalidRequest(errors.New("Content-Digest is malformed"))
	}
	digest, err := platformcrypto.NewDigest("sha256:" + hex.EncodeToString(decoded))
	if err != nil {
		return "", invalidRequest(errors.New("Content-Digest is malformed"))
	}

	return digest, nil
}
