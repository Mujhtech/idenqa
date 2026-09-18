package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

const maximumProjectionRows = policy.MaximumFacts * policy.MaximumObservations

// PolicySelector resolves the tenant's policy assignment inside the same
// repeatable-read transaction as the authoritative input projection. The
// assignment model remains an explicit product decision rather than an
// assumption embedded in persistence.
type PolicySelector interface {
	SelectPolicy(
		context.Context,
		platformpostgres.Transaction,
		tenant.Scope,
		id.Verification,
		time.Time,
	) (id.Policy, error)
}

// FactProjector maps owned, reference-only durable records into the public
// policy fact vocabulary. It must not recover raw evidence or provider data.
type FactProjector interface {
	ProjectFacts(context.Context, Projection) ([]policy.Fact, error)
}

// Projection is the bounded CEL-neutral source material available to a fact
// mapper. It contains only owned state and immutable provenance references.
type Projection struct {
	VerificationID id.Verification
	Authority      AuthorityProjection
	Region         string
	EvaluatedAt    time.Time
	Checks         []CheckProjection
}

// AuthorityProjection pins the authority and latest response visible at the
// evaluation instant. Authority validity is enforced by the source query.
type AuthorityProjection struct {
	AuthorityID        id.Authority
	AcknowledgementID  id.Acknowledgement
	ResponseAction     string
	ResponseRecordedAt time.Time
}

// CheckProjection is one completed check and the exact successful attempt
// whose observations produced its terminal outcome.
type CheckProjection struct {
	CheckID      id.Check
	Name         string
	Outcome      string
	Version      int64
	UpdatedAt    time.Time
	Attempt      AttemptProjection
	Observations []ObservationProjection
}

// AttemptProjection retains exact implementation and contract provenance.
type AttemptProjection struct {
	AttemptID           id.Attempt
	Number              uint32
	RunnerKind          string
	RunnerID            string
	RunnerVersion       string
	PackageDigest       string
	ContractMajor       uint16
	ContractMinor       uint16
	RequestDigest       string
	ConfigurationDigest string
	ResultDigest        string
}

// ObservationProjection is a safe normalised signal, never a provider payload.
type ObservationProjection struct {
	ObservationID id.Observation
	SignalName    string
	SignalOutcome string
	ReasonCodes   []string
	RecordedAt    time.Time
}

type authoritativeQueries interface {
	SetTenantScope(context.Context, string) (string, error)
	FindPolicyAuthoritativeHeader(
		context.Context,
		sqlgen.FindPolicyAuthoritativeHeaderParams,
	) (sqlgen.FindPolicyAuthoritativeHeaderRow, error)
	ListPolicyAuthoritativeObservations(
		context.Context,
		sqlgen.ListPolicyAuthoritativeObservationsParams,
	) ([]sqlgen.ListPolicyAuthoritativeObservationsRow, error)
}

type authoritativeQueryFactory func(platformpostgres.Transaction) authoritativeQueries

// TransactionProjector enriches policy input within the same database snapshot.
type TransactionProjector interface {
	ProjectWithin(context.Context, platformpostgres.Transaction, tenant.Scope, Projection) ([]policy.Fact, error)
}

// Source atomically loads the authoritative PostgreSQL state used to author a
// policy snapshot.
type Source struct {
	completeContext bool
	pool            transactionRunner
	selector        PolicySelector
	projector       FactProjector
	queries         authoritativeQueryFactory
	enrichers       []TransactionProjector
}

var _ policy.AuthoritativeStateSource = (*Source)(nil)

// NewSource constructs the authoritative policy-input PostgreSQL adapter.
func NewSource(
	pool transactionRunner,
	selector PolicySelector,
	projector FactProjector,
	enrichers ...TransactionProjector,
) (*Source, error) {
	if pool == nil || selector == nil || projector == nil {
		return nil, errors.New("policy postgres source: dependencies are required")
	}
	return &Source{
		pool: pool, selector: selector, projector: projector, enrichers: append([]TransactionProjector(nil), enrichers...),
		queries: func(transaction platformpostgres.Transaction) authoritativeQueries {
			return sqlgen.New(transaction)
		},
	}, nil
}

// LoadAuthoritativeState reads assignment, authority, response, and completed
// checks from one tenant-forced repeatable-read snapshot.
func (source *Source) LoadAuthoritativeState(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
	evaluatedAt time.Time,
) (policy.AuthoritativeState, error) {
	if source == nil || scope.ID().IsZero() || verificationID.IsZero() ||
		evaluatedAt.IsZero() || evaluatedAt.Location() != time.UTC {
		return policy.AuthoritativeState{}, fmt.Errorf("%w: authoritative source request", policy.ErrInvalid)
	}
	var result policy.AuthoritativeState
	err := source.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{
			Isolation: platformpostgres.IsolationRepeatableRead,
			ReadOnly:  len(source.enrichers) == 0,
		},
		func(ctx context.Context, transaction platformpostgres.Transaction) error {
			queries := source.queries(transaction)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set authoritative policy tenant scope: %w", err)
			}
			policyID, err := source.selector.SelectPolicy(
				ctx, transaction, scope, verificationID, evaluatedAt,
			)
			if err != nil {
				return fmt.Errorf("select assigned policy: %w", err)
			}
			if policyID.IsZero() {
				return fmt.Errorf("%w: selected policy", policy.ErrInvalid)
			}
			projection, err := loadProjection(ctx, queries, verificationID, scope, evaluatedAt)
			if err != nil {
				return err
			}
			facts, err := source.projector.ProjectFacts(ctx, projection)
			if err != nil {
				return fmt.Errorf("project authoritative policy facts: %w", err)
			}
			for _, enricher := range source.enrichers {
				if enricher == nil {
					return policy.ErrInvalid
				}
				additional, err := enricher.ProjectWithin(ctx, transaction, scope, projection)
				if err != nil {
					return fmt.Errorf("project additional policy facts: %w", err)
				}
				facts = append(facts, additional...)
			}
			var complete *policy.DecisionContext
			if source.completeContext {
				complete, err = loadDecisionContext(ctx, transaction, scope, projection, facts)
				if err != nil {
					return err
				}
			}
			result = policy.AuthoritativeState{
				Context:  complete,
				PolicyID: policyID, AuthorityID: projection.Authority.AuthorityID,
				AcknowledgementID: projection.Authority.AcknowledgementID,
				Region:            projection.Region, Facts: slices.Clone(facts),
			}
			if selector, ok := source.selector.(interface {
				SelectPinnedPolicy(context.Context, platformpostgres.Transaction, tenant.Scope, id.Verification) (*policy.Reference, error)
			}); ok {
				result.PinnedPolicy, err = selector.SelectPinnedPolicy(ctx, transaction, scope, verificationID)
				if err != nil {
					return err
				}
			}
			return nil
		},
	)
	if err != nil {
		return policy.AuthoritativeState{}, err
	}
	return result, nil
}

func loadProjection(
	ctx context.Context,
	queries authoritativeQueries,
	verificationID id.Verification,
	scope tenant.Scope,
	evaluatedAt time.Time,
) (Projection, error) {
	header, err := queries.FindPolicyAuthoritativeHeader(ctx, sqlgen.FindPolicyAuthoritativeHeaderParams{
		EvaluatedAt: timestamp(evaluatedAt), TenantID: scope.ID().String(),
		VerificationID: verificationID.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Projection{}, fmt.Errorf("%w: authoritative policy header", policy.ErrInvalid)
	}
	if err != nil {
		return Projection{}, fmt.Errorf("find authoritative policy header: %w", err)
	}
	authorityID, err := id.ParseAuthority(header.AuthorityID)
	if err != nil {
		return Projection{}, fmt.Errorf("parse authoritative authority id: %w", err)
	}
	acknowledgementID, err := id.ParseAcknowledgement(header.AcknowledgementID)
	if err != nil || header.Region == nil || *header.Region == "" || !header.ResponseRecordedAt.Valid {
		return Projection{}, fmt.Errorf("%w: authoritative policy header", policy.ErrInvalid)
	}
	rows, err := queries.ListPolicyAuthoritativeObservations(
		ctx,
		sqlgen.ListPolicyAuthoritativeObservationsParams{
			TenantID: scope.ID().String(), VerificationID: verificationID.String(),
			EvaluatedAt: timestamp(evaluatedAt),
		},
	)
	if err != nil {
		return Projection{}, fmt.Errorf("list authoritative check observations: %w", err)
	}
	checks, err := assembleChecks(rows, evaluatedAt)
	if err != nil {
		return Projection{}, err
	}
	return Projection{
		VerificationID: verificationID,
		Authority: AuthorityProjection{
			AuthorityID: authorityID, AcknowledgementID: acknowledgementID,
			ResponseAction:     header.ResponseAction,
			ResponseRecordedAt: header.ResponseRecordedAt.Time.UTC(),
		},
		Region: *header.Region, EvaluatedAt: evaluatedAt, Checks: checks,
	}, nil
}

func assembleChecks(
	rows []sqlgen.ListPolicyAuthoritativeObservationsRow,
	evaluatedAt time.Time,
) ([]CheckProjection, error) {
	if len(rows) > maximumProjectionRows {
		return nil, fmt.Errorf("%w: authoritative observation bound", policy.ErrInvalid)
	}
	checks := make([]CheckProjection, 0)
	indexes := make(map[string]int)
	for _, row := range rows {
		index, exists := indexes[row.CheckID]
		if !exists {
			if len(checks) == policy.MaximumFacts {
				return nil, fmt.Errorf("%w: authoritative check bound", policy.ErrInvalid)
			}
			check, err := newCheckProjection(row, evaluatedAt)
			if err != nil {
				return nil, err
			}
			indexes[row.CheckID] = len(checks)
			checks = append(checks, check)
			index = len(checks) - 1
		} else if !sameProjectionAttempt(checks[index], row) {
			return nil, fmt.Errorf("%w: inconsistent authoritative check rows", policy.ErrConflict)
		}
		if len(checks[index].Observations) == policy.MaximumObservations {
			return nil, fmt.Errorf("%w: authoritative observations per check", policy.ErrInvalid)
		}
		observation, err := newObservationProjection(row, evaluatedAt)
		if err != nil {
			return nil, err
		}
		for _, existing := range checks[index].Observations {
			if existing.ObservationID.String() == observation.ObservationID.String() {
				return nil, fmt.Errorf("%w: duplicate authoritative observation", policy.ErrConflict)
			}
		}
		checks[index].Observations = append(checks[index].Observations, observation)
	}
	return checks, nil
}

func newCheckProjection(
	row sqlgen.ListPolicyAuthoritativeObservationsRow,
	evaluatedAt time.Time,
) (CheckProjection, error) {
	checkID, checkErr := id.ParseCheck(row.CheckID)
	attemptID, attemptErr := id.ParseAttempt(row.AttemptID)
	invalid := checkErr != nil || attemptErr != nil || row.CheckOutcome == nil || row.ResultDigest == nil ||
		row.CheckVersion < 1 || row.AttemptNumber < 1 || row.ContractMajor < 1 || row.ContractMajor > 65535 ||
		row.ContractMinor < 0 || row.ContractMinor > 65535 || !row.CheckUpdatedAt.Valid ||
		row.CheckUpdatedAt.Time.After(evaluatedAt)
	if invalid {
		return CheckProjection{}, fmt.Errorf("%w: authoritative check projection", policy.ErrInvalid)
	}
	attemptNumber := uint32(row.AttemptNumber) //nolint:gosec // positive int32 was checked above.
	contractMajor := uint16(row.ContractMajor) //nolint:gosec // range was checked above.
	contractMinor := uint16(row.ContractMinor) //nolint:gosec // range was checked above.
	return CheckProjection{
		CheckID: checkID, Name: row.CheckName, Outcome: *row.CheckOutcome,
		Version: row.CheckVersion, UpdatedAt: row.CheckUpdatedAt.Time.UTC(),
		Attempt: AttemptProjection{
			AttemptID: attemptID, Number: attemptNumber, RunnerKind: row.RunnerKind,
			RunnerID: row.RunnerID, RunnerVersion: row.RunnerVersion,
			PackageDigest: row.PackageDigest, ContractMajor: contractMajor,
			ContractMinor: contractMinor, RequestDigest: row.RequestDigest,
			ConfigurationDigest: row.ConfigurationDigest, ResultDigest: *row.ResultDigest,
		},
	}, nil
}

func newObservationProjection(
	row sqlgen.ListPolicyAuthoritativeObservationsRow,
	evaluatedAt time.Time,
) (ObservationProjection, error) {
	observationID, err := id.ParseObservation(row.ObservationID)
	if err != nil || !row.RecordedAt.Valid || row.RecordedAt.Time.After(evaluatedAt) {
		return ObservationProjection{}, fmt.Errorf("%w: authoritative observation projection", policy.ErrInvalid)
	}
	return ObservationProjection{
		ObservationID: observationID, SignalName: row.SignalName,
		SignalOutcome: row.SignalOutcome, ReasonCodes: slices.Clone(row.ReasonCodes),
		RecordedAt: row.RecordedAt.Time.UTC(),
	}, nil
}

func sameProjectionAttempt(
	check CheckProjection,
	row sqlgen.ListPolicyAuthoritativeObservationsRow,
) bool {
	return check.CheckID.String() == row.CheckID && check.Name == row.CheckName &&
		check.Outcome == valueOrEmpty(row.CheckOutcome) && check.Version == row.CheckVersion &&
		row.CheckUpdatedAt.Valid && check.UpdatedAt.Equal(row.CheckUpdatedAt.Time.UTC()) &&
		check.Attempt.AttemptID.String() == row.AttemptID &&
		check.Attempt.Number == uint32(row.AttemptNumber) && //nolint:gosec // validated at construction.
		check.Attempt.RunnerKind == row.RunnerKind && check.Attempt.RunnerID == row.RunnerID &&
		check.Attempt.RunnerVersion == row.RunnerVersion &&
		check.Attempt.PackageDigest == row.PackageDigest &&
		check.Attempt.ContractMajor == uint16(row.ContractMajor) && //nolint:gosec // validated at construction.
		check.Attempt.ContractMinor == uint16(row.ContractMinor) && //nolint:gosec // validated at construction.
		check.Attempt.RequestDigest == row.RequestDigest &&
		check.Attempt.ConfigurationDigest == row.ConfigurationDigest &&
		check.Attempt.ResultDigest == valueOrEmpty(row.ResultDigest)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
