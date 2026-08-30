package realtime_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRealtimeSchemaIsAValidJSONSchemaDocument(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	encoded, err := root.ReadFile("contracts/capture/realtime/v1/message.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Dialect string                     `json:"$schema"`
		ID      string                     `json:"$id"`
		Type    string                     `json:"type"`
		OneOf   []map[string]string        `json:"oneOf"`
		Defs    map[string]json.RawMessage `json:"$defs"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Dialect != "https://json-schema.org/draft/2020-12/schema" || schema.ID == "" ||
		schema.Type != "object" || len(schema.OneOf) != 18 || len(schema.Defs) == 0 {
		t.Fatalf("schema header or catalogue is incomplete: %#v", schema)
	}
	for _, forbidden := range []string{"evidence_bytes", "file_name", "content_digest", "capture_token", "connection_ticket", "biometric_template", "provider_result"} {
		if strings.Contains(string(encoded), `"`+forbidden+`"`) {
			t.Fatalf("schema exposes forbidden field %q", forbidden)
		}
	}
}
