package v1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// ResourceOperation binds an exact immutable workload selection. Selectors are
// ordered provider/model provenance, evidence policy or review configuration;
// they never contain subjects, evidence IDs, secrets or request identifiers.
// The same published helper can prepare an operator's explicit dependent rules.
func ResourceOperation(kind string, selectors ...string) (string, error) {
	if !slices.Contains([]string{"provider", "model", "evidence", "review"}, kind) || len(selectors) == 0 || len(selectors) > 8 {
		return "", ErrInvalid
	}
	for _, value := range selectors {
		if len(value) == 0 || len(value) > 512 {
			return "", ErrInvalid
		}
	}
	raw, err := json.Marshal(selectors)
	if err != nil {
		return "", ErrInvalid
	}
	digest := sha256.Sum256(raw)
	return "resources.accept/" + kind + "/" + hex.EncodeToString(digest[:]), nil
}

// DependentOperation confines a resource selection to the exact accepted
// workflow rule. Another workflow in the same snapshot cannot lend its grants.
func DependentOperation(workflow, resource string) (string, error) {
	if !operation.MatchString(workflow) || (!strings.HasPrefix(workflow, "verifications.create/") && !strings.HasPrefix(workflow, "verifications.recapture/")) || !operation.MatchString(resource) || !strings.HasPrefix(resource, "resources.accept/") {
		return "", ErrInvalid
	}
	raw, _ := json.Marshal([]string{workflow, resource})
	digest := sha256.Sum256(raw)
	return "workflows.depend/" + hex.EncodeToString(digest[:]), nil
}
