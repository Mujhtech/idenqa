package id_test

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

type fixedClock struct {
	now time.Time
}

func (clock fixedClock) Now() time.Time {
	return clock.now
}

func TestGeneratorCreatesTypedSortableRequestIDs(t *testing.T) {
	t.Parallel()

	source := fixedClock{now: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)}
	generator, err := id.NewGenerator(source, bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}

	first, err := generator.NewRequest()
	if err != nil {
		t.Fatalf("first NewRequest() error = %v", err)
	}
	second, err := generator.NewRequest()
	if err != nil {
		t.Fatalf("second NewRequest() error = %v", err)
	}
	if first.String() >= second.String() {
		t.Fatalf("request IDs are not monotonic: %q >= %q", first, second)
	}

	parsed, err := id.ParseRequest(first.String())
	if err != nil {
		t.Fatalf("ParseRequest() error = %v", err)
	}
	if parsed.String() != first.String() {
		t.Fatalf("parsed request ID = %q, want %q", parsed, first)
	}
}

func TestParseRejectsWrongPrefixAndInvalidULID(t *testing.T) {
	t.Parallel()

	tests := []string{
		"ten_01K3P4NQF00000000000000000",
		"req_not-a-ulid",
	}
	for _, encoded := range tests {
		if _, err := id.ParseRequest(encoded); err == nil {
			t.Errorf("ParseRequest(%q) error = nil", encoded)
		}
	}
}

func TestGeneratorCreatesTypedTenantID(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{2}, 32)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	generated, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	parsed, err := id.ParseTenant(generated.String())
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	if parsed.String() != generated.String() {
		t.Fatalf("ParseTenant() = %q, want %q", parsed, generated)
	}
}

func TestGeneratorCreatesTypedAPIKeyID(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{3}, 32)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	generated, err := generator.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	parsed, err := id.ParseAPIKey(generated.String())
	if err != nil {
		t.Fatalf("ParseAPIKey() error = %v", err)
	}
	if parsed.String() != generated.String() {
		t.Fatalf("ParseAPIKey() = %q, want %q", parsed, generated)
	}
}

func TestGeneratorCreatesTypedProfileID(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{4}, 32)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	generated, err := generator.NewProfile()
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	parsed, err := id.ParseProfile(generated.String())
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	if parsed.String() != generated.String() {
		t.Fatalf("ParseProfile() = %q, want %q", parsed, generated)
	}
}

func TestGeneratorCreatesVerificationEvidenceUploadCaptureTokenEventAndGrantIDs(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{5}, 256)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	verification, err := generator.NewVerification()
	if err != nil {
		t.Fatalf("NewVerification() error = %v", err)
	}
	if parsed, parseErr := id.ParseVerification(verification.String()); parseErr != nil || parsed.String() != verification.String() {
		t.Fatalf("ParseVerification() = %q, %v", parsed, parseErr)
	}
	evidenceID, err := generator.NewEvidence()
	if err != nil {
		t.Fatalf("NewEvidence() error = %v", err)
	}
	if parsed, parseErr := id.ParseEvidence(evidenceID.String()); parseErr != nil || parsed.String() != evidenceID.String() {
		t.Fatalf("ParseEvidence() = %q, %v", parsed, parseErr)
	}
	upload, err := generator.NewUpload()
	if err != nil {
		t.Fatalf("NewUpload() error = %v", err)
	}
	if parsed, parseErr := id.ParseUpload(upload.String()); parseErr != nil || parsed.String() != upload.String() {
		t.Fatalf("ParseUpload() = %q, %v", parsed, parseErr)
	}
	token, err := generator.NewCaptureToken()
	if err != nil {
		t.Fatalf("NewCaptureToken() error = %v", err)
	}
	if parsed, parseErr := id.ParseCaptureToken(token.String()); parseErr != nil || parsed.String() != token.String() {
		t.Fatalf("ParseCaptureToken() = %q, %v", parsed, parseErr)
	}
	event, err := generator.NewEvent()
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	if parsed, parseErr := id.ParseEvent(event.String()); parseErr != nil || parsed.String() != event.String() {
		t.Fatalf("ParseEvent() = %q, %v", parsed, parseErr)
	}
	grant, err := generator.NewGrant()
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	if parsed, parseErr := id.ParseGrant(grant.String()); parseErr != nil || parsed.String() != grant.String() {
		t.Fatalf("ParseGrant() = %q, %v", parsed, parseErr)
	}
	redemption, err := generator.NewRedemption()
	if err != nil {
		t.Fatalf("NewRedemption() error = %v", err)
	}
	if parsed, parseErr := id.ParseRedemption(redemption.String()); parseErr != nil || parsed.String() != redemption.String() {
		t.Fatalf("ParseRedemption() = %q, %v", parsed, parseErr)
	}
}

func TestGeneratorCreatesRealtimeIDs(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{9}, 256)),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		generate func() (string, error)
		parse    func(string) (string, error)
	}{
		{"connection", func() (string, error) { value, err := generator.NewConnection(); return value.String(), err }, func(value string) (string, error) {
			parsed, err := id.ParseConnection(value)
			return parsed.String(), err
		}},
		{"message", func() (string, error) { value, err := generator.NewMessage(); return value.String(), err }, func(value string) (string, error) { parsed, err := id.ParseMessage(value); return parsed.String(), err }},
		{"command", func() (string, error) { value, err := generator.NewCommand(); return value.String(), err }, func(value string) (string, error) { parsed, err := id.ParseCommand(value); return parsed.String(), err }},
		{"challenge", func() (string, error) { value, err := generator.NewChallenge(); return value.String(), err }, func(value string) (string, error) {
			parsed, err := id.ParseChallenge(value)
			return parsed.String(), err
		}},
		{"ticket", func() (string, error) { value, err := generator.NewWebSocketTicket(); return value.String(), err }, func(value string) (string, error) {
			parsed, err := id.ParseWebSocketTicket(value)
			return parsed.String(), err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := test.generate()
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := test.parse(value)
			if err != nil || parsed != value {
				t.Fatalf("parse(%q) = %q, %v", value, parsed, err)
			}
		})
	}
}

func TestGeneratorCreatesExecutionIDs(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{11}, 128)),
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		generate func() (string, error)
		parse    func(string) (string, error)
	}{
		{"provider", func() (string, error) {
			value, generateErr := generator.NewProvider()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseProvider(value)
			return parsed.String(), parseErr
		}},
		{"model", func() (string, error) { value, generateErr := generator.NewModel(); return value.String(), generateErr }, func(value string) (string, error) {
			parsed, parseErr := id.ParseModel(value)
			return parsed.String(), parseErr
		}},
		{"attempt", func() (string, error) {
			value, generateErr := generator.NewAttempt()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseAttempt(value)
			return parsed.String(), parseErr
		}},
		{"check", func() (string, error) { value, generateErr := generator.NewCheck(); return value.String(), generateErr }, func(value string) (string, error) {
			parsed, parseErr := id.ParseCheck(value)
			return parsed.String(), parseErr
		}},
		{"observation", func() (string, error) {
			value, generateErr := generator.NewObservation()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseObservation(value)
			return parsed.String(), parseErr
		}},
		{"policy", func() (string, error) {
			value, generateErr := generator.NewPolicy()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParsePolicy(value)
			return parsed.String(), parseErr
		}},
		{"decision", func() (string, error) {
			value, generateErr := generator.NewDecision()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseDecision(value)
			return parsed.String(), parseErr
		}},
		{"deletion", func() (string, error) {
			value, generateErr := generator.NewDeletion()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseDeletion(value)
			return parsed.String(), parseErr
		}},
		{"legal hold", func() (string, error) {
			value, generateErr := generator.NewLegalHold()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseLegalHold(value)
			return parsed.String(), parseErr
		}},
		{"review case", func() (string, error) {
			value, generateErr := generator.NewReviewCase()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseReviewCase(value)
			return parsed.String(), parseErr
		}},
		{"finding", func() (string, error) {
			value, generateErr := generator.NewFinding()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseFinding(value)
			return parsed.String(), parseErr
		}},
		{"appeal", func() (string, error) {
			value, generateErr := generator.NewAppeal()
			return value.String(), generateErr
		}, func(value string) (string, error) {
			parsed, parseErr := id.ParseAppeal(value)
			return parsed.String(), parseErr
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			generated, generateErr := test.generate()
			if generateErr != nil {
				t.Fatal(generateErr)
			}
			parsed, parseErr := test.parse(generated)
			if parseErr != nil || parsed != generated {
				t.Fatalf("parse(%q) = %q, %v", generated, parsed, parseErr)
			}
		})
	}
}

func TestGeneratorCreatesTaskID(t *testing.T) {
	t.Parallel()
	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{10}, 32)),
	)
	if err != nil {
		t.Fatal(err)
	}
	generated, err := generator.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := id.ParseTask(generated.String())
	if err != nil || parsed.String() != generated.String() {
		t.Fatalf("ParseTask() = %q, %v", parsed, err)
	}
}

func TestNewGeneratorRejectsMissingDependencies(t *testing.T) {
	t.Parallel()

	if _, err := id.NewGenerator(nil, bytes.NewReader(nil)); err == nil {
		t.Error("NewGenerator(nil, entropy) error = nil")
	}
	if _, err := id.NewGenerator(fixedClock{}, nil); err == nil {
		t.Error("NewGenerator(clock, nil) error = nil")
	}
}

func TestGeneratorReportsEntropyFailure(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(fixedClock{}, failingReader{})
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	if _, err := generator.NewRequest(); err == nil {
		t.Error("NewRequest() error = nil")
	}
}

func TestGeneratorRejectsInvalidPrefix(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(fixedClock{}, bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	if _, err := generator.New(id.Prefix("INVALID")); err == nil {
		t.Error("New(INVALID) error = nil")
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy failed")
}
