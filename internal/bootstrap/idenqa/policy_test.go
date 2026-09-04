package idenqa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestPolicyDecisionReproduceWritesClosedFormats(t *testing.T) {
	t.Parallel()
	decision := policyDecisionFixture(t)
	bundle, report, err := policy.NewDecisionBundle(decision)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		format string
		check  func(*testing.T, []byte)
	}{
		{name: "summary", format: "summary", check: func(t *testing.T, output []byte) {
			t.Helper()
			text := string(output)
			if !strings.Contains(text, "policy_decision_reproduced decision_id="+decision.ID().String()) ||
				!strings.Contains(text, "bundle_digest="+bundle.Digest()) || strings.Contains(text, "review.finding") {
				t.Fatalf("unsafe or incomplete summary: %q", text)
			}
		}},
		{name: "json", format: "json", check: func(t *testing.T, output []byte) {
			t.Helper()
			var decoded policy.ReproductionReport
			if err := json.Unmarshal(output, &decoded); err != nil || decoded != report {
				t.Fatalf("JSON report = %+v, error = %v", decoded, err)
			}
		}},
		{name: "bundle", format: "bundle", check: func(t *testing.T, output []byte) {
			t.Helper()
			if !bytes.Equal(output, bundle.Canonical()) {
				t.Fatal("bundle output is not exact canonical bytes")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			command := newPolicyCommandWith(func(
				_ context.Context,
				envFile string,
			) (policy.Repository, func(), error) {
				if envFile != "config.env" {
					t.Fatalf("env file = %q", envFile)
				}
				return policyRepositoryStub{decision: decision}, func() {}, nil
			})
			code := cli.ExecuteArgs(t.Context(), command, []string{
				"decision", "reproduce", "--env-file", "config.env",
				"--tenant", decision.Snapshot().TenantID().String(), "--id", decision.ID().String(),
				"--output", test.format,
			}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("exit code = %d, stderr = %q", code, stderr.String())
			}
			test.check(t, stdout.Bytes())
		})
	}
}

func TestPolicyDecisionVerifyUsesBoundedStdinWithoutRepository(t *testing.T) {
	t.Parallel()
	bundle, report, err := policy.NewDecisionBundle(policyDecisionFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	command := newPolicyCommandWith(func(context.Context, string) (policy.Repository, func(), error) {
		called = true
		return nil, nil, errors.New("unexpected repository open")
	})
	command.SetIn(bytes.NewReader(bundle.Canonical()))
	var stdout, stderr bytes.Buffer
	code := cli.ExecuteArgs(t.Context(), command, []string{
		"decision", "verify", "--bundle-file", "-", "--output", "json",
	}, &stdout, &stderr)
	if code != 0 || called {
		t.Fatalf("exit code = %d, repository called = %v, stderr = %q", code, called, stderr.String())
	}
	var decoded policy.ReproductionReport
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil || decoded != report {
		t.Fatalf("verified report = %+v, error = %v", decoded, err)
	}
}

func TestPolicyDecisionReproduceHonoursCancellationBeforeOpeningRepository(t *testing.T) {
	t.Parallel()
	decision := policyDecisionFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	command := newPolicyCommandWith(func(context.Context, string) (policy.Repository, func(), error) {
		called = true
		return nil, nil, errors.New("unexpected repository open")
	})
	var stdout, stderr bytes.Buffer
	code := cli.ExecuteArgs(ctx, command, []string{
		"decision", "reproduce", "--tenant", decision.Snapshot().TenantID().String(),
		"--id", decision.ID().String(),
	}, &stdout, &stderr)
	if code != 1 || called || !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("exit code = %d, called = %v, stderr = %q", code, called, stderr.String())
	}
}

func TestReadBoundedRejectsEmptyAndOversizedInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		input  []byte
		limit  int
		failed bool
	}{
		{name: "exact", input: []byte("1234"), limit: 4},
		{name: "empty", limit: 4, failed: true},
		{name: "oversized", input: []byte("12345"), limit: 4, failed: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := readBounded(bytes.NewReader(test.input), test.limit)
			if (err != nil) != test.failed || (!test.failed && !bytes.Equal(got, test.input)) {
				t.Fatalf("readBounded() = %q, %v", got, err)
			}
		})
	}
}

type policyRepositoryStub struct{ decision policy.Decision }

func (repository policyRepositoryStub) Append(context.Context, tenant.Scope, policy.Decision) error {
	return errors.New("unexpected append")
}

func (repository policyRepositoryStub) Find(
	_ context.Context,
	scope tenant.Scope,
	decisionID id.Decision,
) (policy.Decision, error) {
	if scope.ID().String() != repository.decision.Snapshot().TenantID().String() ||
		decisionID.String() != repository.decision.ID().String() {
		return policy.Decision{}, policy.ErrDecisionNotFound
	}
	return repository.decision, nil
}

func (repository policyRepositoryStub) FindLatest(
	context.Context,
	tenant.Scope,
	id.Verification,
) (policy.Decision, error) {
	return policy.Decision{}, errors.New("unexpected latest lookup")
}

func policyDecisionFixture(t testing.TB) policy.Decision {
	t.Helper()
	const suffix = "01K3P4NQF00000000000000000"
	tenantID, err := id.ParseTenant("ten_" + suffix)
	if err != nil {
		t.Fatal(err)
	}
	verificationID, _ := id.ParseVerification("ver_" + suffix)
	authorityID, _ := id.ParseAuthority("aut_" + suffix)
	acknowledgementID, _ := id.ParseAcknowledgement("ack_" + suffix)
	policyID, _ := id.ParsePolicy("pol_" + suffix)
	decisionID, _ := id.ParseDecision("dec_" + suffix)
	factKey, _ := policy.NewFactKey("review.finding")
	now := time.Date(2026, time.August, 30, 13, 0, 0, 0, time.UTC)
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{
		TenantID: tenantID, VerificationID: verificationID, AuthorityID: authorityID,
		AcknowledgementID: acknowledgementID, Region: "tenant_home", EvaluatedAt: now,
		Policy: policy.Reference{ID: policyID, Revision: 1, SchemaMajor: 1, SchemaMinor: 0,
			Digest: strings.Repeat("a", 64)},
		Evaluator: policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("b", 64)},
		Facts: []policy.Fact{{Key: factKey, State: policy.RequirementSatisfied,
			ObservedAt: now.Add(-time.Minute), Source: policy.FactSource{
				Kind:          policy.FactSourceReviewFinding,
				ReviewFinding: &policy.ReviewFindingSource{Reference: "finding_1"},
			}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := policy.Resolve(snapshot, []policy.RequirementResult{{
		Name: "manual_review", State: policy.RequirementSatisfied,
		ContributingFacts: []policy.FactKey{factKey}, Candidate: policy.DirectiveCompleteVerified,
		Priority: 1, ReasonCodes: []string{"review_satisfied"},
	}}, "substantial.1")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: decisionID, Snapshot: snapshot, Evaluation: evaluation,
		Actor: policy.ActorHuman, DecidedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}
