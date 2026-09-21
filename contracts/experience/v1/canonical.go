package experience

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// CanonicalDocument returns the canonical JSON encoding of one validated
// document. Canonical form is:
//   - object keys sorted by UTF-8 byte order;
//   - no insignificant whitespace;
//   - integers only, in base-10 without leading zeros;
//   - strings escaped only where JSON requires it: quote, backslash, and
//     control characters (short escapes where available, otherwise \u00xx).
//
// The same rules are implemented by the TypeScript validator so a digest
// computed in either language matches.
func CanonicalDocument(document Document) ([]byte, error) {
	if err := ValidateDocument(document); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("%w: encode: %w", ErrInvalid, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: normalise: %w", ErrInvalid, err)
	}
	canonical, err := canonicalBytes(value)
	if err != nil {
		return nil, err
	}
	if len(canonical) > MaxDocumentBytes {
		return nil, ErrTooLarge
	}
	return canonical, nil
}

// DigestCanonical returns the lowercase SHA-256 digest of already-canonical
// document bytes. Callers must only pass CanonicalDocument output.
func DigestCanonical(canonical []byte) string {
	return digestBytes(canonical)
}

// DigestDocument returns the lowercase SHA-256 digest of canonical document bytes.
func DigestDocument(document Document) (string, error) {
	canonical, err := CanonicalDocument(document)
	if err != nil {
		return "", err
	}
	return digestBytes(canonical), nil
}

// DigestCopy returns the lowercase SHA-256 digest of a mandatory-copy catalogue
// described by its canonical entry encoding.
func DigestCopy(version string, entries []CopyEntry) (string, error) {
	payload, err := canonicalBytes(map[string]any{"version": version, "entries": copyEntriesValue(entries)})
	if err != nil {
		return "", err
	}
	return digestBytes(payload), nil
}

func copyEntriesValue(entries []CopyEntry) []any {
	values := make([]any, len(entries))
	for index, entry := range entries {
		values[index] = map[string]any{"key": entry.Key, "value": entry.Value}
	}
	return values
}

func digestBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// canonicalBytes serialises a decoded JSON value using the canonical rules.
func canonicalBytes(value any) ([]byte, error) {
	var builder strings.Builder
	if err := writeCanonical(&builder, value); err != nil {
		return nil, err
	}
	return []byte(builder.String()), nil
}

func writeCanonical(builder *strings.Builder, value any) error {
	switch typed := value.(type) {
	case nil:
		builder.WriteString("null")
	case bool:
		if typed {
			builder.WriteString("true")
		} else {
			builder.WriteString("false")
		}
	case string:
		writeCanonicalString(builder, typed)
	case json.Number:
		if !validCanonicalNumber(typed.String()) {
			return fmt.Errorf("%w: non-integer number", ErrInvalid)
		}
		builder.WriteString(typed.String())
	case []any:
		builder.WriteByte('[')
		for index, item := range typed {
			if index > 0 {
				builder.WriteByte(',')
			}
			if err := writeCanonical(builder, item); err != nil {
				return err
			}
		}
		builder.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		builder.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				builder.WriteByte(',')
			}
			writeCanonicalString(builder, key)
			builder.WriteByte(':')
			if err := writeCanonical(builder, typed[key]); err != nil {
				return err
			}
		}
		builder.WriteByte('}')
	default:
		return fmt.Errorf("%w: unsupported canonical type", ErrInvalid)
	}
	return nil
}

func validCanonicalNumber(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character < '0' || character > '9' {
			return false
		}
	}
	return len(value) <= 1 || value[0] != '0'
}

const hexDigits = "0123456789abcdef"

func writeCanonicalString(builder *strings.Builder, value string) {
	builder.WriteByte('"')
	for _, character := range value {
		switch character {
		case '"':
			builder.WriteString(`\"`)
		case '\\':
			builder.WriteString(`\\`)
		case '\b':
			builder.WriteString(`\b`)
		case '\f':
			builder.WriteString(`\f`)
		case '\n':
			builder.WriteString(`\n`)
		case '\r':
			builder.WriteString(`\r`)
		case '\t':
			builder.WriteString(`\t`)
		default:
			if character < 0x20 {
				builder.WriteString(`\u00`)
				builder.WriteByte(hexDigits[(character>>4)&0xf])
				builder.WriteByte(hexDigits[character&0xf])
				continue
			}
			builder.WriteRune(character)
		}
	}
	builder.WriteByte('"')
}

// validUTF8Bounded reports whether value is valid UTF-8 within limit bytes.
func validUTF8Bounded(value string, limit int) bool {
	return len(value) <= limit && utf8.ValidString(value)
}

// numericValue converts a bounded canonical integer string.
func numericValue(value string) int64 {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}
