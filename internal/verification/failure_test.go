package verification

import (
	"errors"
	"strings"
	"testing"
)

func TestSessionFailureValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		failure SessionFailure
		wantErr bool
	}{
		{name: "policy prohibition", failure: SessionFailure{Class: "policy", Code: "workflow_prohibited"}},
		{name: "digits and underscores", failure: SessionFailure{Class: "provider_job", Code: "unavailable_2"}},
		{name: "class at bound", failure: SessionFailure{Class: "a" + strings.Repeat("b", 30) + "c", Code: "code"}},
		{name: "code at bound", failure: SessionFailure{Class: "policy", Code: "a" + strings.Repeat("b", 62) + "c"}},
		{name: "zero value", failure: SessionFailure{}, wantErr: true},
		{name: "uppercase class", failure: SessionFailure{Class: "Policy", Code: "workflow_prohibited"}, wantErr: true},
		{name: "dotted class", failure: SessionFailure{Class: "policy.routing", Code: "workflow_prohibited"}, wantErr: true},
		{name: "class leading digit", failure: SessionFailure{Class: "1policy", Code: "workflow_prohibited"}, wantErr: true},
		{name: "code with space", failure: SessionFailure{Class: "policy", Code: "workflow prohibited"}, wantErr: true},
		{name: "code with separator", failure: SessionFailure{Class: "policy", Code: "workflow.prohibited"}, wantErr: true},
		{name: "class over bound", failure: SessionFailure{Class: "a" + strings.Repeat("b", 32), Code: "workflow_prohibited"}, wantErr: true},
		{name: "code over bound", failure: SessionFailure{Class: "policy", Code: "a" + strings.Repeat("b", 63) + "c"}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.failure.Validate()
			if test.wantErr {
				if !errors.Is(err, ErrSessionConflict) {
					t.Fatalf("Validate() = %v, want conflict", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestSessionWithFailureRequiresFailedState(t *testing.T) {
	t.Parallel()
	valid := SessionFailure{Class: "policy", Code: "workflow_prohibited"}
	if _, err := (Session{state: SessionStateCollecting}).WithFailure(valid); err == nil {
		t.Fatal("non-failed session accepted a failure")
	}
	failed := Session{state: SessionStateFailed}
	if _, err := failed.WithFailure(SessionFailure{Class: "Policy", Code: "workflow_prohibited"}); err == nil {
		t.Fatal("invalid failure accepted")
	}
	attached, err := failed.WithFailure(valid)
	if err != nil || attached.Failure() != valid {
		t.Fatalf("WithFailure() = %+v, %v", attached, err)
	}
}
