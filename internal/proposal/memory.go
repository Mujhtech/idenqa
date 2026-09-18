package proposal

import (
	"context"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// InMemoryProposalStore is a deterministic in-memory store for tests and local development.
type InMemoryProposalStore struct {
	mu        sync.Mutex
	proposals map[string]Proposal
}

// NewInMemoryProposalStore returns an empty deterministic in-memory store.
func NewInMemoryProposalStore() *InMemoryProposalStore {
	return &InMemoryProposalStore{proposals: make(map[string]Proposal)}
}

func proposalKey(scope tenant.Scope, proposalID id.Proposal) string {
	return scope.ID().String() + ":" + proposalID.String()
}

// Create persists a validated proposal under tenant scope.
func (store *InMemoryProposalStore) Create(_ context.Context, scope tenant.Scope, proposal Proposal) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := proposalKey(scope, proposal.ID)
	if _, exists := store.proposals[key]; exists {
		return ErrConflict
	}
	if err := proposal.Validate(); err != nil {
		return err
	}
	store.proposals[key] = proposal
	return nil
}

// Get retrieves a proposal under tenant scope.
func (store *InMemoryProposalStore) Get(_ context.Context, scope tenant.Scope, proposalID id.Proposal) (Proposal, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := proposalKey(scope, proposalID)
	proposal, ok := store.proposals[key]
	if !ok {
		return Proposal{}, ErrNotFound
	}
	return proposal, nil
}

// Update applies an optimistic-concurrency transition.
func (store *InMemoryProposalStore) Update(_ context.Context, scope tenant.Scope, proposal Proposal, expectedVersion int64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := proposalKey(scope, proposal.ID)
	existing, ok := store.proposals[key]
	if !ok {
		return ErrNotFound
	}
	if existing.Version != expectedVersion {
		return ErrConflict
	}
	if proposal.Version != expectedVersion+1 {
		return ErrConflict
	}
	if err := proposal.Validate(); err != nil {
		return err
	}
	store.proposals[key] = proposal
	return nil
}

// GetByVerification lists proposals for a verification under tenant scope.
func (store *InMemoryProposalStore) GetByVerification(_ context.Context, scope tenant.Scope, verificationID id.Verification) ([]Proposal, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var result []Proposal
	for _, proposal := range store.proposals {
		if proposal.TenantID == scope.ID() && proposal.VerificationID == verificationID {
			result = append(result, proposal)
		}
	}
	return result, nil
}

// InMemoryCommandStore is an in-memory accepted-command store.
type InMemoryCommandStore struct {
	mu         sync.Mutex
	commands   map[string]AcceptedCommandRecord
	byProposal map[string]string
}

// NewInMemoryCommandStore returns an empty deterministic accepted-command store.
func NewInMemoryCommandStore() *InMemoryCommandStore {
	return &InMemoryCommandStore{
		commands:   make(map[string]AcceptedCommandRecord),
		byProposal: make(map[string]string),
	}
}

func commandKey(scope tenant.Scope, commandID id.AcceptedCommand) string {
	return scope.ID().String() + ":" + commandID.String()
}

// CreateCommand persists an accepted command under tenant scope.
func (store *InMemoryCommandStore) CreateCommand(_ context.Context, scope tenant.Scope, record AcceptedCommandRecord) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := commandKey(scope, record.ID)
	if _, exists := store.commands[key]; exists {
		return ErrConflict
	}
	store.commands[key] = record
	store.byProposal[scope.ID().String()+":"+record.ProposalID.String()] = record.ID.String()
	return nil
}

// GetCommand retrieves an accepted command under tenant scope.
func (store *InMemoryCommandStore) GetCommand(_ context.Context, scope tenant.Scope, commandID id.AcceptedCommand) (AcceptedCommandRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := commandKey(scope, commandID)
	record, ok := store.commands[key]
	if !ok {
		return AcceptedCommandRecord{}, ErrNotFound
	}
	return record, nil
}

// GetByProposal retrieves the accepted command for a proposal.
func (store *InMemoryCommandStore) GetByProposal(_ context.Context, scope tenant.Scope, proposalID id.Proposal) (AcceptedCommandRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := scope.ID().String() + ":" + proposalID.String()
	cmdID, ok := store.byProposal[key]
	if !ok {
		return AcceptedCommandRecord{}, ErrNotFound
	}
	parsed, _ := id.ParseAcceptedCommand(cmdID)
	cmdKey := commandKey(scope, parsed)
	record, ok := store.commands[cmdKey]
	if !ok {
		return AcceptedCommandRecord{}, ErrNotFound
	}
	return record, nil
}

// MarkExecuted records the deterministic execution time of a command.
func (store *InMemoryCommandStore) MarkExecuted(_ context.Context, scope tenant.Scope, commandID id.AcceptedCommand, at time.Time) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	key := commandKey(scope, commandID)
	record, ok := store.commands[key]
	if !ok {
		return ErrNotFound
	}
	record.ExecutedAt = &at
	store.commands[key] = record
	return nil
}

// compile-time checks
var _ Repository = (*InMemoryProposalStore)(nil)
var _ CommandStore = (*InMemoryCommandStore)(nil)
