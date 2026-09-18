package policy

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"strings"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Definition is public policy meaning before the server assigns identity and revision.
type Definition struct {
	SchemaMajor       uint16          `json:"schema_major"`
	SchemaMinor       uint16          `json:"schema_minor"`
	VerifiedAssurance string          `json:"verified_assurance"`
	Rules             []policyv1.Rule `json:"rules"`
}

// Summary is source-free catalog metadata. Activation version is separate from revision.
type Summary struct {
	ID                string     `json:"id"`
	LatestRevision    uint32     `json:"latest_revision"`
	ActiveRevision    *uint32    `json:"active_revision,omitempty"`
	ActivationVersion int64      `json:"activation_version"`
	CreatedAt         time.Time  `json:"created_at"`
	ActivatedAt       *time.Time `json:"activated_at,omitempty"`
}

// RevisionInfo exposes immutable revision identity without expressions.
type RevisionInfo struct {
	PolicyID        string    `json:"policy_id"`
	Revision        uint32    `json:"revision"`
	SchemaMajor     uint16    `json:"schema_major"`
	SchemaMinor     uint16    `json:"schema_minor"`
	Digest          string    `json:"digest"`
	EvaluatorMajor  uint16    `json:"evaluator_major"`
	EvaluatorMinor  uint16    `json:"evaluator_minor"`
	EvaluatorDigest string    `json:"evaluator_digest"`
	CreatedAt       time.Time `json:"created_at"`
}

// RevisionDocument is an explicitly retrieved, authorised source document.
type RevisionDocument struct {
	RevisionInfo
	Document json.RawMessage `json:"document"`
}

// ActivationInfo is an immutable active-pointer switch, including rollback.
type ActivationInfo struct {
	PolicyID         string    `json:"policy_id"`
	Revision         uint32    `json:"revision"`
	PreviousRevision uint32    `json:"previous_revision"`
	Version          int64     `json:"version"`
	ActorID          string    `json:"actor_id"`
	ActivatedAt      time.Time `json:"activated_at"`
}

// Validation reports successful pure compilation without persisting policy meaning.
type Validation struct {
	Valid           bool   `json:"valid"`
	RuleCount       int    `json:"rule_count"`
	EvaluatorMajor  uint16 `json:"evaluator_major"`
	EvaluatorMinor  uint16 `json:"evaluator_minor"`
	EvaluatorDigest string `json:"evaluator_digest"`
}

// ManagementResult is the safe immutable receipt used for public command replay.
type ManagementResult struct {
	Policy     Summary         `json:"policy"`
	Revision   *RevisionInfo   `json:"revision,omitempty"`
	Activation *ActivationInfo `json:"activation,omitempty"`
	Replayed   bool            `json:"replayed"`
}

// Command is one closed administrative action, never a generic state update.
type Command struct {
	Operation        string      `json:"operation"`
	PolicyID         string      `json:"policy_id,omitempty"`
	Definition       *Definition `json:"definition,omitempty"`
	ExpectedRevision uint32      `json:"expected_revision,omitempty"`
	Revision         uint32      `json:"revision,omitempty"`
	ExpectedVersion  int64       `json:"expected_version,omitempty"`
	Reason           string      `json:"reason,omitempty"`
}

// String prevents accidental logging of policy expressions.
func (Command) String() string { return "[REDACTED]" }

// GoString prevents diagnostic formatting from exposing policy expressions.
func (Command) GoString() string { return "[REDACTED]" }

// ManagedCatalog is the transaction-bound catalog consumed by public commands.
type ManagedCatalog interface {
	CatalogRepository
	Inspect(context.Context, tenant.Scope, id.Policy) (Summary, error)
	Lock(context.Context, tenant.Scope, id.Policy) error
	WasActivated(context.Context, tenant.Scope, id.Policy, uint32) (bool, error)
}

// ManagementRepository owns atomic command receipts, audit and scoped inspection.
type ManagementRepository interface {
	CatalogInspectionRepository
	FindRevision(context.Context, tenant.Scope, id.Policy, uint32) (Revision, error)
	Inspect(context.Context, tenant.Scope, id.Policy) (Summary, error)
	List(context.Context, tenant.Scope, string, int) ([]Summary, error)
	Apply(context.Context, tenant.Scope, idempotency.Request, id.Event, Command, func(ManagedCatalog) (ManagementResult, error)) (ManagementResult, error)
}

// ManagementIDs generates server-owned policy and event identities.
type ManagementIDs interface {
	NewPolicy() (id.Policy, error)
	NewEvent() (id.Event, error)
}

// Management enforces public catalog authority above every persistence adapter.
type Management struct {
	repository ManagementRepository
	compiler   RevisionCompiler
	ids        ManagementIDs
	now        func() time.Time
	retention  time.Duration
}

// NewManagement composes public catalog use cases without transport or SQL dependencies.
func NewManagement(repository ManagementRepository, compiler RevisionCompiler, ids ManagementIDs, now func() time.Time, retention time.Duration) (*Management, error) {
	if repository == nil || compiler == nil || ids == nil || now == nil || retention <= 0 {
		return nil, ErrInvalid
	}
	return &Management{repository, compiler, ids, now, retention}, nil
}

var managementReason = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,63}$`)

// Execute authorises and atomically applies an immutable public command.
func (service *Management) Execute(ctx context.Context, actor access.Context, key string, command Command) (ManagementResult, error) {
	permission := access.PermissionPoliciesWrite
	if command.Operation == "activate" || command.Operation == "rollback" {
		permission = access.PermissionPoliciesActivate
	}
	if err := actor.Require(permission); err != nil {
		return ManagementResult{}, err
	}
	switch command.Operation {
	case "create", "revision":
		if command.Definition == nil {
			return ManagementResult{}, ErrInvalid
		}
	case "activate", "rollback":
		if command.Revision == 0 || command.ExpectedVersion < 0 || command.ExpectedVersion == math.MaxInt64 || !managementReason.MatchString(command.Reason) {
			return ManagementResult{}, ErrInvalid
		}
	default:
		return ManagementResult{}, ErrInvalid
	}
	if command.Operation != "create" {
		if _, err := id.ParsePolicy(command.PolicyID); err != nil {
			return ManagementResult{}, ErrRevisionNotFound
		}
	}
	if command.Operation == "revision" && (command.ExpectedRevision == 0 || command.ExpectedRevision == math.MaxUint32) {
		return ManagementResult{}, ErrInvalid
	}
	if key == "" || len(key) > 128 || strings.TrimSpace(key) != key {
		return ManagementResult{}, ErrInvalid
	}
	if command.Definition != nil {
		canonical, err := policyv1.Canonical(policyv1.Document{
			SchemaMajor: command.Definition.SchemaMajor, SchemaMinor: command.Definition.SchemaMinor,
			PolicyID: "pol_00000000000000000000000000", Revision: 1,
			VerifiedAssurance: command.Definition.VerifiedAssurance, Rules: command.Definition.Rules,
		})
		if err != nil {
			return ManagementResult{}, invalidCompilation(ctx, err)
		}
		document, err := policyv1.ParseCanonical(canonical)
		if err != nil {
			return ManagementResult{}, invalidCompilation(ctx, err)
		}
		definition := *command.Definition
		definition.Rules = document.Rules
		command.Definition = &definition
	}
	encoded, err := json.Marshal(command)
	if err != nil {
		return ManagementResult{}, err
	}
	request, err := idempotency.NewRequest(actor.TenantScope().ID(), actor.Principal().KeyID(), "policies."+command.Operation, key, encoded, service.now().UTC().Truncate(time.Microsecond), service.retention)
	if err != nil {
		return ManagementResult{}, err
	}
	event, err := service.ids.NewEvent()
	if err != nil {
		return ManagementResult{}, err
	}
	return service.repository.Apply(ctx, actor.TenantScope(), request, event, command, func(repository ManagedCatalog) (ManagementResult, error) {
		return service.apply(ctx, actor, command, repository)
	})
}
func (service *Management) apply(ctx context.Context, actor access.Context, command Command, repository ManagedCatalog) (ManagementResult, error) {
	scope := actor.TenantScope()
	identifier, err := id.ParsePolicy(command.PolicyID)
	if command.Operation == "create" {
		identifier, err = service.ids.NewPolicy()
	}
	if err != nil {
		return ManagementResult{}, err
	}
	if command.Operation != "create" {
		if err := repository.Lock(ctx, scope, identifier); err != nil {
			return ManagementResult{}, err
		}
	}
	result := ManagementResult{}
	if command.Operation == "create" || command.Operation == "revision" {
		number := uint32(1)
		if command.Operation == "revision" {
			current, err := repository.Inspect(ctx, scope, identifier)
			if err != nil {
				return result, err
			}
			if current.LatestRevision != command.ExpectedRevision {
				return result, ErrRevisionConflict
			}
			number = current.LatestRevision + 1
		}
		revision, err := service.compile(ctx, *command.Definition, identifier, number)
		if err != nil {
			return result, err
		}
		if err := repository.AppendRevision(ctx, scope, revision); err != nil {
			return result, err
		}
		info := revisionInfo(revision)
		result.Revision = &info
	} else {
		if command.Operation == "rollback" {
			existed, err := repository.WasActivated(ctx, scope, identifier, command.Revision)
			if err != nil {
				return result, err
			}
			if !existed {
				return result, ErrActivationConflict
			}
		}
		revision, err := repository.FindRevision(ctx, scope, identifier, command.Revision)
		if err != nil {
			return result, err
		}
		evaluator, err := service.compiler.CompileCanonical(ctx, revision.Canonical())
		if err != nil {
			return result, invalidCompilation(ctx, err)
		}
		if evaluator != revision.Evaluator() {
			return result, ErrRevisionConflict
		}
		activation, err := repository.Activate(ctx, scope, identifier, command.Revision, command.ExpectedVersion, actor.Principal().KeyID(), service.now().UTC().Truncate(time.Microsecond))
		if err != nil {
			return result, err
		}
		info := ActivationInfo{PolicyID: identifier.String(), Revision: activation.Revision().Reference().Revision, PreviousRevision: activation.PreviousRevision(), Version: activation.Version(), ActorID: activation.Actor().String(), ActivatedAt: activation.ActivatedAt()}
		result.Activation = &info
	}
	result.Policy, err = repository.Inspect(ctx, scope, identifier)
	return result, err
}
func (service *Management) compile(ctx context.Context, definition Definition, identifier id.Policy, number uint32) (Revision, error) {
	document := policyv1.Document{SchemaMajor: definition.SchemaMajor, SchemaMinor: definition.SchemaMinor, PolicyID: identifier.String(), Revision: number, VerifiedAssurance: definition.VerifiedAssurance, Rules: definition.Rules}
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		return Revision{}, invalidCompilation(ctx, err)
	}
	evaluator, err := service.compiler.CompileCanonical(ctx, canonical)
	if err != nil {
		return Revision{}, invalidCompilation(ctx, err)
	}
	return NewRevisionCanonical(canonical, evaluator, service.now().UTC().Truncate(time.Microsecond))
}
func invalidCompilation(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.Join(ErrInvalid, err)
}

// Validate compiles supplied meaning without registration, activation or audit mutation.
func (service *Management) Validate(ctx context.Context, actor access.Context, definition Definition) (Validation, error) {
	if err := actor.Require(access.PermissionPoliciesWrite); err != nil {
		return Validation{}, err
	}
	identifier, err := service.ids.NewPolicy()
	if err != nil {
		return Validation{}, err
	}
	revision, err := service.compile(ctx, definition, identifier, 1)
	if err != nil {
		return Validation{}, err
	}
	e := revision.Evaluator()
	return Validation{true, len(definition.Rules), e.Major, e.Minor, e.Digest}, nil
}

// Get retrieves source-free metadata for one visible policy.
func (service *Management) Get(ctx context.Context, actor access.Context, identifier id.Policy) (Summary, error) {
	if err := actor.Require(access.PermissionPoliciesRead); err != nil {
		return Summary{}, err
	}
	return service.repository.Inspect(ctx, actor.TenantScope(), identifier)
}

// List returns a bounded descending identifier page.
func (service *Management) List(ctx context.Context, actor access.Context, before string, limit int) ([]Summary, error) {
	if err := actor.Require(access.PermissionPoliciesRead); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return service.repository.List(ctx, actor.TenantScope(), before, limit)
}

// GetRevision explicitly retrieves authorised policy source and immutable identity.
func (service *Management) GetRevision(ctx context.Context, actor access.Context, identifier id.Policy, number uint32) (RevisionDocument, error) {
	if err := actor.Require(access.PermissionPoliciesRead); err != nil {
		return RevisionDocument{}, err
	}
	revision, err := service.repository.FindRevision(ctx, actor.TenantScope(), identifier, number)
	if err != nil {
		return RevisionDocument{}, err
	}
	return RevisionDocument{revisionInfo(revision), revision.Canonical()}, nil
}

// Revisions returns validated source-free revision history.
func (service *Management) Revisions(ctx context.Context, actor access.Context, identifier id.Policy, before uint32, limit int) ([]RevisionInfo, bool, uint32, error) {
	if _, err := service.Get(ctx, actor, identifier); err != nil {
		return nil, false, 0, err
	}
	inspector, err := NewCatalogInspector(service.repository)
	if err != nil {
		return nil, false, 0, err
	}
	page, err := inspector.ListRevisions(ctx, actor.TenantScope(), identifier, before, limit)
	if err != nil {
		return nil, false, 0, err
	}
	result := make([]RevisionInfo, 0, len(page.Items()))
	for _, item := range page.Items() {
		result = append(result, metadataInfo(item.Reference(), item.Evaluator(), item.CreatedAt()))
	}
	return result, page.HasMore(), page.NextBefore(), nil
}

// Activations returns immutable active-pointer switches, newest first.
func (service *Management) Activations(ctx context.Context, actor access.Context, identifier id.Policy, before int64, limit int) ([]ActivationInfo, bool, int64, error) {
	if _, err := service.Get(ctx, actor, identifier); err != nil {
		return nil, false, 0, err
	}
	inspector, err := NewCatalogInspector(service.repository)
	if err != nil {
		return nil, false, 0, err
	}
	page, err := inspector.ListActivations(ctx, actor.TenantScope(), identifier, before, limit)
	if err != nil {
		return nil, false, 0, err
	}
	result := make([]ActivationInfo, 0, len(page.Items()))
	for _, item := range page.Items() {
		result = append(result, ActivationInfo{identifier.String(), item.Revision().Reference().Revision, item.PreviousRevision(), item.Version(), item.Actor().String(), item.ActivatedAt()})
	}
	return result, page.HasMore(), page.NextBefore(), nil
}
func revisionInfo(revision Revision) RevisionInfo {
	return metadataInfo(revision.Reference(), revision.Evaluator(), revision.CreatedAt())
}
func metadataInfo(reference Reference, evaluator EvaluatorReference, at time.Time) RevisionInfo {
	return RevisionInfo{reference.ID.String(), reference.Revision, reference.SchemaMajor, reference.SchemaMinor, reference.Digest, evaluator.Major, evaluator.Minor, evaluator.Digest, at}
}
