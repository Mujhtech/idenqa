package barcode_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/document/barcode"
)

func TestParseFormats(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		payload   string
		format    barcode.Format
		wantKey   string
		wantValue string
	}{
		{
			name:    "aamva",
			payload: "@\n\x1e\rANSI 636000080002DL00410272\nDCSSMITH\nDACJOHN\nDBB01151990\nDBA01302030\nDAQ123456789\nDCGUSA\nDBC1\n",
			format:  barcode.FormatAAMVA,
			wantKey: "dcs", wantValue: "SMITH",
		},
		{
			name:    "aamva header only",
			payload: "@\n\x1e\rANSI 636000080002DL00410272",
			format:  barcode.FormatAAMVA,
			wantKey: "subfile", wantValue: "DL",
		},
		{
			name: "vcard",
			payload: "BEGIN:VCARD\r\nVERSION:3.0\r\nN:Doe;John;;;\r\nFN:John Doe\r\n" +
				"TEL;TYPE=CELL:+15555550100\r\nEND:VCARD\r\n",
			format:  barcode.FormatVCard,
			wantKey: "n", wantValue: "Doe;John;;;",
		},
		{
			name:    "json nested",
			payload: `{"holder":{"surname":"Doe","given_name":"John"},"document_number":"X123","expiry_date":"2030-01-01","verified":true,"sequence":7}`,
			format:  barcode.FormatJSON,
			wantKey: "holder.surname", wantValue: "Doe",
		},
		{
			name:    "url",
			payload: "https://example.test/verify?document_number=X123&dob=1990-01-01",
			format:  barcode.FormatURL,
			wantKey: "document_number", wantValue: "X123",
		},
		{
			name:    "query",
			payload: "document_number=X123&dob=1990-01-01",
			format:  barcode.FormatQuery,
			wantKey: "document_number", wantValue: "X123",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			payload, err := barcode.Parse(test.payload)
			if err != nil {
				t.Fatal(err)
			}
			if payload.Format != test.format {
				t.Fatalf("format = %s", payload.Format)
			}
			value, ok := payload.Value(test.wantKey)
			if !ok || value != test.wantValue {
				t.Fatalf("%s = %q, %t", test.wantKey, value, ok)
			}
			if !sortedByKey(payload.Fields) {
				t.Fatal("fields are not sorted")
			}
		})
	}
}

func TestParseRejections(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		want    error
	}{
		{name: "empty", payload: "", want: barcode.ErrInvalid},
		{name: "oversized", payload: strings.Repeat("A", barcode.MaximumPayloadBytes+1), want: barcode.ErrOversized},
		{name: "unsupported", payload: "plain text without structure", want: barcode.ErrUnsupported},
		{name: "malformed aamva header", payload: "@\n\x1eANSI 63600008000X", want: barcode.ErrInvalid},
		{name: "malformed json", payload: `{"document_number":`, want: barcode.ErrInvalid},
		{name: "json array", payload: `[{"document_number":"X"}]`, want: barcode.ErrUnsupported},
		{name: "json nested array", payload: `{"names":["a","b"]}`, want: barcode.ErrAmbiguous},
		{name: "unterminated vcard", payload: "BEGIN:VCARD\r\nN:Doe;John;;;\r\n", want: barcode.ErrInvalid},
		{name: "unsupported url scheme", payload: "ftp://example.test/file", want: barcode.ErrUnsupported},
		{name: "duplicate query", payload: "document_number=X&document_number=Y", want: barcode.ErrAmbiguous},
		{name: "unsafe key", payload: `{"document[number]":"X"}`, want: barcode.ErrInvalid},
		{name: "conflicting duplicate json keys", payload: `{"document_number":"X","document-number":"Y"}`, want: barcode.ErrAmbiguous},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := barcode.Parse(test.payload); !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestParseAAMVASubfileHeaderToken(t *testing.T) {
	t.Parallel()
	payload, err := barcode.Parse("@\x1eANSI 636000080002DL00410272ZC03160009\nDAQ123")
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := payload.Value("subfile"); value != "DL" {
		t.Fatalf("subfile = %q", value)
	}
	if value, _ := payload.Value("daq"); value != "123" {
		t.Fatalf("daq = %q", value)
	}
}

func FuzzParse(f *testing.F) {
	f.Add("@\n\x1e\rANSI 636000080002DL00410272\nDCSSMITH\nDACJOHN\n")
	f.Add("BEGIN:VCARD\r\nN:Doe;John;;;\r\nEND:VCARD\r\n")
	f.Add(`{"document_number":"X123","nested":{"a":"b"}}`)
	f.Add("https://example.test/verify?document_number=X123")
	f.Add("document_number=X123&dob=1990-01-01")
	f.Fuzz(func(t *testing.T, payload string) {
		parsed, err := barcode.Parse(payload)
		if err != nil {
			return
		}
		if len(parsed.Fields) > barcode.MaximumFields {
			t.Fatalf("unbounded fields: %d", len(parsed.Fields))
		}
		seen := make(map[string]struct{}, len(parsed.Fields))
		for _, field := range parsed.Fields {
			if field.Key == "" || len(field.Key) > barcode.MaximumKeyLength {
				t.Fatalf("invalid key: %q", field.Key)
			}
			if len(field.Value) > barcode.MaximumValueLength {
				t.Fatalf("unbounded value: %d", len(field.Value))
			}
			if _, exists := seen[field.Key]; exists {
				t.Fatalf("duplicate key: %q", field.Key)
			}
			seen[field.Key] = struct{}{}
		}
	})
}

func sortedByKey(fields []barcode.Field) bool {
	for index := 1; index < len(fields); index++ {
		if fields[index-1].Key >= fields[index].Key {
			return false
		}
	}
	return true
}
