// Package postgres adapts portable-experience lifecycle ports to
// tenant-scoped PostgreSQL with forced row-level security, immutable revisions,
// append-only history, and atomic audit.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store persists experience aggregates, immutable signed revisions, targeting
// projections, append-only lifecycle history, and session pins.
type Store struct {
	pool transactionRunner
}

// New constructs the experience PostgreSQL adapter.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("experience postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

// Create inserts one draft aggregate, its signed revision, targeting
// projection, lifecycle event, and audit record atomically.
func (store *Store) Create(ctx context.Context, scope tenant.Scope, mutation experience.CreateMutation) (experience.Experience, error) {
	var result experience.Experience
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.experiences
			(tenant_id,id,state,revision,latest_version,approved_version,published_version,created_at,updated_at)
			VALUES ($1,$2,'draft',1,$3,0,0,$4,$4)`,
			scope.ID().String(), mutation.Document.ExperienceID, mutation.Document.Version, mutation.At); err != nil {
			return fmt.Errorf("insert experience: %w", err)
		}
		if err := insertRevision(ctx, tx, scope, mutation.Document.ExperienceID, experience.RevisionDraft, mutation); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, scope, mutation.Document.ExperienceID, eventRecord{
			Operation: string(experience.OperationCreate), ToState: experience.StateDraft,
			Version: mutation.Document.Version, TargetVersion: mutation.Document.Version,
			Actor: mutation.Actor, Digest: mutation.Manifest.Digest, At: mutation.At,
		}); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, scope, mutation.EventID, "experience.created", mutation.Document.ExperienceID, mutation.Actor, mutation.Manifest.Digest, mutation.At); err != nil {
			return err
		}
		identifier, err := id.ParseExperience(mutation.Document.ExperienceID)
		if err != nil {
			return err
		}
		value, err := loadExperience(ctx, tx, scope, identifier)
		result = value
		return err
	})
	return result, err
}

// Find loads one tenant-scoped aggregate with its latest revision document.
func (store *Store) Find(ctx context.Context, scope tenant.Scope, identifier id.Experience) (experience.Experience, error) {
	var result experience.Experience
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, err := loadExperience(ctx, tx, scope, identifier)
		result = value
		return err
	})
	return result, err
}

// List returns one bounded ascending page ordered by identifier.
func (store *Store) List(ctx context.Context, scope tenant.Scope, position *experience.Position, limit int) (experience.Page, error) {
	page := experience.Page{Experiences: make([]experience.Experience, 0, limit)}
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		after := ""
		if position != nil {
			after = position.Before.String()
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.experiences
			WHERE tenant_id=$1 AND ($2='' OR id > $2) ORDER BY id LIMIT $3`, scope.ID().String(), after, limit+1)
		if err != nil {
			return fmt.Errorf("list experiences: %w", err)
		}
		identifiers := make([]string, 0, limit+1)
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				rows.Close()
				return fmt.Errorf("scan experience id: %w", err)
			}
			identifiers = append(identifiers, encoded)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		hasMore := len(identifiers) > limit
		if hasMore {
			identifiers = identifiers[:limit]
		}
		for _, encoded := range identifiers {
			identifier, err := id.ParseExperience(encoded)
			if err != nil {
				return experience.ErrInvalid
			}
			value, err := loadExperience(ctx, tx, scope, identifier)
			if err != nil {
				return err
			}
			page.Experiences = append(page.Experiences, value)
		}
		if hasMore && len(page.Experiences) > 0 {
			page.Next = &experience.Position{Before: page.Experiences[len(page.Experiences)-1].ID}
		}
		return nil
	})
	return page, err
}

// ApplyRevision saves one new immutable draft revision.
func (store *Store) ApplyRevision(ctx context.Context, scope tenant.Scope, mutation experience.RevisionMutation) (experience.Experience, error) {
	var result experience.Experience
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		current, err := lockExperience(ctx, tx, scope, mutation.ExperienceID)
		if err != nil {
			return err
		}
		if current.Revision != mutation.ExpectedRevision {
			return experience.ErrConflict
		}
		plan, applied, err := experience.PlanTransition(current, experience.OperationUpdate, mutation.Document.Version)
		if err != nil {
			return err
		}
		if !applied {
			result = current
			return nil
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.experiences
			SET state=$3, revision=revision+1, latest_version=$4, approved_version=$5, updated_at=$6
			WHERE tenant_id=$1 AND id=$2`,
			scope.ID().String(), mutation.ExperienceID.String(), string(plan.State), plan.LatestVersion, plan.ApprovedVersion, mutation.At); err != nil {
			return fmt.Errorf("update experience draft: %w", err)
		}
		if err := insertRevision(ctx, tx, scope, mutation.ExperienceID.String(), experience.RevisionDraft, experience.CreateMutation{
			Manifest: mutation.Manifest, Document: mutation.Document, Actor: mutation.Actor, EventID: mutation.EventID, At: mutation.At,
		}); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, scope, mutation.ExperienceID.String(), eventRecord{
			Operation: string(experience.OperationUpdate), FromState: current.State, ToState: plan.State,
			Version: plan.LatestVersion, TargetVersion: plan.TargetVersion, Actor: mutation.Actor, Reason: mutation.Reason,
			Digest: mutation.Manifest.Digest, At: mutation.At,
		}); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, scope, mutation.EventID, "experience.updated", mutation.ExperienceID.String(), mutation.Actor, mutation.Manifest.Digest, mutation.At); err != nil {
			return err
		}
		value, err := loadExperience(ctx, tx, scope, mutation.ExperienceID)
		result = value
		return err
	})
	return result, err
}

// ApplyTransition applies one validated lifecycle change with its history and
// audit record. The domain plan is recomputed under the row lock.
func (store *Store) ApplyTransition(ctx context.Context, scope tenant.Scope, mutation experience.TransitionMutation) (experience.Experience, error) {
	var result experience.Experience
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		current, err := lockExperience(ctx, tx, scope, mutation.ExperienceID)
		if err != nil {
			return err
		}
		if current.Revision != mutation.ExpectedRevision {
			return experience.ErrConflict
		}
		plan, applied, err := experience.PlanTransition(current, mutation.Operation, mutation.TargetVersion)
		if err != nil {
			return err
		}
		if !applied {
			result = current
			return nil
		}
		if plan.SupersededVersion != 0 {
			if _, err := tx.Exec(ctx, `UPDATE idenqa.experience_revisions SET state=$4
				WHERE tenant_id=$1 AND experience_id=$2 AND version=$3`,
				scope.ID().String(), mutation.ExperienceID.String(), versionInt32(plan.SupersededVersion), string(plan.SupersededRevision)); err != nil {
				return fmt.Errorf("supersede experience revision: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.experience_revisions SET state=$4
			WHERE tenant_id=$1 AND experience_id=$2 AND version=$3`,
			scope.ID().String(), mutation.ExperienceID.String(), versionInt32(plan.TargetVersion), string(plan.TargetRevision)); err != nil {
			return fmt.Errorf("advance experience revision state: %w", err)
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.experiences
			SET state=$3, revision=revision+1, approved_version=$4, published_version=$5, updated_at=$6
			WHERE tenant_id=$1 AND id=$2`,
			scope.ID().String(), mutation.ExperienceID.String(), string(plan.State), versionInt32(plan.ApprovedVersion), versionInt32(plan.PublishedVersion), mutation.At); err != nil {
			return fmt.Errorf("apply experience transition: %w", err)
		}
		digest := mutation.Digest
		if digest == "" {
			return experience.ErrInvalid
		}
		if err := insertEvent(ctx, tx, scope, mutation.ExperienceID.String(), eventRecord{
			Operation: string(mutation.Operation), FromState: current.State, ToState: plan.State,
			Version: plan.LatestVersion, TargetVersion: plan.TargetVersion, Actor: mutation.Actor, Reason: mutation.Reason,
			Digest: digest, At: mutation.At,
		}); err != nil {
			return err
		}
		if err := appendAudit(ctx, tx, scope, mutation.EventID, auditEventType(mutation.Operation), mutation.ExperienceID.String(), mutation.Actor, digest, mutation.At); err != nil {
			return err
		}
		value, err := loadExperience(ctx, tx, scope, mutation.ExperienceID)
		result = value
		return err
	})
	return result, err
}

// LoadPublished returns every live published revision for resolution.
func (store *Store) LoadPublished(ctx context.Context, scope tenant.Scope) ([]experience.Published, error) {
	result := make([]experience.Published, 0)
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT experiences.id, revisions.version, revisions.document,
			revisions.digest, revisions.key_id, revisions.signature, revisions.created_at
			FROM idenqa.experiences
			JOIN idenqa.experience_revisions revisions
			  ON revisions.tenant_id=experiences.tenant_id AND revisions.experience_id=experiences.id
			 AND revisions.version=experiences.published_version
			WHERE experiences.tenant_id=$1 AND experiences.state='published' AND experiences.published_version > 0
			ORDER BY experiences.id`, scope.ID().String())
		if err != nil {
			return fmt.Errorf("load published experiences: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			var document []byte
			var version int32
			var published experience.Published
			if err := rows.Scan(&encoded, &version, &document, &published.Manifest.Digest, &published.Manifest.KeyID, &published.Manifest.Signature, &published.PublishedAt); err != nil {
				return fmt.Errorf("scan published experience: %w", err)
			}
			identifier, err := id.ParseExperience(encoded)
			if err != nil || version <= 0 {
				return experience.ErrInvalid
			}
			parsed, err := contract.ParseDocument(document)
			if err != nil {
				return fmt.Errorf("parse published experience document: %w", err)
			}
			published.ExperienceID = identifier
			published.Version = uint32(version)
			published.Manifest.Document = parsed
			published.Manifest.Algorithm = contract.SignatureAlgorithm
			result = append(result, published)
		}
		return rows.Err()
	})
	return result, err
}

// Revision loads one immutable revision.
func (store *Store) Revision(ctx context.Context, scope tenant.Scope, identifier id.Experience, version uint32) (experience.Revision, error) {
	var result experience.Revision
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, err := loadRevision(ctx, tx, scope, identifier, version)
		result = value
		return err
	})
	return result, err
}

// Changes loads the append-only lifecycle history.
func (store *Store) Changes(ctx context.Context, scope tenant.Scope, identifier id.Experience) ([]experience.Change, error) {
	result := make([]experience.Change, 0)
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT sequence, operation, COALESCE(from_state,''), to_state, version, target_version,
			actor_id, COALESCE(reason,''), digest, occurred_at
			FROM idenqa.experience_events WHERE tenant_id=$1 AND experience_id=$2 ORDER BY sequence`,
			scope.ID().String(), identifier.String())
		if err != nil {
			return fmt.Errorf("load experience history: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var sequence int64
			var change experience.Change
			if err := rows.Scan(&sequence, &change.Operation, &change.FromState, &change.ToState, &change.Version,
				&change.TargetVersion, &change.Actor, &change.Reason, &change.Digest, &change.OccurredAt); err != nil {
				return fmt.Errorf("scan experience history: %w", err)
			}
			change.Sequence = uint64(sequence) //nolint:gosec // sequence is positive by constraint.
			result = append(result, change)
		}
		return rows.Err()
	})
	return result, err
}

// SavePin stores the first pin for a session and is idempotent on resume.
func (store *Store) SavePin(ctx context.Context, scope tenant.Scope, pin experience.Pin) (experience.Pin, error) {
	var result experience.Pin
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.experience_session_pins
			(tenant_id,verification_id,experience_id,version,locale,tenant_copy_version,mandatory_copy_version,source,digest,key_id,pinned_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT (tenant_id,verification_id) DO NOTHING`,
			scope.ID().String(), pin.VerificationID.String(), pin.ExperienceID.String(), versionInt32(pin.Version),
			pin.Locale, pin.TenantCopyVersion, pin.MandatoryCopyVersion, pin.Source, pin.Digest, pin.KeyID, pin.PinnedAt); err != nil {
			return fmt.Errorf("insert session pin: %w", err)
		}
		value, err := loadPin(ctx, tx, scope, pin.VerificationID)
		result = value
		return err
	})
	return result, err
}

// FindPin loads one immutable session pin.
func (store *Store) FindPin(ctx context.Context, scope tenant.Scope, verificationID id.Verification) (experience.Pin, error) {
	var result experience.Pin
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, err := loadPin(ctx, tx, scope, verificationID)
		result = value
		return err
	})
	return result, err
}

type eventRecord struct {
	Operation     string
	FromState     experience.State
	ToState       experience.State
	Version       uint32
	TargetVersion uint32
	Actor         string
	Reason        string
	Digest        string
	At            time.Time
}

func loadExperience(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.Experience) (experience.Experience, error) {
	result := experience.Experience{ID: identifier, TenantID: scope.ID()}
	var state string
	err := tx.QueryRow(ctx, `SELECT state, revision, latest_version, approved_version, published_version, created_at, updated_at
		FROM idenqa.experiences WHERE tenant_id=$1 AND id=$2`,
		scope.ID().String(), identifier.String()).Scan(
		&state, &result.Revision, &result.LatestVersion, &result.ApprovedVersion, &result.PublishedVersion,
		&result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return experience.Experience{}, experience.ErrNotFound
	}
	if err != nil {
		return experience.Experience{}, fmt.Errorf("load experience: %w", err)
	}
	result.State = experience.State(state)
	result.Document = contract.Document{}
	if result.LatestVersion > 0 {
		revision, err := loadRevision(ctx, tx, scope, identifier, result.LatestVersion)
		if err != nil {
			return experience.Experience{}, err
		}
		result.Document = revision.Manifest.Document
	}
	return result, nil
}

func lockExperience(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.Experience) (experience.Experience, error) {
	var state string
	result := experience.Experience{ID: identifier, TenantID: scope.ID()}
	err := tx.QueryRow(ctx, `SELECT state, revision, latest_version, approved_version, published_version, created_at, updated_at
		FROM idenqa.experiences WHERE tenant_id=$1 AND id=$2 FOR UPDATE`,
		scope.ID().String(), identifier.String()).Scan(
		&state, &result.Revision, &result.LatestVersion, &result.ApprovedVersion, &result.PublishedVersion,
		&result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return experience.Experience{}, experience.ErrNotFound
	}
	if err != nil {
		return experience.Experience{}, fmt.Errorf("lock experience: %w", err)
	}
	result.State = experience.State(state)
	revision, err := loadRevision(ctx, tx, scope, identifier, result.LatestVersion)
	if err != nil {
		return experience.Experience{}, err
	}
	result.Document = revision.Manifest.Document
	return result, nil
}

func loadRevision(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.Experience, version uint32) (experience.Revision, error) {
	var state string
	var document []byte
	result := experience.Revision{ExperienceID: identifier, Version: version}
	err := tx.QueryRow(ctx, `SELECT state, document, digest, key_id, signature, created_at
		FROM idenqa.experience_revisions WHERE tenant_id=$1 AND experience_id=$2 AND version=$3`,
		scope.ID().String(), identifier.String(), versionInt32(version)).Scan(
		&state, &document, &result.Manifest.Digest, &result.Manifest.KeyID, &result.Manifest.Signature, &result.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return experience.Revision{}, experience.ErrNotFound
	}
	if err != nil {
		return experience.Revision{}, fmt.Errorf("load experience revision: %w", err)
	}
	parsed, err := contract.ParseDocument(document)
	if err != nil {
		return experience.Revision{}, fmt.Errorf("parse experience revision: %w", err)
	}
	if parsed.Version != version {
		return experience.Revision{}, experience.ErrInvalid
	}
	result.State = experience.RevisionState(state)
	result.Manifest.Document = parsed
	result.Manifest.Algorithm = contract.SignatureAlgorithm
	return result, nil
}

func insertRevision(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, experienceID string, state experience.RevisionState, mutation experience.CreateMutation) error {
	document, err := json.Marshal(mutation.Document)
	if err != nil {
		return fmt.Errorf("encode experience document: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.experience_revisions
		(tenant_id,experience_id,version,state,document,digest,key_id,signature,actor_id,created_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
		scope.ID().String(), experienceID, versionInt32(mutation.Document.Version), string(state), string(document),
		mutation.Manifest.Digest, mutation.Manifest.KeyID, mutation.Manifest.Signature, mutation.Actor, mutation.At); err != nil {
		return fmt.Errorf("insert experience revision: %w", err)
	}
	for index, rule := range mutation.Document.Targeting {
		countries, _ := json.Marshal(jsonArray(rule.Countries))
		applications, _ := json.Marshal(jsonArray(rule.ApplicationIDs))
		origins, _ := json.Marshal(jsonArray(rule.Origins))
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.experience_targeting
			(tenant_id,experience_id,version,rule_index,workflow,countries,application_ids,origins,sdk_version_min,sdk_version_max,specificity)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`,
			scope.ID().String(), experienceID, versionInt32(mutation.Document.Version), index, nullableText(rule.Workflow),
			string(countries), string(applications), string(origins), nullableText(rule.SDKVersionMin), nullableText(rule.SDKVersionMax),
			targetSpecificity(rule)); err != nil {
			return fmt.Errorf("insert experience targeting: %w", err)
		}
	}
	return nil
}

func insertEvent(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, experienceID string, record eventRecord) error {
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM idenqa.experience_events WHERE tenant_id=$1 AND experience_id=$2`,
		scope.ID().String(), experienceID).Scan(&sequence); err != nil {
		return fmt.Errorf("allocate experience event sequence: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.experience_events
		(tenant_id,experience_id,sequence,operation,from_state,to_state,version,target_version,actor_id,reason,digest,occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		scope.ID().String(), experienceID, sequence, record.Operation, nullableState(record.FromState), string(record.ToState),
		versionInt32(record.Version), versionInt32(record.TargetVersion), record.Actor, nullableText(record.Reason), record.Digest, record.At); err != nil {
		return fmt.Errorf("insert experience event: %w", err)
	}
	return nil
}

func appendAudit(
	ctx context.Context,
	tx platformpostgres.Transaction,
	scope tenant.Scope,
	eventID id.Event,
	eventType string,
	aggregateID string,
	actor string,
	digest string,
	at time.Time,
) error {
	if eventID.IsZero() {
		return experience.ErrInvalid
	}
	_, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
		EventID: eventID.String(), EventType: eventType, AggregateID: aggregateID, ActorID: actor,
		EventDigest: digest, OccurredAt: at,
	})
	if err != nil {
		return fmt.Errorf("append experience audit: %w", err)
	}
	return nil
}

func auditEventType(operation experience.TransitionOperation) string {
	switch operation {
	case experience.OperationApprove:
		return "experience.approved"
	case experience.OperationPublish:
		return "experience.published"
	case experience.OperationRevoke:
		return "experience.revoked"
	case experience.OperationRollback:
		return "experience.rolled_back"
	default:
		return "experience.updated"
	}
}

func loadPin(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification) (experience.Pin, error) {
	result := experience.Pin{TenantID: scope.ID(), VerificationID: verificationID}
	var encoded string
	var version int32
	err := tx.QueryRow(ctx, `SELECT experience_id, version, locale, tenant_copy_version, mandatory_copy_version, source, digest, key_id, pinned_at
		FROM idenqa.experience_session_pins WHERE tenant_id=$1 AND verification_id=$2`,
		scope.ID().String(), verificationID.String()).Scan(
		&encoded, &version, &result.Locale, &result.TenantCopyVersion, &result.MandatoryCopyVersion,
		&result.Source, &result.Digest, &result.KeyID, &result.PinnedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return experience.Pin{}, experience.ErrNotFound
	}
	if err != nil {
		return experience.Pin{}, fmt.Errorf("load session pin: %w", err)
	}
	identifier, err := id.ParseExperience(encoded)
	if err != nil || version <= 0 {
		return experience.Pin{}, experience.ErrInvalid
	}
	result.ExperienceID = identifier
	result.Version = uint32(version)
	return result, nil
}

func setScope(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return experience.ErrInvalid
	}
	var value string
	if err := tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value); err != nil {
		return fmt.Errorf("set experience tenant scope: %w", err)
	}
	return nil
}

func jsonArray(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// versionInt32 converts one contract-validated bounded version for storage.
func versionInt32(value uint32) int32 {
	return int32(value) //nolint:gosec // Experience versions are bounded by contract.MaxVersion.
}

func targetSpecificity(rule contract.Target) int32 {
	score := 0
	if rule.Workflow != "" {
		score++
	}
	if len(rule.Countries) > 0 {
		score++
	}
	if len(rule.ApplicationIDs) > 0 {
		score++
	}
	if len(rule.Origins) > 0 {
		score++
	}
	if rule.SDKVersionMin != "" {
		score++
	}
	return versionInt32(uint32(score))
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableState(value experience.State) any {
	if value == "" {
		return nil
	}
	return string(value)
}

var (
	_ experience.Repository    = (*Store)(nil)
	_ experience.PinRepository = (*Store)(nil)
)
