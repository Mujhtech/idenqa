package experience

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var (
	experienceIDPattern = regexp.MustCompile(`^exp_[0-9A-HJKMNP-TV-Z]{26}$`)
	digestPattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	localePattern       = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8})*$`)
	countryPattern      = regexp.MustCompile(`^[A-Z]{2}$`)
	workflowPattern     = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	applicationPattern  = regexp.MustCompile(`^[!-~]{1,200}$`)
	semverPattern       = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	colorPattern        = regexp.MustCompile(`^#[0-9a-f]{6}$`)
	copyKeyPattern      = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,127}$`)
	assetKeyPattern     = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	objectKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,511}$`)
	keyIDPattern        = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)
	copyVersionPattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
)

var mandatoryVersionPattern = regexp.MustCompile(`^mc-[0-9]{4}-[0-9]{2}-[0-9]{2}(-[0-9]{1,2})?$`)

// allowedMIMEs is the closed vetted-asset content-type allow-list. SVG is
// deliberately excluded because it can carry script.
var allowedMIMEs = map[string]struct{}{
	"image/png":  {},
	"image/jpeg": {},
	"image/webp": {},
	"image/avif": {},
}

// reservedCopyPrefixes are the Core-owned mandatory-copy namespaces tenant copy
// must never claim.
var reservedCopyPrefixes = []string{"regulatory.", "consent.", "safety.", "accessibility."}

// reservedCopyKey reports whether a tenant copy key collides with Core-owned
// mandatory copy.
func reservedCopyKey(key string) bool {
	for _, prefix := range reservedCopyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// ValidateDocument checks the closed experience document contract.
func ValidateDocument(document Document) error {
	if document.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: schema_version", ErrUnsupportedVersion)
	}
	if !experienceIDPattern.MatchString(document.ExperienceID) {
		return fmt.Errorf("%w: experience_id", ErrInvalid)
	}
	if document.Version == 0 || document.Version > MaxVersion {
		return fmt.Errorf("%w: version", ErrInvalid)
	}
	if err := validateText(document.Name, MaxNameBytes); err != nil {
		return fmt.Errorf("%w: name", err)
	}
	if !mandatoryVersionPattern.MatchString(document.MandatoryCopyVersion) {
		return fmt.Errorf("%w: mandatory_copy_version", ErrInvalid)
	}
	if err := validateCopy(document.Copy, document.DefaultLocale); err != nil {
		return err
	}
	if err := validateTargeting(document.Targeting); err != nil {
		return err
	}
	if err := validateAssets(document.Assets, document.Theme); err != nil {
		return err
	}
	if err := validateLinks(document.Links); err != nil {
		return err
	}
	if err := validateTheme(document.Theme); err != nil {
		return err
	}
	if err := validateOrigins(document.AllowedOrigins); err != nil {
		return err
	}
	return nil
}

func validateText(value string, limit int) error {
	if value == "" {
		return ErrInvalid
	}
	if !validUTF8Bounded(value, limit) {
		return ErrTooLarge
	}
	for _, character := range value {
		if character < 0x20 && character != '\n' && character != '\t' {
			return ErrInvalid
		}
	}
	return nil
}

func validateCopy(catalogue Copy, defaultLocale string) error {
	if !copyVersionPattern.MatchString(catalogue.Version) {
		return fmt.Errorf("%w: copy.version", ErrInvalid)
	}
	if len(catalogue.Locales) == 0 || len(catalogue.Locales) > MaxLocales {
		return fmt.Errorf("%w: copy.locales", ErrInvalid)
	}
	seenLocales := make(map[string]struct{}, len(catalogue.Locales))
	hasDefault := false
	for localeIndex, locale := range catalogue.Locales {
		if !localePattern.MatchString(locale.Locale) || len(locale.Locale) > MaxLocaleBytes {
			return fmt.Errorf("%w: copy.locales[%d].locale", ErrInvalid, localeIndex)
		}
		if _, duplicate := seenLocales[locale.Locale]; duplicate {
			return fmt.Errorf("%w: copy.locales[%d].locale duplicate", ErrInvalid, localeIndex)
		}
		seenLocales[locale.Locale] = struct{}{}
		if locale.Locale == defaultLocale {
			hasDefault = true
		}
		if len(locale.Entries) == 0 || len(locale.Entries) > MaxCopyEntriesPerLocale {
			return fmt.Errorf("%w: copy.locales[%d].entries", ErrInvalid, localeIndex)
		}
		seenKeys := make(map[string]struct{}, len(locale.Entries))
		for entryIndex, entry := range locale.Entries {
			if len(entry.Key) > MaxCopyKeyBytes || !copyKeyPattern.MatchString(entry.Key) {
				return fmt.Errorf("%w: copy.locales[%d].entries[%d].key", ErrInvalid, localeIndex, entryIndex)
			}
			if reservedCopyKey(entry.Key) {
				return fmt.Errorf("%w: copy.locales[%d].entries[%d].key", ErrReservedCopy, localeIndex, entryIndex)
			}
			if _, duplicate := seenKeys[entry.Key]; duplicate {
				return fmt.Errorf("%w: copy.locales[%d].entries[%d].key duplicate", ErrInvalid, localeIndex, entryIndex)
			}
			seenKeys[entry.Key] = struct{}{}
			if err := validateText(entry.Value, MaxCopyValueBytes); err != nil {
				return fmt.Errorf("%w: copy.locales[%d].entries[%d].value", err, localeIndex, entryIndex)
			}
		}
	}
	if !hasDefault {
		return fmt.Errorf("%w: default_locale not in copy.locales", ErrInvalid)
	}
	return nil
}

func validateTargeting(rules []Target) error {
	if len(rules) > MaxTargetingRules {
		return fmt.Errorf("%w: targeting", ErrTooLarge)
	}
	seen := make([]string, 0, len(rules))
	for index, rule := range rules {
		if len(rule.Countries) > MaxRuleCountries || len(rule.ApplicationIDs) > MaxRuleApplications || len(rule.Origins) > MaxRuleOrigins {
			return fmt.Errorf("%w: targeting[%d]", ErrTooLarge, index)
		}
		for _, country := range rule.Countries {
			if !countryPattern.MatchString(country) {
				return fmt.Errorf("%w: targeting[%d].countries", ErrInvalid, index)
			}
		}
		for _, application := range rule.ApplicationIDs {
			if !applicationPattern.MatchString(application) {
				return fmt.Errorf("%w: targeting[%d].application_ids", ErrInvalid, index)
			}
		}
		if err := validateOrigins(rule.Origins); err != nil {
			return fmt.Errorf("%w: targeting[%d].origins", err, index)
		}
		if rule.Workflow != "" && !workflowPattern.MatchString(rule.Workflow) {
			return fmt.Errorf("%w: targeting[%d].workflow", ErrInvalid, index)
		}
		for _, dimension := range [][]string{rule.Countries, rule.ApplicationIDs, rule.Origins} {
			if hasDuplicate(dimension) {
				return fmt.Errorf("%w: targeting[%d] duplicate dimension value", ErrInvalid, index)
			}
		}
		if (rule.SDKVersionMin == "") != (rule.SDKVersionMax == "") {
			return fmt.Errorf("%w: targeting[%d].sdk_version range must be complete", ErrInvalid, index)
		}
		if rule.SDKVersionMin != "" {
			if !semverPattern.MatchString(rule.SDKVersionMin) || !semverPattern.MatchString(rule.SDKVersionMax) {
				return fmt.Errorf("%w: targeting[%d].sdk_version", ErrInvalid, index)
			}
			if compareSemver(rule.SDKVersionMin, rule.SDKVersionMax) > 0 {
				return fmt.Errorf("%w: targeting[%d].sdk_version range inverted", ErrInvalid, index)
			}
		}
		encoded := ruleFingerprint(rule)
		if slices.Contains(seen, encoded) {
			return fmt.Errorf("%w: targeting[%d] duplicate rule", ErrInvalid, index)
		}
		seen = append(seen, encoded)
	}
	return nil
}

func hasDuplicate(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}

func ruleFingerprint(rule Target) string {
	parts := []string{rule.Workflow, strings.Join(rule.Countries, ","), strings.Join(rule.ApplicationIDs, ","), strings.Join(rule.Origins, ","), rule.SDKVersionMin, rule.SDKVersionMax}
	return strings.Join(parts, "\x00")
}

func compareSemver(left string, right string) int {
	leftParts, rightParts := strings.Split(left, "."), strings.Split(right, ".")
	for index := 0; index < 3; index++ {
		leftValue := numericValue(leftParts[index])
		rightValue := numericValue(rightParts[index])
		if leftValue != rightValue {
			if leftValue < rightValue {
				return -1
			}
			return 1
		}
	}
	return 0
}

func validateAssets(assets []Asset, theme Theme) error {
	if len(assets) > MaxAssets {
		return fmt.Errorf("%w: assets", ErrTooLarge)
	}
	seen := make(map[string]struct{}, len(assets))
	for index, asset := range assets {
		if !assetKeyPattern.MatchString(asset.Key) {
			return fmt.Errorf("%w: assets[%d].key", ErrInvalid, index)
		}
		if _, duplicate := seen[asset.Key]; duplicate {
			return fmt.Errorf("%w: assets[%d].key duplicate", ErrInvalid, index)
		}
		seen[asset.Key] = struct{}{}
		if asset.Kind != "image" {
			return fmt.Errorf("%w: assets[%d].kind", ErrInvalid, index)
		}
		if _, allowed := allowedMIMEs[asset.MIME]; !allowed {
			return fmt.Errorf("%w: assets[%d].mime", ErrInvalid, index)
		}
		if asset.Size <= 0 || asset.Size > MaxAssetBytes {
			return fmt.Errorf("%w: assets[%d].size", ErrTooLarge, index)
		}
		if !digestPattern.MatchString(asset.Digest) {
			return fmt.Errorf("%w: assets[%d].digest", ErrInvalid, index)
		}
		if !validObjectKey(asset.ObjectKey) {
			return fmt.Errorf("%w: assets[%d].object_key", ErrInvalid, index)
		}
		if !validObjectVersion(asset.ObjectVersion) {
			return fmt.Errorf("%w: assets[%d].object_version", ErrInvalid, index)
		}
	}
	if theme.LogoAssetKey != "" {
		if !assetKeyPattern.MatchString(theme.LogoAssetKey) {
			return fmt.Errorf("%w: theme.logo_asset_key", ErrInvalid)
		}
		if _, exists := seen[theme.LogoAssetKey]; !exists {
			return fmt.Errorf("%w: theme.logo_asset_key has no asset", ErrInvalid)
		}
	}
	return nil
}

func validObjectKey(value string) bool {
	if !objectKeyPattern.MatchString(value) {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func validObjectVersion(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return false
		}
	}
	return value != "." && value != ".."
}

func validateLinks(links Links) error {
	if err := validateURL(links.Support); err != nil {
		return fmt.Errorf("%w: links.support", err)
	}
	if err := validateURL(links.Privacy); err != nil {
		return fmt.Errorf("%w: links.privacy", err)
	}
	if err := validateURL(links.Terms); err != nil {
		return fmt.Errorf("%w: links.terms", err)
	}
	if len(links.Custom) > MaxCustomLinks {
		return fmt.Errorf("%w: links.custom", ErrTooLarge)
	}
	seen := make([]string, 0, len(links.Custom))
	for index, link := range links.Custom {
		if err := validateText(link.Label, MaxNameBytes); err != nil {
			return fmt.Errorf("%w: links.custom[%d].label", err, index)
		}
		if err := validateURL(link.URL); err != nil {
			return fmt.Errorf("%w: links.custom[%d].url", err, index)
		}
		if slices.Contains(seen, link.URL) {
			return fmt.Errorf("%w: links.custom[%d].url duplicate", ErrInvalid, index)
		}
		seen = append(seen, link.URL)
	}
	return nil
}

func validateURL(value string) error {
	if value == "" || len(value) > MaxURLBytes || !validUTF8Bounded(value, MaxURLBytes) {
		return ErrInvalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return ErrInvalid
	}
	return nil
}

func validateTheme(theme Theme) error {
	for _, color := range []string{theme.PrimaryColor, theme.AccentColor, theme.BackgroundColor, theme.TextColor} {
		if !colorPattern.MatchString(color) {
			return fmt.Errorf("%w: theme color", ErrInvalid)
		}
	}
	return nil
}

func validateOrigins(origins []string) error {
	if len(origins) > MaxOrigins {
		return ErrTooLarge
	}
	if hasDuplicate(origins) {
		return fmt.Errorf("%w: duplicate origin", ErrInvalid)
	}
	for _, origin := range origins {
		if err := validateOrigin(origin); err != nil {
			return err
		}
	}
	return nil
}

func validateOrigin(value string) error {
	if value == "" || len(value) > MaxURLBytes {
		return ErrInvalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ErrInvalid
	}
	if parsed.Host != strings.ToLower(parsed.Host) {
		return ErrInvalid
	}
	return nil
}

// ParseDocument accepts a bounded closed JSON document and validates it.
func ParseDocument(input []byte) (Document, error) {
	if len(input) == 0 || len(input) > MaxDocumentBytes || !validUTF8Bounded(string(input), MaxDocumentBytes) {
		return Document{}, ErrInvalid
	}
	if err := rejectDuplicateFields(input); err != nil {
		return Document{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	if err := requireEOF(decoder); err != nil {
		return Document{}, err
	}
	if err := ValidateDocument(document); err != nil {
		return Document{}, err
	}
	return document, nil
}

// ParseManifest accepts a bounded closed JSON signed envelope.
func ParseManifest(input []byte) (Manifest, error) {
	if len(input) == 0 || len(input) > 2*MaxDocumentBytes || !validUTF8Bounded(string(input), 2*MaxDocumentBytes) {
		return Manifest{}, ErrInvalid
	}
	if err := rejectDuplicateFields(input); err != nil {
		return Manifest{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	if err := requireEOF(decoder); err != nil {
		return Manifest{}, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ValidateManifest checks the closed envelope shape without verifying the key.
func ValidateManifest(manifest Manifest) error {
	if err := ValidateDocument(manifest.Document); err != nil {
		return err
	}
	if !digestPattern.MatchString(manifest.Digest) {
		return fmt.Errorf("%w: digest", ErrInvalid)
	}
	if manifest.Algorithm != "ed25519" {
		return fmt.Errorf("%w: algorithm", ErrInvalid)
	}
	if !keyIDPattern.MatchString(manifest.KeyID) {
		return fmt.Errorf("%w: key_id", ErrInvalid)
	}
	signature, err := hex.DecodeString(manifest.Signature)
	if err != nil || len(signature) == 0 || len(manifest.Signature) > 256 {
		return fmt.Errorf("%w: signature", ErrInvalid)
	}
	return nil
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

// rejectDuplicateFields rejects any object with a repeated key at any depth.
func rejectDuplicateFields(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: json token", ErrInvalid)
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return ErrInvalid
				}
				key, keyOK := keyToken.(string)
				if !keyOK {
					return ErrInvalid
				}
				if _, exists := seen[key]; exists {
					return ErrInvalid
				}
				seen[key] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return ErrInvalid
		}
	}
	if err := visit(); err != nil {
		return err
	}
	if decoder.More() {
		return ErrInvalid
	}
	return nil
}
