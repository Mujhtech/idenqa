package verification

import (
	"encoding/json"
	"os"
	"testing"
)

func TestCaptureProfileSchemas_AreValidJSONSchemaDocuments(t *testing.T) {
	t.Parallel()
	root, err := os.OpenRoot("../..")
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	for _, path := range []string{
		"contracts/capture-profile/v1/profile.schema.json",
		"contracts/capture-profile/v1/registry.schema.json",
	} {
		t.Run(path, func(t *testing.T) {
			encoded, err := root.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%q) error = %v", path, err)
			}
			var schema struct {
				Dialect string `json:"$schema"`
				ID      string `json:"$id"`
				Type    string `json:"type"`
			}
			if err := json.Unmarshal(encoded, &schema); err != nil {
				t.Fatalf("Unmarshal(%q) error = %v", path, err)
			}
			if schema.Dialect != "https://json-schema.org/draft/2020-12/schema" || schema.ID == "" || schema.Type != "object" {
				t.Fatalf("schema header = %#v", schema)
			}
		})
	}
}
