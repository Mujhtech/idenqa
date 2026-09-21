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
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// SubjectSchemaName identifies the stable subject-export NDJSON envelope.
	SubjectSchemaName = "idenqa.subject-export"
	// SubjectSchemaVersion is the current subject-export envelope version.
	SubjectSchemaVersion = 1
)

// SubjectCollection is one subject-export record family.
type SubjectCollection string

// The complete subject-export collection vocabulary.
const (
	SubjectCollectionSubject                 SubjectCollection = "subject"
	SubjectCollectionIdentityRecords         SubjectCollection = "identity_records"
	SubjectCollectionVerifications           SubjectCollection = "verifications"
	SubjectCollectionVerificationTransitions SubjectCollection = "verification_transitions"
	SubjectCollectionVerificationChecks      SubjectCollection = "verification_checks"
	SubjectCollectionVerificationAttempts    SubjectCollection = "verification_attempts"
	SubjectCollectionEvidenceAssets          SubjectCollection = "evidence_assets"
	SubjectCollectionDecisions               SubjectCollection = "decisions"
	SubjectCollectionReviewCases             SubjectCollection = "review_cases"
)

var allSubjectCollections = []SubjectCollection{
	SubjectCollectionSubject,
	SubjectCollectionIdentityRecords,
	SubjectCollectionVerifications,
	SubjectCollectionVerificationTransitions,
	SubjectCollectionVerificationChecks,
	SubjectCollectionVerificationAttempts,
	SubjectCollectionEvidenceAssets,
	SubjectCollectionDecisions,
	SubjectCollectionReviewCases,
}

// AccessSubjectCollections returns the canonical access-request collection order.
func AccessSubjectCollections() []SubjectCollection { return slices.Clone(allSubjectCollections) }

// PortabilitySubjectCollections returns the structured-profile subset.
func PortabilitySubjectCollections() []SubjectCollection {
	return []SubjectCollection{
		SubjectCollectionSubject,
		SubjectCollectionIdentityRecords,
		SubjectCollectionVerifications,
		SubjectCollectionDecisions,
	}
}

// SubjectSources reads the exact subject-linked collections. Every implementation
// must filter by the supplied subject; the exporter never widens the scope.
type SubjectSources interface {
	IdentitySubject(context.Context, tenant.Scope, string) (Record, error)
	IdentityRecordsForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
	VerificationsForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
	VerificationTransitionsForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
	VerificationChecksForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
	VerificationAttemptsForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
	EvidenceAssetsForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
	DecisionsForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
	ReviewCasesForSubject(context.Context, tenant.Scope, string, string, int) ([]Record, error)
}

// SubjectResult is the bounded result of one subject-export stream.
type SubjectResult struct {
	Digest string
	Bytes  int64
	Counts map[string]int64
}

// SubjectEvidenceRecord is evidence metadata with no object location, checksum,
// or key reference.
type SubjectEvidenceRecord struct {
	ID                string    `json:"id"`
	VerificationID    string    `json:"verification_id"`
	RequirementKey    string    `json:"requirement_key"`
	EvidenceType      string    `json:"evidence_type"`
	Artefact          string    `json:"artefact"`
	AcquisitionMethod string    `json:"acquisition_method"`
	Assurances        []string  `json:"assurances"`
	Region            string    `json:"region"`
	RetentionClass    string    `json:"retention_class"`
	ContentRevision   int32     `json:"content_revision"`
	CiphertextSize    int64     `json:"ciphertext_size"`
	PlaintextDigest   string    `json:"plaintext_digest"`
	MediaType         string    `json:"media_type"`
	Integrity         string    `json:"integrity"`
	State             string    `json:"state"`
	Version           int64     `json:"version"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SubjectExporter streams a canonical subject-scoped NDJSON bundle. Raw
// evidence bytes, object locations, provider payloads, and decrypted identity
// values never appear in the bundle.
type SubjectExporter struct {
	sources SubjectSources
	now     func() time.Time
}

// NewSubjectExporter constructs the subject export boundary.
func NewSubjectExporter(sources SubjectSources, now func() time.Time) (*SubjectExporter, error) {
	if sources == nil {
		return nil, ErrInvalid
	}
	if now == nil {
		now = time.Now
	}
	return &SubjectExporter{sources: sources, now: now}, nil
}

// ErrInvalidSubject means the subject identifier or selection is malformed.
var ErrInvalidSubject = errors.New("tenantexport: invalid subject export request")

// Export streams header, selected collections, and footer. The callback
// receives each complete NDJSON line exactly once.
func (exporter *SubjectExporter) Export(ctx context.Context, scope tenant.Scope, subjectID string, selection []SubjectCollection, emit func([]byte) error) (SubjectResult, error) {
	if exporter == nil || emit == nil || scope.ID().IsZero() {
		return SubjectResult{}, ErrInvalidSubject
	}
	if _, err := id.ParseSubject(subjectID); err != nil {
		return SubjectResult{}, ErrInvalidSubject
	}
	selected, err := subjectSelection(selection)
	if err != nil {
		return SubjectResult{}, err
	}
	stream := &subjectStream{emit: emit, hash: sha256.New(), counts: map[string]int64{}, bytes: 0}
	for _, collection := range selected {
		stream.counts[string(collection)] = 0
	}
	if err := exporter.emitLine(stream, "header", subjectHeader{
		Schema:        SubjectSchemaName,
		SchemaVersion: SubjectSchemaVersion,
		SubjectID:     subjectID,
		ExportedAt:    exporter.now().UTC(),
		Collections:   selected,
	}, stream.hash); err != nil {
		return SubjectResult{}, err
	}
	for _, stage := range exporter.pipeline() {
		if !slices.Contains(selected, stage.collection) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return SubjectResult{}, err
		}
		if err := exporter.runCollection(ctx, scope, subjectID, stage, stream); err != nil {
			return SubjectResult{}, fmt.Errorf("export subject %s: %w", stage.collection, err)
		}
	}
	footer := subjectFooter{Counts: stream.counts, Digest: "sha256:" + hex.EncodeToString(stream.hash.Sum(nil))}
	if err := exporter.emitLine(stream, "footer", footer, nil); err != nil {
		return SubjectResult{}, err
	}
	return SubjectResult{Digest: footer.Digest, Bytes: stream.bytes, Counts: stream.counts}, nil
}

type subjectHeader struct {
	Schema        string              `json:"schema"`
	SchemaVersion int                 `json:"schema_version"`
	SubjectID     string              `json:"subject_id"`
	ExportedAt    time.Time           `json:"exported_at"`
	Collections   []SubjectCollection `json:"collections"`
}

type subjectFooter struct {
	Counts map[string]int64 `json:"counts"`
	Digest string           `json:"digest"`
}

type subjectStage struct {
	collection SubjectCollection
	source     func(context.Context, tenant.Scope, string, string, int) ([]Record, error)
}

func (exporter *SubjectExporter) pipeline() []subjectStage {
	sources := exporter.sources
	return []subjectStage{
		{SubjectCollectionSubject, func(ctx context.Context, scope tenant.Scope, subjectID, _ string, _ int) ([]Record, error) {
			record, err := sources.IdentitySubject(ctx, scope, subjectID)
			if err != nil {
				return nil, err
			}
			return []Record{record}, nil
		}},
		{SubjectCollectionIdentityRecords, sources.IdentityRecordsForSubject},
		{SubjectCollectionVerifications, sources.VerificationsForSubject},
		{SubjectCollectionVerificationTransitions, sources.VerificationTransitionsForSubject},
		{SubjectCollectionVerificationChecks, sources.VerificationChecksForSubject},
		{SubjectCollectionVerificationAttempts, sources.VerificationAttemptsForSubject},
		{SubjectCollectionEvidenceAssets, sources.EvidenceAssetsForSubject},
		{SubjectCollectionDecisions, sources.DecisionsForSubject},
		{SubjectCollectionReviewCases, sources.ReviewCasesForSubject},
	}
}

func (exporter *SubjectExporter) runCollection(ctx context.Context, scope tenant.Scope, subjectID string, stage subjectStage, stream *subjectStream) error {
	batch := DefaultBatchSize
	if stage.collection == SubjectCollectionDecisions {
		batch = DecisionBatchSize
	}
	after := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		records, err := stage.source(ctx, scope, subjectID, after, batch)
		if err != nil {
			return err
		}
		if len(records) > batch {
			return ErrInvalid
		}
		for _, record := range records {
			if record.Position == "" {
				return ErrInvalid
			}
			if err := exporter.emitLine(stream, string(stage.collection), record.Fields, stream.hash); err != nil {
				return err
			}
			stream.counts[string(stage.collection)]++
		}
		if len(records) < batch {
			return nil
		}
		next := records[len(records)-1].Position
		if next == after {
			return ErrInvalid
		}
		after = next
	}
}

func (exporter *SubjectExporter) emitLine(stream *subjectStream, collection string, fields any, digest hash.Hash) error {
	encoded, err := json.Marshal(fields)
	if err != nil {
		return fmt.Errorf("encode subject export record: %w", err)
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
	if err := stream.emit(line); err != nil {
		return err
	}
	stream.bytes += int64(len(line))
	if digest != nil {
		_, _ = digest.Write(line)
	}
	return nil
}

type subjectStream struct {
	emit   func([]byte) error
	hash   hash.Hash
	counts map[string]int64
	bytes  int64
}

func subjectSelection(requested []SubjectCollection) ([]SubjectCollection, error) {
	if len(requested) == 0 {
		return AccessSubjectCollections(), nil
	}
	seen := make(map[SubjectCollection]struct{}, len(requested))
	for _, collection := range requested {
		if !slices.Contains(allSubjectCollections, collection) {
			return nil, fmt.Errorf("tenantexport: unknown subject collection %q", collection)
		}
		if _, duplicate := seen[collection]; duplicate {
			return nil, fmt.Errorf("tenantexport: subject collection %q is repeated", collection)
		}
		seen[collection] = struct{}{}
	}
	ordered := make([]SubjectCollection, 0, len(requested))
	for _, collection := range allSubjectCollections {
		if _, wanted := seen[collection]; wanted {
			ordered = append(ordered, collection)
		}
	}
	return ordered, nil
}
