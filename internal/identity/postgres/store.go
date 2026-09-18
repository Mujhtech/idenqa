// Package postgres implements tenant-forced identity persistence and key custody.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	auditpg "github.com/Mujhtech/idenqa/internal/audit/postgres"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/identity"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/outbox"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type transactionRunner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// IdentifierGenerator keeps clocks and identifiers explicitly injectable.
type IdentifierGenerator interface {
	New(id.Prefix) (id.Value, error)
	NewEvent() (id.Event, error)
	NewDeletion() (id.Deletion, error)
}

// DeletionStopper uses the verification-owned transition inside the identity transaction.
type DeletionStopper interface {
	CancelForDeletionWithin(context.Context, tenant.Scope, pg.Transaction, id.Verification, id.APIKey, time.Time) error
}

// Store owns immutable records and rebuildable current projections.
type Store struct {
	pool    transactionRunner
	keys    platformcrypto.KeyUnwrapper
	ids     IdentifierGenerator
	stopper DeletionStopper
}

// New composes the identity adapter. Missing KMS fails closed only on protected-data operations.
func New(pool transactionRunner, keys platformcrypto.KeyUnwrapper, ids IdentifierGenerator, stopper DeletionStopper) (*Store, error) {
	if pool == nil || ids == nil {
		return nil, identity.ErrInvalid
	}
	return &Store{pool, keys, ids, stopper}, nil
}

func setScope(ctx context.Context, tx pg.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return identity.ErrInvalid
	}
	var v string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&v)
}

// Execute retries serialization/deadlock failures without changing a committed replay result.
func (s *Store) Execute(ctx context.Context, scope tenant.Scope, c identity.Command) (identity.Result, error) {
	if scope.ID().IsZero() || c.Actor.IsZero() || c.At.IsZero() || c.Region == "" {
		return identity.Result{}, identity.ErrInvalid
	}
	for attempt := 0; attempt < 3; attempt++ {
		var result identity.Result
		e := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			if e := setScope(ctx, tx, scope); e != nil {
				return e
			}
			var e error
			result, e = s.executeWithin(ctx, tx, scope, c)
			return e
		})
		if e == nil {
			return result, nil
		}
		var db *pgconn.PgError
		if errors.As(e, &db) {
			if (db.Code == "40001" || db.Code == "40P01") && ctx.Err() == nil {
				continue
			}
			if db.Code == "23505" {
				return identity.Result{}, identity.ErrConflict
			}
			if db.Code == "23503" {
				return identity.Result{}, identity.ErrNotFound
			}
		}
		return identity.Result{}, fmt.Errorf("execute identity command: %w", e)
	}
	return identity.Result{}, identity.ErrConflict
}

func (s *Store) executeWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command) (identity.Result, error) {
	canonical, e := json.Marshal(struct {
		Operation, Subject, State, Verification, Region string
		Version                                         int64
		External                                        *string
		Record                                          *identity.RecordInput
		Configuration                                   *identity.Configuration
	}{c.Operation, c.SubjectID, c.State, c.VerificationID, c.Region, c.ExpectedVersion, c.ExternalReference, c.Record, c.Configuration})
	if e != nil {
		return identity.Result{}, e
	}
	defer clear(canonical)
	// Fingerprints containing predictable claims are keyed before entering ordinary
	// idempotency storage. Deletes remain possible even when KMS is unavailable.
	if c.ExternalReference != nil || (c.Record != nil && (c.Record.Value != nil || c.Record.Original != nil)) {
		key, e := s.lookupKey(ctx, tx, scope, c.Region, true)
		if e != nil {
			return identity.Result{}, e
		}
		fingerprint, e := token(key, "identity.command.v1", scope.ID().String(), c.Region, string(canonical))
		clear(key)
		if e != nil {
			return identity.Result{}, e
		}
		clear(canonical)
		canonical = []byte(fingerprint)
	}
	retry, e := idempotency.NewRequest(scope.ID(), c.Actor, "identity."+c.Operation, c.Key, canonical, c.At, 24*time.Hour)
	if e != nil {
		return identity.Result{}, e
	}
	queries := sqlgen.New(tx)
	reservation, e := idempg.Reserve(ctx, queries, retry)
	if e != nil {
		return identity.Result{}, e
	}
	var result identity.Result
	if replay, ok := reservation.Result(); ok {
		e := json.Unmarshal(replay.Body(), &result)
		return result, e
	}
	switch c.Operation {
	case "create":
		result, e = s.createSubject(ctx, tx, scope, c)
	case "configure":
		result, e = s.configure(ctx, tx, scope, c)
	default:
		subject, wrapped, _, err := loadSubject(ctx, tx, scope, c.SubjectID, c.Region, true)
		if err != nil {
			return result, err
		}
		if subject.Version != c.ExpectedVersion || subject.State == "deleting" || subject.State == "deleted" {
			return result, identity.ErrConflict
		}
		switch c.Operation {
		case "update":
			result, e = s.updateSubject(ctx, tx, scope, c, subject, wrapped)
		case "link":
			result, e = s.link(ctx, tx, scope, c, subject)
		case "record":
			if subject.State != "active" {
				return result, identity.ErrConflict
			}
			result, e = s.appendRecord(ctx, tx, scope, c, subject, wrapped)
		case "rebuild":
			result, e = s.rebuild(ctx, tx, scope, c, subject)
		case "delete":
			result, e = s.requestDeletion(ctx, tx, scope, c, subject)
		default:
			return result, identity.ErrInvalid
		}
	}
	if e != nil {
		return result, e
	}
	aggregate := "identity.configuration"
	version := result.Version
	if result.Subject != nil {
		aggregate = result.Subject.ID
		version = result.Subject.Version
	}
	eventDigest, e := identity.Digest(result)
	if e != nil {
		return result, e
	}
	if e = s.event(ctx, tx, scope, c.Actor.String(), aggregate, version, "identity."+c.Operation, eventDigest, c.At); e != nil {
		return result, e
	}
	if e = s.catalogueEvent(ctx, tx, scope, c, result); e != nil {
		return result, e
	}
	b, e := json.Marshal(result)
	if e != nil {
		return result, e
	}
	status := 200
	if c.Operation == "create" || c.Operation == "record" {
		status = 201
	}
	if c.Operation == "delete" {
		status = 202
	}
	receipt, e := idempotency.NewResult(status, b)
	if e != nil {
		return result, e
	}
	return result, idempg.Complete(ctx, queries, retry, receipt, c.At)
}

// catalogueEvent publishes the selected subject catalogue transitions only.
func (s *Store) catalogueEvent(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command, result identity.Result) error {
	if result.Subject == nil {
		return nil
	}
	subject := result.Subject
	seed := ""
	eventType := webhookv1.Type("")
	var fields map[string]any
	switch c.Operation {
	case "create":
		eventType = webhookv1.SubjectCreated
		seed = string(eventType) + ":" + subject.ID + ":" + strconv.FormatInt(subject.Version, 10)
		fields = map[string]any{"subject_id": subject.ID, "version": subject.Version, "state": subject.State}
	case "update":
		eventType = webhookv1.SubjectUpdated
		seed = string(eventType) + ":" + subject.ID + ":" + strconv.FormatInt(subject.Version, 10)
		fields = map[string]any{"subject_id": subject.ID, "version": subject.Version, "state": subject.State}
	case "delete":
		if subject.DeletionID == "" {
			return identity.ErrInvalid
		}
		eventType = webhookv1.SubjectDeletionRequested
		seed = string(eventType) + ":" + subject.ID + ":" + subject.DeletionID
		fields = map[string]any{"subject_id": subject.ID, "deletion_id": subject.DeletionID}
	default:
		return nil
	}

	nested := map[string]any{"id": subject.ID, "type": "subject", "state": subject.State, "version": subject.Version}
	if subject.DeletionID != "" {
		nested["deletion_id"] = subject.DeletionID
	}
	fields["subject"] = nested

	return deliverypostgres.EmitCatalogueEvent(ctx, tx, scope.ID().String(), subject.Region, eventType, seed, c.At, fields)
}

func (s *Store) event(ctx context.Context, tx pg.Transaction, scope tenant.Scope, actor, aggregate string, version int64, kind, digest string, at time.Time) error {
	eventID, e := s.ids.NewEvent()
	if e != nil {
		return e
	}
	if _, e = auditpg.AppendInTransaction(ctx, tx, scope, auditpg.Event{EventID: eventID.String(), EventType: kind, AggregateID: aggregate, ActorID: actor, EventDigest: digest, OccurredAt: at}); e != nil {
		return e
	}
	if kind == "identity.reveal" {
		return nil
	}
	intent, e := outbox.NewIntent(eventID, "identity", aggregate, version, kind+".v1", 1, struct {
		Digest string `json:"digest"`
	}{digest}, at)
	if e != nil {
		return e
	}
	return sqlgen.New(tx).InsertOutboxEvent(ctx, sqlgen.InsertOutboxEventParams{
		ID: intent.ID.String(), TenantID: scope.ID().String(), AggregateType: intent.AggregateType, AggregateID: intent.AggregateID,
		AggregateVersion: intent.AggregateVersion, EventType: intent.EventType, SchemaVersion: 1, Payload: intent.Payload, OccurredAt: pgtype.Timestamptz{Time: intent.OccurredAt, Valid: true}, CreatedAt: pgtype.Timestamptz{Time: at, Valid: true},
	})
}

func loadSubject(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subjectID, region string, lock bool) (identity.Subject, []byte, []byte, error) {
	var s identity.Subject
	var wrapped, external []byte
	query := `SELECT id,region,state,version,created_at,updated_at,coalesce(deletion_id,''),erased_at,wrapped_key,external_cipher FROM idenqa.identity_subjects WHERE tenant_id=$1 AND id=$2 AND region=$3`
	if lock {
		query += ` FOR UPDATE`
	}
	e := tx.QueryRow(ctx, query, scope.ID().String(), subjectID, region).Scan(&s.ID, &s.Region, &s.State, &s.Version, &s.CreatedAt, &s.UpdatedAt, &s.DeletionID, &s.ErasedAt, &wrapped, &external)
	if errors.Is(e, pgx.ErrNoRows) {
		e = identity.ErrNotFound
	}
	return s, wrapped, external, e
}
