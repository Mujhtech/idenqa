package pack

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/internal/document"
)

// Lifecycle operations recorded in immutable history.
const (
	OperationActivate  = "activate"
	OperationDeprecate = "deprecate"
	OperationRetire    = "retire"
)

// Entry is one registered pack revision with its current registry lifecycle.
type Entry struct {
	Pack      Pack
	State     LifecycleState
	Version   int64
	UpdatedAt time.Time
}

// DocumentSummary is a bounded document entry in a pack list projection.
type DocumentSummary struct {
	Type         DocumentType `json:"type"`
	SupportLevel SupportLevel `json:"support_level"`
}

// Summary is the safe list projection of one registered pack revision.
type Summary struct {
	Country        string            `json:"country"`
	CountryAlpha3  string            `json:"country_alpha3"`
	Revision       uint32            `json:"revision"`
	Digest         string            `json:"digest"`
	PublishedAt    time.Time         `json:"published_at"`
	LifecycleState LifecycleState    `json:"lifecycle_state"`
	LegalReview    LegalReview       `json:"legal_review"`
	Documents      []DocumentSummary `json:"documents"`
}

// SupportProjection is the honest support-level projection of one document.
// It contains only pack content and never a legal conclusion or approval.
type SupportProjection struct {
	Country                    string                 `json:"country"`
	CountryAlpha3              string                 `json:"country_alpha3"`
	DocumentType               DocumentType           `json:"document_type"`
	PackRevision               uint32                 `json:"pack_revision"`
	PackDigest                 string                 `json:"pack_digest"`
	LifecycleState             LifecycleState         `json:"lifecycle_state"`
	LegalReview                LegalReview            `json:"legal_review"`
	SupportLevel               SupportLevel           `json:"support_level"`
	KnownVersions              []KnownVersion         `json:"known_versions"`
	RequiredSides              []Side                 `json:"required_sides"`
	SupportedFields            []document.Name        `json:"supported_fields"`
	SecurityChecks             []string               `json:"security_checks"`
	Barcode                    Declaration            `json:"barcode"`
	MRZ                        Declaration            `json:"mrz"`
	NFC                        Declaration            `json:"nfc"`
	ModelAndParserRequirements []RequirementReference `json:"model_and_parser_requirements"`
	EvaluationCoverage         EvaluationCoverage     `json:"evaluation_coverage"`
	KnownLimitations           []string               `json:"known_limitations"`
	Evidence                   []string               `json:"evidence"`
	AssuranceMappings          []AssuranceMapping     `json:"assurance_mappings"`
	RequirementSlots           []RequirementSlot      `json:"requirement_slots"`
	Authority                  string                 `json:"authority"`
}

// StoredState is one durable lifecycle projection row.
type StoredState struct {
	Country   string
	Revision  uint32
	State     LifecycleState
	Digest    string
	Version   int64
	UpdatedAt time.Time
}

// Change is one append-only lifecycle transition for one pack revision.
type Change struct {
	Country           string
	Revision          uint32
	TransitionVersion int64
	Operation         string
	PreviousState     LifecycleState
	State             LifecycleState
	PackDigest        string
	Reason            string
	Actor             string
	RecordedAt        time.Time
}

// Store is the consumer-side persistence port for lifecycle state and history.
// Apply must commit every change atomically or none of them.
type Store interface {
	LoadStates(context.Context) ([]StoredState, error)
	LoadHistory(context.Context, string) ([]Change, error)
	Apply(context.Context, []Change) error
}

// Transition requests one expected-version lifecycle operation.
type Transition struct {
	Country         string
	Revision        uint32
	ExpectedVersion int64
	Reason          string
	Actor           string
}

type entry struct {
	pack    Pack
	state   LifecycleState
	version int64
	updated time.Time
}

// Registry owns immutable pack revisions and their recorded lifecycle.
type Registry struct {
	mu       sync.RWMutex
	entries  map[string]map[uint32]*entry
	active   map[string]uint32
	versions map[string]int64
	history  map[string][]Change
	store    Store
	now      func() time.Time
}

// NewRegistry validates and registers immutable pack revisions. Packs with the
// same country and revision, or more than one non-draft active revision per
// country, are rejected. A nil store keeps lifecycle in memory only.
func NewRegistry(packs []Pack, store Store, now func() time.Time) (*Registry, error) {
	if len(packs) == 0 || now == nil {
		return nil, fmt.Errorf("%w: registry input", ErrInvalid)
	}
	registry := &Registry{
		entries:  make(map[string]map[uint32]*entry),
		active:   make(map[string]uint32),
		versions: make(map[string]int64),
		history:  make(map[string][]Change),
		store:    store,
		now:      now,
	}
	for _, value := range packs {
		if err := value.Validate(); err != nil {
			return nil, err
		}
		revisions := registry.entries[value.Country]
		if revisions == nil {
			revisions = make(map[uint32]*entry)
			registry.entries[value.Country] = revisions
		}
		if _, exists := revisions[value.Revision]; exists {
			return nil, fmt.Errorf("%w: duplicate pack revision", ErrInvalid)
		}
		revisions[value.Revision] = &entry{pack: value.Clone(), state: value.Lifecycle}
	}
	for country, revisions := range registry.entries {
		for revision, value := range revisions {
			if value.state != LifecycleActive {
				continue
			}
			if _, exists := registry.active[country]; exists {
				return nil, fmt.Errorf("%w: multiple active pack revisions", ErrInvalid)
			}
			registry.active[country] = revision
		}
	}
	return registry, nil
}

// Load restores durable lifecycle state and history over the embedded packs.
// A stored state that does not match a registered revision, digest, or
// transition invariant fails closed.
func (registry *Registry) Load(ctx context.Context) error {
	if registry.store == nil {
		return nil
	}
	states, err := registry.store.LoadStates(ctx)
	if err != nil {
		return fmt.Errorf("pack: load lifecycle states: %w", err)
	}
	for _, state := range states {
		revisions, exists := registry.entries[state.Country]
		if !exists {
			return fmt.Errorf("%w: stored state for unregistered country", ErrInvalid)
		}
		value, exists := revisions[state.Revision]
		if !exists || value.pack.Digest != state.Digest || !state.State.Valid() || state.Version <= 0 || !validUTC(state.UpdatedAt) {
			return fmt.Errorf("%w: stored pack state", ErrInvalid)
		}
		value.state = state.State
		value.version = state.Version
		value.updated = state.UpdatedAt
		if state.Version > registry.versions[state.Country] {
			registry.versions[state.Country] = state.Version
		}
	}
	registry.active = make(map[string]uint32)
	for country, revisions := range registry.entries {
		for revision, value := range revisions {
			if value.state != LifecycleActive {
				continue
			}
			if _, exists := registry.active[country]; exists {
				return fmt.Errorf("%w: stored multiple active pack revisions", ErrInvalid)
			}
			registry.active[country] = revision
		}
	}
	countries := make([]string, 0, len(registry.entries))
	for country := range registry.entries {
		countries = append(countries, country)
	}
	for _, country := range countries {
		history, err := registry.store.LoadHistory(ctx, country)
		if err != nil {
			return fmt.Errorf("pack: load lifecycle history: %w", err)
		}
		registry.history[country] = slices.Clone(history)
	}
	return nil
}

// List returns every registered pack revision ordered by country and
// descending revision.
func (registry *Registry) List() []Summary {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	countries := make([]string, 0, len(registry.entries))
	for country := range registry.entries {
		countries = append(countries, country)
	}
	sort.Strings(countries)
	result := make([]Summary, 0)
	for _, country := range countries {
		revisions := registry.sortedRevisions(country)
		for _, revision := range revisions {
			result = append(result, registry.summaryLocked(country, revision))
		}
	}
	return result
}

// Get returns one exact registered revision.
func (registry *Registry) Get(country string, revision uint32) (Entry, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	country, _, ok := NormalizeCountry(country)
	if !ok || revision == 0 {
		return Entry{}, fmt.Errorf("%w: country or revision", ErrNotFound)
	}
	value, exists := registry.entries[country][revision]
	if !exists {
		return Entry{}, ErrNotFound
	}
	return registry.entryLocked(value), nil
}

// Active returns the currently active revision for the country.
func (registry *Registry) Active(country string) (Entry, error) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	country, _, ok := NormalizeCountry(country)
	if !ok {
		return Entry{}, ErrNotFound
	}
	revision, exists := registry.active[country]
	if !exists {
		return Entry{}, ErrNotFound
	}
	return registry.entryLocked(registry.entries[country][revision]), nil
}

// Support resolves the active pack projection for one document type. The
// country may be an ISO 3166-1 alpha-2 or alpha-3 code; the document type may
// be either a pack or canonical analysis type name.
func (registry *Registry) Support(country, documentType string) (SupportProjection, bool) {
	entry, err := registry.Active(country)
	if err != nil {
		return SupportProjection{}, false
	}
	mapped := DocumentType(strings.TrimSpace(documentType))
	if !mapped.Valid() {
		var ok bool
		mapped, ok = DocumentTypeFromCanonical(string(mapped))
		if !ok {
			return SupportProjection{}, false
		}
	}
	value, ok := entry.Pack.Document(mapped)
	if !ok {
		return SupportProjection{}, false
	}
	return SupportProjection{
		Country:                    entry.Pack.Country,
		CountryAlpha3:              entry.Pack.CountryAlpha3,
		DocumentType:               value.Type,
		PackRevision:               entry.Pack.Revision,
		PackDigest:                 entry.Pack.Digest,
		LifecycleState:             entry.State,
		LegalReview:                entry.Pack.LegalReview,
		SupportLevel:               value.SupportLevel,
		KnownVersions:              slices.Clone(value.KnownVersions),
		RequiredSides:              slices.Clone(value.RequiredSides),
		SupportedFields:            slices.Clone(value.SupportedFields),
		SecurityChecks:             slices.Clone(value.SecurityChecks),
		Barcode:                    value.Barcode,
		MRZ:                        value.MRZ,
		NFC:                        value.NFC,
		ModelAndParserRequirements: slices.Clone(value.ModelAndParserRequirements),
		EvaluationCoverage:         EvaluationCoverage{State: value.EvaluationCoverage.State, References: slices.Clone(value.EvaluationCoverage.References)},
		KnownLimitations:           slices.Clone(value.KnownLimitations),
		Evidence:                   slices.Clone(value.Evidence),
		AssuranceMappings:          slices.Clone(value.AssuranceMappings),
		RequirementSlots:           slices.Clone(entry.Pack.RequirementSlots),
		Authority:                  entry.Pack.Authority,
	}, true
}

// History returns the append-only transition history for one country.
func (registry *Registry) History(country string) []Change {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	country, _, ok := NormalizeCountry(country)
	if !ok {
		return []Change{}
	}
	return slices.Clone(registry.history[country])
}

// Activate makes a draft or deprecated revision active. A different active
// revision is deprecated in the same recorded transition.
func (registry *Registry) Activate(ctx context.Context, request Transition) (Entry, error) {
	return registry.transition(ctx, request, OperationActivate)
}

// Deprecate moves an active revision to deprecated.
func (registry *Registry) Deprecate(ctx context.Context, request Transition) (Entry, error) {
	return registry.transition(ctx, request, OperationDeprecate)
}

// Retire moves an active, deprecated, or draft revision to its terminal state.
func (registry *Registry) Retire(ctx context.Context, request Transition) (Entry, error) {
	return registry.transition(ctx, request, OperationRetire)
}

func (registry *Registry) transition(ctx context.Context, request Transition, operation string) (Entry, error) {
	country, _, ok := NormalizeCountry(request.Country)
	if !ok || request.Revision == 0 || request.ExpectedVersion < 0 ||
		!validToken(request.Reason, 100) || !validToken(request.Actor, 100) {
		return Entry{}, fmt.Errorf("%w: transition request", ErrInvalid)
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	revisions, exists := registry.entries[country]
	if !exists {
		return Entry{}, ErrNotFound
	}
	target, exists := revisions[request.Revision]
	if !exists {
		return Entry{}, ErrNotFound
	}
	if registry.versions[country] != request.ExpectedVersion {
		return Entry{}, fmt.Errorf("%w: expected version", ErrConflict)
	}
	if err := allowedTransition(operation, target.state); err != nil {
		return Entry{}, err
	}
	nextVersion := registry.versions[country] + 1
	recordedAt := registry.now().UTC().Truncate(time.Microsecond)
	changes := make([]Change, 0, 2)
	if operation == OperationActivate {
		if previousRevision, exists := registry.active[country]; exists && previousRevision != request.Revision {
			previous := revisions[previousRevision]
			changes = append(changes, Change{
				Country: country, Revision: previousRevision, TransitionVersion: nextVersion,
				Operation: OperationDeprecate, PreviousState: previous.state, State: LifecycleDeprecated,
				PackDigest: previous.pack.Digest, Reason: request.Reason, Actor: request.Actor, RecordedAt: recordedAt,
			})
		}
	}
	changes = append(changes, Change{
		Country: country, Revision: request.Revision, TransitionVersion: nextVersion,
		Operation: operation, PreviousState: target.state, State: targetState(operation),
		PackDigest: target.pack.Digest, Reason: request.Reason, Actor: request.Actor, RecordedAt: recordedAt,
	})
	if registry.store != nil {
		if err := registry.store.Apply(ctx, changes); err != nil {
			return Entry{}, fmt.Errorf("pack: apply lifecycle transition: %w", err)
		}
	}
	for _, change := range changes {
		value := revisions[change.Revision]
		value.state = change.State
		value.version = change.TransitionVersion
		value.updated = change.RecordedAt
		if change.State == LifecycleActive {
			registry.active[country] = change.Revision
		}
		if change.State != LifecycleActive {
			if active, exists := registry.active[country]; exists && active == change.Revision {
				delete(registry.active, country)
			}
		}
		registry.history[country] = append(registry.history[country], change)
	}
	registry.versions[country] = nextVersion
	return registry.entryLocked(target), nil
}

func allowedTransition(operation string, state LifecycleState) error {
	switch operation {
	case OperationActivate:
		if state == LifecycleDraft || state == LifecycleDeprecated {
			return nil
		}
	case OperationDeprecate:
		if state == LifecycleActive {
			return nil
		}
	case OperationRetire:
		if state == LifecycleActive || state == LifecycleDeprecated || state == LifecycleDraft {
			return nil
		}
	}
	return fmt.Errorf("%w: lifecycle transition", ErrConflict)
}

func targetState(operation string) LifecycleState {
	switch operation {
	case OperationActivate:
		return LifecycleActive
	case OperationDeprecate:
		return LifecycleDeprecated
	default:
		return LifecycleRetired
	}
}

func (registry *Registry) sortedRevisions(country string) []uint32 {
	revisions := make([]uint32, 0, len(registry.entries[country]))
	for revision := range registry.entries[country] {
		revisions = append(revisions, revision)
	}
	sort.Slice(revisions, func(left, right int) bool { return revisions[left] > revisions[right] })
	return revisions
}

func (registry *Registry) summaryLocked(country string, revision uint32) Summary {
	value := registry.entries[country][revision]
	documents := make([]DocumentSummary, 0, len(value.pack.Documents))
	for _, entry := range value.pack.Documents {
		documents = append(documents, DocumentSummary{Type: entry.Type, SupportLevel: entry.SupportLevel})
	}
	return Summary{
		Country:        value.pack.Country,
		CountryAlpha3:  value.pack.CountryAlpha3,
		Revision:       value.pack.Revision,
		Digest:         value.pack.Digest,
		PublishedAt:    value.pack.PublishedAt,
		LifecycleState: value.state,
		LegalReview:    value.pack.LegalReview,
		Documents:      documents,
	}
}

func (registry *Registry) entryLocked(value *entry) Entry {
	return Entry{
		Pack:      value.pack.Clone(),
		State:     value.state,
		Version:   value.version,
		UpdatedAt: value.updated,
	}
}

// Version returns the current per-country transition version. It reports zero
// for an unregistered country.
func (registry *Registry) Version(country string) int64 {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalized, _, ok := NormalizeCountry(country)
	if !ok {
		return 0
	}
	return registry.versions[normalized]
}
