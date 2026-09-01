package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestSourceLoadsOneRepeatableReadProjection(t *testing.T) {
	t.Parallel()

	fixture := newSourceFixture(t)
	queries := &authoritativeQueriesStub{
		header: sqlgen.FindPolicyAuthoritativeHeaderRow{
			Region: pointer("eu-west-1"), AuthorityID: fixture.authorityID.String(),
			AcknowledgementID: fixture.acknowledgementID.String(), ResponseAction: "consent",
			ResponseRecordedAt: policyTimestamp(fixture.evaluatedAt.Add(-time.Minute)),
		},
		rows: []sqlgen.ListPolicyAuthoritativeObservationsRow{
			projectionRow(fixture, "obs_01ARZ3NDEKTSV4RRFFQ69G5FB8", fixture.evaluatedAt.Add(-2*time.Minute)),
			projectionRow(fixture, "obs_01ARZ3NDEKTSV4RRFFQ69G5FB9", fixture.evaluatedAt.Add(-time.Minute)),
		},
	}
	runner := &transactionRunnerStub{}
	selector := &selectorStub{policyID: fixture.policyID}
	projector := &projectorStub{facts: []policy.Fact{{
		Key: "authority.response", State: policy.RequirementSatisfied,
		Source: policy.FactSource{
			Kind:      policy.FactSourceProcessingAuthority,
			Authority: &policy.AuthoritySource{AuthorityID: fixture.authorityID},
		},
		ObservedAt: fixture.evaluatedAt.Add(-time.Minute),
	}}}
	source, err := NewSource(runner, selector, projector)
	if err != nil {
		t.Fatalf("NewSource() error = %v", err)
	}
	source.queries = func(platformpostgres.Transaction) authoritativeQueries { return queries }

	state, err := source.LoadAuthoritativeState(
		context.Background(), fixture.scope, fixture.verificationID, fixture.evaluatedAt,
	)
	if err != nil {
		t.Fatalf("LoadAuthoritativeState() error = %v", err)
	}
	if runner.calls != 1 || runner.options.Isolation != platformpostgres.IsolationRepeatableRead ||
		!runner.options.ReadOnly {
		t.Fatalf("transaction = calls %d, options %+v", runner.calls, runner.options)
	}
	if queries.scope != fixture.scope.ID().String() || selector.calls != 1 || projector.calls != 1 {
		t.Fatal("source did not force scope, select assignment, and project facts exactly once")
	}
	if state.PolicyID.String() != fixture.policyID.String() || state.Region != "eu-west-1" ||
		state.AuthorityID.String() != fixture.authorityID.String() ||
		state.AcknowledgementID.String() != fixture.acknowledgementID.String() || len(state.Facts) != 1 {
		t.Fatalf("state = %+v", state)
	}
	if len(projector.projection.Checks) != 1 || len(projector.projection.Checks[0].Observations) != 2 ||
		projector.projection.Checks[0].Attempt.AttemptID.String() != fixture.attemptID.String() {
		t.Fatalf("projection = %+v", projector.projection)
	}
}

func TestSourceFailsClosedWithoutPinnedRegion(t *testing.T) {
	t.Parallel()

	fixture := newSourceFixture(t)
	queries := &authoritativeQueriesStub{header: sqlgen.FindPolicyAuthoritativeHeaderRow{
		AuthorityID: fixture.authorityID.String(), AcknowledgementID: fixture.acknowledgementID.String(),
		ResponseAction: "consent", ResponseRecordedAt: policyTimestamp(fixture.evaluatedAt),
	}}
	source, err := NewSource(
		&transactionRunnerStub{}, &selectorStub{policyID: fixture.policyID}, &projectorStub{},
	)
	if err != nil {
		t.Fatal(err)
	}
	source.queries = func(platformpostgres.Transaction) authoritativeQueries { return queries }

	_, err = source.LoadAuthoritativeState(
		context.Background(), fixture.scope, fixture.verificationID, fixture.evaluatedAt,
	)
	if !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("LoadAuthoritativeState() error = %v, want ErrInvalid", err)
	}
}

func TestAssembleChecksRejectsInconsistentAttemptRows(t *testing.T) {
	t.Parallel()

	fixture := newSourceFixture(t)
	first := projectionRow(fixture, "obs_01ARZ3NDEKTSV4RRFFQ69G5FB8", fixture.evaluatedAt.Add(-time.Minute))
	second := projectionRow(fixture, "obs_01ARZ3NDEKTSV4RRFFQ69G5FB9", fixture.evaluatedAt)
	second.AttemptID = "atm_01ARZ3NDEKTSV4RRFFQ69G5FBA"
	_, err := assembleChecks([]sqlgen.ListPolicyAuthoritativeObservationsRow{first, second}, fixture.evaluatedAt)
	if !errors.Is(err, policy.ErrConflict) {
		t.Fatalf("assembleChecks() error = %v, want ErrConflict", err)
	}
}

type sourceFixture struct {
	scope             tenant.Scope
	verificationID    id.Verification
	policyID          id.Policy
	authorityID       id.Authority
	acknowledgementID id.Acknowledgement
	checkID           id.Check
	attemptID         id.Attempt
	evaluatedAt       time.Time
}

func newSourceFixture(t *testing.T) sourceFixture {
	t.Helper()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	authorityID, _ := id.ParseAuthority("aut_01ARZ3NDEKTSV4RRFFQ69G5FAY")
	acknowledgementID, _ := id.ParseAcknowledgement("ack_01ARZ3NDEKTSV4RRFFQ69G5FAZ")
	checkID, _ := id.ParseCheck("chk_01ARZ3NDEKTSV4RRFFQ69G5FB0")
	attemptID, _ := id.ParseAttempt("atm_01ARZ3NDEKTSV4RRFFQ69G5FB1")
	return sourceFixture{
		scope: scope, verificationID: verificationID, policyID: policyID,
		authorityID: authorityID, acknowledgementID: acknowledgementID,
		checkID: checkID, attemptID: attemptID,
		evaluatedAt: time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
	}
}

func projectionRow(
	fixture sourceFixture,
	observationID string,
	recordedAt time.Time,
) sqlgen.ListPolicyAuthoritativeObservationsRow {
	return sqlgen.ListPolicyAuthoritativeObservationsRow{
		CheckID: fixture.checkID.String(), CheckName: "document.authenticity",
		CheckOutcome: pointer("passed"), CheckVersion: 3,
		CheckUpdatedAt: policyTimestamp(fixture.evaluatedAt.Add(-time.Minute)),
		AttemptID:      fixture.attemptID.String(), AttemptNumber: 1, RunnerKind: "provider",
		RunnerID: "provider.test", RunnerVersion: "1.0.0",
		PackageDigest: digest("a"), ContractMajor: 1, ContractMinor: 0,
		RequestDigest: digest("b"), ConfigurationDigest: digest("c"), ResultDigest: pointer(digest("d")),
		ObservationID: observationID, SignalName: "document.authenticity",
		SignalOutcome: "satisfied", ReasonCodes: []string{"document_authentic"},
		RecordedAt: policyTimestamp(recordedAt),
	}
}

type transactionRunnerStub struct {
	calls   int
	options platformpostgres.TransactionOptions
	err     error
}

func (runner *transactionRunnerStub) WithinTransaction(
	ctx context.Context,
	options platformpostgres.TransactionOptions,
	work func(context.Context, platformpostgres.Transaction) error,
) error {
	runner.calls++
	runner.options = options
	if runner.err != nil {
		return runner.err
	}
	return work(ctx, nil)
}

type authoritativeQueriesStub struct {
	scope  string
	header sqlgen.FindPolicyAuthoritativeHeaderRow
	rows   []sqlgen.ListPolicyAuthoritativeObservationsRow
	err    error
}

func (queries *authoritativeQueriesStub) SetTenantScope(_ context.Context, scope string) (string, error) {
	queries.scope = scope
	return scope, nil
}

func (queries *authoritativeQueriesStub) FindPolicyAuthoritativeHeader(
	context.Context,
	sqlgen.FindPolicyAuthoritativeHeaderParams,
) (sqlgen.FindPolicyAuthoritativeHeaderRow, error) {
	return queries.header, queries.err
}

func (queries *authoritativeQueriesStub) ListPolicyAuthoritativeObservations(
	context.Context,
	sqlgen.ListPolicyAuthoritativeObservationsParams,
) ([]sqlgen.ListPolicyAuthoritativeObservationsRow, error) {
	return queries.rows, queries.err
}

type selectorStub struct {
	policyID id.Policy
	calls    int
}

func (selector *selectorStub) SelectPolicy(
	context.Context,
	platformpostgres.Transaction,
	tenant.Scope,
	id.Verification,
	time.Time,
) (id.Policy, error) {
	selector.calls++
	return selector.policyID, nil
}

type projectorStub struct {
	facts      []policy.Fact
	projection Projection
	calls      int
}

func (projector *projectorStub) ProjectFacts(_ context.Context, projection Projection) ([]policy.Fact, error) {
	projector.calls++
	projector.projection = projection
	return projector.facts, nil
}

func policyTimestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func pointer(value string) *string { return &value }

func digest(character string) string {
	result := ""
	for range 64 {
		result += character
	}
	return result
}
