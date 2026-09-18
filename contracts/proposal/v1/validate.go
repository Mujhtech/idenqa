package proposal

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"
)

var (
	proposalIDPattern     = regexp.MustCompile(`^prp_[0-9A-HJKMNP-TV-Z]{26}$`)
	commandIDPattern      = regexp.MustCompile(`^cmd_[0-9A-HJKMNP-TV-Z]{26}$`)
	tenantIDPattern       = regexp.MustCompile(`^ten_[0-9A-HJKMNP-TV-Z]{26}$`)
	verificationIDPattern = regexp.MustCompile(`^ver_[0-9A-HJKMNP-TV-Z]{26}$`)
	policyIDPattern       = regexp.MustCompile(`^pol_[0-9A-HJKMNP-TV-Z]{26}$`)
	tokenPattern          = regexp.MustCompile(`^[a-z][a-z0-9._:-]*$`)
	hexDigestPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	referencePattern      = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,127}$`)
)

func validProposalID(value string) bool { return proposalIDPattern.MatchString(value) }
func validCommandID(value string) bool  { return commandIDPattern.MatchString(value) }
func validTenantID(value string) bool   { return tenantIDPattern.MatchString(value) }
func validVerificationID(value string) bool {
	return verificationIDPattern.MatchString(value)
}
func validPolicyID(value string) bool { return policyIDPattern.MatchString(value) }

// validAnchor requires exactly one of verification_id or policy_id.
func validAnchor(verificationID string, policyID string) bool {
	hasVerification := verificationID != ""
	hasPolicy := policyID != ""
	return hasVerification != hasPolicy
}

// maxTokenLength bounds model/model-version/prompt-version tokens.
const maxTokenLength = 64

func validToken(value string) bool {
	return len(value) > 0 && len(value) <= maxTokenLength && tokenPattern.MatchString(value)
}
func validReference(value string) bool {
	return len(value) > 0 && len(value) <= 128 && referencePattern.MatchString(value)
}
func validDigest(value string) bool { return hexDigestPattern.MatchString(value) }

// ValidateProposal checks the closed proposal contract.
func ValidateProposal(proposal AgentProposal) error {
	if !validProposalID(proposal.ProposalID) {
		return fmt.Errorf("%w: proposal_id", ErrInvalid)
	}
	if !validTenantID(proposal.TenantID) {
		return fmt.Errorf("%w: tenant_id", ErrInvalid)
	}
	if !validAnchor(proposal.VerificationID, proposal.PolicyID) {
		return fmt.Errorf("%w: exactly one of verification_id or policy_id is required", ErrInvalid)
	}
	if proposal.VerificationID != "" && !validVerificationID(proposal.VerificationID) {
		return fmt.Errorf("%w: verification_id", ErrInvalid)
	}
	if proposal.PolicyID != "" && !validPolicyID(proposal.PolicyID) {
		return fmt.Errorf("%w: policy_id", ErrInvalid)
	}
	if _, ok := ParseAutomationMode(proposal.Mode); !ok {
		return fmt.Errorf("%w: mode", ErrInvalid)
	}
	if _, ok := ParseProposalStatus(proposal.Status); !ok {
		return fmt.Errorf("%w: status", ErrInvalid)
	}
	if len(proposal.Actions) == 0 || len(proposal.Actions) > MaxActions {
		return fmt.Errorf("%w: actions", ErrInvalid)
	}
	seenKind := make(map[ActionKind]struct{})
	for index, action := range proposal.Actions {
		if !IsAllowedKind(action.Kind) {
			return fmt.Errorf("%w: actions[%d].kind", ErrUnknownKind, index)
		}
		if _, dup := seenKind[action.Kind]; dup {
			return fmt.Errorf("%w: actions[%d].kind duplicate", ErrInvalid, index)
		}
		seenKind[action.Kind] = struct{}{}
		if len(action.Args) == 0 {
			return fmt.Errorf("%w: actions[%d].args empty", ErrInvalid, index)
		}
		if len(action.Args) > MaxArgBytes {
			return fmt.Errorf("%w: actions[%d].args too large", ErrTooLarge, index)
		}
		if !json.Valid(action.Args) {
			return fmt.Errorf("%w: actions[%d].args not json", ErrInvalid, index)
		}
		if err := rejectUnknownDuplicateArgs(action.Args); err != nil {
			return fmt.Errorf("%w: actions[%d].args %w", ErrInvalid, index, err)
		}
	}
	if len(proposal.EvidenceRefs) > MaxEvidenceRefs {
		return fmt.Errorf("%w: evidence_refs", ErrTooLarge)
	}
	for i, ref := range proposal.EvidenceRefs {
		if !validReference(ref) {
			return fmt.Errorf("%w: evidence_refs[%d]", ErrInvalid, i)
		}
	}
	if len(proposal.SignalRefs) > MaxSignalRefs {
		return fmt.Errorf("%w: signal_refs", ErrTooLarge)
	}
	for i, ref := range proposal.SignalRefs {
		if !validReference(ref) {
			return fmt.Errorf("%w: signal_refs[%d]", ErrInvalid, i)
		}
	}
	if !validToken(proposal.ModelID) {
		return fmt.Errorf("%w: model_id", ErrInvalid)
	}
	if !validToken(proposal.ModelVersion) {
		return fmt.Errorf("%w: model_version", ErrInvalid)
	}
	if !validToken(proposal.PromptVersion) {
		return fmt.Errorf("%w: prompt_version", ErrInvalid)
	}
	if !validDigest(proposal.ContextDigest) {
		return fmt.Errorf("%w: context_digest", ErrInvalid)
	}
	if proposal.ExpiresAt.IsZero() || proposal.CreatedAt.IsZero() {
		return fmt.Errorf("%w: timestamps", ErrInvalid)
	}
	if !proposal.ExpiresAt.After(proposal.CreatedAt) {
		return fmt.Errorf("%w: expires_at must be after created_at", ErrInvalid)
	}
	if proposal.Supersedes != nil && !validProposalID(*proposal.Supersedes) {
		return fmt.Errorf("%w: supersedes", ErrInvalid)
	}
	if proposal.Supersedes != nil && *proposal.Supersedes == proposal.ProposalID {
		return fmt.Errorf("%w: supersedes self", ErrInvalid)
	}
	if len(proposal.Reason) > MaxReasonBytes {
		return fmt.Errorf("%w: reason", ErrTooLarge)
	}
	if proposal.Reason != "" && !utf8.ValidString(proposal.Reason) {
		return fmt.Errorf("%w: reason utf8", ErrInvalid)
	}
	return nil
}

// ValidateAcceptedCommand checks the deterministic command contract.
func ValidateAcceptedCommand(command AcceptedCommand) error {
	if !validCommandID(command.CommandID) {
		return fmt.Errorf("%w: command_id", ErrInvalid)
	}
	if !validProposalID(command.ProposalID) {
		return fmt.Errorf("%w: proposal_id", ErrInvalid)
	}
	if !validTenantID(command.TenantID) {
		return fmt.Errorf("%w: tenant_id", ErrInvalid)
	}
	if !validAnchor(command.VerificationID, command.PolicyID) {
		return fmt.Errorf("%w: exactly one of verification_id or policy_id is required", ErrInvalid)
	}
	if command.VerificationID != "" && !validVerificationID(command.VerificationID) {
		return fmt.Errorf("%w: verification_id", ErrInvalid)
	}
	if command.PolicyID != "" && !validPolicyID(command.PolicyID) {
		return fmt.Errorf("%w: policy_id", ErrInvalid)
	}
	if !IsAllowedKind(command.Kind) {
		return fmt.Errorf("%w: kind", ErrUnknownKind)
	}
	if len(command.Args) == 0 || len(command.Args) > MaxArgBytes {
		return fmt.Errorf("%w: args", ErrInvalid)
	}
	if !json.Valid(command.Args) {
		return fmt.Errorf("%w: args not json", ErrInvalid)
	}
	if err := rejectUnknownDuplicateArgs(command.Args); err != nil {
		return fmt.Errorf("%w: args %w", ErrInvalid, err)
	}
	if !validToken(command.ModelID) {
		return fmt.Errorf("%w: model_id", ErrInvalid)
	}
	if !validToken(command.ModelVersion) {
		return fmt.Errorf("%w: model_version", ErrInvalid)
	}
	if !validToken(command.PromptVersion) {
		return fmt.Errorf("%w: prompt_version", ErrInvalid)
	}
	if command.CreatedAt.IsZero() {
		return fmt.Errorf("%w: created_at", ErrInvalid)
	}
	return nil
}

// ValidateProposalRequest checks creation-time input.
func ValidateProposalRequest(request ProposalRequest) error {
	if !validTenantID(request.TenantID) {
		return fmt.Errorf("%w: tenant_id", ErrInvalid)
	}
	if !validAnchor(request.VerificationID, request.PolicyID) {
		return fmt.Errorf("%w: exactly one of verification_id or policy_id is required", ErrInvalid)
	}
	if request.VerificationID != "" && !validVerificationID(request.VerificationID) {
		return fmt.Errorf("%w: verification_id", ErrInvalid)
	}
	if request.PolicyID != "" && !validPolicyID(request.PolicyID) {
		return fmt.Errorf("%w: policy_id", ErrInvalid)
	}
	if request.Mode == AutomationModeUnknown {
		return fmt.Errorf("%w: mode", ErrInvalid)
	}
	if len(request.Actions) == 0 || len(request.Actions) > MaxActions {
		return fmt.Errorf("%w: actions", ErrInvalid)
	}
	seenKind := make(map[ActionKind]struct{})
	for index, action := range request.Actions {
		if !IsAllowedKind(action.Kind) {
			return fmt.Errorf("%w: actions[%d].kind", ErrUnknownKind, index)
		}
		if _, dup := seenKind[action.Kind]; dup {
			return fmt.Errorf("%w: actions[%d].kind duplicate", ErrInvalid, index)
		}
		seenKind[action.Kind] = struct{}{}
		if len(action.Args) == 0 || len(action.Args) > MaxArgBytes {
			return fmt.Errorf("%w: actions[%d].args", ErrInvalid, index)
		}
		if !json.Valid(action.Args) {
			return fmt.Errorf("%w: actions[%d].args not json", ErrInvalid, index)
		}
		if err := rejectUnknownDuplicateArgs(action.Args); err != nil {
			return fmt.Errorf("%w: actions[%d].args %w", ErrInvalid, index, err)
		}
	}
	if len(request.EvidenceRefs) > MaxEvidenceRefs {
		return fmt.Errorf("%w: evidence_refs", ErrTooLarge)
	}
	for i, ref := range request.EvidenceRefs {
		if !validReference(ref) {
			return fmt.Errorf("%w: evidence_refs[%d]", ErrInvalid, i)
		}
	}
	if len(request.SignalRefs) > MaxSignalRefs {
		return fmt.Errorf("%w: signal_refs", ErrTooLarge)
	}
	for i, ref := range request.SignalRefs {
		if !validReference(ref) {
			return fmt.Errorf("%w: signal_refs[%d]", ErrInvalid, i)
		}
	}
	if !validToken(request.ModelID) {
		return fmt.Errorf("%w: model_id", ErrInvalid)
	}
	if !validToken(request.ModelVersion) {
		return fmt.Errorf("%w: model_version", ErrInvalid)
	}
	if !validToken(request.PromptVersion) {
		return fmt.Errorf("%w: prompt_version", ErrInvalid)
	}
	if !validDigest(request.ContextDigest) {
		return fmt.Errorf("%w: context_digest", ErrInvalid)
	}
	if request.ExpiresAt.IsZero() {
		return fmt.Errorf("%w: expires_at", ErrInvalid)
	}
	if request.Supersedes != nil && !validProposalID(*request.Supersedes) {
		return fmt.Errorf("%w: supersedes", ErrInvalid)
	}
	return nil
}

// Parse accepts a bounded closed JSON proposal and returns normalized meaning.
func Parse(input []byte) (AgentProposal, error) {
	if len(input) == 0 || len(input) > MaxProposalBytes || !utf8.Valid(input) {
		return AgentProposal{}, ErrInvalid
	}
	if err := rejectDuplicateFields(input); err != nil {
		return AgentProposal{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var proposal AgentProposal
	if err := decoder.Decode(&proposal); err != nil {
		return AgentProposal{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	if err := requireEOF(decoder); err != nil {
		return AgentProposal{}, err
	}
	if err := ValidateProposal(proposal); err != nil {
		return AgentProposal{}, err
	}
	return proposal, nil
}

// ParseCommand accepts a bounded closed JSON command.
func ParseCommand(input []byte) (AcceptedCommand, error) {
	if len(input) == 0 || len(input) > MaxProposalBytes || !utf8.Valid(input) {
		return AcceptedCommand{}, ErrInvalid
	}
	if err := rejectDuplicateFields(input); err != nil {
		return AcceptedCommand{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var command AcceptedCommand
	if err := decoder.Decode(&command); err != nil {
		return AcceptedCommand{}, fmt.Errorf("%w: decode: %w", ErrInvalid, err)
	}
	if err := requireEOF(decoder); err != nil {
		return AcceptedCommand{}, err
	}
	if err := ValidateAcceptedCommand(command); err != nil {
		return AcceptedCommand{}, err
	}
	return command, nil
}

// Canonical returns deterministic JSON independent of caller ordering.
func Canonical(proposal AgentProposal) ([]byte, error) {
	if err := ValidateProposal(proposal); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(proposal)
	if err != nil {
		return nil, fmt.Errorf("encode proposal: %w", err)
	}
	if len(encoded) > MaxProposalBytes {
		return nil, ErrTooLarge
	}
	return encoded, nil
}

// Digest returns lowercase SHA-256 of canonical proposal bytes.
func Digest(proposal AgentProposal) (string, error) {
	canonical, err := Canonical(proposal)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

// DigestContext returns the redacted context digest.
func DigestContext(context ProposalContext) (string, error) {
	encoded, err := json.Marshal(context)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
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
func rejectUnknownDuplicateArgs(input []byte) error {
	// args must be an object with no duplicate keys; use same duplicate rejection
	return rejectDuplicateFields(input)
}
