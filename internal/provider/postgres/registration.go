package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	retrydb "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// RegistrationStore owns tenant-scoped secret-free provider registrations.
type RegistrationStore struct {
	pool   transactionRunner
	source clock.Clock
}

// NewRegistrationStore constructs durable tenant provider registration state.
func NewRegistrationStore(pool transactionRunner, source clock.Clock) (*RegistrationStore, error) {
	if pool == nil || source == nil {
		return nil, provider.ErrRegistrationInvalid
	}
	return &RegistrationStore{pool, source}, nil
}

func (store *RegistrationStore) readScoped(ctx context.Context, scope tenant.Scope, work func(context.Context, pg.Transaction) error) error {
	if scope.ID().IsZero() {
		return provider.ErrRegistrationInvalid
	}
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		return work(ctx, tx)
	})
}

// Enabled returns the tenant's enabled registrations for one adapter and
// region. An empty region returns every enabled registration for the adapter so
// callers can apply the documented single-enabled rule without broadening
// beyond what they can prove.
func (store *RegistrationStore) Enabled(ctx context.Context, tenantID id.Tenant, adapter, region string) ([]provider.Registration, error) {
	if tenantID.IsZero() || adapter == "" || (region != "" && !validRegistrationToken(region)) {
		return nil, provider.ErrRegistrationInvalid
	}
	var result []provider.Registration
	err := store.readScoped(ctx, mustScope(tenantID), func(ctx context.Context, tx pg.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,adapter_id,region,configuration,inputs,selfie_requirement,restrictions,enabled,version,actor_key_id,created_at,updated_at FROM idenqa.provider_registrations WHERE tenant_id=$1 AND adapter_id=$2 AND enabled AND ($3='' OR region=$3) ORDER BY created_at DESC, id DESC LIMIT 8`, tenantID.String(), adapter, region)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			registration, err := scanRegistration(rows, tenantID.String())
			if err != nil {
				return err
			}
			result = append(result, registration)
		}
		return rows.Err()
	})
	return result, err
}

// Get reads one registration by exact tenant-scoped identifier.
func (store *RegistrationStore) Get(ctx context.Context, scope tenant.Scope, registrationID string) (provider.Registration, error) {
	identifier, err := id.ParseProviderRegistration(registrationID)
	if err != nil {
		return provider.Registration{}, provider.ErrRegistrationNotFound
	}
	var result provider.Registration
	err = store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		row := tx.QueryRow(ctx, `SELECT id,adapter_id,region,configuration,inputs,selfie_requirement,restrictions,enabled,version,actor_key_id,created_at,updated_at FROM idenqa.provider_registrations WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String())
		var scanErr error
		result, scanErr = scanRegistration(row, scope.ID().String())
		if errors.Is(scanErr, pgx.ErrNoRows) {
			return provider.ErrRegistrationNotFound
		}
		return scanErr
	})
	if err != nil {
		return provider.Registration{}, err
	}
	return result, nil
}

// List returns the bounded newest-first tenant page after one stable position.
func (store *RegistrationStore) List(ctx context.Context, scope tenant.Scope, after *provider.RegistrationPosition, limit int) (provider.RegistrationPage, error) {
	if limit < 1 || limit > 100 {
		return provider.RegistrationPage{}, provider.ErrRegistrationInvalid
	}
	if after != nil {
		if after.ID == "" || after.CreatedAt.IsZero() {
			return provider.RegistrationPage{}, provider.ErrRegistrationInvalid
		}
		if _, err := id.ParseProviderRegistration(after.ID); err != nil {
			return provider.RegistrationPage{}, provider.ErrRegistrationInvalid
		}
	}
	page := provider.RegistrationPage{Registrations: []provider.Registration{}}
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,adapter_id,region,configuration,inputs,selfie_requirement,restrictions,enabled,version,actor_key_id,created_at,updated_at FROM idenqa.provider_registrations WHERE tenant_id=$1 AND ($2::timestamptz IS NULL OR (created_at,id) < ($2,$3)) ORDER BY created_at DESC, id DESC LIMIT $4`, scope.ID().String(), positionTime(after), positionID(after), limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			registration, err := scanRegistration(rows, scope.ID().String())
			if err != nil {
				return err
			}
			page.Registrations = append(page.Registrations, registration)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(page.Registrations) > limit {
			page.Registrations = page.Registrations[:limit]
			last := page.Registrations[len(page.Registrations)-1]
			page.Next = &provider.RegistrationPosition{CreatedAt: last.CreatedAt, ID: last.ID}
		}
		return nil
	})
	return page, err
}

// Health returns a bounded read over the tenant's own request and dispatch
// records for one registration. No external probe is performed.
func (store *RegistrationStore) Health(ctx context.Context, scope tenant.Scope, registration provider.Registration) (provider.RegistrationHealth, error) {
	health := provider.RegistrationHealth{RegistrationID: registration.ID, AdapterID: registration.AdapterID}
	err := store.readScoped(ctx, scope, func(ctx context.Context, tx pg.Transaction) error {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.provider_requests WHERE tenant_id=$1 AND request_body->'configuration'->>'provider_id'=$2 AND request_body->'adapter'->>'adapter_id'=$3`, scope.ID().String(), registration.Configuration.ProviderID, registration.AdapterID).Scan(&health.Requests); err != nil {
			return fmt.Errorf("count registration requests: %w", err)
		}
		var lastOutcome *string
		var lastClass *string
		var lastCode *string
		var lastAt *time.Time
		if err := tx.QueryRow(ctx, `WITH recent AS (
				SELECT dispatch.claimed_at, dispatch.result_body
				FROM idenqa.provider_dispatches AS dispatch
				JOIN idenqa.provider_requests AS requests ON requests.tenant_id=dispatch.tenant_id AND requests.attempt_id=dispatch.attempt_id
				WHERE dispatch.tenant_id=$1 AND requests.request_body->'configuration'->>'provider_id'=$2 AND requests.request_body->'adapter'->>'adapter_id'=$3
				ORDER BY dispatch.claimed_at DESC, dispatch.attempt_id DESC
				LIMIT 1000
			)
			SELECT count(*) FILTER (WHERE result_body IS NULL),
				count(*) FILTER (WHERE result_body->>'outcome'='completed'),
				count(*) FILTER (WHERE result_body->>'outcome'='failed'),
				(SELECT result_body->>'outcome' FROM recent WHERE result_body IS NOT NULL ORDER BY claimed_at DESC LIMIT 1),
				(SELECT result_body->'failure'->>'class' FROM recent WHERE result_body->>'outcome'='failed' ORDER BY claimed_at DESC LIMIT 1),
				(SELECT result_body->'failure'->>'code' FROM recent WHERE result_body->>'outcome'='failed' ORDER BY claimed_at DESC LIMIT 1),
				(SELECT max(claimed_at) FROM recent)
			FROM recent`, scope.ID().String(), registration.Configuration.ProviderID, registration.AdapterID).
			Scan(&health.Pending, &health.Completed, &health.Failed, &lastOutcome, &lastClass, &lastCode, &lastAt); err != nil {
			return fmt.Errorf("read registration dispatches: %w", err)
		}
		if lastOutcome != nil {
			health.LastOutcome = *lastOutcome
		}
		if lastClass != nil {
			health.LastFailure = &provider.RegistrationHealthFailure{Class: *lastClass}
			if lastCode != nil {
				health.LastFailure.Code = *lastCode
			}
			if lastAt != nil {
				health.LastFailure.RecordedAt = lastAt.UTC()
			}
		}
		if lastAt != nil {
			utc := lastAt.UTC()
			health.LastActivityAt = &utc
		}
		return nil
	})
	return health, err
}

// Apply reserves idempotency before locking the registration root; audit,
// outbox and append-only history share COMMIT with the version CAS.
func (store *RegistrationStore) Apply(ctx context.Context, scope tenant.Scope, request idempotency.Request, event id.Event, command provider.RegistrationCommand) (provider.RegistrationReceipt, error) {
	if scope.ID().IsZero() || scope.ID() != request.TenantID() || event.IsZero() || command.Reason == "" {
		return provider.RegistrationReceipt{}, provider.ErrRegistrationInvalid
	}
	for range 3 {
		var result provider.RegistrationReceipt
		err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return err
			}
			reservation, err := retrydb.Reserve(ctx, queries, request)
			if err != nil {
				return err
			}
			if prior, ok := reservation.Result(); ok {
				if err := json.Unmarshal(prior.Body(), &result); err != nil {
					return err
				}
				result.Replayed = true
				return nil
			}
			now := store.source.Now().UTC().Truncate(time.Microsecond)
			registration, previous, err := applyRegistration(ctx, tx, scope, command, now)
			if err != nil {
				return err
			}
			result = provider.RegistrationReceipt{Registration: registration, Operation: command.Operation, Reason: command.Reason}
			receipt, err := json.Marshal(result)
			if err != nil {
				return err
			}
			if previous == nil {
				if _, err := tx.Exec(ctx, `INSERT INTO idenqa.provider_registrations(tenant_id,id,adapter_id,region,configuration,inputs,selfie_requirement,restrictions,enabled,version,actor_key_id,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, scope.ID().String(), registration.ID, registration.AdapterID, registration.Region, encodeRegistrationConfiguration(registration.Configuration), encodeRegistrationInputs(registration.Inputs), nullableRegistrationString(registration.SelfieRequirement), encodeRegistrationRestrictions(registration.Restrictions), registration.Enabled, registration.Version, request.Principal().String(), registration.CreatedAt, registration.UpdatedAt); err != nil {
					return mapRegistrationUnique(err)
				}
			} else {
				if _, err := tx.Exec(ctx, `UPDATE idenqa.provider_registrations SET adapter_id=$3,region=$4,configuration=$5,inputs=$6,selfie_requirement=$7,restrictions=$8,enabled=$9,version=$10,actor_key_id=$11,updated_at=$12 WHERE tenant_id=$1 AND id=$2 AND version=$13`, scope.ID().String(), registration.ID, registration.AdapterID, registration.Region, encodeRegistrationConfiguration(registration.Configuration), encodeRegistrationInputs(registration.Inputs), nullableRegistrationString(registration.SelfieRequirement), encodeRegistrationRestrictions(registration.Restrictions), registration.Enabled, registration.Version, request.Principal().String(), registration.UpdatedAt, command.ExpectedVersion); err != nil {
					return mapRegistrationUnique(err)
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.provider_registration_history(tenant_id,registration_id,version,actor_key_id,operation,reason,receipt) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), registration.ID, registration.Version, request.Principal().String(), command.Operation, command.Reason, receipt); err != nil {
				return err
			}
			payload, err := json.Marshal(struct {
				RegistrationID string `json:"registration_id"`
				AdapterID      string `json:"adapter_id"`
				Region         string `json:"region"`
				Version        int64  `json:"version"`
				Enabled        bool   `json:"enabled"`
				Reason         string `json:"reason"`
			}{registration.ID, registration.AdapterID, registration.Region, registration.Version, registration.Enabled, command.Reason})
			if err != nil {
				return err
			}
			digest := sha256.Sum256(payload)
			eventType := "provider.registration." + command.Operation + ".v1"
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.outbox_events(id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at) VALUES($1,$2,'provider_registration',$3,$4,$5,1,$6,$7,$7)`, event.String(), scope.ID().String(), registration.ID, registration.Version, eventType, payload, now); err != nil {
				return err
			}
			if _, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{EventID: event.String(), EventType: eventType, AggregateID: registration.ID, ActorID: request.Principal().String(), EventDigest: hex.EncodeToString(digest[:]), OccurredAt: now}); err != nil {
				return err
			}
			value, err := idempotency.NewResult(200, receipt)
			if err != nil {
				return err
			}
			return retrydb.Complete(ctx, queries, request, value, now)
		})
		if err == nil {
			return result, nil
		}
		var conflict *pgconn.PgError
		if !errors.As(err, &conflict) || (conflict.Code != "40001" && conflict.Code != "40P01") {
			return provider.RegistrationReceipt{}, fmt.Errorf("apply provider registration: %w", err)
		}
		if ctx.Err() != nil {
			return provider.RegistrationReceipt{}, ctx.Err()
		}
	}
	return provider.RegistrationReceipt{}, provider.ErrRegistrationConflict
}

func applyRegistration(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command provider.RegistrationCommand, now time.Time) (provider.Registration, *provider.Registration, error) {
	switch command.Operation {
	case "create":
		if command.ExpectedVersion != 0 || command.Write == nil || command.Write.AdapterID == "" {
			return provider.Registration{}, nil, provider.ErrRegistrationInvalid
		}
		identifier, err := id.ParseProviderRegistration(command.RegistrationID)
		if err != nil {
			return provider.Registration{}, nil, provider.ErrRegistrationInvalid
		}
		return provider.Registration{ID: identifier.String(), TenantID: scope.ID().String(), AdapterID: command.Write.AdapterID, Region: command.Write.Region,
			Configuration: command.Write.Configuration, Inputs: cloneRegistrationInputs(command.Write.Inputs), SelfieRequirement: command.Write.SelfieRequirement,
			Restrictions: command.Write.Restrictions, Enabled: false, Version: 1, ActorID: "", CreatedAt: now, UpdatedAt: now}, nil, nil
	case "update", "enable", "disable":
		current, err := lockRegistration(ctx, tx, scope, command.RegistrationID)
		if err != nil {
			return provider.Registration{}, nil, err
		}
		if current.Version != command.ExpectedVersion {
			return provider.Registration{}, nil, provider.ErrRegistrationConflict
		}
		next := current
		next.Version = current.Version + 1
		next.UpdatedAt = now
		switch command.Operation {
		case "update":
			if command.Write == nil {
				return provider.Registration{}, nil, provider.ErrRegistrationInvalid
			}
			next.AdapterID, next.Region, next.Configuration = command.Write.AdapterID, command.Write.Region, command.Write.Configuration
			next.Inputs, next.SelfieRequirement, next.Restrictions = cloneRegistrationInputs(command.Write.Inputs), command.Write.SelfieRequirement, command.Write.Restrictions
		case "enable":
			if current.Enabled {
				return provider.Registration{}, nil, provider.ErrRegistrationConflict
			}
			next.Enabled = true
		case "disable":
			if !current.Enabled {
				return provider.Registration{}, nil, provider.ErrRegistrationConflict
			}
			next.Enabled = false
		}
		return next, &current, nil
	default:
		return provider.Registration{}, nil, provider.ErrRegistrationInvalid
	}
}

func lockRegistration(ctx context.Context, tx pg.Transaction, scope tenant.Scope, registrationID string) (provider.Registration, error) {
	identifier, err := id.ParseProviderRegistration(registrationID)
	if err != nil {
		return provider.Registration{}, provider.ErrRegistrationNotFound
	}
	row := tx.QueryRow(ctx, `SELECT id,adapter_id,region,configuration,inputs,selfie_requirement,restrictions,enabled,version,actor_key_id,created_at,updated_at FROM idenqa.provider_registrations WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), identifier.String())
	registration, err := scanRegistration(row, scope.ID().String())
	if errors.Is(err, pgx.ErrNoRows) {
		return provider.Registration{}, provider.ErrRegistrationNotFound
	}
	return registration, err
}

type registrationScanner interface {
	Scan(...any) error
}

func scanRegistration(row registrationScanner, tenantID string) (provider.Registration, error) {
	var registration provider.Registration
	var configuration, inputs, restrictions []byte
	var selfie *string
	if err := row.Scan(&registration.ID, &registration.AdapterID, &registration.Region, &configuration, &inputs, &selfie, &restrictions, &registration.Enabled, &registration.Version, &registration.ActorID, &registration.CreatedAt, &registration.UpdatedAt); err != nil {
		return provider.Registration{}, err
	}
	registration.TenantID = tenantID
	if json.Unmarshal(configuration, &registration.Configuration) != nil {
		return provider.Registration{}, provider.ErrRegistrationInvalid
	}
	if len(inputs) > 0 {
		if json.Unmarshal(inputs, &registration.Inputs) != nil {
			return provider.Registration{}, provider.ErrRegistrationInvalid
		}
	}
	if selfie != nil {
		registration.SelfieRequirement = *selfie
	}
	if len(restrictions) > 0 {
		var value providerv1.Restrictions
		if json.Unmarshal(restrictions, &value) != nil {
			return provider.Registration{}, provider.ErrRegistrationInvalid
		}
		registration.Restrictions = &value
	}
	registration.CreatedAt, registration.UpdatedAt = registration.CreatedAt.UTC(), registration.UpdatedAt.UTC()
	return registration, nil
}

func encodeRegistrationConfiguration(value providerv1.ConfigurationReference) []byte {
	encoded, _ := json.Marshal(value)
	return encoded
}

func encodeRegistrationInputs(value []providerv1.InputReference) []byte {
	if len(value) == 0 {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func encodeRegistrationRestrictions(value *providerv1.Restrictions) []byte {
	if value == nil {
		return nil
	}
	encoded, _ := json.Marshal(value)
	return encoded
}

func nullableRegistrationString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func cloneRegistrationInputs(value []providerv1.InputReference) []providerv1.InputReference {
	if len(value) == 0 {
		return nil
	}
	return append([]providerv1.InputReference(nil), value...)
}

func validRegistrationToken(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index > 0 && ((character >= '0' && character <= '9') || character == '_' || character == '.' || character == '-') {
			continue
		}
		return false
	}
	return true
}

func mustScope(tenantID id.Tenant) tenant.Scope {
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return tenant.Scope{}
	}
	return scope
}

func positionTime(position *provider.RegistrationPosition) *time.Time {
	if position == nil {
		return nil
	}
	value := position.CreatedAt.UTC()
	return &value
}

func positionID(position *provider.RegistrationPosition) string {
	if position == nil {
		return ""
	}
	return position.ID
}

func mapRegistrationUnique(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) && databaseError.Code == "23505" {
		return provider.ErrRegistrationConflict
	}
	return err
}
