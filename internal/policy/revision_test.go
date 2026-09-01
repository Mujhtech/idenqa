package policy_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestRevisionCanonicalAndActivation(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	canonical := catalogCanonical(t)
	evaluator := policy.EvaluatorReference{Major: 1, Minor: 0, Digest: string(bytes.Repeat([]byte{'a'}, 64))}
	revision, err := policy.NewRevisionCanonical(canonical, evaluator, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	canonical[0] = 'x'
	if revision.Canonical()[0] != '{' {
		t.Fatal("revision retained caller-owned canonical bytes")
	}
	returned := revision.Canonical()
	returned[0] = 'x'
	if revision.Canonical()[0] != '{' {
		t.Fatal("revision exposed mutable canonical bytes")
	}

	actor, err := id.ParseAPIKey("key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	activation, err := policy.RestoreActivation(revision, 1, 0, actor, createdAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if activation.Version() != 1 || activation.PreviousRevision() != 0 ||
		activation.Revision().Reference() != revision.Reference() || activation.Actor() != actor {
		t.Fatalf("activation = %+v", activation)
	}
	if _, err := policy.RestoreActivation(revision, 2, 1, actor, createdAt.Add(time.Second)); !errors.Is(err, policy.ErrActivationConflict) {
		t.Fatalf("same-revision previous error = %v", err)
	}
}

func TestRestoreRevisionRejectsPersistedIdentityMismatch(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	evaluator := policy.EvaluatorReference{Major: 1, Minor: 0, Digest: string(bytes.Repeat([]byte{'b'}, 64))}
	revision, err := policy.NewRevisionCanonical(catalogCanonical(t), evaluator, createdAt)
	if err != nil {
		t.Fatal(err)
	}
	reference := revision.Reference()
	reference.Revision++
	if _, err := policy.RestoreRevision(reference, evaluator, revision.Canonical(), createdAt); !errors.Is(err, policy.ErrRevisionConflict) {
		t.Fatalf("mismatched identity error = %v", err)
	}
}

func catalogCanonical(t *testing.T) []byte {
	t.Helper()
	canonical, err := policyv1.Canonical(policyv1.Document{
		SchemaMajor: 1, SchemaMinor: 0,
		PolicyID: "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", Revision: 1,
		Rules: []policyv1.Rule{{
			Name: "terminal", When: `facts["document.authenticity"] == "satisfied"`,
			Result: policyv1.Result{
				State: policyv1.RequirementNotSatisfied, Directive: policyv1.DirectiveCompleteNotVerified,
				Priority: 1, ContributingFacts: []string{"document.authenticity"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
