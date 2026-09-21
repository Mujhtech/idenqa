// Package experience defines the closed, transport-independent portable
// capture-experience contract. One immutable document revision describes the
// tenant copy, mandatory-copy reference, targeting rules, vetted assets, links,
// and safe theme tokens that every capture client renders. Raw evidence bytes,
// credentials, subject data, and executable content are never part of the
// document.
//
// The document is canonicalised, hashed with SHA-256, and signed with Ed25519
// through an owned signing port. Clients verify the digest, key id, and
// signature before rendering.
package experience

import (
	"encoding/json"
	"time"
)

// Version bounds for the v1 experience contract.
const (
	SchemaVersion = "1.0"
	MajorVersion  = 1
	MinorVersion  = 0

	// MaxDocumentBytes bounds one canonical or encoded document.
	MaxDocumentBytes = 256 * 1024
	// MaxCopyEntriesPerLocale bounds one locale catalogue.
	MaxCopyEntriesPerLocale = 64
	// MaxCopyValueBytes bounds one copy value.
	MaxCopyValueBytes = 2048
	// MaxCopyKeyBytes bounds one copy key.
	MaxCopyKeyBytes = 128
	// MaxLocales bounds the locale catalogue.
	MaxLocales = 16
	// MaxAssets bounds vetted asset references.
	MaxAssets = 16
	// MaxAssetBytes bounds one asset's declared size.
	MaxAssetBytes = 2 * 1024 * 1024
	// MaxLinks bounds custom links.
	MaxCustomLinks = 8
	// MaxOrigins bounds allowed-origin declarations.
	MaxOrigins = 16
	// MaxTargetingRules bounds one document's targeting rules.
	MaxTargetingRules = 32
	// MaxRuleCountries bounds the countries dimension of one rule.
	MaxRuleCountries = 64
	// MaxRuleApplications bounds the application-id dimension of one rule.
	MaxRuleApplications = 16
	// MaxRuleOrigins bounds the origin dimension of one rule.
	MaxRuleOrigins = 16
	// MaxNameBytes bounds the human-readable experience name.
	MaxNameBytes = 120
	// MaxVersion bounds every immutable integer revision.
	MaxVersion uint32 = 1_000_000
	// MaxURLBytes bounds one advertised link.
	MaxURLBytes = 2048
	// MaxLocaleBytes bounds one BCP 47 language tag.
	MaxLocaleBytes = 35
)

// Version identifies one compatible contract revision.
type Version struct {
	Major uint16 `json:"major"`
	Minor uint16 `json:"minor"`
}

// CurrentVersion is the contract implemented by this package.
var CurrentVersion = Version{Major: MajorVersion, Minor: MinorVersion}

// Accepts reports whether this implementation can consume requested.
func (version Version) Accepts(requested Version) bool {
	return version.Major != 0 && version.Major == requested.Major && version.Minor >= requested.Minor
}

// Document is one immutable experience revision. Field semantics:
//   - Version is the immutable integer revision of ExperienceID.
//   - Copy.Version identifies the tenant copy catalogue pinned per session.
//   - MandatoryCopyVersion references a Core-owned, non-overridable copy
//     catalogue; tenant copy must never use its reserved key namespaces.
//   - Targeting is ordered within the document: the first matching rule of the
//     most specific matching document wins.
type Document struct {
	SchemaVersion        string   `json:"schema_version"`
	ExperienceID         string   `json:"experience_id"`
	Version              uint32   `json:"version"`
	Name                 string   `json:"name"`
	Copy                 Copy     `json:"copy"`
	MandatoryCopyVersion string   `json:"mandatory_copy_version"`
	DefaultLocale        string   `json:"default_locale"`
	Targeting            []Target `json:"targeting"`
	Assets               []Asset  `json:"assets,omitempty"`
	Links                Links    `json:"links"`
	Theme                Theme    `json:"theme"`
	AllowedOrigins       []string `json:"allowed_origins,omitempty"`
}

// Copy is the structured tenant copy catalogue keyed by locale.
type Copy struct {
	Version string       `json:"version"`
	Locales []LocaleCopy `json:"locales"`
}

// LocaleCopy is one locale's complete tenant copy.
type LocaleCopy struct {
	Locale  string      `json:"locale"`
	Entries []CopyEntry `json:"entries"`
}

// CopyEntry is one bounded tenant copy value.
type CopyEntry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Target constrains when a document applies. Empty dimensions are unconstrained.
type Target struct {
	Workflow       string   `json:"workflow,omitempty"`
	Countries      []string `json:"countries,omitempty"`
	ApplicationIDs []string `json:"application_ids,omitempty"`
	Origins        []string `json:"origins,omitempty"`
	SDKVersionMin  string   `json:"sdk_version_min,omitempty"`
	SDKVersionMax  string   `json:"sdk_version_max,omitempty"`
}

// Asset is a vetted asset reference stored through the owned object-store port.
// Kind is closed to "image" and MIME excludes executable and script-capable
// content. ObjectVersion identifies the exact immutable object-store version.
type Asset struct {
	Key           string `json:"key"`
	Kind          string `json:"kind"`
	MIME          string `json:"mime"`
	Size          int64  `json:"size"`
	Digest        string `json:"digest"`
	ObjectKey     string `json:"object_key"`
	ObjectVersion string `json:"object_version"`
}

// Links are validated HTTPS destinations.
type Links struct {
	Support string       `json:"support"`
	Privacy string       `json:"privacy"`
	Terms   string       `json:"terms"`
	Custom  []CustomLink `json:"custom,omitempty"`
}

// CustomLink is one optional HTTPS link with bounded tenant-facing text.
type CustomLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Theme carries safe visual tokens. Host appearance-only CSS variables remain
// authoritative for host-local branding; these tokens are the portable default.
type Theme struct {
	PrimaryColor    string `json:"primary_color"`
	AccentColor     string `json:"accent_color"`
	BackgroundColor string `json:"background_color"`
	TextColor       string `json:"text_color"`
	LogoAssetKey    string `json:"logo_asset_key,omitempty"`
}

// Manifest is the signed envelope returned to clients. Digest and Signature
// cover the canonical document bytes, never the envelope.
type Manifest struct {
	Document  Document `json:"document"`
	Digest    string   `json:"digest"`
	KeyID     string   `json:"key_id"`
	Algorithm string   `json:"algorithm"`
	Signature string   `json:"signature"`
}

// Pinned identifies the exact experience and copy versions a session renders.
type Pinned struct {
	ExperienceID         string    `json:"experience_id"`
	Version              uint32    `json:"version"`
	Locale               string    `json:"locale"`
	TenantCopyVersion    string    `json:"tenant_copy_version"`
	MandatoryCopyVersion string    `json:"mandatory_copy_version"`
	Source               string    `json:"source"`
	Digest               string    `json:"digest"`
	KeyID                string    `json:"key_id"`
	PinnedAt             time.Time `json:"pinned_at"`
}

// MandatoryCopy is the Core-owned catalogue served alongside a resolution. Its
// entries are not overridable by tenant copy and are versioned independently.
type MandatoryCopy struct {
	Version string      `json:"version"`
	Digest  string      `json:"digest"`
	Entries []CopyEntry `json:"entries"`
}

// Resolution is the complete answer to a capture bootstrap: the signed
// document, the Core-owned mandatory copy, and the exact pinned versions.
type Resolution struct {
	Manifest      Manifest      `json:"manifest"`
	MandatoryCopy MandatoryCopy `json:"mandatory_copy"`
	Pinned        Pinned        `json:"pinned"`
	Fallback      bool          `json:"fallback"`
}

// EncodeManifest serialises a manifest without canonicalisation.
func EncodeManifest(manifest Manifest) ([]byte, error) {
	encoded, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	if len(encoded) > 2*MaxDocumentBytes {
		return nil, ErrTooLarge
	}
	return encoded, nil
}
