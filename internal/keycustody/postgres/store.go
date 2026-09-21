// Package postgres implements tenant-forced HMAC key lifecycle persistence.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

const maximumWrappedKeyBytes = 64 * 1024

type transactionRunner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// Store persists immutable key versions and their wrapped material.
type Store struct {
	pool       transactionRunner
	wrapper    platformcrypto.KeyWrapper
	unwrapper  platformcrypto.KeyUnwrapper
	references keycustody.References
	generate   func() (string, []byte, error)
}

// New composes HMAC key persistence with provider-neutral key custody. The
// reference checker may be nil; a nil checker reports every inactive version
// unreferenced.
func New(
	pool transactionRunner,
	wrapper platformcrypto.KeyWrapper,
	unwrapper platformcrypto.KeyUnwrapper,
	references keycustody.References,
	generate func() (string, []byte, error),
) (*Store, error) {
	if pool == nil || generate == nil {
		return nil, keycustody.ErrInvalid
	}

	return &Store{pool: pool, wrapper: wrapper, unwrapper: unwrapper, references: references, generate: generate}, nil
}

func setScope(ctx context.Context, tx pg.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return keycustody.ErrInvalid
	}
	var value string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value)
}

// Execute commits one validated lifecycle command, its audit record, and its
// idempotency result atomically.
func (store *Store) Execute(ctx context.Context, scope tenant.Scope, command keycustody.Command) (keycustody.Result, error) {
	if store == nil || scope.ID().IsZero() || command.Operation == "" {
		return keycustody.Result{}, keycustody.ErrInvalid
	}
	var result keycustody.Result
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		queries := sqlgen.New(tx)
		reservation, err := idempg.Reserve(ctx, queries, command.Retry)
		if err != nil {
			return err
		}
		if replay, ok := reservation.Result(); ok {
			return json.Unmarshal(replay.Body(), &result)
		}
		generated, err := store.mutate(ctx, tx, scope, command, &result)
		if err != nil {
			return err
		}
		if generated != nil {
			clear(generated)
		}
		digest, err := resultDigest(result)
		if err != nil {
			return err
		}
		if _, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
			EventID: "keycustody." + command.Operation + "." + digest, EventType: "keycustody." + command.Operation,
			AggregateID: "keycustody." + command.Domain, ActorID: command.ActorKeyID,
			EventDigest: digest, OccurredAt: command.At,
		}); err != nil {
			return err
		}
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(200, body)
		if err != nil {
			return err
		}

		return idempg.Complete(ctx, queries, command.Retry, replay, command.At)
	})
	return result, err
}

// mutate applies one lifecycle operation and returns generated material that
// the caller must clear.
func (store *Store) mutate(
	ctx context.Context,
	tx pg.Transaction,
	scope tenant.Scope,
	command keycustody.Command,
	result *keycustody.Result,
) ([]byte, error) {
	switch command.Operation {
	case "create":
		return store.create(ctx, tx, scope, command, result)
	case "rotate":
		return store.rotate(ctx, tx, scope, command, result)
	case "disable":
		return nil, store.transition(ctx, tx, scope, command, result, keycustody.StateDisabled)
	case "retire":
		return nil, store.transition(ctx, tx, scope, command, result, keycustody.StateRetired)
	default:
		return nil, keycustody.ErrInvalid
	}
}

func (store *Store) create(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command keycustody.Command, result *keycustody.Result) ([]byte, error) {
	var generation int64
	err := tx.QueryRow(ctx, `SELECT generation FROM idenqa.hmac_key_domains WHERE tenant_id=$1 AND domain=$2 FOR UPDATE`, scope.ID().String(), command.Domain).Scan(&generation)
	if err == nil {
		return nil, keycustody.ErrConflict
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	identifier, material, err := store.generate()
	if err != nil {
		return nil, err
	}
	wrapped, err := store.wrap(ctx, scope, command.Domain, identifier, 1, material)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(wrapped.Record())
	if err != nil {
		return nil, keycustody.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.hmac_key_domains(tenant_id,domain,generation,active_version,created_at,updated_at)
		VALUES($1,$2,1,1,$3,$3)`, scope.ID().String(), command.Domain, command.At); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.hmac_keys(tenant_id,domain,version,id,state,wrapped_key,created_at)
		VALUES($1,$2,1,$3,'active',$4,$5)`, scope.ID().String(), command.Domain, identifier, encoded, command.At); err != nil {
		return nil, err
	}
	*result = keycustody.Result{Domain: command.Domain, ActiveVersion: 1, Generation: 1, Enabled: true}

	return material, nil
}

func (store *Store) rotate(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command keycustody.Command, result *keycustody.Result) ([]byte, error) {
	var generation int64
	err := tx.QueryRow(ctx, `SELECT generation FROM idenqa.hmac_key_domains WHERE tenant_id=$1 AND domain=$2 FOR UPDATE`, scope.ID().String(), command.Domain).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, keycustody.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if generation != command.ExpectedVersion {
		return nil, keycustody.ErrConflict
	}
	if generation >= keycustody.MaximumVersions {
		return nil, keycustody.ErrInvalid
	}
	next := generation + 1
	identifier, material, err := store.generate()
	if err != nil {
		return nil, err
	}
	wrapped, err := store.wrap(ctx, scope, command.Domain, identifier, next, material)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(wrapped.Record())
	if err != nil {
		return nil, keycustody.ErrUnavailable
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.hmac_keys(tenant_id,domain,version,id,state,wrapped_key,created_at)
		VALUES($1,$2,$3,$4,'active',$5,$6)`, scope.ID().String(), command.Domain, next, identifier, encoded, command.At); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.hmac_key_domains SET generation=$3,active_version=$3,updated_at=$4
		WHERE tenant_id=$1 AND domain=$2`, scope.ID().String(), command.Domain, next, command.At); err != nil {
		return nil, err
	}
	*result = keycustody.Result{Domain: command.Domain, ActiveVersion: next, Generation: next, Enabled: true}

	return material, nil
}

func (store *Store) transition(
	ctx context.Context,
	tx pg.Transaction,
	scope tenant.Scope,
	command keycustody.Command,
	result *keycustody.Result,
	state keycustody.State,
) error {
	var generation int64
	var activeVersion *int64
	err := tx.QueryRow(ctx, `SELECT generation,active_version FROM idenqa.hmac_key_domains WHERE tenant_id=$1 AND domain=$2 FOR UPDATE`, scope.ID().String(), command.Domain).Scan(&generation, &activeVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return keycustody.ErrNotFound
	}
	if err != nil {
		return err
	}
	if command.ExpectedVersion > generation {
		return keycustody.ErrNotFound
	}
	var current string
	err = tx.QueryRow(ctx, `SELECT state FROM idenqa.hmac_keys WHERE tenant_id=$1 AND domain=$2 AND version=$3 FOR UPDATE`, scope.ID().String(), command.Domain, command.ExpectedVersion).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return keycustody.ErrNotFound
	}
	if err != nil {
		return err
	}
	switch state {
	case keycustody.StateDisabled:
		if keycustody.State(current) != keycustody.StateActive {
			return keycustody.ErrConflict
		}
	case keycustody.StateRetired:
		if keycustody.State(current) == keycustody.StateActive {
			return keycustody.ErrReferenced
		}
		if keycustody.State(current) != keycustody.StateDisabled {
			return keycustody.ErrConflict
		}
		if activeVersion != nil && *activeVersion == command.ExpectedVersion {
			return keycustody.ErrReferenced
		}
		if store.references != nil {
			referenced, err := store.references.ReferencedKeyVersion(ctx, scope, command.Domain, command.ExpectedVersion)
			if err != nil {
				return keycustody.ErrUnavailable
			}
			if referenced {
				return keycustody.ErrReferenced
			}
		}
	default:
		return keycustody.ErrInvalid
	}
	var retiredAt any
	if state == keycustody.StateRetired {
		retiredAt = command.At
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.hmac_keys SET state=$4,retired_at=$5 WHERE tenant_id=$1 AND domain=$2 AND version=$3`,
		scope.ID().String(), command.Domain, command.ExpectedVersion, string(state), retiredAt); err != nil {
		return err
	}
	if state == keycustody.StateDisabled && activeVersion != nil && *activeVersion == command.ExpectedVersion {
		if _, err := tx.Exec(ctx, `UPDATE idenqa.hmac_key_domains SET active_version=NULL,updated_at=$3 WHERE tenant_id=$1 AND domain=$2`,
			scope.ID().String(), command.Domain, command.At); err != nil {
			return err
		}
		activeVersion = nil
	}
	versions, err := store.versions(ctx, tx, scope, command.Domain)
	if err != nil {
		return err
	}
	active := int64(0)
	if activeVersion != nil {
		active = *activeVersion
	}
	*result = keycustody.Result{Domain: command.Domain, ActiveVersion: active, Generation: generation, Versions: versions, Enabled: active > 0}

	return nil
}

func (store *Store) wrap(ctx context.Context, scope tenant.Scope, domain, identifier string, version int64, material []byte) (kms.WrappedKey, error) {
	if store.wrapper == nil {
		return kms.WrappedKey{}, keycustody.ErrUnavailable
	}
	purpose, err := kms.NewPurpose(domain)
	if err != nil {
		return kms.WrappedKey{}, keycustody.ErrInvalid
	}
	aad, err := materialContext(domain, scope.ID().String(), identifier, version)
	if err != nil {
		return kms.WrappedKey{}, keycustody.ErrUnavailable
	}
	wrapped, err := store.wrapper.Wrap(ctx, purpose, material, aad)
	if err != nil {
		return kms.WrappedKey{}, keycustody.ErrUnavailable
	}

	return wrapped, nil
}

// Read returns one domain's safe lifecycle metadata.
func (store *Store) Read(ctx context.Context, scope tenant.Scope, domain string) (keycustody.Result, error) {
	if _, err := keycustody.ParseDomain(domain); err != nil {
		return keycustody.Result{}, err
	}
	var result keycustody.Result
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var generation int64
		var activeVersion *int64
		err := tx.QueryRow(ctx, `SELECT generation,active_version FROM idenqa.hmac_key_domains WHERE tenant_id=$1 AND domain=$2`, scope.ID().String(), domain).Scan(&generation, &activeVersion)
		if errors.Is(err, pgx.ErrNoRows) {
			return keycustody.ErrNotFound
		}
		if err != nil {
			return err
		}
		versions, err := store.versions(ctx, tx, scope, domain)
		if err != nil {
			return err
		}
		active := int64(0)
		if activeVersion != nil {
			active = *activeVersion
		}
		result = keycustody.Result{Domain: domain, ActiveVersion: active, Generation: generation, Versions: versions, Enabled: active > 0 && hasActive(versions)}

		return nil
	})
	return result, err
}

// Version returns one immutable key version record.
func (store *Store) Version(ctx context.Context, scope tenant.Scope, domain string, version int64) (keycustody.Version, error) {
	var result keycustody.Version
	if version < 1 {
		return result, keycustody.ErrInvalid
	}
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var state string
		var createdAt time.Time
		var retiredAt *time.Time
		err := tx.QueryRow(ctx, `SELECT id,state,created_at,retired_at FROM idenqa.hmac_keys WHERE tenant_id=$1 AND domain=$2 AND version=$3`,
			scope.ID().String(), domain, version).Scan(&result.ID, &state, &createdAt, &retiredAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return keycustody.ErrNotFound
		}
		if err != nil {
			return err
		}
		result.Domain, result.Version, result.State = domain, version, keycustody.State(state)
		result.CreatedAt, result.RetiredAt = createdAt.UTC(), retiredAt

		return nil
	})
	return result, err
}

// Versions returns every retained version for one domain in ascending order.
func (store *Store) Versions(ctx context.Context, scope tenant.Scope, domain string) ([]keycustody.Version, error) {
	if _, err := keycustody.ParseDomain(domain); err != nil {
		return nil, err
	}
	var versions []keycustody.Version
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var err error
		versions, err = store.versions(ctx, tx, scope, domain)

		return err
	})
	return versions, err
}

// Material releases the exact version's unwrapped HMAC key. The caller must
// clear the returned slice.
func (store *Store) Material(ctx context.Context, scope tenant.Scope, domain string, version int64) ([]byte, error) {
	if store.unwrapper == nil {
		return nil, keycustody.ErrUnavailable
	}
	var material []byte
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var identifier string
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT id,wrapped_key FROM idenqa.hmac_keys WHERE tenant_id=$1 AND domain=$2 AND version=$3`,
			scope.ID().String(), domain, version).Scan(&identifier, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return keycustody.ErrNotFound
		}
		if err != nil {
			return err
		}
		var record kms.WrappedKeyRecord
		if len(raw) == 0 || len(raw) > maximumWrappedKeyBytes || json.Unmarshal(raw, &record) != nil {
			return keycustody.ErrUnavailable
		}
		wrapped, err := kms.NewWrappedKey(record)
		if err != nil {
			return keycustody.ErrUnavailable
		}
		purpose, err := kms.NewPurpose(domain)
		if err != nil {
			return keycustody.ErrInvalid
		}
		aad, err := materialContext(domain, scope.ID().String(), identifier, version)
		if err != nil {
			return keycustody.ErrUnavailable
		}
		material, err = store.unwrapper.Unwrap(ctx, purpose, wrapped, aad)
		if err != nil {
			return keycustody.ErrUnavailable
		}
		if len(material) != keycustody.KeySize {
			clear(material)
			return keycustody.ErrUnavailable
		}

		return nil
	})
	if err != nil {
		clear(material)
		return nil, err
	}

	return material, nil
}

func (store *Store) versions(ctx context.Context, tx pg.Transaction, scope tenant.Scope, domain string) ([]keycustody.Version, error) {
	rows, err := tx.Query(ctx, `SELECT id,version,state,created_at,retired_at FROM idenqa.hmac_keys
		WHERE tenant_id=$1 AND domain=$2 ORDER BY version`, scope.ID().String(), domain)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	versions := make([]keycustody.Version, 0, 8)
	for rows.Next() {
		var version keycustody.Version
		var state string
		var createdAt time.Time
		var retiredAt *time.Time
		if err := rows.Scan(&version.ID, &version.Version, &state, &createdAt, &retiredAt); err != nil {
			return nil, err
		}
		version.Domain, version.State = domain, keycustody.State(state)
		version.CreatedAt, version.RetiredAt = createdAt.UTC(), retiredAt
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(versions) > keycustody.MaximumVersions {
		return nil, keycustody.ErrUnavailable
	}

	return versions, nil
}

func hasActive(versions []keycustody.Version) bool {
	for _, version := range versions {
		if version.State == keycustody.StateActive {
			return true
		}
	}

	return false
}

func materialContext(domain, tenantID, identifier string, version int64) ([]byte, error) {
	return json.Marshal([]string{"idenqa.hmac-key.v1", domain, tenantID, identifier, strconv.FormatInt(version, 10)})
}

func resultDigest(result keycustody.Result) (string, error) {
	body, err := json.Marshal(struct {
		Domain        string
		ActiveVersion int64
		Generation    int64
	}{
		Domain: result.Domain, ActiveVersion: result.ActiveVersion, Generation: result.Generation,
	})
	if err != nil {
		return "", keycustody.ErrUnavailable
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
