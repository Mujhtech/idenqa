// Package tenantexport streams a bounded, portable, tenant-owned export of
// Idenqa Core reference data. It exists for portability and offboarding; it is
// not the subject data-subject-access request.
package tenantexport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// SchemaName identifies the stable NDJSON envelope.
	SchemaName = "idenqa.tenant-export"
	// SchemaVersion is the current envelope schema version.
	SchemaVersion = 1

	// DefaultBatchSize bounds one per-collection keyset page. Decision batches
	// stay smaller because each canonical bundle may be large.
	DefaultBatchSize = 100
	// DecisionBatchSize bounds one decision bundle page.
	DecisionBatchSize = 10
	// MaximumCollections bounds an explicit collection filter.
	MaximumCollections = 24

	exportPermission = access.PermissionTenantExport
)

var (
	// ErrInvalid rejects malformed collections, sources, or record fields.
	ErrInvalid = errors.New("tenantexport: invalid value")
	// ErrLimit means a caller-imposed size or duration guard was reached.
	ErrLimit = errors.New("tenantexport: export limit reached")
)

// Collection is one export record family.
type Collection string

// The complete export collection vocabulary.
const (
	CollectionTenant                  Collection = "tenant"
	CollectionCaptureProfiles         Collection = "capture_profiles"
	CollectionPolicies                Collection = "policies"
	CollectionPolicyRevisions         Collection = "policy_revisions"
	CollectionPolicyActivations       Collection = "policy_activations"
	CollectionVerifications           Collection = "verifications"
	CollectionVerificationTransitions Collection = "verification_transitions"
	CollectionVerificationChecks      Collection = "verification_checks"
	CollectionVerificationAttempts    Collection = "verification_attempts"
	CollectionDecisions               Collection = "decisions"
	CollectionAuditRecords            Collection = "audit_records"
	CollectionWebhookEndpoints        Collection = "webhook_endpoints"
	CollectionReviewCases             Collection = "review_cases"
	CollectionReviewFindings          Collection = "review_findings"
	CollectionIdentitySubjects        Collection = "identity_subjects"
	CollectionIdentityRecords         Collection = "identity_records"
	CollectionEvidenceAssets          Collection = "evidence_assets"
	CollectionFraudConfiguration      Collection = "fraud_configuration"
	CollectionPrivacyDeletions        Collection = "privacy_deletions"
	CollectionPrivacyHolds            Collection = "privacy_holds"
)

var allCollections = []Collection{
	CollectionTenant,
	CollectionCaptureProfiles,
	CollectionPolicies,
	CollectionPolicyRevisions,
	CollectionPolicyActivations,
	CollectionVerifications,
	CollectionVerificationTransitions,
	CollectionVerificationChecks,
	CollectionVerificationAttempts,
	CollectionDecisions,
	CollectionAuditRecords,
	CollectionWebhookEndpoints,
	CollectionReviewCases,
	CollectionReviewFindings,
	CollectionIdentitySubjects,
	CollectionIdentityRecords,
	CollectionEvidenceAssets,
	CollectionFraudConfiguration,
	CollectionPrivacyDeletions,
	CollectionPrivacyHolds,
}

// AllCollections returns the canonical export order.
func AllCollections() []Collection { return slices.Clone(allCollections) }

// Known reports whether name is a supported collection.
func (collection Collection) Known() bool { return slices.Contains(allCollections, collection) }

// ParseCollections validates an optional comma-separated collection filter.
// An empty value selects every collection.
func ParseCollections(value string) ([]Collection, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	entries := strings.Split(trimmed, ",")
	if len(entries) > MaximumCollections {
		return nil, fmt.Errorf("tenantexport: at most %d collections may be selected", MaximumCollections)
	}
	seen := make(map[Collection]struct{}, len(entries))
	selection := make([]Collection, 0, len(entries))
	for _, entry := range entries {
		if entry == "" || strings.TrimSpace(entry) != entry {
			return nil, errors.New("tenantexport: collection filter contains an invalid entry")
		}
		collection := Collection(entry)
		if !collection.Known() {
			return nil, fmt.Errorf("tenantexport: unknown collection %q", entry)
		}
		if _, duplicate := seen[collection]; duplicate {
			return nil, fmt.Errorf("tenantexport: collection %q is repeated", entry)
		}
		seen[collection] = struct{}{}
		selection = append(selection, collection)
	}
	return selection, nil
}

// Record is one source row ready for the canonical envelope. Position is the
// opaque ascending keyset cursor for the collection and is never written
// directly; the corresponding identifier field must already be in Fields.
type Record struct {
	Position string
	Fields   any
}

type pageSource func(context.Context, tenant.Scope, string, int) ([]Record, error)

// Narrow per-collection sources consumed by Exporter. Each paginated method
// returns at most limit ascending-position records.
type (
	// TenantSource reads the single authenticated tenant metadata record.
	TenantSource interface {
		Tenant(context.Context, tenant.Scope) (Record, error)
	}
	// CaptureProfileSource reads capture profiles with their published revision.
	CaptureProfileSource interface {
		CaptureProfiles(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// PolicySource reads policy catalogs and activation state.
	PolicySource interface {
		Policies(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// PolicyRevisionSource reads immutable policy revision metadata.
	PolicyRevisionSource interface {
		PolicyRevisions(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// PolicyActivationSource reads immutable activation history.
	PolicyActivationSource interface {
		PolicyActivations(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// VerificationSource reads verification session snapshots.
	VerificationSource interface {
		Verifications(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// VerificationTransitionSource reads verification lifecycle receipts.
	VerificationTransitionSource interface {
		VerificationTransitions(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// VerificationCheckSource reads verification check metadata.
	VerificationCheckSource interface {
		VerificationChecks(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// VerificationAttemptSource reads verification attempt provenance metadata.
	VerificationAttemptSource interface {
		VerificationAttempts(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// DecisionSource reads exact byte-canonical decision bundles.
	DecisionSource interface {
		Decisions(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// AuditRecordSource reads the tenant's append-only audit chain.
	AuditRecordSource interface {
		AuditRecords(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// WebhookEndpointSource reads endpoint configuration without secrets.
	WebhookEndpointSource interface {
		WebhookEndpoints(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// ReviewCaseSource reads manual review case metadata.
	ReviewCaseSource interface {
		ReviewCases(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// ReviewFindingSource reads immutable review finding metadata.
	ReviewFindingSource interface {
		ReviewFindings(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// IdentitySubjectSource reads persistent subject metadata without ciphertext.
	IdentitySubjectSource interface {
		IdentitySubjects(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// IdentityRecordSource reads identity record metadata, masks, and digests.
	IdentityRecordSource interface {
		IdentityRecords(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// EvidenceAssetSource reads evidence asset metadata without ciphertext.
	EvidenceAssetSource interface {
		EvidenceAssets(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// FraudConfigurationSource reads versioned fraud configuration.
	FraudConfigurationSource interface {
		FraudConfiguration(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// PrivacyDeletionSource reads deletion workflow status.
	PrivacyDeletionSource interface {
		PrivacyDeletions(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
	// PrivacyHoldSource reads legal-hold status.
	PrivacyHoldSource interface {
		PrivacyHolds(context.Context, tenant.Scope, string, int) ([]Record, error)
	}
)

// Sources collects the per-collection read adapters. Implementations are
// composed by the process boundary; the exporter never opens its own storage.
type Sources struct {
	Tenant                  TenantSource
	CaptureProfiles         CaptureProfileSource
	Policies                PolicySource
	PolicyRevisions         PolicyRevisionSource
	PolicyActivations       PolicyActivationSource
	Verifications           VerificationSource
	VerificationTransitions VerificationTransitionSource
	VerificationChecks      VerificationCheckSource
	VerificationAttempts    VerificationAttemptSource
	Decisions               DecisionSource
	AuditRecords            AuditRecordSource
	WebhookEndpoints        WebhookEndpointSource
	ReviewCases             ReviewCaseSource
	ReviewFindings          ReviewFindingSource
	IdentitySubjects        IdentitySubjectSource
	IdentityRecords         IdentityRecordSource
	EvidenceAssets          EvidenceAssetSource
	FraudConfiguration      FraudConfigurationSource
	PrivacyDeletions        PrivacyDeletionSource
	PrivacyHolds            PrivacyHoldSource
}

// Exporter rechecks tenant-export authority and streams canonical NDJSON lines
// to a caller-supplied writer callback.
type Exporter struct {
	sources Sources
	now     func() time.Time
}

// Authority is the authenticated application boundary consumed by Exporter.
// access.Context satisfies it at the transport composition boundary.
type Authority interface {
	TenantScope() tenant.Scope
	Require(access.Permission) error
}

// NewExporter constructs the export boundary with every source present.
func NewExporter(sources Sources, now func() time.Time) (*Exporter, error) {
	if now == nil {
		now = time.Now
	}
	if !sources.complete() {
		return nil, ErrInvalid
	}
	return &Exporter{sources: sources, now: now}, nil
}

func (sources Sources) complete() bool {
	return sources.Tenant != nil &&
		sources.CaptureProfiles != nil &&
		sources.Policies != nil &&
		sources.PolicyRevisions != nil &&
		sources.PolicyActivations != nil &&
		sources.Verifications != nil &&
		sources.VerificationTransitions != nil &&
		sources.VerificationChecks != nil &&
		sources.VerificationAttempts != nil &&
		sources.Decisions != nil &&
		sources.AuditRecords != nil &&
		sources.WebhookEndpoints != nil &&
		sources.ReviewCases != nil &&
		sources.ReviewFindings != nil &&
		sources.IdentitySubjects != nil &&
		sources.IdentityRecords != nil &&
		sources.EvidenceAssets != nil &&
		sources.FraudConfiguration != nil &&
		sources.PrivacyDeletions != nil &&
		sources.PrivacyHolds != nil
}

type headerRecord struct {
	Schema        string       `json:"schema"`
	SchemaVersion int          `json:"schema_version"`
	TenantID      string       `json:"tenant_id"`
	ExportedAt    time.Time    `json:"exported_at"`
	Collections   []Collection `json:"collections"`
}

type footerRecord struct {
	Counts map[string]int64 `json:"counts"`
	Digest string           `json:"digest"`
}

// Export authorises, then streams the header, every selected collection in
// canonical order, and the closing footer. The callback receives each complete
// NDJSON line, including its trailing newline, exactly once. emit is never
// called concurrently. Cancelling ctx stops promptly between pages and
// between records.
func (exporter *Exporter) Export(
	ctx context.Context,
	authority Authority,
	selection []Collection,
	emit func([]byte) error,
) error {
	if exporter == nil || emit == nil || authority == nil {
		return ErrInvalid
	}
	scope := authority.TenantScope()
	if scope.ID().IsZero() {
		return ErrInvalid
	}
	if err := authority.Require(exportPermission); err != nil {
		return fmt.Errorf("authorize tenant export: %w", err)
	}
	selected, err := exporter.selection(selection)
	if err != nil {
		return err
	}
	stream := &recordStream{emit: emit, hash: sha256.New(), counts: make(map[string]int64, len(selected))}
	for _, collection := range selected {
		stream.counts[string(collection)] = 0
	}
	if err := emitLine(emit, "header", headerRecord{
		Schema:        SchemaName,
		SchemaVersion: SchemaVersion,
		TenantID:      scope.ID().String(),
		ExportedAt:    exporter.now().UTC(),
		Collections:   selected,
	}, stream.hash); err != nil {
		return err
	}
	for _, stage := range exporter.pipeline() {
		if !slices.Contains(selected, stage.collection) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := exporter.runCollection(ctx, scope, stage, stream); err != nil {
			return fmt.Errorf("export %s: %w", stage.collection, err)
		}
	}
	footer := footerRecord{
		Counts: stream.counts,
		Digest: "sha256:" + hex.EncodeToString(stream.hash.Sum(nil)),
	}
	return emitLine(emit, "footer", footer, nil)
}

func (exporter *Exporter) selection(requested []Collection) ([]Collection, error) {
	if len(requested) == 0 {
		return AllCollections(), nil
	}
	if len(requested) > MaximumCollections {
		return nil, ErrInvalid
	}
	seen := make(map[Collection]struct{}, len(requested))
	for _, collection := range requested {
		if !collection.Known() {
			return nil, fmt.Errorf("tenantexport: unknown collection %q", collection)
		}
		if _, duplicate := seen[collection]; duplicate {
			return nil, fmt.Errorf("tenantexport: collection %q is repeated", collection)
		}
		seen[collection] = struct{}{}
	}
	ordered := make([]Collection, 0, len(requested))
	for _, collection := range allCollections {
		if _, wanted := seen[collection]; wanted {
			ordered = append(ordered, collection)
		}
	}
	return ordered, nil
}

type stage struct {
	collection Collection
	source     pageSource
}

func (exporter *Exporter) pipeline() []stage {
	sources := exporter.sources
	return []stage{
		{CollectionTenant, func(ctx context.Context, scope tenant.Scope, _ string, _ int) ([]Record, error) {
			record, err := sources.Tenant.Tenant(ctx, scope)
			if err != nil {
				return nil, err
			}
			return []Record{record}, nil
		}},
		{CollectionCaptureProfiles, sources.CaptureProfiles.CaptureProfiles},
		{CollectionPolicies, sources.Policies.Policies},
		{CollectionPolicyRevisions, sources.PolicyRevisions.PolicyRevisions},
		{CollectionPolicyActivations, sources.PolicyActivations.PolicyActivations},
		{CollectionVerifications, sources.Verifications.Verifications},
		{CollectionVerificationTransitions, sources.VerificationTransitions.VerificationTransitions},
		{CollectionVerificationChecks, sources.VerificationChecks.VerificationChecks},
		{CollectionVerificationAttempts, sources.VerificationAttempts.VerificationAttempts},
		{CollectionDecisions, sources.Decisions.Decisions},
		{CollectionAuditRecords, sources.AuditRecords.AuditRecords},
		{CollectionWebhookEndpoints, sources.WebhookEndpoints.WebhookEndpoints},
		{CollectionReviewCases, sources.ReviewCases.ReviewCases},
		{CollectionReviewFindings, sources.ReviewFindings.ReviewFindings},
		{CollectionIdentitySubjects, sources.IdentitySubjects.IdentitySubjects},
		{CollectionIdentityRecords, sources.IdentityRecords.IdentityRecords},
		{CollectionEvidenceAssets, sources.EvidenceAssets.EvidenceAssets},
		{CollectionFraudConfiguration, sources.FraudConfiguration.FraudConfiguration},
		{CollectionPrivacyDeletions, sources.PrivacyDeletions.PrivacyDeletions},
		{CollectionPrivacyHolds, sources.PrivacyHolds.PrivacyHolds},
	}
}

func (exporter *Exporter) runCollection(
	ctx context.Context,
	scope tenant.Scope,
	stage stage,
	stream *recordStream,
) (int, error) {
	batch := DefaultBatchSize
	if stage.collection == CollectionDecisions {
		batch = DecisionBatchSize
	}
	count := 0
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return count, err
		}
		records, err := stage.source(ctx, scope, after, batch)
		if err != nil {
			return count, err
		}
		if len(records) > batch {
			return count, ErrInvalid
		}
		for _, record := range records {
			if record.Position == "" {
				return count, ErrInvalid
			}
			if err := stream.write(string(stage.collection), record.Fields); err != nil {
				return count, err
			}
			count++
		}
		if len(records) < batch {
			return count, nil
		}
		next := records[len(records)-1].Position
		if next == after {
			return count, ErrInvalid
		}
		after = next
	}
}

type recordStream struct {
	emit   func([]byte) error
	hash   hash.Hash
	counts map[string]int64
}

func (stream *recordStream) write(collection string, fields any) error {
	if err := emitLine(stream.emit, collection, fields, stream.hash); err != nil {
		return err
	}
	stream.counts[collection]++
	return nil
}

// emitLine encodes fields as a JSON object and writes
// {"record":"collection",<fields>}\n. When digest is non-nil the exact emitted
// bytes are added to it, so the closing footer can cover every preceding line.
func emitLine(emit func([]byte) error, collection string, fields any, digest hash.Hash) error {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode export record: %w", err)
	}
	if len(encoded) < 2 || encoded[0] != '{' || encoded[len(encoded)-1] != '}' {
		return ErrInvalid
	}
	line := make([]byte, 0, len(encoded)+len(collection)+16)
	line = append(line, `{"record":"`...)
	line = append(line, collection...)
	line = append(line, '"', ',')
	line = append(line, encoded[1:]...)
	line = append(line, '\n')
	if err := emit(line); err != nil {
		return err
	}
	if digest != nil {
		_, _ = digest.Write(line)
	}
	return nil
}
