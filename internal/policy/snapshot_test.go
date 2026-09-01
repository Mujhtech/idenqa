package policy_test

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

const policyTestULID = "01K3P4NQF00000000000000000"

type fixture struct {
	now       time.Time
	input     policy.SnapshotInput
	checkFact policy.FactKey
	authFact  policy.FactKey
	response  policy.FactKey
	decision  id.Decision
}

func TestNewFactKeyRejectsUnboundedAndUnnamespacedValues(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "namespaced", value: "check.document_authenticity", valid: true},
		{name: "missing namespace", value: "document_authenticity"},
		{name: "uppercase", value: "check.Document"},
		{name: "dynamic punctuation", value: "check.document[value]"},
		{name: "too long", value: "check." + strings.Repeat("a", 123)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := policy.NewFactKey(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("NewFactKey(%q) error = %v", test.value, err)
			}
		})
	}
}

func TestNewSnapshotCanonicalisesOrderAndDefensivelyCopiesInputs(t *testing.T) {
	t.Parallel()
	first := policyFixture(t)
	second := policyFixture(t)
	slices.Reverse(second.input.Facts)

	snapshot, err := policy.NewSnapshot(first.input)
	if err != nil {
		t.Fatal(err)
	}
	reordered, err := policy.NewSnapshot(second.input)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Digest() != reordered.Digest() || !slices.Equal(snapshot.Canonical(), reordered.Canonical()) {
		t.Fatal("equivalent fact order changed canonical snapshot meaning")
	}

	first.input.Facts[0].ReasonCodes[0] = "mutated"
	first.input.Facts[0].Source.Check.ObservationIDs[0] = id.Observation{}
	exposed := snapshot.Facts()
	for index := range exposed {
		if len(exposed[index].ReasonCodes) > 0 {
			exposed[index].ReasonCodes[0] = "mutated_again"
		}
		if exposed[index].Source.Check != nil {
			exposed[index].Source.Check.ObservationIDs[0] = id.Observation{}
		}
	}
	if string(snapshot.Canonical()) != string(reordered.Canonical()) {
		t.Fatal("snapshot aliases caller or accessor slices")
	}
}

func TestNewSnapshotValidatesEveryClosedFactSource(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	reviewKey, _ := policy.NewFactKey("review.finding")
	tests := []struct {
		name   string
		source policy.FactSource
		valid  bool
	}{
		{name: "check", source: base.input.Facts[0].Source, valid: true},
		{name: "authority", source: base.input.Facts[1].Source, valid: true},
		{name: "subject response", source: base.input.Facts[2].Source, valid: true},
		{name: "reserved review finding", source: policy.FactSource{Kind: policy.FactSourceReviewFinding,
			ReviewFinding: &policy.ReviewFindingSource{Reference: "review.finding_accepted"}}, valid: true},
		{name: "kind shape mismatch", source: policy.FactSource{Kind: policy.FactSourceCheck,
			Authority: &policy.AuthoritySource{AuthorityID: base.input.AuthorityID}}},
		{name: "multiple union branches", source: policy.FactSource{Kind: policy.FactSourceProcessingAuthority,
			Authority:       &policy.AuthoritySource{AuthorityID: base.input.AuthorityID},
			SubjectResponse: &policy.SubjectResponseSource{AcknowledgementID: base.input.AcknowledgementID}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := base.input
			input.Facts = []policy.Fact{{Key: reviewKey, State: policy.RequirementSatisfied,
				Source: test.source, ObservedAt: base.now}}
			_, err := policy.NewSnapshot(input)
			if (err == nil) != test.valid {
				t.Fatalf("NewSnapshot() error = %v", err)
			}
		})
	}
}

func TestNewSnapshotRejectsStaleFutureDuplicateAndConflictingFacts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		mutate   func(*policy.SnapshotInput)
		expected error
	}{
		{name: "stale", mutate: func(input *policy.SnapshotInput) {
			expiresAt := input.EvaluatedAt
			input.Facts[0].ExpiresAt = &expiresAt
		}, expected: policy.ErrStaleFact},
		{name: "future observation", mutate: func(input *policy.SnapshotInput) {
			input.Facts[0].ObservedAt = input.EvaluatedAt.Add(time.Second)
		}, expected: policy.ErrInvalid},
		{name: "exact duplicate", mutate: func(input *policy.SnapshotInput) {
			input.Facts = append(input.Facts, input.Facts[0])
		}, expected: policy.ErrInvalid},
		{name: "conflicting duplicate", mutate: func(input *policy.SnapshotInput) {
			conflict := input.Facts[0]
			conflict.State = policy.RequirementNotSatisfied
			input.Facts = append(input.Facts, conflict)
		}, expected: policy.ErrConflict},
		{name: "unsupported policy version", mutate: func(input *policy.SnapshotInput) {
			input.Policy.SchemaMinor++
		}, expected: policy.ErrVersion},
		{name: "unsupported evaluator version", mutate: func(input *policy.SnapshotInput) {
			input.Evaluator.Major++
		}, expected: policy.ErrVersion},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := policyFixture(t).input
			test.mutate(&value)
			_, err := policy.NewSnapshot(value)
			if !errors.Is(err, test.expected) {
				t.Fatalf("NewSnapshot() error = %v, want %v", err, test.expected)
			}
		})
	}
}

func TestNewSnapshotEnforcesFactAndReasonBounds(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	tooMany := make([]policy.Fact, policy.MaximumFacts+1)
	for index := range tooMany {
		key, _ := policy.NewFactKey("check.fact_" + strings.Repeat("a", index%100+1))
		tooMany[index] = base.input.Facts[0]
		tooMany[index].Key = key
	}
	base.input.Facts = tooMany
	if _, err := policy.NewSnapshot(base.input); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("NewSnapshot(too many facts) error = %v", err)
	}

	base = policyFixture(t)
	base.input.Facts[0].ReasonCodes = make([]string, policy.MaximumReasonsPerItem+1)
	if _, err := policy.NewSnapshot(base.input); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("NewSnapshot(too many reasons) error = %v", err)
	}
}

func TestRestoreSnapshotRejectsDigestTampering(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, err := policy.NewSnapshot(base.input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.RestoreSnapshot(base.input, strings.Repeat("f", 64)); !errors.Is(err, policy.ErrReproduction) {
		t.Fatalf("RestoreSnapshot(tampered) error = %v", err)
	}
	if restored, err := policy.RestoreSnapshot(base.input, snapshot.Digest()); err != nil || restored.Digest() != snapshot.Digest() {
		t.Fatalf("RestoreSnapshot(valid) = %q, %v", restored.Digest(), err)
	}
}

func TestFactTypeCannotRepresentRawOrDynamicValues(t *testing.T) {
	t.Parallel()
	forbidden := map[reflect.Kind]bool{reflect.Map: true, reflect.Interface: true}
	typeOfFact := reflect.TypeFor[policy.Fact]()
	for index := range typeOfFact.NumField() {
		field := typeOfFact.Field(index)
		if forbidden[field.Type.Kind()] || (field.Type.Kind() == reflect.Slice && field.Type.Elem().Kind() == reflect.Uint8) {
			t.Fatalf("Fact.%s exposes forbidden %s", field.Name, field.Type)
		}
	}
}

func policyFixture(t testing.TB) fixture {
	t.Helper()
	parse := func(prefix id.Prefix) string { return string(prefix) + "_" + policyTestULID }
	tenantID, err := id.ParseTenant(parse(id.TenantPrefix))
	if err != nil {
		t.Fatal(err)
	}
	verificationID, _ := id.ParseVerification(parse(id.VerificationPrefix))
	authorityID, _ := id.ParseAuthority(parse(id.AuthorityPrefix))
	acknowledgementID, _ := id.ParseAcknowledgement(parse(id.AcknowledgementPrefix))
	policyID, _ := id.ParsePolicy(parse(id.PolicyPrefix))
	checkID, _ := id.ParseCheck(parse(id.CheckPrefix))
	attemptID, _ := id.ParseAttempt(parse(id.AttemptPrefix))
	observationID, _ := id.ParseObservation(parse(id.ObservationPrefix))
	decisionID, _ := id.ParseDecision(parse(id.DecisionPrefix))
	checkFact, _ := policy.NewFactKey("check.document_authenticity")
	authFact, _ := policy.NewFactKey("authority.processing_permitted")
	responseFact, _ := policy.NewFactKey("response.subject_acknowledged")
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	digestA, digestB, digestC := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	return fixture{
		now: now, checkFact: checkFact, authFact: authFact, response: responseFact, decision: decisionID,
		input: policy.SnapshotInput{
			TenantID: tenantID, VerificationID: verificationID, AuthorityID: authorityID,
			AcknowledgementID: acknowledgementID, Region: "tenant_home",
			Policy:    policy.Reference{ID: policyID, Revision: 3, SchemaMajor: 1, SchemaMinor: 0, Digest: digestA},
			Evaluator: policy.EvaluatorReference{Major: 1, Minor: 0, Digest: digestB}, EvaluatedAt: now,
			Facts: []policy.Fact{
				{Key: checkFact, State: policy.RequirementSatisfied, ObservedAt: now.Add(-time.Minute),
					ReasonCodes: []string{"document_authenticity_satisfied"}, Source: policy.FactSource{Kind: policy.FactSourceCheck,
						Check: &policy.CheckSource{CheckID: checkID, CheckVersion: 4, AttemptID: attemptID,
							ObservationIDs: []id.Observation{observationID}, ContractDigest: digestB, ImplementationDigest: digestC}}},
				{Key: authFact, State: policy.RequirementSatisfied, ObservedAt: now.Add(-time.Minute),
					Source: policy.FactSource{Kind: policy.FactSourceProcessingAuthority,
						Authority: &policy.AuthoritySource{AuthorityID: authorityID}}},
				{Key: responseFact, State: policy.RequirementSatisfied, ObservedAt: now.Add(-time.Minute),
					Source: policy.FactSource{Kind: policy.FactSourceSubjectResponse,
						SubjectResponse: &policy.SubjectResponseSource{AcknowledgementID: acknowledgementID}}},
			},
		},
	}
}
