package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// NewEvidenceAdapter composes the evidence envelope-key class over the owning
// evidence rewrap workflow.
func NewEvidenceAdapter(store *Store, rewrapper *evidence.Rewrapper, workerID string, now func() time.Time) (*EvidenceAdapter, error) {
	if store == nil || rewrapper == nil || workerID == "" || now == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &EvidenceAdapter{store: store, rewrapper: rewrapper, workerID: workerID, now: now}, nil
}

// EvidenceAdapter rewraps KMS-wrapped evidence content keys through the
// evidence optimistic transition and its immutable rewrap audit.
type EvidenceAdapter struct {
	store     *Store
	rewrapper *evidence.Rewrapper
	workerID  string
	now       func() time.Time
}

// Class returns the evidence content-key class.
func (adapter *EvidenceAdapter) Class() keyrewrap.Class { return keyrewrap.ClassEvidenceContent }

// List pages evidence asset identifiers in tenant order.
func (adapter *EvidenceAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,version FROM idenqa.evidence_assets
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list evidence rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var identifier string
			var version int64
			if err := rows.Scan(&identifier, &version); err != nil {
				return err
			}
			parsed, err := id.ParseEvidence(identifier)
			if err != nil {
				return keyrewrap.ErrUnavailable
			}
			targets = append(targets, keyrewrap.Target{TenantID: scope.ID(), Class: adapter.Class(), Object: parsed.String(), Version: version})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap rewraps one exact evidence content key. A concurrent lifecycle
// transition fails closed and is retried from the durable cursor.
func (adapter *EvidenceAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil || target.Version < 1 {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	identifier, err := id.ParseEvidence(target.Object)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	attribution := evidence.CommandAttribution{
		Principal:   evidence.Actor{Type: "service.worker", ID: adapter.workerID},
		TenantActor: evidence.Actor{Type: "service.key_rewrap", ID: "fleet-key-rewrap"},
		Reason:      "scheduled KMS material epoch rewrap",
	}
	asset, err := adapter.rewrapper.Rewrap(ctx, scope, identifier, target.Version, attribution, adapter.now().UTC().Truncate(time.Microsecond))
	if errors.Is(err, evidence.ErrVersionConflict) {
		return keyrewrap.Outcome{}, keyrewrap.ErrConflict
	}
	if errors.Is(err, evidence.ErrConflict) {
		// The stored wrapping already carries the active provider identity.
		return keyrewrap.Outcome{Changed: false}, nil
	}
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrUnavailable
	}

	return keyrewrap.Outcome{Changed: true, Wrapping: asset.Record().Content.Envelope.WrappedKey}, nil
}

// NewWebhookEventAdapter rewraps canonical catalogue event bodies.
func NewWebhookEventAdapter(store *Store) (*WebhookEventAdapter, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &WebhookEventAdapter{store: store}, nil
}

// WebhookEventAdapter is the catalogue event body class.
type WebhookEventAdapter struct{ store *Store }

// Class returns the webhook event class.
func (adapter *WebhookEventAdapter) Class() keyrewrap.Class { return keyrewrap.ClassWebhookEvent }

// List pages non-expired event bodies in tenant order.
func (adapter *WebhookEventAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.webhook_events
			WHERE tenant_id=$1 AND id>$2 AND body IS NOT NULL AND payload_expired_at IS NULL
			ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list webhook event rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var identifier string
			if err := rows.Scan(&identifier); err != nil {
				return err
			}
			parsed, err := id.ParseEvent(identifier)
			if err != nil {
				return keyrewrap.ErrUnavailable
			}
			targets = append(targets, keyrewrap.Target{TenantID: scope.ID(), Class: adapter.Class(), Object: parsed.String()})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap replaces one event body wrapping under the rewrap-only trigger
// transition. The canonical plaintext and body digest are unchanged.
func (adapter *WebhookEventAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	eventID, err := id.ParseEvent(target.Object)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	var outcome keyrewrap.Outcome
	err = adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		var raw []byte
		var provider, reference, keyVersion, algorithm *string
		err := tx.QueryRow(ctx, `SELECT body,body_provider,body_reference,body_key_version,body_algorithm
			FROM idenqa.webhook_events WHERE tenant_id=$1 AND id=$2 AND body IS NOT NULL FOR UPDATE`,
			scope.ID().String(), eventID.String()).Scan(&raw, &provider, &reference, &keyVersion, &algorithm)
		if errors.Is(err, pgx.ErrNoRows) {
			return keyrewrap.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("load webhook event body: %w", err)
		}
		previous, err := restoreWrapping(raw, provider, reference, keyVersion, algorithm)
		if err != nil {
			return err
		}
		next, changed, err := keyrewrap.RewrapRecord(ctx, adapter.store.wrapper, adapter.store.unwrapper,
			delivery.BodyPurpose(), previous, delivery.BodyContext(scope.ID().String(), eventID.String()))
		if err != nil {
			return err
		}
		if !changed {
			outcome = keyrewrap.Outcome{Changed: false, Wrapping: previous}

			return nil
		}
		if err := setRewrapFlag(ctx, tx); err != nil {
			return err
		}
		digest := sha256.Sum256(next.Ciphertext)
		tag, err := tx.Exec(ctx, `UPDATE idenqa.webhook_events SET body=$3,body_digest=$4,
			body_provider=$5,body_reference=$6,body_key_version=$7,body_algorithm=$8
			WHERE tenant_id=$1 AND id=$2 AND body IS NOT NULL`,
			scope.ID().String(), eventID.String(), next.Ciphertext, hex.EncodeToString(digest[:]),
			next.Provider, next.Reference, next.Version, next.Algorithm)
		if err != nil {
			return fmt.Errorf("rewrap webhook event body: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return keyrewrap.ErrConflict
		}
		outcome = keyrewrap.Outcome{Changed: true, Wrapping: next}

		return nil
	})

	return outcome, err
}

// NewWebhookDeliveryAdapter rewraps per-endpoint delivery bodies.
func NewWebhookDeliveryAdapter(store *Store) (*WebhookDeliveryAdapter, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &WebhookDeliveryAdapter{store: store}, nil
}

// WebhookDeliveryAdapter is the delivery body class.
type WebhookDeliveryAdapter struct{ store *Store }

// Class returns the webhook delivery class.
func (adapter *WebhookDeliveryAdapter) Class() keyrewrap.Class { return keyrewrap.ClassWebhookDelivery }

// List pages non-expired delivery bodies in tenant order.
func (adapter *WebhookDeliveryAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.webhook_deliveries
			WHERE tenant_id=$1 AND id>$2 AND body IS NOT NULL AND payload_expired_at IS NULL
			ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list webhook delivery rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var identifier string
			if err := rows.Scan(&identifier); err != nil {
				return err
			}
			parsed, err := id.ParseDelivery(identifier)
			if err != nil {
				return keyrewrap.ErrUnavailable
			}
			targets = append(targets, keyrewrap.Target{TenantID: scope.ID(), Class: adapter.Class(), Object: parsed.String()})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap replaces one delivery body wrapping while preserving its lifecycle.
func (adapter *WebhookDeliveryAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	deliveryID, err := id.ParseDelivery(target.Object)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	var outcome keyrewrap.Outcome
	err = adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		var raw []byte
		var eventID string
		var provider, reference, keyVersion, algorithm *string
		err := tx.QueryRow(ctx, `SELECT body,event_id,body_provider,body_reference,body_key_version,body_algorithm
			FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND id=$2 AND body IS NOT NULL FOR UPDATE`,
			scope.ID().String(), deliveryID.String()).Scan(&raw, &eventID, &provider, &reference, &keyVersion, &algorithm)
		if errors.Is(err, pgx.ErrNoRows) {
			return keyrewrap.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("load webhook delivery body: %w", err)
		}
		previous, err := restoreWrapping(raw, provider, reference, keyVersion, algorithm)
		if err != nil {
			return err
		}
		next, changed, err := keyrewrap.RewrapRecord(ctx, adapter.store.wrapper, adapter.store.unwrapper,
			delivery.BodyPurpose(), previous, delivery.BodyContext(scope.ID().String(), eventID))
		if err != nil {
			return err
		}
		if !changed {
			outcome = keyrewrap.Outcome{Changed: false, Wrapping: previous}

			return nil
		}
		digest := sha256.Sum256(next.Ciphertext)
		tag, err := tx.Exec(ctx, `UPDATE idenqa.webhook_deliveries SET body=$3,body_digest=$4,
			body_provider=$5,body_reference=$6,body_key_version=$7,body_algorithm=$8
			WHERE tenant_id=$1 AND id=$2 AND body IS NOT NULL`,
			scope.ID().String(), deliveryID.String(), next.Ciphertext, hex.EncodeToString(digest[:]),
			next.Provider, next.Reference, next.Version, next.Algorithm)
		if err != nil {
			return fmt.Errorf("rewrap webhook delivery body: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return keyrewrap.ErrConflict
		}
		outcome = keyrewrap.Outcome{Changed: true, Wrapping: next}

		return nil
	})

	return outcome, err
}

// NewWebhookSecretAdapter rewraps endpoint signing secrets.
func NewWebhookSecretAdapter(store *Store) (*WebhookSecretAdapter, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &WebhookSecretAdapter{store: store}, nil
}

// WebhookSecretAdapter is the endpoint signing-secret class.
type WebhookSecretAdapter struct{ store *Store }

// Class returns the webhook secret class.
func (adapter *WebhookSecretAdapter) Class() keyrewrap.Class { return keyrewrap.ClassWebhookSecret }

const secretPurpose = "delivery.webhook-secret"

// List pages endpoint secret versions by endpoint then version.
func (adapter *WebhookSecretAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT endpoint_id,version,provider,reference,key_version,algorithm,ciphertext
			FROM idenqa.webhook_secrets
			WHERE tenant_id=$1 AND (endpoint_id || '|' || lpad(version::text,9,'0')) > $2
			ORDER BY endpoint_id,version LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list webhook secret rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var endpointID string
			var version int64
			var record kms.WrappedKeyRecord
			if err := rows.Scan(&endpointID, &version, &record.Provider, &record.Reference, &record.Version, &record.Algorithm, &record.Ciphertext); err != nil {
				return err
			}
			parsed, err := id.ParseWebhookEndpoint(endpointID)
			if err != nil {
				return keyrewrap.ErrUnavailable
			}
			targets = append(targets, keyrewrap.Target{
				TenantID: scope.ID(), Class: adapter.Class(),
				Object: secretObject(parsed, version), Version: version,
			})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap replaces one signing-secret wrapping while keeping the exact secret
// version and its signature meaning.
func (adapter *WebhookSecretAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil || target.Version < 1 {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	endpoint, version, err := parseSecretObject(target.Object)
	if err != nil {
		return keyrewrap.Outcome{}, err
	}
	purpose, err := kms.NewPurpose(secretPurpose)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	authenticatedContext := []byte(fmt.Sprintf("v1\n%s\n%s\n%d", scope.ID().String(), endpoint.String(), version))
	var outcome keyrewrap.Outcome
	err = adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		var record kms.WrappedKeyRecord
		err := tx.QueryRow(ctx, `SELECT provider,reference,key_version,algorithm,ciphertext
			FROM idenqa.webhook_secrets WHERE tenant_id=$1 AND endpoint_id=$2 AND version=$3 FOR UPDATE`,
			scope.ID().String(), endpoint.String(), version).Scan(
			&record.Provider, &record.Reference, &record.Version, &record.Algorithm, &record.Ciphertext)
		if errors.Is(err, pgx.ErrNoRows) {
			return keyrewrap.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("load webhook secret: %w", err)
		}
		next, changed, err := keyrewrap.RewrapRecord(ctx, adapter.store.wrapper, adapter.store.unwrapper, purpose, record, authenticatedContext)
		if err != nil {
			return err
		}
		if !changed {
			outcome = keyrewrap.Outcome{Changed: false, Wrapping: record}

			return nil
		}
		if err := setRewrapFlag(ctx, tx); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.webhook_secrets SET provider=$4,reference=$5,
			key_version=$6,algorithm=$7,ciphertext=$8
			WHERE tenant_id=$1 AND endpoint_id=$2 AND version=$3`,
			scope.ID().String(), endpoint.String(), version, next.Provider, next.Reference,
			next.Version, next.Algorithm, next.Ciphertext)
		if err != nil {
			return fmt.Errorf("rewrap webhook secret: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return keyrewrap.ErrConflict
		}
		outcome = keyrewrap.Outcome{Changed: true, Wrapping: next}

		return nil
	})
	if err != nil {
		return keyrewrap.Outcome{}, err
	}

	return outcome, nil
}

// NewHMACKeyAdapter rewraps tenant predictable-identifier HMAC key material.
func NewHMACKeyAdapter(store *Store) (*HMACKeyAdapter, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &HMACKeyAdapter{store: store}, nil
}

// HMACKeyAdapter is the tenant HMAC key class.
type HMACKeyAdapter struct{ store *Store }

// Class returns the HMAC key class.
func (adapter *HMACKeyAdapter) Class() keyrewrap.Class { return keyrewrap.ClassHMACKey }

// List pages HMAC key versions by domain then version.
func (adapter *HMACKeyAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT domain,version
			FROM idenqa.hmac_keys
			WHERE tenant_id=$1 AND (domain || '|' || lpad(version::text,9,'0')) > $2
			ORDER BY domain,version LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list hmac key rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var domain string
			var version int64
			if err := rows.Scan(&domain, &version); err != nil {
				return err
			}
			targets = append(targets, keyrewrap.Target{
				TenantID: scope.ID(), Class: adapter.Class(),
				Object: hmacObject(domain, version), Version: version,
			})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap replaces one HMAC key wrapping under the recorded-rewrap trigger
// transition. The state, identifier, and creation instant are unchanged.
func (adapter *HMACKeyAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil || target.Version < 1 {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	domain, version, err := parseHMACObject(target.Object)
	if err != nil {
		return keyrewrap.Outcome{}, err
	}
	purpose, err := kms.NewPurpose(domain)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	var outcome keyrewrap.Outcome
	err = adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		var identifier string
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT id,wrapped_key FROM idenqa.hmac_keys
			WHERE tenant_id=$1 AND domain=$2 AND version=$3 FOR UPDATE`,
			scope.ID().String(), domain, version).Scan(&identifier, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return keyrewrap.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("load hmac key wrapping: %w", err)
		}
		previous, err := decodeWrapped(raw)
		if err != nil {
			return err
		}
		authenticatedContext, err := json.Marshal([]string{"idenqa.hmac-key.v1", domain, scope.ID().String(), identifier, strconv.FormatInt(version, 10)})
		if err != nil {
			return keyrewrap.ErrInvalid
		}
		defer clear(authenticatedContext)
		next, changed, err := keyrewrap.RewrapRecord(ctx, adapter.store.wrapper, adapter.store.unwrapper, purpose, previous, authenticatedContext)
		if err != nil {
			return err
		}
		if !changed {
			outcome = keyrewrap.Outcome{Changed: false, Wrapping: previous}

			return nil
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return keyrewrap.ErrInvalid
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.hmac_keys SET wrapped_key=$4,rewrapped_at=$5
			WHERE tenant_id=$1 AND domain=$2 AND version=$3`,
			scope.ID().String(), domain, version, encoded, adapter.store.now())
		if err != nil {
			return fmt.Errorf("rewrap hmac key: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return keyrewrap.ErrConflict
		}
		outcome = keyrewrap.Outcome{Changed: true, Wrapping: next}

		return nil
	})
	if err != nil {
		return keyrewrap.Outcome{}, err
	}

	return outcome, nil
}

// NewIdentityLookupAdapter rewraps tenant/region identity lookup keys.
func NewIdentityLookupAdapter(store *Store) (*IdentityLookupAdapter, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &IdentityLookupAdapter{store: store}, nil
}

// IdentityLookupAdapter is the identity lookup-key class.
type IdentityLookupAdapter struct{ store *Store }

// Class returns the identity lookup-key class.
func (adapter *IdentityLookupAdapter) Class() keyrewrap.Class {
	return keyrewrap.ClassIdentityLookupKey
}

// List pages lookup keys in region order.
func (adapter *IdentityLookupAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT region FROM idenqa.identity_keys
			WHERE tenant_id=$1 AND region>$2 ORDER BY region LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list identity lookup rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var region string
			if err := rows.Scan(&region); err != nil {
				return err
			}
			targets = append(targets, keyrewrap.Target{TenantID: scope.ID(), Class: adapter.Class(), Object: region})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap replaces one identity lookup-key wrapping.
func (adapter *IdentityLookupAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	const domain = "identity.lookup.v1"
	purpose, err := kms.NewPurpose(domain)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	authenticatedContext, err := json.Marshal([]string{domain, scope.ID().String(), target.Object, "lookup", "1"})
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	defer clear(authenticatedContext)

	return adapter.store.rewrapJSONKey(ctx, target, purpose, authenticatedContext, `UPDATE idenqa.identity_keys SET wrapped_key=$3 WHERE tenant_id=$1 AND region=$2`)
}

// NewIdentitySubjectAdapter rewraps per-subject external-reference keys.
func NewIdentitySubjectAdapter(store *Store) (*IdentitySubjectAdapter, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &IdentitySubjectAdapter{store: store}, nil
}

// IdentitySubjectAdapter is the per-subject key class.
type IdentitySubjectAdapter struct{ store *Store }

// Class returns the identity subject-key class.
func (adapter *IdentitySubjectAdapter) Class() keyrewrap.Class {
	return keyrewrap.ClassIdentitySubjectKey
}

// List pages subject keys in subject order.
func (adapter *IdentitySubjectAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.identity_subjects
			WHERE tenant_id=$1 AND id>$2 AND wrapped_key IS NOT NULL
			ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list identity subject rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var identifier string
			if err := rows.Scan(&identifier); err != nil {
				return err
			}
			parsed, err := id.ParseSubject(identifier)
			if err != nil {
				return keyrewrap.ErrUnavailable
			}
			targets = append(targets, keyrewrap.Target{TenantID: scope.ID(), Class: adapter.Class(), Object: parsed.String()})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap replaces one per-subject key wrapping.
func (adapter *IdentitySubjectAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	var region string
	err = adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}

		return tx.QueryRow(ctx, `SELECT region FROM idenqa.identity_subjects WHERE tenant_id=$1 AND id=$2 AND wrapped_key IS NOT NULL`,
			scope.ID().String(), target.Object).Scan(&region)
	})
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrUnavailable
	}
	const domain = "identity.subject.v1"
	purpose, err := kms.NewPurpose(domain)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	authenticatedContext, err := json.Marshal([]string{domain, scope.ID().String(), region, target.Object, "1"})
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	defer clear(authenticatedContext)

	return adapter.store.rewrapJSONKey(ctx, target, purpose, authenticatedContext, `UPDATE idenqa.identity_subjects SET wrapped_key=$3 WHERE tenant_id=$1 AND id=$2`)
}

// NewFraudKeyAdapter rewraps tenant/region fraud correlation keys.
func NewFraudKeyAdapter(store *Store) (*FraudKeyAdapter, error) {
	if store == nil {
		return nil, keyrewrap.ErrInvalid
	}

	return &FraudKeyAdapter{store: store}, nil
}

// FraudKeyAdapter is the fraud correlation-key class.
type FraudKeyAdapter struct{ store *Store }

// Class returns the fraud correlation-key class.
func (adapter *FraudKeyAdapter) Class() keyrewrap.Class { return keyrewrap.ClassFraudCorrelationKey }

// List pages fraud keys in region order.
func (adapter *FraudKeyAdapter) List(ctx context.Context, scope tenant.Scope, after string, limit int) ([]keyrewrap.Target, error) {
	targets := make([]keyrewrap.Target, 0, limit)
	err := adapter.store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT region FROM idenqa.fraud_keys
			WHERE tenant_id=$1 AND region>$2 ORDER BY region LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list fraud key rewrap targets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var region string
			if err := rows.Scan(&region); err != nil {
				return err
			}
			targets = append(targets, keyrewrap.Target{TenantID: scope.ID(), Class: adapter.Class(), Object: region})
		}

		return rows.Err()
	})
	if err != nil {
		return nil, err
	}

	return targets, nil
}

// Rewrap replaces one fraud correlation-key wrapping.
func (adapter *FraudKeyAdapter) Rewrap(ctx context.Context, target keyrewrap.Target) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	const domain = "fraud.correlation.v1"
	purpose, err := kms.NewPurpose(domain)
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	authenticatedContext, err := json.Marshal([]string{domain, scope.ID().String(), target.Object, "1"})
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	defer clear(authenticatedContext)

	return adapter.store.rewrapJSONKey(ctx, target, purpose, authenticatedContext, `UPDATE idenqa.fraud_keys SET wrapped_key=$3 WHERE tenant_id=$1 AND region=$2`)
}

// rewrapJSONKey rewraps one jsonb wrapping column with a class-supplied update
// statement and no aggregate version change.
func (store *Store) rewrapJSONKey(
	ctx context.Context,
	target keyrewrap.Target,
	purpose kms.Purpose,
	authenticatedContext []byte,
	updateSQL string,
) (keyrewrap.Outcome, error) {
	scope, err := target.Scope()
	if err != nil {
		return keyrewrap.Outcome{}, keyrewrap.ErrInvalid
	}
	var outcome keyrewrap.Outcome
	err = store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope.ID().String()); err != nil {
			return err
		}
		var raw []byte
		var err error
		switch target.Class {
		case keyrewrap.ClassIdentityLookupKey:
			err = tx.QueryRow(ctx, `SELECT wrapped_key FROM idenqa.identity_keys WHERE tenant_id=$1 AND region=$2 FOR UPDATE`,
				scope.ID().String(), target.Object).Scan(&raw)
		case keyrewrap.ClassIdentitySubjectKey:
			err = tx.QueryRow(ctx, `SELECT wrapped_key FROM idenqa.identity_subjects WHERE tenant_id=$1 AND id=$2 AND wrapped_key IS NOT NULL FOR UPDATE`,
				scope.ID().String(), target.Object).Scan(&raw)
		case keyrewrap.ClassFraudCorrelationKey:
			err = tx.QueryRow(ctx, `SELECT wrapped_key FROM idenqa.fraud_keys WHERE tenant_id=$1 AND region=$2 FOR UPDATE`,
				scope.ID().String(), target.Object).Scan(&raw)
		default:
			return keyrewrap.ErrInvalid
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return keyrewrap.ErrUnavailable
		}
		if err != nil {
			return fmt.Errorf("load key wrapping: %w", err)
		}
		previous, err := decodeWrapped(raw)
		if err != nil {
			return err
		}
		next, changed, err := keyrewrap.RewrapRecord(ctx, store.wrapper, store.unwrapper, purpose, previous, authenticatedContext)
		if err != nil {
			return err
		}
		if !changed {
			outcome = keyrewrap.Outcome{Changed: false, Wrapping: previous}

			return nil
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return keyrewrap.ErrInvalid
		}
		tag, err := tx.Exec(ctx, updateSQL, scope.ID().String(), target.Object, encoded)
		if err != nil {
			return fmt.Errorf("rewrap key: %w", err)
		}
		if tag.RowsAffected() != 1 {
			return keyrewrap.ErrConflict
		}
		outcome = keyrewrap.Outcome{Changed: true, Wrapping: next}

		return nil
	})
	if err != nil {
		return keyrewrap.Outcome{}, err
	}

	return outcome, nil
}

func decodeWrapped(raw []byte) (kms.WrappedKeyRecord, error) {
	var record kms.WrappedKeyRecord
	if len(raw) == 0 || len(raw) > 64*1024 || json.Unmarshal(raw, &record) != nil {
		return kms.WrappedKeyRecord{}, keyrewrap.ErrUnavailable
	}
	if _, err := kms.NewWrappedKey(record); err != nil {
		return kms.WrappedKeyRecord{}, keyrewrap.ErrUnavailable
	}

	return record, nil
}

func restoreWrapping(body []byte, provider, reference, keyVersion, algorithm *string) (kms.WrappedKeyRecord, error) {
	present := 0
	for _, value := range []*string{provider, reference, keyVersion, algorithm} {
		if value != nil {
			present++
		}
	}
	if present != 4 || len(body) == 0 {
		return kms.WrappedKeyRecord{}, keyrewrap.ErrUnavailable
	}
	record := kms.WrappedKeyRecord{
		Provider: *provider, Reference: *reference, Version: *keyVersion,
		Algorithm: *algorithm, Ciphertext: body,
	}
	if _, err := kms.NewWrappedKey(record); err != nil {
		return kms.WrappedKeyRecord{}, keyrewrap.ErrUnavailable
	}

	return record, nil
}

func secretObject(endpoint id.WebhookEndpoint, version int64) string {
	return fmt.Sprintf("%s|%09d", endpoint.String(), version)
}

func parseSecretObject(value string) (id.WebhookEndpoint, int64, error) {
	endpoint, version, ok := splitObject(value)
	if !ok {
		return id.WebhookEndpoint{}, 0, keyrewrap.ErrInvalid
	}
	parsed, err := id.ParseWebhookEndpoint(endpoint)
	if err != nil || version < 1 {
		return id.WebhookEndpoint{}, 0, keyrewrap.ErrInvalid
	}

	return parsed, version, nil
}

func hmacObject(domain string, version int64) string {
	return fmt.Sprintf("%s|%09d", domain, version)
}

func parseHMACObject(value string) (string, int64, error) {
	domain, version, ok := splitObject(value)
	if !ok || domain == "" {
		return "", 0, keyrewrap.ErrInvalid
	}
	if _, err := kms.NewPurpose(domain); err != nil {
		return "", 0, keyrewrap.ErrInvalid
	}

	return domain, version, nil
}

func splitObject(value string) (string, int64, bool) {
	index := strings.LastIndexByte(value, '|')
	if index <= 0 || index+1 >= len(value) {
		return "", 0, false
	}
	version, err := strconv.ParseInt(value[index+1:], 10, 64)
	if err != nil || version < 1 {
		return "", 0, false
	}

	return value[:index], version, true
}
