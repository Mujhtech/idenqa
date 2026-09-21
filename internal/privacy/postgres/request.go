package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// CreatePrivacyRequest persists a new request, its audit event, and optional
// creation events atomically.
func (store *Store) CreatePrivacyRequest(ctx context.Context, scope tenant.Scope, actor privacy.Actor, request privacy.Request) error {
	if request.Validate() != nil || actor.ID == "" {
		return privacy.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if err := insertPrivacyRequest(ctx, tx, scope, request); err != nil {
			return err
		}
		if err := appendPrivacyEvents(ctx, tx, scope, request); err != nil {
			return err
		}
		if err := appendPrivacyDecisions(ctx, tx, scope, request); err != nil {
			return err
		}
		return appendRequestAudit(ctx, tx, scope, actor, request, "privacy.request.requested")
	})
}

// FindPrivacyRequest loads one tenant-scoped request with its immutable
// decisions and append-only events.
func (store *Store) FindPrivacyRequest(ctx context.Context, scope tenant.Scope, identifier id.PrivacyRequest) (privacy.Request, error) {
	var result privacy.Request
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, err := findPrivacyRequest(ctx, tx, scope, identifier)
		if err != nil {
			return err
		}
		result = value
		return nil
	})
	return result, err
}

func findPrivacyRequest(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.PrivacyRequest) (privacy.Request, error) {
	var result privacy.Request
	var requestType, state, channel, region string
	var subjectID, verificationID, reasonCode, failureClass, effectKind, effectRef, effectDigest *string
	var payload []byte
	err := tx.QueryRow(ctx, `SELECT type,state,channel,subject_id,verification_id,region,payload,reason_code,failure_class,
			effect_kind,effect_reference,effect_digest,expires_at,requested_at,updated_at,version
		FROM idenqa.privacy_requests WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()).Scan(
		&requestType, &state, &channel, &subjectID, &verificationID, &region, &payload, &reasonCode, &failureClass,
		&effectKind, &effectRef, &effectDigest, &result.ExpiresAt, &result.RequestedAt, &result.UpdatedAt, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return privacy.Request{}, privacy.ErrInvalid
	}
	if err != nil {
		return privacy.Request{}, fmt.Errorf("find privacy request: %w", err)
	}
	result.ID = identifier
	result.Type, result.State, result.Channel, result.Region = privacy.RequestType(requestType), privacy.RequestState(state), privacy.Channel(channel), region
	result.SubjectID, result.Payload = dereference(subjectID), append([]byte(nil), payload...)
	if verificationID != nil {
		result.VerificationID = *verificationID
	}
	if reasonCode != nil {
		result.ReasonCode = privacy.ReasonCode(*reasonCode)
	}
	if failureClass != nil {
		result.FailureClass = *failureClass
	}
	if effectKind != nil {
		result.EffectKind = *effectKind
	}
	if effectRef != nil {
		result.EffectRef = *effectRef
	}
	if effectDigest != nil {
		result.EffectDigest = *effectDigest
	}
	result.ExpiresAt, result.RequestedAt, result.UpdatedAt = result.ExpiresAt.UTC(), result.RequestedAt.UTC(), result.UpdatedAt.UTC()
	decisions, err := findPrivacyDecisions(ctx, tx, scope, identifier)
	if err != nil {
		return privacy.Request{}, err
	}
	result.Decisions = decisions
	events, err := findPrivacyEvents(ctx, tx, scope, identifier)
	if err != nil {
		return privacy.Request{}, err
	}
	result.Events = events
	if err := result.Validate(); err != nil {
		return privacy.Request{}, err
	}
	return result, nil
}

func insertPrivacyRequest(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, request privacy.Request) error {
	_, err := tx.Exec(ctx, `INSERT INTO idenqa.privacy_requests
		(tenant_id,id,type,state,channel,subject_id,verification_id,region,payload,reason_code,failure_class,
		 effect_kind,effect_reference,effect_digest,expires_at,requested_at,updated_at,version)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		scope.ID().String(), request.ID.String(), string(request.Type), string(request.State), string(request.Channel),
		nullableText(request.SubjectID), nullableText(request.VerificationID), request.Region, request.Payload,
		nullableText(string(request.ReasonCode)), nullableText(request.FailureClass), nullableText(request.EffectKind),
		nullableText(request.EffectRef), nullableText(request.EffectDigest), request.ExpiresAt, request.RequestedAt,
		request.UpdatedAt, request.Version)
	if err != nil {
		return fmt.Errorf("insert privacy request: %w", err)
	}
	return nil
}

func appendPrivacyEvents(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, request privacy.Request) error {
	for _, event := range request.Events {
		digest := eventDigest(request.ID, event)
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.privacy_request_events
			(tenant_id,request_id,sequence,event_type,from_state,to_state,reason_code,actor_digest,detail,digest,occurred_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (tenant_id,request_id,sequence) DO NOTHING`,
			scope.ID().String(), request.ID.String(), event.Sequence, event.Type, string(event.From), string(event.To),
			nullableText(string(event.ReasonCode)), nullableText(event.ActorDigest), nullableText(event.Detail), digest, event.OccurredAt); err != nil {
			return fmt.Errorf("append privacy request event: %w", err)
		}
	}
	return nil
}

func appendPrivacyDecisions(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, request privacy.Request) error {
	for _, decision := range request.Decisions {
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.privacy_request_decisions
			(tenant_id,request_id,id,outcome,reason_code,actor_id,decided_at,version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (tenant_id,request_id,id) DO NOTHING`,
			scope.ID().String(), request.ID.String(), decision.ID.String(), string(decision.Outcome),
			string(decision.ReasonCode), decision.Actor, decision.DecidedAt, decision.Version); err != nil {
			return fmt.Errorf("append privacy request decision: %w", err)
		}
	}
	return nil
}

func findPrivacyEvents(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.PrivacyRequest) ([]privacy.Event, error) {
	rows, err := tx.Query(ctx, `SELECT sequence,event_type,from_state,to_state,reason_code,actor_digest,detail,digest,occurred_at
		FROM idenqa.privacy_request_events WHERE tenant_id=$1 AND request_id=$2 ORDER BY sequence`, scope.ID().String(), identifier.String())
	if err != nil {
		return nil, fmt.Errorf("find privacy request events: %w", err)
	}
	defer rows.Close()
	result := []privacy.Event{}
	for rows.Next() {
		var event privacy.Event
		var reasonCode, actorDigest, detail *string
		if err := rows.Scan(&event.Sequence, &event.Type, (*string)(&event.From), (*string)(&event.To), &reasonCode, &actorDigest, &detail, &event.Digest, &event.OccurredAt); err != nil {
			return nil, fmt.Errorf("scan privacy request event: %w", err)
		}
		if reasonCode != nil {
			event.ReasonCode = privacy.ReasonCode(*reasonCode)
		}
		if actorDigest != nil {
			event.ActorDigest = *actorDigest
		}
		if detail != nil {
			event.Detail = *detail
		}
		event.OccurredAt = event.OccurredAt.UTC()
		result = append(result, event)
	}
	return result, rows.Err()
}

func findPrivacyDecisions(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.PrivacyRequest) ([]privacy.Decision, error) {
	rows, err := tx.Query(ctx, `SELECT id,outcome,reason_code,actor_id,decided_at,version
		FROM idenqa.privacy_request_decisions WHERE tenant_id=$1 AND request_id=$2 ORDER BY version,id`, scope.ID().String(), identifier.String())
	if err != nil {
		return nil, fmt.Errorf("find privacy request decisions: %w", err)
	}
	defer rows.Close()
	result := []privacy.Decision{}
	for rows.Next() {
		var value privacy.Decision
		var encoded, outcome, reason string
		if err := rows.Scan(&encoded, &outcome, &reason, &value.Actor, &value.DecidedAt, &value.Version); err != nil {
			return nil, fmt.Errorf("scan privacy request decision: %w", err)
		}
		identifier, err := id.ParsePrivacyDecision(encoded)
		if err != nil {
			return nil, privacy.ErrInvalid
		}
		value.ID, value.Outcome, value.ReasonCode = identifier, privacy.DecisionOutcome(outcome), privacy.ReasonCode(reason)
		value.DecidedAt = value.DecidedAt.UTC()
		result = append(result, value)
	}
	return result, rows.Err()
}

// SavePrivacyRequest updates one optimistic transition with its audit record.
func (store *Store) SavePrivacyRequest(ctx context.Context, scope tenant.Scope, actor privacy.Actor, request privacy.Request, expectedVersion int64) error {
	if request.Validate() != nil || expectedVersion < 1 || request.Version != expectedVersion+1 {
		return privacy.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.privacy_requests SET state=$3,reason_code=$4,failure_class=$5,
			effect_kind=$6,effect_reference=$7,effect_digest=$8,updated_at=$9,version=$10
			WHERE tenant_id=$1 AND id=$2 AND version=$11`, scope.ID().String(), request.ID.String(), string(request.State),
			nullableText(string(request.ReasonCode)), nullableText(request.FailureClass), nullableText(request.EffectKind),
			nullableText(request.EffectRef), nullableText(request.EffectDigest), request.UpdatedAt, request.Version, expectedVersion)
		if err != nil {
			return fmt.Errorf("update privacy request: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return privacy.ErrConflict
		}
		if err := appendPrivacyEvents(ctx, tx, scope, request); err != nil {
			return err
		}
		if err := appendPrivacyDecisions(ctx, tx, scope, request); err != nil {
			return err
		}
		return appendRequestAudit(ctx, tx, scope, actor, request, latestEventType(request))
	})
}

// ListPrivacyRequests returns one bounded ascending page.
func (store *Store) ListPrivacyRequests(ctx context.Context, scope tenant.Scope, filter privacy.RequestFilter, position string, limit int) ([]privacy.Request, error) {
	if limit < 1 || limit > 101 || (position != "" && len(position) > 64) {
		return nil, privacy.ErrInvalid
	}
	var identifiers []id.PrivacyRequest
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.privacy_requests
			WHERE tenant_id=$1 AND ($2='' OR state=$2) AND ($3='' OR type=$3) AND ($4='' OR subject_id=$4) AND ($5='' OR id>$5)
			ORDER BY id LIMIT $6`, scope.ID().String(), string(filter.State), string(filter.Type), filter.SubjectID, position, limit)
		if err != nil {
			return fmt.Errorf("list privacy requests: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return fmt.Errorf("scan privacy request: %w", err)
			}
			identifier, err := id.ParsePrivacyRequest(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			identifiers = append(identifiers, identifier)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := make([]privacy.Request, 0, len(identifiers))
	for _, identifier := range identifiers {
		value, err := store.FindPrivacyRequest(ctx, scope, identifier)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// DuePrivacyRequests lists a bounded deterministic expiry batch.
func (store *Store) DuePrivacyRequests(ctx context.Context, scope tenant.Scope, now time.Time, limit int) ([]id.PrivacyRequest, error) {
	if limit < 1 || limit > 1000 {
		return nil, privacy.ErrInvalid
	}
	var result []id.PrivacyRequest
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.privacy_requests
			WHERE tenant_id=$1 AND state IN ('requested','in_review') AND expires_at <= $2
			ORDER BY expires_at,id LIMIT $3`, scope.ID().String(), now, limit)
		if err != nil {
			return fmt.Errorf("list due privacy requests: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return fmt.Errorf("scan due privacy request: %w", err)
			}
			identifier, err := id.ParsePrivacyRequest(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			result = append(result, identifier)
		}
		return rows.Err()
	})
	return result, err
}

// SubjectPrivacyRequests lists a bounded ascending page of requests created
// through one outcome-credential verification.
func (store *Store) SubjectPrivacyRequests(ctx context.Context, scope tenant.Scope, verificationID, position string, limit int) ([]privacy.Request, error) {
	if limit < 1 || limit > 101 || (position != "" && len(position) > 64) {
		return nil, privacy.ErrInvalid
	}
	var identifiers []id.PrivacyRequest
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.privacy_requests
			WHERE tenant_id=$1 AND channel='subject_outcome' AND verification_id=$2 AND ($3='' OR id>$3)
			ORDER BY id LIMIT $4`, scope.ID().String(), verificationID, position, limit)
		if err != nil {
			return fmt.Errorf("list subject privacy requests: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return fmt.Errorf("scan subject privacy request: %w", err)
			}
			identifier, err := id.ParsePrivacyRequest(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			identifiers = append(identifiers, identifier)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := make([]privacy.Request, 0, len(identifiers))
	for _, identifier := range identifiers {
		value, err := store.FindPrivacyRequest(ctx, scope, identifier)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// CreateRestriction persists one restriction and its audit event.
func (store *Store) CreateRestriction(ctx context.Context, scope tenant.Scope, actor privacy.Actor, restriction privacy.Restriction) error {
	if restriction.Validate() != nil || actor.ID == "" {
		return privacy.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.privacy_restrictions
			(tenant_id,id,request_id,subject_id,scope,purpose,reason_code,region,state,starts_at,lifted_at,lift_reason_code,version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, scope.ID().String(), restriction.ID.String(),
			restriction.RequestID.String(), restriction.SubjectID, string(restriction.Scope), nullableText(restriction.Purpose),
			string(restriction.ReasonCode), restriction.Region, string(restriction.State), restriction.StartsAt,
			nullableTime(restriction.LiftedAt), nullableText(string(restriction.LiftReasonCode)), restriction.Version); err != nil {
			return fmt.Errorf("insert privacy restriction: %w", err)
		}
		return appendRestrictionAudit(ctx, tx, scope, actor, restriction, "privacy.restriction.created")
	})
}

// FindRestriction loads one tenant-scoped restriction.
func (store *Store) FindRestriction(ctx context.Context, scope tenant.Scope, identifier id.PrivacyRestriction) (privacy.Restriction, error) {
	var result privacy.Restriction
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, err := findRestriction(ctx, tx, scope, identifier)
		if err != nil {
			return err
		}
		result = value
		return nil
	})
	return result, err
}

func findRestriction(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.PrivacyRestriction) (privacy.Restriction, error) {
	var result privacy.Restriction
	var requestID, scopeValue, purpose, liftReason *string
	var liftedAt *time.Time
	err := tx.QueryRow(ctx, `SELECT request_id,subject_id,scope,purpose,reason_code,region,state,starts_at,lifted_at,lift_reason_code,version
		FROM idenqa.privacy_restrictions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()).Scan(
		&requestID, &result.SubjectID, &scopeValue, &purpose, (*string)(&result.ReasonCode), &result.Region,
		(*string)(&result.State), &result.StartsAt, &liftedAt, &liftReason, &result.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return privacy.Restriction{}, privacy.ErrInvalid
	}
	if err != nil {
		return privacy.Restriction{}, fmt.Errorf("find privacy restriction: %w", err)
	}
	result.ID = identifier
	if requestID != nil {
		result.RequestID, _ = id.ParsePrivacyRequest(*requestID)
	}
	result.Scope = privacy.RestrictionScope(dereference(scopeValue))
	if purpose != nil {
		result.Purpose = *purpose
	}
	if liftedAt != nil {
		result.LiftedAt = liftedAt.UTC()
	}
	if liftReason != nil {
		result.LiftReasonCode = privacy.RestrictionReason(*liftReason)
	}
	result.StartsAt = result.StartsAt.UTC()
	if err := result.Validate(); err != nil {
		return privacy.Restriction{}, err
	}
	return result, nil
}

// SaveRestriction lifts one restriction with optimistic concurrency.
func (store *Store) SaveRestriction(ctx context.Context, scope tenant.Scope, actor privacy.Actor, restriction privacy.Restriction, expectedVersion int64) error {
	if restriction.Validate() != nil || expectedVersion < 1 || restriction.Version != expectedVersion+1 {
		return privacy.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.privacy_restrictions
			SET state=$3,lifted_at=$4,lift_reason_code=$5,version=$6
			WHERE tenant_id=$1 AND id=$2 AND version=$7 AND state='active'`, scope.ID().String(), restriction.ID.String(),
			string(restriction.State), restriction.LiftedAt, string(restriction.LiftReasonCode), restriction.Version, expectedVersion)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.Join(privacy.ErrConflict, err)
		}
		return appendRestrictionAudit(ctx, tx, scope, actor, restriction, "privacy.restriction.lifted")
	})
}

// ListRestrictions returns one bounded page, optionally for one exact subject.
func (store *Store) ListRestrictions(ctx context.Context, scope tenant.Scope, subjectID, position string, limit int) ([]privacy.Restriction, error) {
	if limit < 1 || limit > 101 || (position != "" && len(position) > 64) {
		return nil, privacy.ErrInvalid
	}
	identifiers := []id.PrivacyRestriction{}
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.privacy_restrictions
			WHERE tenant_id=$1 AND ($2='' OR subject_id=$2) AND ($3='' OR id>$3)
			ORDER BY id LIMIT $4`, scope.ID().String(), subjectID, position, limit)
		if err != nil {
			return fmt.Errorf("list privacy restrictions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return fmt.Errorf("scan privacy restriction: %w", err)
			}
			identifier, err := id.ParsePrivacyRestriction(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			identifiers = append(identifiers, identifier)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := []privacy.Restriction{}
	err = store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		for _, identifier := range identifiers {
			value, err := findRestriction(ctx, tx, scope, identifier)
			if err != nil {
				return err
			}
			result = append(result, value)
		}
		return nil
	})
	return result, err
}

// ActiveRestrictions lists subject-blocking restrictions for one exact subject.
func (store *Store) ActiveRestrictions(ctx context.Context, scope tenant.Scope, subjectID string, at time.Time) ([]privacy.Restriction, error) {
	identifiers := []id.PrivacyRestriction{}
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.privacy_restrictions
			WHERE tenant_id=$1 AND subject_id=$2 AND state='active' AND scope='subject' AND starts_at <= $3
			ORDER BY starts_at,id`, scope.ID().String(), subjectID, at)
		if err != nil {
			return fmt.Errorf("list active privacy restrictions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return fmt.Errorf("scan active privacy restriction: %w", err)
			}
			identifier, err := id.ParsePrivacyRestriction(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			identifiers = append(identifiers, identifier)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := []privacy.Restriction{}
	for _, identifier := range identifiers {
		value, err := store.FindRestriction(ctx, scope, identifier)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// CreateDisclosure persists one immutable disclosure record.
func (store *Store) CreateDisclosure(ctx context.Context, scope tenant.Scope, actor privacy.Actor, disclosure privacy.Disclosure) error {
	if disclosure.Validate() != nil || actor.ID == "" {
		return privacy.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.privacy_disclosures
			(tenant_id,id,request_id,recipient,purpose,data_class,legal_basis,region,reference,disclosed_at,version)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, scope.ID().String(), disclosure.ID.String(),
			disclosure.RequestID.String(), disclosure.Recipient, disclosure.Purpose, string(disclosure.DataClass),
			disclosure.LegalBasis, disclosure.Region, disclosure.Reference, disclosure.DisclosedAt, disclosure.Version); err != nil {
			return fmt.Errorf("insert privacy disclosure: %w", err)
		}
		return appendPrivacyAudit(ctx, tx, scope, actor, "privacy.disclosure.created", disclosure.ID.String(), 1, disclosure.DisclosedAt)
	})
}

// ListDisclosures returns one bounded page for one request or the whole tenant.
func (store *Store) ListDisclosures(ctx context.Context, scope tenant.Scope, requestID id.PrivacyRequest, position string, limit int) ([]privacy.Disclosure, error) {
	if limit < 1 || limit > 101 || (position != "" && len(position) > 64) {
		return nil, privacy.ErrInvalid
	}
	result := []privacy.Disclosure{}
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		requestFilter := ""
		if !requestID.IsZero() {
			requestFilter = requestID.String()
		}
		rows, err := tx.Query(ctx, `SELECT id,request_id,recipient,purpose,data_class,legal_basis,region,reference,disclosed_at,version
			FROM idenqa.privacy_disclosures
			WHERE tenant_id=$1 AND ($2='' OR request_id=$2) AND ($3='' OR id>$3)
			ORDER BY id LIMIT $4`, scope.ID().String(), requestFilter, position, limit)
		if err != nil {
			return fmt.Errorf("list privacy disclosures: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value privacy.Disclosure
			var encoded, encodedRequest, class string
			if err := rows.Scan(&encoded, &encodedRequest, &value.Recipient, &value.Purpose, &class, &value.LegalBasis,
				&value.Region, &value.Reference, &value.DisclosedAt, &value.Version); err != nil {
				return fmt.Errorf("scan privacy disclosure: %w", err)
			}
			identifier, err := id.ParsePrivacyDisclosure(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			parsedRequest, err := id.ParsePrivacyRequest(encodedRequest)
			if err != nil {
				return privacy.ErrInvalid
			}
			value.ID, value.RequestID, value.DataClass = identifier, parsedRequest, privacy.DisclosureClass(class)
			value.DisclosedAt = value.DisclosedAt.UTC()
			result = append(result, value)
		}
		return rows.Err()
	})
	return result, err
}

// ListProcessors returns one bounded ascending inventory page.
func (store *Store) ListProcessors(ctx context.Context, scope tenant.Scope, position string, limit int) ([]privacy.Processor, error) {
	if limit < 1 || limit > 101 || (position != "" && len(position) > 64) {
		return nil, privacy.ErrInvalid
	}
	identifiers := []id.Processor{}
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.processor_inventory
			WHERE tenant_id=$1 AND ($2='' OR id>$2) ORDER BY id LIMIT $3`, scope.ID().String(), position, limit)
		if err != nil {
			return fmt.Errorf("list processor inventory: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var encoded string
			if err := rows.Scan(&encoded); err != nil {
				return fmt.Errorf("scan processor inventory: %w", err)
			}
			identifier, err := id.ParseProcessor(encoded)
			if err != nil {
				return privacy.ErrInvalid
			}
			identifiers = append(identifiers, identifier)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	result := []privacy.Processor{}
	for _, identifier := range identifiers {
		value, err := store.FindProcessor(ctx, scope, identifier)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

// FindProcessor loads one inventory entry.
func (store *Store) FindProcessor(ctx context.Context, scope tenant.Scope, identifier id.Processor) (privacy.Processor, error) {
	var result privacy.Processor
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, err := findProcessor(ctx, tx, scope, identifier)
		if err != nil {
			return err
		}
		result = value
		return nil
	})
	return result, err
}

func findProcessor(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, identifier id.Processor) (privacy.Processor, error) {
	var result privacy.Processor
	var role string
	var classesJSON, regionsJSON []byte
	err := tx.QueryRow(ctx, `SELECT name,role,purpose,data_classes,regions,transfer_mechanism,version,created_at,updated_at
		FROM idenqa.processor_inventory WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()).Scan(
		&result.Name, &role, &result.Purpose, &classesJSON, &regionsJSON, &result.TransferMechanism, &result.Version,
		&result.CreatedAt, &result.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return privacy.Processor{}, privacy.ErrInvalid
	}
	if err != nil {
		return privacy.Processor{}, fmt.Errorf("find processor inventory: %w", err)
	}
	result.ID, result.Role = identifier, privacy.ProcessorRole(role)
	if err := json.Unmarshal(classesJSON, &result.DataClasses); err != nil {
		return privacy.Processor{}, privacy.ErrInvalid
	}
	if err := json.Unmarshal(regionsJSON, &result.Regions); err != nil {
		return privacy.Processor{}, privacy.ErrInvalid
	}
	result.CreatedAt, result.UpdatedAt = result.CreatedAt.UTC(), result.UpdatedAt.UTC()
	if err := result.Validate(); err != nil {
		return privacy.Processor{}, err
	}
	return result, nil
}

// PutProcessor inserts version one or applies one expected-version update and
// records the append-only revision atomically.
func (store *Store) PutProcessor(ctx context.Context, scope tenant.Scope, actor privacy.Actor, processor privacy.Processor, expectedVersion int64) error {
	if processor.Validate() != nil || actor.ID == "" || expectedVersion < 0 || processor.Version != expectedVersion+1 {
		return privacy.ErrInvalid
	}
	classes, err := json.Marshal(processor.DataClasses)
	if err != nil {
		return privacy.ErrInvalid
	}
	regions, err := json.Marshal(processor.Regions)
	if err != nil {
		return privacy.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if expectedVersion == 0 {
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.processor_inventory
				(tenant_id,id,name,role,purpose,data_classes,regions,transfer_mechanism,version,created_at,updated_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, scope.ID().String(), processor.ID.String(),
				processor.Name, string(processor.Role), processor.Purpose, classes, regions, processor.TransferMechanism,
				processor.Version, processor.CreatedAt, processor.UpdatedAt); err != nil {
				return fmt.Errorf("insert processor inventory: %w", err)
			}
		} else {
			tag, err := tx.Exec(ctx, `UPDATE idenqa.processor_inventory
				SET name=$3,role=$4,purpose=$5,data_classes=$6,regions=$7,transfer_mechanism=$8,version=$9,updated_at=$10
				WHERE tenant_id=$1 AND id=$2 AND version=$11`, scope.ID().String(), processor.ID.String(), processor.Name,
				string(processor.Role), processor.Purpose, classes, regions, processor.TransferMechanism, processor.Version,
				processor.UpdatedAt, expectedVersion)
			if err != nil || tag.RowsAffected() != 1 {
				return errors.Join(privacy.ErrConflict, err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.processor_inventory_revisions
			(tenant_id,id,version,name,role,purpose,data_classes,regions,transfer_mechanism,recorded_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT (tenant_id,id,version) DO NOTHING`,
			scope.ID().String(), processor.ID.String(), processor.Version, processor.Name, string(processor.Role),
			processor.Purpose, classes, regions, processor.TransferMechanism, processor.UpdatedAt); err != nil {
			return fmt.Errorf("insert processor inventory revision: %w", err)
		}
		return appendPrivacyAudit(ctx, tx, scope, actor, "privacy.processor.put", processor.ID.String(), processor.Version, processor.UpdatedAt)
	})
}

func appendRequestAudit(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, actor privacy.Actor, request privacy.Request, eventType string) error {
	return appendPrivacyAudit(ctx, tx, scope, actor, eventType, request.ID.String(), request.Version, request.UpdatedAt)
}

func appendRestrictionAudit(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, actor privacy.Actor, restriction privacy.Restriction, eventType string) error {
	return appendPrivacyAudit(ctx, tx, scope, actor, eventType, restriction.ID.String(), restriction.Version, restriction.AuditTime())
}

func appendPrivacyAudit(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, actor privacy.Actor, eventType, reference string, version int64, occurredAt time.Time) error {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%s\n%d", eventType, scope.ID().String(), reference, version)))
	_, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
		EventID:     referenceToken("event", fmt.Sprintf("%s:%s:%d", eventType, reference, version)),
		EventType:   eventType,
		AggregateID: referenceToken("privacy", reference),
		ActorID:     referenceToken("actor", actor.ID),
		EventDigest: hex.EncodeToString(digest[:]),
		OccurredAt:  occurredAt.UTC(),
	})
	return err
}

func eventDigest(identifier id.PrivacyRequest, event privacy.Event) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n%s\n%s\n%s", identifier.String(), event.Type, event.Sequence, event.From, event.To, event.OccurredAt.Format(time.RFC3339Nano))))
	return hex.EncodeToString(digest[:])
}

func latestEventType(request privacy.Request) string {
	if len(request.Events) == 0 {
		return "privacy.request.updated"
	}
	return request.Events[len(request.Events)-1].Type
}

func dereference(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var (
	_ privacy.RequestRepository     = (*Store)(nil)
	_ privacy.RestrictionRepository = (*Store)(nil)
	_ privacy.DisclosureRepository  = (*Store)(nil)
	_ privacy.ProcessorRepository   = (*Store)(nil)
)
