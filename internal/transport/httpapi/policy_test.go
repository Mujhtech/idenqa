package httpapi

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestPolicyDecodeRejectsAmbiguousAndUnboundedMeaning(t *testing.T) {
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{"valid", `{"definition":{"schema_major":1,"schema_minor":0,"verified_assurance":"","rules":[]}}`, true},
		{"missing minor", `{"definition":{"schema_major":1,"verified_assurance":"","rules":[]}}`, false},
		{"null minor", `{"definition":{"schema_major":1,"schema_minor":null,"verified_assurance":"","rules":[]}}`, false},
		{"null assurance", `{"definition":{"schema_major":1,"schema_minor":0,"verified_assurance":null,"rules":[]}}`, false},
		{"case alias", `{"definition":{"schema_major":1,"schema_minor":0,"verified_assurance":"","rules":[]},"Definition":{}}`, false},
		{"null reasons", `{"definition":{"schema_major":1,"schema_minor":0,"verified_assurance":"","rules":[{"name":"rule","when":"true","result":{"state":"prohibited","directive":"fail_workflow","priority":1,"contributing_facts":["synthetic.document"],"reason_codes":null}}]}}`, false},
		{"missing reasons", `{"definition":{"schema_major":1,"schema_minor":0,"verified_assurance":"","rules":[{"name":"rule","when":"true","result":{"state":"prohibited","directive":"fail_workflow","priority":1,"contributing_facts":["synthetic.document"]}}]}}`, false},
		{"duplicate envelope", `{"definition":{},"definition":{}}`, false},
		{"duplicate nested", `{"definition":{"schema_major":1,"schema_major":2}}`, false},
		{"duplicate rule", `{"definition":{"rules":[{"name":"first","name":"second"}]}}`, false},
		{"unknown", `{"definition":{"unexpected":true}}`, false},
		{"trailing", `{"definition":{}} {}`, false},
		{"invalid utf8", "{\"definition\":{\"verified_assurance\":\"\xff\"}}", false},
		{"oversized", `{"definition":{"verified_assurance":"` + strings.Repeat("a", 300000) + `"}}`, false},
		{"deeply nested", strings.Repeat("[", 40) + "0" + strings.Repeat("]", 40), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/policies", bytes.NewBufferString(test.body))
			request.Header.Set("Content-Type", "application/json")
			_, err := decodePolicyJSON[struct {
				Definition policy.Definition `json:"definition"`
			}](request)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
		})
	}
}
