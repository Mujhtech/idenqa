package proposal_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
)

func TestProposalEnvelopeCannotCarryRawBytesOrMaps(t *testing.T) {
	t.Parallel()

	// json.RawMessage is intentionally []byte but bounded and validated; allow only for Args fields
	for _, value := range []reflect.Type{reflect.TypeFor[proposalv1.AgentProposal](), reflect.TypeFor[proposalv1.AcceptedCommand](), reflect.TypeFor[proposalv1.ProposalRequest]()} {
		for index := range value.NumField() {
			field := value.Field(index)
			kind := field.Type.Kind()
			if kind == reflect.Map && field.Name != "Actions" {
				t.Fatalf("unsafe payload field %s has type %s", field.Name, field.Type)
			}
			if kind == reflect.Slice && field.Type.Elem().Kind() == reflect.Uint8 {
				// Allow json.RawMessage for bounded action args only
				if field.Type == reflect.TypeFor[json.RawMessage]() && field.Name == "Args" {
					continue
				}
				t.Fatalf("unsafe raw bytes field %s", field.Name)
			}
		}
	}
	// BoundedAction.Args is allowed as json.RawMessage, but must be bounded and validated
	if field, ok := reflect.TypeFor[proposalv1.BoundedAction]().FieldByName("Args"); ok {
		if field.Type != reflect.TypeFor[json.RawMessage]() {
			t.Fatalf("BoundedAction.Args must be json.RawMessage")
		}
	}
}

func TestProposalContractCompatibility(t *testing.T) {
	t.Parallel()

	if !proposalv1.CurrentVersion.Accepts(proposalv1.Version{Major: 1}) ||
		proposalv1.CurrentVersion.Accepts(proposalv1.Version{Major: 2}) ||
		proposalv1.CurrentVersion.Accepts(proposalv1.Version{Major: 1, Minor: 1}) {
		t.Fatal("proposal contract compatibility is not major-stable and minor-monotonic")
	}
}

func TestAllowedKindsAreClosed(t *testing.T) {
	t.Parallel()

	if len(proposalv1.AllowedKinds()) != 8 {
		t.Fatalf("expected 8 allowed kinds, got %d", len(proposalv1.AllowedKinds()))
	}
	for _, kind := range proposalv1.AllowedKinds() {
		if !proposalv1.IsAllowedKind(kind) {
			t.Fatalf("allowed kind %s not recognized", kind)
		}
	}
	if proposalv1.IsAllowedKind("unknown.kind") {
		t.Fatal("unknown kind must not be allowed")
	}
}

func TestProposalValidationAndCanonical(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC().Truncate(time.Microsecond)
	expires := now.Add(time.Hour)
	proposal := proposalv1.AgentProposal{
		ProposalID:     "prp_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		TenantID:       "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		VerificationID: "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Mode:           "assist",
		Status:         "pending",
		Actions: []proposalv1.BoundedAction{
			{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{"summary":true}`)},
		},
		EvidenceRefs:  []string{"evd_ref_1"},
		SignalRefs:    []string{"sig_ref_1"},
		ModelID:       "model.test",
		ModelVersion:  "v1",
		PromptVersion: "p1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:     expires,
		CreatedAt:     now,
	}
	if err := proposalv1.ValidateProposal(proposal); err != nil {
		t.Fatalf("valid proposal rejected: %v", err)
	}
	canonical, err := proposalv1.Canonical(proposal)
	if err != nil {
		t.Fatalf("canonical: %v", err)
	}
	parsed, err := proposalv1.Parse(canonical)
	if err != nil {
		t.Fatalf("parse canonical: %v", err)
	}
	if parsed.ProposalID != proposal.ProposalID {
		t.Fatal("canonical round-trip mismatch")
	}
	digest, err := proposalv1.Digest(proposal)
	if err != nil {
		t.Fatalf("digest: %v", err)
	}
	if len(digest) != 64 {
		t.Fatalf("digest length %d", len(digest))
	}
	// duplicate kind must fail
	proposal.Actions = append(proposal.Actions, proposal.Actions[0])
	if err := proposalv1.ValidateProposal(proposal); err == nil {
		t.Fatal("duplicate kind must be rejected")
	}
}
