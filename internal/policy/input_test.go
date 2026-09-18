package policy_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type authoritativeSource struct {
	state policy.AuthoritativeState
	err   error
	calls atomic.Int32
}

func (source *authoritativeSource) LoadAuthoritativeState(
	ctx context.Context,
	_ tenant.Scope,
	_ id.Verification,
	_ time.Time,
) (policy.AuthoritativeState, error) {
	source.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return policy.AuthoritativeState{}, err
	}
	return source.state, source.err
}

type activePolicyReader struct {
	activation policy.Activation
	err        error
	after      func()
	calls      atomic.Int32
}

func (reader *activePolicyReader) FindActive(
	ctx context.Context,
	_ tenant.Scope,
	_ id.Policy,
) (policy.Activation, error) {
	reader.calls.Add(1)
	if err := ctx.Err(); err != nil {
		return policy.Activation{}, err
	}
	activation, err := reader.activation, reader.err
	if reader.after != nil {
		reader.after()
	}
	return activation, err
}

type exactRevisionReader struct{ revisions map[uint32]policy.Revision }

func (reader exactRevisionReader) FindRevision(
	ctx context.Context,
	_ tenant.Scope,
	_ id.Policy,
	revision uint32,
) (policy.Revision, error) {
	if err := ctx.Err(); err != nil {
		return policy.Revision{}, err
	}
	result, exists := reader.revisions[revision]
	if !exists {
		return policy.Revision{}, policy.ErrRevisionNotFound
	}
	return result, nil
}

func TestNewActiveInputLoaderRequiresDependencies(t *testing.T) {
	t.Parallel()
	source := &authoritativeSource{}
	reader := &activePolicyReader{}
	for _, test := range []struct {
		name   string
		source policy.AuthoritativeStateSource
		reader policy.ActivePolicyReader
	}{
		{name: "source", reader: reader},
		{name: "reader", source: source},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := policy.NewActiveInputLoader(test.source, test.reader); err == nil {
				t.Fatal("NewActiveInputLoader() error = nil")
			}
		})
	}
}

func TestActiveInputLoaderPinsActiveRevisionAndOwnsFacts(t *testing.T) {
	t.Parallel()
	fixture := activeInputFixture(t)
	loader, err := policy.NewActiveInputLoader(fixture.source, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	input, err := loader.LoadPolicyInput(t.Context(), fixture.scope, fixture.verificationID, fixture.at)
	if err != nil {
		t.Fatal(err)
	}
	if input.Policy != fixture.reader.activation.Revision().Reference() ||
		input.AuthorityID.String() != fixture.source.state.AuthorityID.String() ||
		input.AcknowledgementID.String() != fixture.source.state.AcknowledgementID.String() ||
		input.Region != "tenant_home" || len(input.Facts) != 1 {
		t.Fatalf("LoadPolicyInput() = %+v", input)
	}
	fixture.source.state.Facts[0].ReasonCodes[0] = "mutated"
	if input.Facts[0].ReasonCodes[0] != "authoritative" {
		t.Fatal("returned facts alias the source")
	}
}

func TestActiveInputLoaderRejectsInvalidStateBeforeCatalogRead(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*policy.AuthoritativeState)
		want   error
	}{
		{name: "policy", change: func(state *policy.AuthoritativeState) { state.PolicyID = id.Policy{} }, want: policy.ErrInvalid},
		{name: "authority", change: func(state *policy.AuthoritativeState) { state.AuthorityID = id.Authority{} }, want: policy.ErrInvalid},
		{name: "acknowledgement", change: func(state *policy.AuthoritativeState) { state.AcknowledgementID = id.Acknowledgement{} }, want: policy.ErrInvalid},
		{name: "region", change: func(state *policy.AuthoritativeState) { state.Region = "EU West" }, want: policy.ErrInvalid},
		{name: "facts", change: func(state *policy.AuthoritativeState) { state.Facts = nil }, want: policy.ErrInvalid},
		{name: "duplicate fact", change: func(state *policy.AuthoritativeState) { state.Facts = append(state.Facts, state.Facts[0]) }, want: policy.ErrConflict},
		{name: "future fact", change: func(state *policy.AuthoritativeState) {
			state.Facts[0].ObservedAt = state.Facts[0].ObservedAt.Add(time.Second)
		}, want: policy.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := activeInputFixture(t)
			test.change(&fixture.source.state)
			loader, err := policy.NewActiveInputLoader(fixture.source, fixture.reader)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := loader.LoadPolicyInput(t.Context(), fixture.scope, fixture.verificationID, fixture.at); !errors.Is(err, test.want) {
				t.Fatalf("LoadPolicyInput() error = %v, want %v", err, test.want)
			}
			if fixture.reader.calls.Load() != 0 {
				t.Fatal("invalid authoritative state reached the catalog")
			}
		})
	}
}

func TestActiveInputLoaderPropagatesCancellationAndDependencyFailure(t *testing.T) {
	t.Parallel()
	fixture := activeInputFixture(t)
	loader, err := policy.NewActiveInputLoader(fixture.source, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := loader.LoadPolicyInput(canceled, fixture.scope, fixture.verificationID, fixture.at); !errors.Is(err, context.Canceled) || fixture.source.calls.Load() != 0 {
		t.Fatalf("canceled LoadPolicyInput() error = %v", err)
	}
	fixture.source.err = errors.New("projection unavailable")
	if _, err := loader.LoadPolicyInput(t.Context(), fixture.scope, fixture.verificationID, fixture.at); !strings.Contains(err.Error(), "load authoritative policy state") {
		t.Fatalf("source error = %v", err)
	}
	fixture.source.err = nil
	fixture.reader.err = policy.ErrActivationNotFound
	if _, err := loader.LoadPolicyInput(t.Context(), fixture.scope, fixture.verificationID, fixture.at); !errors.Is(err, policy.ErrActivationNotFound) {
		t.Fatalf("catalog error = %v", err)
	}
}

func TestAuthorEvaluatesPinnedRevisionAcrossActivationSwitch(t *testing.T) {
	t.Parallel()
	fixture := activeInputFixture(t)
	first := fixture.reader.activation.Revision()
	firstDocument, err := policyv1.ParseCanonical(first.Canonical())
	if err != nil {
		t.Fatal(err)
	}
	secondDocument := firstDocument
	secondDocument.Revision = 2
	secondDocument.Rules[0].When = `facts["document.authenticity"] == "not_satisfied"`
	secondDocument.Rules[0].Result.State = policyv1.RequirementNotSatisfied
	secondDocument.Rules[0].Result.Directive = policyv1.DirectiveCompleteNotVerified
	secondCanonical, err := policyv1.Canonical(secondDocument)
	if err != nil {
		t.Fatal(err)
	}
	second, err := policy.NewRevisionCanonical(secondCanonical, first.Evaluator(), fixture.at.Add(-30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := id.ParseAPIKey("key_01K3P4NQF00000000000000006")
	secondActivation, err := policy.RestoreActivation(second, 2, 1, actor, fixture.at.Add(-time.Second))
	if err != nil {
		t.Fatal(err)
	}
	fixture.reader.after = func() { fixture.reader.activation = secondActivation }
	inputs, err := policy.NewActiveInputLoader(fixture.source, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	resolver, err := policycel.NewResolver(exactRevisionReader{revisions: map[uint32]policy.Revision{1: first, 2: second}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	author, err := policy.NewAuthor(newAuthorRepository(), inputs, resolver)
	if err != nil {
		t.Fatal(err)
	}
	decisionID, _ := id.ParseDecision("dec_01K3P4NQF00000000000000007")
	decision, err := author.Author(t.Context(), fixture.scope, policy.AuthorRequest{
		DecisionID: decisionID, VerificationID: fixture.verificationID,
		EvaluatedAt: fixture.at, DecidedAt: fixture.at.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Snapshot().Policy().Revision != 1 || decision.Evaluation().Outcome() != policy.OutcomeVerified ||
		fixture.reader.activation.Revision().Reference().Revision != 2 {
		t.Fatalf("decision policy=%d outcome=%q active=%d", decision.Snapshot().Policy().Revision,
			decision.Evaluation().Outcome(), fixture.reader.activation.Revision().Reference().Revision)
	}
}

type activeInputTestFixture struct {
	scope          tenant.Scope
	verificationID id.Verification
	at             time.Time
	source         *authoritativeSource
	reader         *activePolicyReader
}

func activeInputFixture(t testing.TB) activeInputTestFixture {
	t.Helper()
	at := time.Date(2026, time.August, 31, 15, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01K3P4NQF00000000000000001")
	verificationID, _ := id.ParseVerification("ver_01K3P4NQF00000000000000002")
	authorityID, _ := id.ParseAuthority("aut_01K3P4NQF00000000000000003")
	acknowledgementID, _ := id.ParseAcknowledgement("ack_01K3P4NQF00000000000000004")
	policyID, _ := id.ParsePolicy("pol_01K3P4NQF00000000000000005")
	actor, _ := id.ParseAPIKey("key_01K3P4NQF00000000000000006")
	scope, _ := tenant.NewScope(tenantID)
	factKey, _ := policy.NewFactKey("document.authenticity")
	document := policyv1.Document{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: policyID.String(), Revision: 1,
		VerifiedAssurance: "global_individual_substantial.1",
		Rules: []policyv1.Rule{{Name: "verified", When: `facts["document.authenticity"] == "satisfied"`, Result: policyv1.Result{
			State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified,
			Priority: 1, ContributingFacts: []string{"document.authenticity"}, ReasonCodes: []string{"requirements_satisfied"},
		}}},
	}
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := policy.NewRevisionCanonical(canonical, (policycel.Compiler{}).Reference(), at.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	activation, err := policy.RestoreActivation(revision, 1, 0, actor, at.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	state := policy.AuthoritativeState{
		PolicyID: policyID, AuthorityID: authorityID, AcknowledgementID: acknowledgementID,
		Region: "tenant_home", Facts: []policy.Fact{{
			Key: factKey, State: policy.RequirementSatisfied,
			Source:     policy.FactSource{Kind: policy.FactSourceProcessingAuthority, Authority: &policy.AuthoritySource{AuthorityID: authorityID}},
			ObservedAt: at, ReasonCodes: []string{"authoritative"},
		}},
	}
	return activeInputTestFixture{
		scope: scope, verificationID: verificationID, at: at,
		source: &authoritativeSource{state: state}, reader: &activePolicyReader{activation: activation},
	}
}

func TestRecapturePinnedInputBypassesActiveRevision(t *testing.T) {
	fixture := activeInputFixture(t)
	pin := fixture.reader.activation.Revision().Reference()
	fixture.source.state.PinnedPolicy = &pin
	fixture.reader.err = errors.New("active revision unavailable")
	loader, err := policy.NewActiveInputLoader(fixture.source, fixture.reader)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loader.LoadPolicyInput(t.Context(), fixture.scope, fixture.verificationID, fixture.at)
	if err != nil || result.Policy != pin || fixture.reader.calls.Load() != 0 {
		t.Fatalf("pinned input=%v %v", result.Policy, err)
	}
	pin.Digest = "invalid"
	if _, err := loader.LoadPolicyInput(t.Context(), fixture.scope, fixture.verificationID, fixture.at); !errors.Is(err, policy.ErrRevisionConflict) {
		t.Fatalf("invalid pin=%v", err)
	}
}
