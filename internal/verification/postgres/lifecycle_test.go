package postgres

import (
	"bytes"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestLifecycleCommandDigestIncludesFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	eventID, err := id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	verificationID, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	command := verification.LifecycleCommand{
		EventID: eventID, VerificationID: verificationID, ExpectedVersion: 1,
		Target: verification.SessionStateFailed, ActorID: "tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		OccurredAt: now, Failure: verification.SessionFailure{Class: "policy", Code: "workflow_prohibited"},
	}
	_, first, err := lifecycleCommandDigest(command)
	if err != nil {
		t.Fatal(err)
	}
	changed := command
	changed.Failure.Code = "different_reason"
	_, second, err := lifecycleCommandDigest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("failure change did not change replay identity")
	}
	changed = command
	changed.Failure.Class = "provider"
	_, third, err := lifecycleCommandDigest(changed)
	if err != nil {
		t.Fatal(err)
	}
	if first == third {
		t.Fatal("failure class change did not change replay identity")
	}
	// Non-failed commands keep the pre-failure digest shape so in-flight
	// replays written before this migration still match.
	unfailed := command
	unfailed.Target = verification.SessionStateProcessing
	unfailed.Failure = verification.SessionFailure{}
	encoded, _, err := lifecycleCommandDigest(unfailed)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(`"failure"`)) {
		t.Fatalf("non-failed command encoded a failure member: %s", encoded)
	}
}
