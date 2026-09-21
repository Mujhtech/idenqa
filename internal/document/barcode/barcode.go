// Package barcode parses already-decoded barcode payload strings into bounded
// canonical key/value fields. Image decoding, barcode detection, and error
// correction are deliberately out of scope: a provider or capture client must
// supply the decoded payload. Supported payload shapes are AAMVA/PDF417
// header-and-element text, VCARD, generic URL, URL query, and JSON objects.
package barcode

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
)

// Payload and field bounds.
const (
	// MaximumPayloadBytes is the largest decoded payload accepted.
	MaximumPayloadBytes = 8192
	// MaximumFields bounds canonical fields in one payload.
	MaximumFields = 64
	// MaximumKeyLength bounds one canonical field key.
	MaximumKeyLength = 64
	// MaximumValueLength bounds one canonical field value.
	MaximumValueLength = 512
	// MaximumJSONDepth bounds JSON object nesting.
	MaximumJSONDepth = 3
)

// Stable parse failures.
var (
	// ErrOversized means the payload exceeds MaximumPayloadBytes.
	ErrOversized = errors.New("barcode: payload exceeds bound")
	// ErrUnsupported means the payload shape is outside the closed set.
	ErrUnsupported = errors.New("barcode: unsupported payload")
	// ErrInvalid means the payload shape was recognised but malformed.
	ErrInvalid = errors.New("barcode: invalid payload")
	// ErrAmbiguous means duplicate or non-scalar content prevents one canonical value.
	ErrAmbiguous = errors.New("barcode: ambiguous payload")
)

// Format identifies one decoded payload shape.
type Format string

// Supported payload formats.
const (
	FormatAAMVA Format = "aamva"
	FormatVCard Format = "vcard"
	FormatURL   Format = "url"
	FormatQuery Format = "query"
	FormatJSON  Format = "json"
)

// Field is one canonical key and value. Keys are lower-case and stable.
type Field struct {
	Key   string
	Value string
}

// Payload is one bounded decoded payload. Format is the closed shape
// identifier; Fields are sorted by key.
type Payload struct {
	Format Format
	Fields []Field
}

// Value returns the canonical value for the exact key, when present.
func (payload Payload) Value(key string) (string, bool) {
	for _, field := range payload.Fields {
		if field.Key == key {
			return field.Value, true
		}
	}
	return "", false
}

// Parse classifies and parses one decoded payload string.
func Parse(payload string) (Payload, error) {
	if payload == "" {
		return Payload{}, ErrInvalid
	}
	if len(payload) > MaximumPayloadBytes {
		return Payload{}, ErrOversized
	}
	trimmed := strings.TrimSpace(payload)
	if trimmed == "" {
		return Payload{}, ErrInvalid
	}
	switch {
	case strings.HasPrefix(strings.TrimLeft(trimmed, "\x1e"), "@"):
		return parseAAMVA(trimmed)
	case strings.HasPrefix(trimmed, "{"):
		return parseJSON(trimmed)
	case strings.HasPrefix(strings.ToUpper(trimmed), "BEGIN:VCARD"):
		return parseVCard(trimmed)
	case strings.Contains(trimmed, "://"):
		return parseURL(trimmed)
	case strings.Contains(trimmed, "="):
		return parseQuery(trimmed)
	default:
		return Payload{}, ErrUnsupported
	}
}

func parseAAMVA(payload string) (Payload, error) {
	collector := newCollector(FormatAAMVA)
	body := strings.TrimLeft(strings.TrimLeft(strings.TrimSpace(payload), "\x1e")[1:], "\x1e\r\n\t ")
	const headerPrefix = "ANSI "
	if !strings.HasPrefix(body, headerPrefix) {
		return Payload{}, ErrInvalid
	}
	body = body[len(headerPrefix):]
	if len(body) < 12 || !allDigits(body[:12]) {
		return Payload{}, ErrInvalid
	}
	for _, header := range []struct{ key, value string }{
		{"aamva.iin", body[0:6]},
		{"aamva.version", body[6:8]},
		{"aamva.jurisdiction_version", body[8:10]},
		{"aamva.entries", body[10:12]},
	} {
		if err := collector.add(header.key, header.value); err != nil {
			return Payload{}, err
		}
	}
	body = body[12:]
	if len(body) >= 2 && isAlpha2(body[:2]) {
		if err := collector.add("subfile", body[:2]); err != nil {
			return Payload{}, err
		}
	}
	separator := strings.IndexAny(body, "\x1e\r\n")
	if separator < 0 {
		return collector.payload()
	}
	for _, token := range strings.FieldsFunc(body[separator:], func(character rune) bool {
		return character == '\x1e' || character == '\r' || character == '\n'
	}) {
		if token == "" {
			continue
		}
		if isSubfileHeader(token) {
			if _, exists := collector.value("subfile"); !exists {
				if err := collector.add("subfile", token[:2]); err != nil {
					return Payload{}, err
				}
			}
			continue
		}
		if len(token) < 4 || !isAlpha3(token[:3]) {
			return Payload{}, ErrInvalid
		}
		if err := collector.add(strings.ToLower(token[:3]), token[3:]); err != nil {
			return Payload{}, err
		}
	}
	return collector.payload()
}

func isSubfileHeader(token string) bool {
	return len(token) >= 10 && isAlpha2(token[:2]) && allDigits(token[2:6]) && allDigits(token[6:10])
}

func parseVCard(payload string) (Payload, error) {
	collector := newCollector(FormatVCard)
	lines := strings.Split(strings.ReplaceAll(payload, "\r\n", "\n"), "\n")
	unfolded := make([]string, 0, len(lines))
	for _, line := range lines {
		if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') && len(unfolded) > 0 {
			unfolded[len(unfolded)-1] += strings.TrimLeft(line, " \t")
			continue
		}
		unfolded = append(unfolded, line)
	}
	started, ended := false, false
	for _, line := range unfolded {
		trimmed := strings.TrimSpace(line)
		if strings.EqualFold(trimmed, "BEGIN:VCARD") {
			started = true
			continue
		}
		if strings.EqualFold(trimmed, "END:VCARD") {
			ended = true
			break
		}
		if !started || trimmed == "" {
			continue
		}
		name, value, found := strings.Cut(trimmed, ":")
		if !found {
			return Payload{}, ErrInvalid
		}
		if index := strings.IndexByte(name, ';'); index >= 0 {
			name = name[:index]
		}
		if index := strings.IndexByte(name, '.'); index >= 0 {
			name = name[index+1:]
		}
		key, ok := canonicalKey(name)
		if !ok {
			continue
		}
		if err := collector.add(key, strings.TrimSpace(value)); err != nil {
			return Payload{}, err
		}
	}
	if !started || !ended {
		return Payload{}, ErrInvalid
	}
	return collector.payload()
}

func parseJSON(payload string) (Payload, error) {
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return Payload{}, ErrInvalid
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return Payload{}, ErrInvalid
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return Payload{}, ErrUnsupported
	}
	collector := newCollector(FormatJSON)
	if err := flattenJSON(collector, "", object, 0); err != nil {
		return Payload{}, err
	}
	return collector.payload()
}

func flattenJSON(collector *collector, prefix string, object map[string]any, depth int) error {
	if depth > MaximumJSONDepth {
		return ErrAmbiguous
	}
	for key, value := range object {
		canonical, ok := canonicalKey(key)
		if !ok {
			return ErrInvalid
		}
		if prefix != "" {
			canonical = prefix + "." + canonical
		}
		switch typed := value.(type) {
		case nil:
			continue
		case map[string]any:
			if err := flattenJSON(collector, canonical, typed, depth+1); err != nil {
				return err
			}
		case []any:
			return ErrAmbiguous
		case string:
			if err := collector.add(canonical, typed); err != nil {
				return err
			}
		case json.Number:
			if err := collector.add(canonical, typed.String()); err != nil {
				return err
			}
		case bool:
			if err := collector.add(canonical, fmt.Sprintf("%t", typed)); err != nil {
				return err
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}

func parseURL(payload string) (Payload, error) {
	parsed, err := url.Parse(payload)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return Payload{}, ErrUnsupported
	}
	collector := newCollector(FormatURL)
	if err := collector.add("url.host", strings.ToLower(parsed.Host)); err != nil {
		return Payload{}, err
	}
	if parsed.Path != "" {
		if err := collector.add("url.path", parsed.Path); err != nil {
			return Payload{}, err
		}
	}
	if err := addQuery(collector, parsed.Query()); err != nil {
		return Payload{}, err
	}
	return collector.payload()
}

func parseQuery(payload string) (Payload, error) {
	values, err := url.ParseQuery(strings.TrimSpace(payload))
	if err != nil {
		return Payload{}, ErrInvalid
	}
	if len(values) == 0 {
		return Payload{}, ErrUnsupported
	}
	collector := newCollector(FormatQuery)
	if err := addQuery(collector, values); err != nil {
		return Payload{}, err
	}
	return collector.payload()
}

func addQuery(collector *collector, values url.Values) error {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		items := values[key]
		if len(items) != 1 {
			return ErrAmbiguous
		}
		canonical, ok := canonicalKey(key)
		if !ok {
			return ErrInvalid
		}
		if err := collector.add(canonical, items[0]); err != nil {
			return err
		}
	}
	return nil
}

type collector struct {
	format Format
	index  map[string]int
	fields []Field
}

func newCollector(format Format) *collector {
	return &collector{format: format, index: make(map[string]int)}
}

func (collector *collector) add(key, value string) error {
	if value == "" {
		return nil
	}
	if len(key) > MaximumKeyLength || len(value) > MaximumValueLength {
		return ErrInvalid
	}
	if existing, exists := collector.index[key]; exists {
		if collector.fields[existing].Value == value {
			return nil
		}
		return ErrAmbiguous
	}
	if len(collector.fields) >= MaximumFields {
		return ErrOversized
	}
	collector.index[key] = len(collector.fields)
	collector.fields = append(collector.fields, Field{Key: key, Value: value})
	return nil
}

func (collector *collector) value(key string) (string, bool) {
	index, exists := collector.index[key]
	if !exists {
		return "", false
	}
	return collector.fields[index].Value, true
}

func (collector *collector) payload() (Payload, error) {
	sort.Slice(collector.fields, func(left, right int) bool {
		return collector.fields[left].Key < collector.fields[right].Key
	})
	return Payload{Format: collector.format, Fields: collector.fields}, nil
}

func canonicalKey(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.NewReplacer(" ", "_", "-", "_", "/", "_").Replace(key)
	for strings.Contains(key, "__") {
		key = strings.ReplaceAll(key, "__", "_")
	}
	key = strings.Trim(key, "_")
	if key == "" || len(key) > MaximumKeyLength || strings.HasPrefix(key, ".") || strings.HasSuffix(key, ".") {
		return "", false
	}
	for index := 0; index < len(key); index++ {
		character := key[index]
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '_' || character == '.' {
			continue
		}
		return "", false
	}
	return key, true
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func isAlpha2(value string) bool { return len(value) == 2 && letters(value) }

func isAlpha3(value string) bool { return len(value) == 3 && letters(value) }

func letters(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 'A' || value[index] > 'Z' {
			return false
		}
	}
	return true
}
