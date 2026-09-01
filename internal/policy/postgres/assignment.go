package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// SessionPolicySelector reads the immutable policy assignment pinned by
// verification-session creation. Tenant scope must already be forced on tx.
type SessionPolicySelector struct{}

// SelectPolicy returns no fallback for upgraded sessions without an assignment.
func (SessionPolicySelector) SelectPolicy(
	ctx context.Context,
	tx platformpostgres.Transaction,
	scope tenant.Scope,
	verificationID id.Verification,
	_ time.Time,
) (id.Policy, error) {
	if tx == nil || scope.ID().IsZero() || verificationID.IsZero() {
		return id.Policy{}, fmt.Errorf("%w: session policy assignment", policy.ErrInvalid)
	}
	var encoded *string
	err := tx.QueryRow(ctx, `
SELECT policy_id
FROM idenqa.verification_sessions
WHERE tenant_id = $1 AND id = $2 AND policy_id IS NOT NULL
`, scope.ID().String(), verificationID.String()).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) || encoded == nil {
		return id.Policy{}, fmt.Errorf("%w: session policy assignment", policy.ErrInvalid)
	}
	if err != nil {
		return id.Policy{}, fmt.Errorf("select session policy assignment: %w", err)
	}
	identifier, err := id.ParsePolicy(*encoded)
	if err != nil {
		return id.Policy{}, fmt.Errorf("parse session policy assignment: %w", err)
	}
	return identifier, nil
}

var _ PolicySelector = SessionPolicySelector{}
