package verification

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

var (
	// ErrCheckNotFound deliberately also covers cross-tenant misses.
	ErrCheckNotFound = errors.New("verification: check not found")
	// ErrCheckVersion identifies an optimistic concurrency conflict.
	ErrCheckVersion = errors.New("verification: check version conflict")
)

// CheckRepository is the narrow tenant-scoped persistence boundary consumed here.
type CheckRepository interface {
	FindCheck(context.Context, tenant.Scope, id.Check) (Check, error)
	SaveCheck(context.Context, tenant.Scope, CheckCommit) (bool, error)
}

// CheckEventIDGenerator supplies durable safe-progress intent identities.
type CheckEventIDGenerator interface {
	NewEvent() (id.Event, error)
}

// ResultReceipt is the exact safe inbox identity of one external result delivery.
type ResultReceipt struct {
	AttemptID   id.Attempt
	Fingerprint string
	ReceivedAt  time.Time
}

// NewResultReceipt validates one bounded result-delivery identity.
func NewResultReceipt(attemptID id.Attempt, fingerprint string, receivedAt time.Time) (ResultReceipt, error) {
	if attemptID.IsZero() || !digestToken(fingerprint) || !utcNonZero(receivedAt) {
		return ResultReceipt{}, ErrInvalidCheck
	}
	return ResultReceipt{AttemptID: attemptID, Fingerprint: fingerprint, ReceivedAt: receivedAt}, nil
}

// Validate checks a result receipt reconstructed at an application boundary.
func (receipt ResultReceipt) Validate() error {
	if receipt.AttemptID.IsZero() || !digestToken(receipt.Fingerprint) || !utcNonZero(receipt.ReceivedAt) {
		return ErrInvalidCheck
	}
	return nil
}

// CheckCommit is one atomic check, inbox, reconciliation, and outbox mutation.
type CheckCommit struct {
	Check           Check
	ExpectedVersion int64
	EventID         id.Event
	Receipt         *ResultReceipt
}

// ReconciliationClaim is one fenced durable reconciliation lease.
type ReconciliationClaim struct {
	TenantID       id.Tenant
	VerificationID id.Verification
	CheckID        id.Check
	AttemptID      id.Attempt
	Reason         string
	ClaimToken     id.Task
	ClaimCount     uint32
	ClaimedAt      time.Time
	LeaseExpiresAt time.Time
}

// ReconciliationRepository owns bounded claim and fenced resolution mechanics.
type ReconciliationRepository interface {
	ClaimReconciliation(context.Context, tenant.Scope, id.Task, time.Time, time.Duration) (ReconciliationClaim, error)
	ResolveReconciliation(context.Context, tenant.Scope, ReconciliationClaim, time.Time) error
}

// ReconciliationHook receives safe diagnostics; it must reread durable state.
type ReconciliationHook interface {
	RequestReconciliation(context.Context, tenant.Scope, id.Check, id.Attempt, string) error
}

// CheckProgress contains capture-safe aggregate facts only.
type CheckProgress struct {
	VerificationID id.Verification
	CheckID        id.Check
	State          CheckState
	Version        int64
	OccurredAt     time.Time
}

// ProgressPublisher publishes a safe prompt; consumers reread authoritative state.
type ProgressPublisher interface {
	PublishCheckProgress(context.Context, tenant.Scope, CheckProgress) error
}

// ExecutionService persists one domain transition with optimistic concurrency.
type ExecutionService struct {
	repository     CheckRepository
	identifiers    CheckEventIDGenerator
	progress       ProgressPublisher
	reconciliation ReconciliationHook
}

// NewExecutionService constructs an application service without infrastructure types.
func NewExecutionService(
	repository CheckRepository,
	identifiers CheckEventIDGenerator,
	progress ProgressPublisher,
	reconciliation ReconciliationHook,
) (*ExecutionService, error) {
	if repository == nil || identifiers == nil || progress == nil || reconciliation == nil {
		return nil, errors.New("verification: execution service dependencies are required")
	}
	return &ExecutionService{repository: repository, identifiers: identifiers, progress: progress, reconciliation: reconciliation}, nil
}

// Apply loads and saves one exact check transition. mutate must be deterministic.
func (service *ExecutionService) Apply(
	ctx context.Context,
	scope tenant.Scope,
	checkID id.Check,
	mutate func(*Check) (string, error),
) (Check, string, error) {
	return service.apply(ctx, scope, checkID, nil, mutate)
}

// ApplyResult atomically claims a result receipt with its resulting state transition.
func (service *ExecutionService) ApplyResult(
	ctx context.Context,
	scope tenant.Scope,
	checkID id.Check,
	receipt ResultReceipt,
	mutate func(*Check) (string, error),
) (Check, string, error) {
	if receipt.Validate() != nil {
		return Check{}, "", ErrInvalidCheck
	}
	return service.apply(ctx, scope, checkID, &receipt, mutate)
}

func (service *ExecutionService) apply(
	ctx context.Context,
	scope tenant.Scope,
	checkID id.Check,
	receipt *ResultReceipt,
	mutate func(*Check) (string, error),
) (Check, string, error) {
	if err := ctx.Err(); err != nil {
		return Check{}, "", err
	}
	if checkID.IsZero() || mutate == nil {
		return Check{}, "", ErrInvalidCheck
	}
	check, err := service.repository.FindCheck(ctx, scope, checkID)
	if err != nil {
		return Check{}, "", err
	}
	expected := check.Version
	disposition, mutationErr := mutate(&check)
	if mutationErr != nil && !errors.Is(mutationErr, ErrStaleAttempt) && !errors.Is(mutationErr, ErrAttemptConflict) {
		return Check{}, disposition, mutationErr
	}
	if check.Version != expected {
		eventID, eventErr := service.identifiers.NewEvent()
		if eventErr != nil {
			return Check{}, disposition, fmt.Errorf("generate check progress event id: %w", eventErr)
		}
		duplicate, saveErr := service.repository.SaveCheck(ctx, scope, CheckCommit{
			Check: check, ExpectedVersion: expected, EventID: eventID, Receipt: receipt,
		})
		if saveErr != nil {
			return Check{}, disposition, saveErr
		}
		if duplicate {
			authoritative, findErr := service.repository.FindCheck(ctx, scope, checkID)
			return authoritative, "duplicate", findErr
		}
	}
	if errors.Is(mutationErr, ErrStaleAttempt) || errors.Is(mutationErr, ErrAttemptConflict) {
		attemptID := id.Attempt{}
		diagnostics := check.Diagnostics()
		if len(diagnostics) != 0 {
			attemptID = diagnostics[len(diagnostics)-1].AttemptID
		}
		if hookErr := service.reconciliation.RequestReconciliation(ctx, scope, check.ID, attemptID, disposition); hookErr != nil {
			return Check{}, disposition, fmt.Errorf("request attempt reconciliation: %w", hookErr)
		}
		return check, disposition, mutationErr
	}
	if err := service.progress.PublishCheckProgress(ctx, scope, CheckProgress{VerificationID: check.VerificationID,
		CheckID: check.ID, State: check.State, Version: check.Version, OccurredAt: check.UpdatedAt}); err != nil {
		return Check{}, disposition, fmt.Errorf("publish check progress: %w", err)
	}
	return check, disposition, nil
}

// MemoryCheckRepository is a deterministic optimistic store for aggregate tests.
type MemoryCheckRepository struct {
	mutex    sync.Mutex
	checks   map[string]Check
	receipts map[string]struct{}
}

// NewMemoryCheckRepository constructs an empty deterministic repository.
func NewMemoryCheckRepository() *MemoryCheckRepository {
	return &MemoryCheckRepository{checks: make(map[string]Check), receipts: make(map[string]struct{})}
}

// Insert adds initial state for tests and local deterministic execution.
func (repository *MemoryCheckRepository) Insert(check Check) error {
	if repository == nil || check.ID.IsZero() || check.TenantID.IsZero() || check.Version < 1 {
		return ErrInvalidCheck
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	key := check.TenantID.String() + "\x00" + check.ID.String()
	if _, exists := repository.checks[key]; exists {
		return ErrCheckVersion
	}
	repository.checks[key] = cloneCheck(check)
	return nil
}

// FindCheck returns only an exact tenant-owned check.
func (repository *MemoryCheckRepository) FindCheck(ctx context.Context, scope tenant.Scope, checkID id.Check) (Check, error) {
	if err := ctx.Err(); err != nil {
		return Check{}, err
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	check, exists := repository.checks[scope.ID().String()+"\x00"+checkID.String()]
	if !exists {
		return Check{}, ErrCheckNotFound
	}
	return cloneCheck(check), nil
}

// SaveCheck applies one optimistic aggregate version change.
func (repository *MemoryCheckRepository) SaveCheck(ctx context.Context, scope tenant.Scope, commit CheckCommit) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	check, expected := commit.Check, commit.ExpectedVersion
	if check.TenantID.String() != scope.ID().String() || check.Version <= expected {
		return false, ErrInvalidCheck
	}
	if commit.EventID.IsZero() {
		return false, ErrInvalidCheck
	}
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	var receiptKey string
	if commit.Receipt != nil {
		receiptKey = scope.ID().String() + "\x00" + commit.Receipt.AttemptID.String() + "\x00" + commit.Receipt.Fingerprint
		if _, exists := repository.receipts[receiptKey]; exists {
			return true, nil
		}
	}
	key := scope.ID().String() + "\x00" + check.ID.String()
	current, exists := repository.checks[key]
	if !exists {
		return false, ErrCheckNotFound
	}
	if current.Version != expected {
		return false, ErrCheckVersion
	}
	if commit.Receipt != nil {
		repository.receipts[receiptKey] = struct{}{}
	}
	repository.checks[key] = cloneCheck(check)
	return false, nil
}

func cloneCheck(value Check) Check {
	copyOf := value
	copyOf.attempts = value.Attempts()
	copyOf.diagnostics = value.Diagnostics()
	return copyOf
}

var _ CheckRepository = (*MemoryCheckRepository)(nil)
