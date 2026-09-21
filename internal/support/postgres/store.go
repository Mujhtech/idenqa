// Package postgres implements tenant-forced support-access persistence.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/support"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

const listLimit = 100

type transactionRunner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

type identifierGenerator interface {
	New(id.Prefix) (id.Value, error)
}

// Store persists support grants, break-glass requests, and use records.
type Store struct {
	pool        transactionRunner
	identifiers identifierGenerator
}

// New composes support persistence with an explicit identifier generator.
func New(pool transactionRunner, identifiers identifierGenerator) (*Store, error) {
	if pool == nil || identifiers == nil {
		return nil, support.ErrInvalid
	}

	return &Store{pool: pool, identifiers: identifiers}, nil
}

func setScope(ctx context.Context, tx pg.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return support.ErrInvalid
	}
	var value string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value)
}

// Execute commits one validated support command, its audit record, and its
// idempotency result atomically.
func (store *Store) Execute(ctx context.Context, scope tenant.Scope, command support.Command) (support.Result, error) {
	if store == nil || scope.ID().IsZero() || command.Operation == "" {
		return support.Result{}, support.ErrInvalid
	}
	var result support.Result
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
		if err := store.mutate(ctx, tx, scope, command, &result); err != nil {
			return err
		}
		digest, err := supportDigest(command.Operation, result)
		if err != nil {
			return err
		}
		aggregate := "support." + command.Identifier
		if aggregate == "support." {
			aggregate = "support.grant." + command.Grantee
		}
		if _, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
			EventID: "support." + command.Operation + "." + digest, EventType: "support." + command.Operation,
			AggregateID: aggregate, ActorID: command.ActorKeyID, EventDigest: digest, OccurredAt: command.At,
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

func (store *Store) mutate(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	switch command.Operation {
	case "grant":
		return store.grant(ctx, tx, scope, command, result)
	case "grant_revoke":
		return store.revokeGrant(ctx, tx, scope, command, result)
	case "break_glass_request":
		return store.requestEmergency(ctx, tx, scope, command, result)
	case "break_glass_approve":
		return store.approveEmergency(ctx, tx, scope, command, result)
	case "break_glass_deny":
		return store.denyEmergency(ctx, tx, scope, command, result)
	case "break_glass_revoke":
		return store.revokeEmergency(ctx, tx, scope, command, result)
	case "break_glass_use":
		return store.useEmergency(ctx, tx, scope, command, result)
	default:
		return support.ErrInvalid
	}
}

func (store *Store) grant(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	identifier, err := store.identifiers.New(support.GrantIDPrefix)
	if err != nil {
		return support.ErrUnavailable
	}
	patterns, err := json.Marshal(command.Patterns)
	if err != nil {
		return support.ErrInvalid
	}
	permissions, err := json.Marshal(command.Permissions)
	if err != nil {
		return support.ErrInvalid
	}
	expiresAt := command.At.Add(command.Duration)
	grant := support.Grant{
		ID: identifier.String(), Grantee: command.Grantee, Patterns: command.Patterns,
		Permissions: command.Permissions, Reason: command.Reason, GrantedBy: command.ActorKeyID,
		State: support.StateActive, Version: 1, StartsAt: command.At, ExpiresAt: expiresAt,
		CreatedAt: command.At, UpdatedAt: command.At,
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.support_grants
		(tenant_id,id,grantee,patterns,permissions,reason,granted_by,state,version,starts_at,expires_at,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,'active',1,$8,$9,$10,$10)`,
		scope.ID().String(), grant.ID, grant.Grantee, patterns, permissions, grant.Reason, grant.GrantedBy,
		grant.StartsAt, grant.ExpiresAt, grant.CreatedAt); err != nil {
		return err
	}
	*result = support.Result{Grant: &grant}

	return nil
}

func (store *Store) revokeGrant(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	var state string
	var version int64
	err := tx.QueryRow(ctx, `SELECT state,version FROM idenqa.support_grants WHERE tenant_id=$1 AND id=$2 FOR UPDATE`,
		scope.ID().String(), command.Identifier).Scan(&state, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return support.ErrNotFound
	}
	if err != nil {
		return err
	}
	if version != command.ExpectedVersion {
		return support.ErrConflict
	}
	if support.State(state) != support.StateActive {
		return support.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.support_grants
		SET state='revoked',version=version+1,updated_at=$3,revoked_at=$3,revoked_by=$4,revocation_reason=$5
		WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), command.Identifier, command.At, command.ActorKeyID, command.Reason); err != nil {
		return err
	}
	grant, err := store.readGrant(ctx, tx, scope, command.Identifier)
	if err != nil {
		return err
	}
	*result = support.Result{Grant: &grant}

	return nil
}

func (store *Store) requestEmergency(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	identifier, err := store.identifiers.New(support.EmergencyIDPrefix)
	if err != nil {
		return support.ErrUnavailable
	}
	permissions, err := json.Marshal(command.Permissions)
	if err != nil {
		return support.ErrInvalid
	}
	emergency := support.Emergency{
		ID: identifier.String(), Requester: command.ActorKeyID, Reason: command.Reason,
		Permissions: command.Permissions, Duration: command.Duration, State: support.StateRequested,
		Version: 1, RequestedAt: command.At, ApprovalExpiresAt: command.At.Add(support.ApprovalWindow),
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.break_glass_requests
		(tenant_id,id,requester,reason,permissions,duration_seconds,state,version,requested_at,approval_expires_at,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,'requested',1,$7,$8,$7,$7)`,
		scope.ID().String(), emergency.ID, emergency.Requester, emergency.Reason, permissions,
		int64(emergency.Duration/time.Second), emergency.RequestedAt, emergency.ApprovalExpiresAt); err != nil {
		return err
	}
	*result = support.Result{Emergency: &emergency}

	return nil
}

func (store *Store) approveEmergency(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	emergency, err := store.lockEmergency(ctx, tx, scope, command)
	if err != nil {
		return err
	}
	if emergency.State != support.StateRequested {
		return support.ErrConflict
	}
	if !command.At.Before(emergency.ApprovalExpiresAt) {
		return support.ErrExpired
	}
	if emergency.Requester == command.ActorKeyID {
		return support.ErrForbidden
	}
	usableUntil := command.At.Add(emergency.Duration)
	if _, err := tx.Exec(ctx, `UPDATE idenqa.break_glass_requests
		SET state='approved',version=version+1,updated_at=$3,approved_at=$3,approved_by=$4,usable_until=$5
		WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), command.Identifier, command.At, command.ActorKeyID, usableUntil); err != nil {
		return err
	}
	updated, err := store.readEmergency(ctx, tx, scope, command.Identifier)
	if err != nil {
		return err
	}
	*result = support.Result{Emergency: &updated}

	return nil
}

func (store *Store) denyEmergency(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	emergency, err := store.lockEmergency(ctx, tx, scope, command)
	if err != nil {
		return err
	}
	if emergency.State != support.StateRequested {
		return support.ErrConflict
	}
	if emergency.Requester == command.ActorKeyID {
		return support.ErrForbidden
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.break_glass_requests
		SET state='denied',version=version+1,updated_at=$3,denied_at=$3,denied_by=$4
		WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), command.Identifier, command.At, command.ActorKeyID); err != nil {
		return err
	}
	updated, err := store.readEmergency(ctx, tx, scope, command.Identifier)
	if err != nil {
		return err
	}
	*result = support.Result{Emergency: &updated}

	return nil
}

func (store *Store) revokeEmergency(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	emergency, err := store.lockEmergency(ctx, tx, scope, command)
	if err != nil {
		return err
	}
	if emergency.State != support.StateApproved && emergency.State != support.StateRequested {
		return support.ErrConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.break_glass_requests
		SET state='revoked',version=version+1,updated_at=$3,revoked_at=$3,revoked_by=$4,revocation_reason=$5
		WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), command.Identifier, command.At, command.ActorKeyID, command.Reason); err != nil {
		return err
	}
	updated, err := store.readEmergency(ctx, tx, scope, command.Identifier)
	if err != nil {
		return err
	}
	*result = support.Result{Emergency: &updated}

	return nil
}

func (store *Store) useEmergency(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command, result *support.Result) error {
	emergency, err := store.lockEmergency(ctx, tx, scope, command)
	if err != nil {
		return err
	}
	if emergency.State != support.StateApproved {
		return support.ErrConflict
	}
	if emergency.UsableUntil == nil || !command.At.Before(*emergency.UsableUntil) {
		return support.ErrExpired
	}
	permission := command.Permissions[0]
	allowed := false
	for _, approved := range emergency.Permissions {
		if approved == permission {
			allowed = true
			break
		}
	}
	if !allowed {
		return support.ErrForbidden
	}
	var sequence int64
	if err := tx.QueryRow(ctx, `SELECT COALESCE(MAX(sequence),0)+1 FROM idenqa.break_glass_uses WHERE tenant_id=$1 AND request_id=$2`,
		scope.ID().String(), command.Identifier).Scan(&sequence); err != nil {
		return err
	}
	if sequence > support.MaximumBreakGlassUses {
		return support.ErrInvalid
	}
	use := support.Use{Sequence: sequence, Permission: permission, Target: command.Target, Actor: command.ActorKeyID, UsedAt: command.At}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.break_glass_uses(tenant_id,request_id,sequence,permission,target,actor,used_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), command.Identifier, sequence, use.Permission, use.Target, use.Actor, use.UsedAt); err != nil {
		return err
	}
	uses, err := store.readUses(ctx, tx, scope, command.Identifier)
	if err != nil {
		return err
	}
	*result = support.Result{Emergency: &emergency, Uses: uses}

	return nil
}

func (store *Store) lockEmergency(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command support.Command) (support.Emergency, error) {
	var state string
	var version int64
	var requester string
	var reason string
	var permissions []byte
	var duration int64
	var requestedAt time.Time
	var approvalExpiresAt time.Time
	var usableUntil *time.Time
	err := tx.QueryRow(ctx, `SELECT state,version,requester,reason,permissions,duration_seconds,requested_at,approval_expires_at,usable_until
		FROM idenqa.break_glass_requests WHERE tenant_id=$1 AND id=$2 FOR UPDATE`,
		scope.ID().String(), command.Identifier).Scan(&state, &version, &requester, &reason, &permissions, &duration, &requestedAt, &approvalExpiresAt, &usableUntil)
	if errors.Is(err, pgx.ErrNoRows) {
		return support.Emergency{}, support.ErrNotFound
	}
	if err != nil {
		return support.Emergency{}, err
	}
	if version != command.ExpectedVersion {
		return support.Emergency{}, support.ErrConflict
	}
	var decoded []string
	if json.Unmarshal(permissions, &decoded) != nil {
		return support.Emergency{}, support.ErrUnavailable
	}

	return support.Emergency{
		ID: command.Identifier, Requester: requester, Reason: reason, Permissions: decoded,
		Duration: time.Duration(duration) * time.Second, State: support.State(state), Version: version,
		RequestedAt: requestedAt.UTC(), ApprovalExpiresAt: approvalExpiresAt.UTC(), UsableUntil: usableUntil,
	}, nil
}

// Read returns one grant, one emergency with its uses, or a bounded list.
func (store *Store) Read(ctx context.Context, scope tenant.Scope, kind, reference string) (support.Result, error) {
	var result support.Result
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		switch kind {
		case "grant":
			grant, err := store.readGrant(ctx, tx, scope, reference)
			if err != nil {
				return err
			}
			result.Grant = &grant
		case "grant_grantee":
			grant, err := store.readGrantForGrantee(ctx, tx, scope, reference)
			if err != nil {
				return err
			}
			result.Grant = &grant
		case "grants":
			grants, err := store.readGrants(ctx, tx, scope)
			if err != nil {
				return err
			}
			result.Grants = grants
		case "emergency":
			emergency, err := store.readEmergency(ctx, tx, scope, reference)
			if err != nil {
				return err
			}
			uses, err := store.readUses(ctx, tx, scope, reference)
			if err != nil {
				return err
			}
			result.Emergency, result.Uses = &emergency, uses
		default:
			return support.ErrInvalid
		}

		return nil
	})
	return result, err
}

func (store *Store) readGrant(ctx context.Context, tx pg.Transaction, scope tenant.Scope, identifier string) (support.Grant, error) {
	var grant support.Grant
	var patterns, permissions []byte
	var state string
	var revokedAt *time.Time
	var revokedBy, revocationReason *string
	err := tx.QueryRow(ctx, `SELECT id,grantee,patterns,permissions,reason,granted_by,state,version,starts_at,expires_at,created_at,updated_at,revoked_at,revoked_by,revocation_reason
		FROM idenqa.support_grants WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier).
		Scan(&grant.ID, &grant.Grantee, &patterns, &permissions, &grant.Reason, &grant.GrantedBy, &state, &grant.Version,
			&grant.StartsAt, &grant.ExpiresAt, &grant.CreatedAt, &grant.UpdatedAt, &revokedAt, &revokedBy, &revocationReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return grant, support.ErrNotFound
	}
	if err != nil {
		return grant, err
	}
	if json.Unmarshal(patterns, &grant.Patterns) != nil || json.Unmarshal(permissions, &grant.Permissions) != nil {
		return grant, support.ErrUnavailable
	}
	grant.State, grant.RevokedAt, grant.RevokedBy, grant.RevocationReason = support.State(state), revokedAt, revokedBy, revocationReason
	grant.StartsAt, grant.ExpiresAt = grant.StartsAt.UTC(), grant.ExpiresAt.UTC()
	grant.CreatedAt, grant.UpdatedAt = grant.CreatedAt.UTC(), grant.UpdatedAt.UTC()

	return grant, nil
}

func (store *Store) readGrantForGrantee(ctx context.Context, tx pg.Transaction, scope tenant.Scope, grantee string) (support.Grant, error) {
	var identifier string
	err := tx.QueryRow(ctx, `SELECT id FROM idenqa.support_grants WHERE tenant_id=$1 AND grantee=$2 AND state='active'
		ORDER BY expires_at DESC LIMIT 1`, scope.ID().String(), grantee).Scan(&identifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return support.Grant{}, support.ErrNotFound
	}
	if err != nil {
		return support.Grant{}, err
	}

	return store.readGrant(ctx, tx, scope, identifier)
}

func (store *Store) readGrants(ctx context.Context, tx pg.Transaction, scope tenant.Scope) ([]support.Grant, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM idenqa.support_grants WHERE tenant_id=$1 ORDER BY created_at DESC LIMIT $2`,
		scope.ID().String(), listLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var identifiers []string
	for rows.Next() {
		var identifier string
		if err := rows.Scan(&identifier); err != nil {
			return nil, err
		}
		identifiers = append(identifiers, identifier)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	grants := make([]support.Grant, 0, len(identifiers))
	for _, identifier := range identifiers {
		grant, err := store.readGrant(ctx, tx, scope, identifier)
		if err != nil {
			return nil, err
		}
		grants = append(grants, grant)
	}

	return grants, nil
}

func (store *Store) readEmergency(ctx context.Context, tx pg.Transaction, scope tenant.Scope, identifier string) (support.Emergency, error) {
	var emergency support.Emergency
	var permissions []byte
	var duration int64
	var state string
	var approvedAt, deniedAt, revokedAt *time.Time
	var approvedBy, deniedBy, revokedBy, revocationReason *string
	err := tx.QueryRow(ctx, `SELECT id,requester,reason,permissions,duration_seconds,state,version,requested_at,approval_expires_at,approved_at,approved_by,usable_until,denied_at,denied_by,revoked_at,revoked_by,revocation_reason
		FROM idenqa.break_glass_requests WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier).
		Scan(&emergency.ID, &emergency.Requester, &emergency.Reason, &permissions, &duration, &state, &emergency.Version,
			&emergency.RequestedAt, &emergency.ApprovalExpiresAt, &approvedAt, &approvedBy, &emergency.UsableUntil,
			&deniedAt, &deniedBy, &revokedAt, &revokedBy, &revocationReason)
	if errors.Is(err, pgx.ErrNoRows) {
		return emergency, support.ErrNotFound
	}
	if err != nil {
		return emergency, err
	}
	if json.Unmarshal(permissions, &emergency.Permissions) != nil {
		return emergency, support.ErrUnavailable
	}
	emergency.Duration = time.Duration(duration) * time.Second
	emergency.State = support.State(state)
	emergency.ApprovedAt, emergency.ApprovedBy = approvedAt, approvedBy
	emergency.DeniedAt, emergency.DeniedBy = deniedAt, deniedBy
	emergency.RevokedAt, emergency.RevokedBy, emergency.RevocationReason = revokedAt, revokedBy, revocationReason
	emergency.RequestedAt, emergency.ApprovalExpiresAt = emergency.RequestedAt.UTC(), emergency.ApprovalExpiresAt.UTC()

	return emergency, nil
}

func (store *Store) readUses(ctx context.Context, tx pg.Transaction, scope tenant.Scope, identifier string) ([]support.Use, error) {
	rows, err := tx.Query(ctx, `SELECT sequence,permission,target,actor,used_at FROM idenqa.break_glass_uses
		WHERE tenant_id=$1 AND request_id=$2 ORDER BY sequence LIMIT $3`, scope.ID().String(), identifier, int32(support.MaximumBreakGlassUses))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	uses := make([]support.Use, 0, 8)
	for rows.Next() {
		var use support.Use
		if err := rows.Scan(&use.Sequence, &use.Permission, &use.Target, &use.Actor, &use.UsedAt); err != nil {
			return nil, err
		}
		use.UsedAt = use.UsedAt.UTC()
		uses = append(uses, use)
	}

	return uses, rows.Err()
}

func supportDigest(operation string, result support.Result) (string, error) {
	var canonical any
	switch {
	case result.Grant != nil:
		canonical = struct {
			Operation string
			ID        string
			Version   int64
			State     support.State
		}{operation, result.Grant.ID, result.Grant.Version, result.Grant.State}
	case result.Emergency != nil:
		canonical = struct {
			Operation string
			ID        string
			Version   int64
			State     support.State
		}{operation, result.Emergency.ID, result.Emergency.Version, result.Emergency.State}
	default:
		canonical = operation
	}
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", support.ErrUnavailable
	}

	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}
