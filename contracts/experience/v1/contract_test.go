package experience

import (
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T) (Document, Manifest, map[string]ed25519.PublicKey) {
	t.Helper()
	raw, err := os.ReadFile("testdata/manifest.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var vector struct {
		PublicKeyHex    string   `json:"public_key_hex"`
		CanonicalDigest string   `json:"canonical_digest"`
		Manifest        Manifest `json:"manifest"`
	}
	if err := json.Unmarshal(raw, &vector); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	document, err := ParseDocument(mustJSON(t, vector.Manifest.Document))
	if err != nil {
		t.Fatalf("parse fixture document: %v", err)
	}
	public, err := hex.DecodeString(vector.PublicKeyHex)
	if err != nil || len(public) != ed25519.PublicKeySize {
		t.Fatalf("decode fixture public key: %v", err)
	}
	if digest, err := DigestDocument(document); err != nil || digest != vector.CanonicalDigest {
		t.Fatalf("fixture digest = %q, %v; want %q", digest, err, vector.CanonicalDigest)
	}
	if vector.Manifest.Digest != vector.CanonicalDigest {
		t.Fatalf("fixture manifest digest = %q, want %q", vector.Manifest.Digest, vector.CanonicalDigest)
	}
	keys := map[string]ed25519.PublicKey{vector.Manifest.KeyID: ed25519.PublicKey(public)}
	if _, err := VerifyManifest(vector.Manifest, keys); err != nil {
		t.Fatalf("verify fixture manifest: %v", err)
	}
	return document, vector.Manifest, keys
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode fixture: %v", err)
	}
	return encoded
}

func TestCanonicalDigestMatchesFixture(t *testing.T) {
	t.Parallel()
	document, manifest, keys := fixture(t)
	canonical, err := CanonicalDocument(document)
	if err != nil {
		t.Fatalf("CanonicalDocument() error = %v", err)
	}
	if !strings.Contains(string(canonical), `"copy":{"locales"`) {
		t.Fatalf("canonical bytes are not key-sorted: %s", canonical)
	}
	reparsed, err := ParseDocument(canonical)
	if err != nil {
		t.Fatalf("ParseDocument(canonical) error = %v", err)
	}
	second, err := CanonicalDocument(reparsed)
	if err != nil || string(second) != string(canonical) {
		t.Fatalf("canonical form is not stable: %v", err)
	}
	if _, err := VerifyManifest(manifest, keys); err != nil {
		t.Fatalf("VerifyManifest() error = %v", err)
	}
}

func TestVerifyManifestRejectsTamperWrongKeyAndUnknownKey(t *testing.T) {
	t.Parallel()
	_, manifest, keys := fixture(t)
	tampered := manifest
	tampered.Document.Name = "Hijacked capture"
	if _, err := VerifyManifest(tampered, keys); !errors.Is(err, ErrSignature) {
		t.Fatalf("tampered VerifyManifest() error = %v, want ErrSignature", err)
	}
	wrong, _, _ := ed25519.GenerateKey(nil)
	if _, err := VerifyManifest(manifest, map[string]ed25519.PublicKey{manifest.KeyID: wrong}); !errors.Is(err, ErrSignature) {
		t.Fatalf("wrong-key VerifyManifest() error = %v, want ErrSignature", err)
	}
	if _, err := VerifyManifest(manifest, map[string]ed25519.PublicKey{}); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("unknown-key VerifyManifest() error = %v, want ErrUnknownKey", err)
	}
	rekeyed := manifest
	rekeyed.KeyID = "expkey_other_1"
	if _, err := VerifyManifest(rekeyed, keys); !errors.Is(err, ErrUnknownKey) {
		t.Fatalf("rekeyed VerifyManifest() error = %v, want ErrUnknownKey", err)
	}
	forged := manifest
	forged.Digest = strings.Repeat("0", 64)
	if _, err := VerifyManifest(forged, keys); !errors.Is(err, ErrSignature) {
		t.Fatalf("forged-digest VerifyManifest() error = %v, want ErrSignature", err)
	}
}

func TestParseDocumentRejectsClosedContractViolations(t *testing.T) {
	t.Parallel()
	document, _, _ := fixture(t)
	base := func() Document { return mustDecode(t, mustJSON(t, document)) }

	tests := []struct {
		name   string
		mutate func(*Document)
		want   error
	}{
		{name: "schema version", mutate: func(d *Document) { d.SchemaVersion = "2.0" }, want: ErrUnsupportedVersion},
		{name: "experience id", mutate: func(d *Document) { d.ExperienceID = "exp_bad" }, want: ErrInvalid},
		{name: "zero version", mutate: func(d *Document) { d.Version = 0 }, want: ErrInvalid},
		{name: "reserved mandatory copy", mutate: func(d *Document) {
			d.Copy.Locales[0].Entries = append(d.Copy.Locales[0].Entries, CopyEntry{Key: "consent.explicit", Value: "override"})
		}, want: ErrReservedCopy},
		{name: "duplicate locale", mutate: func(d *Document) {
			d.Copy.Locales = append(d.Copy.Locales, d.Copy.Locales[0])
		}, want: ErrInvalid},
		{name: "default locale missing", mutate: func(d *Document) { d.DefaultLocale = "de" }, want: ErrInvalid},
		{name: "executable asset", mutate: func(d *Document) { d.Assets[0].MIME = "image/svg+xml" }, want: ErrInvalid},
		{name: "asset size bound", mutate: func(d *Document) { d.Assets[0].Size = MaxAssetBytes + 1 }, want: ErrTooLarge},
		{name: "asset digest", mutate: func(d *Document) { d.Assets[0].Digest = "not-a-digest" }, want: ErrInvalid},
		{name: "object key traversal", mutate: func(d *Document) { d.Assets[0].ObjectKey = "experiences/../secret" }, want: ErrInvalid},
		{name: "insecure link", mutate: func(d *Document) { d.Links.Support = "http://support.acme.example" }, want: ErrInvalid},
		{name: "link credentials", mutate: func(d *Document) { d.Links.Privacy = "https://user:pass@acme.example/privacy" }, want: ErrInvalid},
		{name: "insecure origin", mutate: func(d *Document) { d.AllowedOrigins = []string{"http://capture.acme.example"} }, want: ErrInvalid},
		{name: "origin path", mutate: func(d *Document) { d.AllowedOrigins = []string{"https://capture.acme.example/path"} }, want: ErrInvalid},
		{name: "color token", mutate: func(d *Document) { d.Theme.PrimaryColor = "blue" }, want: ErrInvalid},
		{name: "logo without asset", mutate: func(d *Document) { d.Theme.LogoAssetKey = "missing" }, want: ErrInvalid},
		{name: "duplicate targeting rule", mutate: func(d *Document) {
			d.Targeting = append(d.Targeting, d.Targeting[0])
		}, want: ErrInvalid},
		{name: "incomplete sdk range", mutate: func(d *Document) { d.Targeting[0].SDKVersionMax = "" }, want: ErrInvalid},
		{name: "inverted sdk range", mutate: func(d *Document) {
			d.Targeting[0].SDKVersionMin, d.Targeting[0].SDKVersionMax = "3.0.0", "1.0.0"
		}, want: ErrInvalid},
		{name: "lowercase country", mutate: func(d *Document) { d.Targeting[0].Countries = []string{"ng"} }, want: ErrInvalid},
		{name: "unknown field", mutate: func(_ *Document) {}, want: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := base()
			test.mutate(&candidate)
			raw := mustJSON(t, candidate)
			if test.name == "unknown field" {
				raw = []byte(strings.Replace(string(raw), `"schema_version"`, `"unexpected":"x","schema_version"`, 1))
			}
			if _, err := ParseDocument(raw); !errors.Is(err, test.want) {
				t.Fatalf("ParseDocument() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestParseDocumentRejectsDuplicateFieldsAndTrailingContent(t *testing.T) {
	t.Parallel()
	document, _, _ := fixture(t)
	raw := string(mustJSON(t, document))
	if _, err := ParseDocument([]byte(raw[:len(raw)-1] + `,"version":2}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate field error = %v, want ErrInvalid", err)
	}
	if _, err := ParseDocument(append([]byte(raw), []byte(`{}`)...)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("trailing content error = %v, want ErrInvalid", err)
	}
	if _, err := ParseDocument([]byte(`{"schema_version":` + strings.Repeat(" ", MaxDocumentBytes) + `}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("oversize error = %v, want ErrInvalid", err)
	}
}

func mustDecode(t *testing.T, raw []byte) Document {
	t.Helper()
	document, err := ParseDocument(raw)
	if err != nil {
		t.Fatalf("ParseDocument() error = %v", err)
	}
	return document
}
