package policyv1

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"
	"unicode/utf8"
)

var (
	policyIDPattern = regexp.MustCompile(`^pol_[0-9A-HJKMNP-TV-Z]{26}$`)
	tokenPattern    = regexp.MustCompile(`^[a-z][a-z0-9._:-]*$`)
	factKeyPattern  = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
)

// Parse accepts a bounded closed JSON document and returns normalized meaning.
func Parse(input []byte) (Document, error) {
	if len(input) == 0 || len(input) > MaximumDocumentBytes || !utf8.Valid(input) {
		return Document{}, ErrInvalid
	}
	if err := rejectDuplicateFields(input); err != nil {
		return Document{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var document Document
	if err := decoder.Decode(&document); err != nil {
		return Document{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	if err := requireEOF(decoder); err != nil {
		return Document{}, err
	}
	return normalize(document)
}

// ParseCanonical additionally requires byte-exact canonical JSON.
func ParseCanonical(input []byte) (Document, error) {
	document, err := Parse(input)
	if err != nil {
		return Document{}, err
	}
	canonical, err := Canonical(document)
	if err != nil {
		return Document{}, err
	}
	if !bytes.Equal(input, canonical) {
		return Document{}, ErrNonCanonical
	}
	return document, nil
}

// Validate checks the complete closed document contract.
func Validate(document Document) error {
	_, err := normalize(document)
	return err
}

// Canonical returns deterministic JSON independent of caller slice ordering.
func Canonical(document Document) ([]byte, error) {
	normalized, err := normalize(document)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("encode policy v1 document: %w", err)
	}
	if len(encoded) > MaximumDocumentBytes {
		return nil, ErrInvalid
	}
	return encoded, nil
}

// Digest returns the lowercase SHA-256 digest of canonical document meaning.
func Digest(document Document) (string, error) {
	canonical, err := Canonical(document)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func normalize(document Document) (Document, error) {
	if document.SchemaMajor != SchemaMajor || document.SchemaMinor != SchemaMinor {
		return Document{}, ErrVersion
	}
	if !policyIDPattern.MatchString(document.PolicyID) || document.Revision == 0 ||
		len(document.Rules) == 0 || len(document.Rules) > MaximumRules ||
		(document.VerifiedAssurance != "" && !validToken(document.VerifiedAssurance, 128)) {
		return Document{}, ErrInvalid
	}
	normalized := document
	normalized.Rules = make([]Rule, len(document.Rules))
	hasVerified := false
	for index, rule := range document.Rules {
		validated, err := normalizeRule(rule)
		if err != nil {
			return Document{}, fmt.Errorf("%w: rule %d", err, index)
		}
		normalized.Rules[index] = validated
		hasVerified = hasVerified || validated.Result.Directive == DirectiveCompleteVerified
	}
	if hasVerified && document.VerifiedAssurance == "" {
		return Document{}, ErrInvalid
	}
	sort.Slice(normalized.Rules, func(left, right int) bool {
		return normalized.Rules[left].Name < normalized.Rules[right].Name
	})
	for index := 1; index < len(normalized.Rules); index++ {
		if normalized.Rules[index-1].Name == normalized.Rules[index].Name {
			return Document{}, ErrInvalid
		}
	}
	return normalized, nil
}

func normalizeRule(rule Rule) (Rule, error) {
	if !validToken(rule.Name, 128) || len(rule.When) == 0 ||
		len(rule.When) > MaximumExpressionBytes || !utf8.ValidString(rule.When) ||
		!validState(rule.Result.State) || !validDirective(rule.Result.Directive) ||
		rule.Result.Priority == 0 || rule.Result.Priority > 1000 ||
		len(rule.Result.ContributingFacts) == 0 ||
		len(rule.Result.ContributingFacts) > MaximumFactsPerRule ||
		len(rule.Result.ReasonCodes) > MaximumReasonsPerRule {
		return Rule{}, ErrInvalid
	}
	if !validResultMeaning(rule.Result.State, rule.Result.Directive) {
		return Rule{}, ErrInvalid
	}
	normalized := rule
	normalized.Result.ContributingFacts = slices.Clone(rule.Result.ContributingFacts)
	normalized.Result.ReasonCodes = slices.Clone(rule.Result.ReasonCodes)
	for _, fact := range normalized.Result.ContributingFacts {
		if len(fact) > 128 || !factKeyPattern.MatchString(fact) {
			return Rule{}, ErrInvalid
		}
	}
	for _, reason := range normalized.Result.ReasonCodes {
		if !validToken(reason, 100) {
			return Rule{}, ErrInvalid
		}
	}
	sort.Strings(normalized.Result.ContributingFacts)
	sort.Strings(normalized.Result.ReasonCodes)
	if adjacentDuplicate(normalized.Result.ContributingFacts) || adjacentDuplicate(normalized.Result.ReasonCodes) {
		return Rule{}, ErrInvalid
	}
	return normalized, nil
}

func validState(value RequirementState) bool {
	switch value {
	case RequirementSatisfied, RequirementNotSatisfied, RequirementInconclusive,
		RequirementUnavailable, RequirementProhibited:
		return true
	default:
		return false
	}
}

func validDirective(value Directive) bool {
	switch value {
	case DirectiveCompleteVerified, DirectiveCompleteNotVerified, DirectiveCompleteInconclusive,
		DirectiveRequestInput, DirectiveRunCheck, DirectiveRetryCheck, DirectiveUseFallback,
		DirectiveRouteManualReview, DirectiveFailWorkflow:
		return true
	default:
		return false
	}
}

func validResultMeaning(state RequirementState, directive Directive) bool {
	if state == RequirementProhibited {
		return directive == DirectiveFailWorkflow
	}
	switch directive {
	case DirectiveCompleteVerified:
		return state == RequirementSatisfied
	case DirectiveCompleteNotVerified:
		return state == RequirementNotSatisfied
	case DirectiveCompleteInconclusive:
		return state == RequirementInconclusive || state == RequirementUnavailable
	default:
		return true
	}
}

func validToken(value string, maximum int) bool {
	return len(value) > 0 && len(value) <= maximum && tokenPattern.MatchString(value)
}

func adjacentDuplicate(values []string) bool {
	for index := 1; index < len(values); index++ {
		if values[index-1] == values[index] {
			return true
		}
	}
	return false
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ErrInvalid
	}
	return nil
}

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
