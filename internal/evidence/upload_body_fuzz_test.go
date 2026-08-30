package evidence

import (
	"bufio"
	"bytes"
	"testing"
)

func FuzzValidateUploadSignature(f *testing.F) {
	f.Add(MediaTypeJPEG, []byte{0xff, 0xd8, 0xff})
	f.Add(MediaTypePNG, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a})
	f.Add(MediaTypeJPEG, []byte{})
	f.Add("image/webp", []byte("RIFF"))

	f.Fuzz(func(t *testing.T, mediaType string, body []byte) {
		err := validateUploadSignature(bufio.NewReader(bytes.NewReader(body)), mediaType)
		if err == nil {
			switch mediaType {
			case MediaTypeJPEG:
				if !bytes.HasPrefix(body, jpegSignature) {
					t.Fatalf("accepted JPEG without signature: %x", body)
				}
			case MediaTypePNG:
				if !bytes.HasPrefix(body, pngSignature) {
					t.Fatalf("accepted PNG without signature: %x", body)
				}
			default:
				t.Fatalf("accepted unsupported media type %q", mediaType)
			}
		}
	})
}
