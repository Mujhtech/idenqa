package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	usagev1 "github.com/Mujhtech/idenqa/contracts/usage/v1"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
)

// AppendWithin records one content-free observation in its owning state
// transition transaction. It neither configures a meter nor establishes a charge.
// The tenant usage lock and database occurrence time permit a future consistent
// coverage read to exclude in-flight earlier events without guessing from emptiness.
func AppendWithin(ctx context.Context, tx pg.Transaction, receipt usagev1.Receipt, identity string) error {
	switch receipt.Event {
	case "provider_dispatch", "provider_terminal", "model_dispatch", "model_terminal", "verification_completed":
	default:
		return errors.New("invalid usage event")
	}
	if receipt.CoreTenantID == "" || identity == "" {
		return errors.New("invalid usage identity")
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('idenqa.usage:'||$1,0))`, receipt.CoreTenantID); err != nil {
		return fmt.Errorf("lock usage source: %w", err)
	}
	at, err := sourceClock(ctx, tx, receipt.CoreTenantID)
	if err != nil {
		return fmt.Errorf("read usage source clock: %w", err)
	}
	seed := strings.ReplaceAll(receipt.Event, "_", "-") + "\x00" + receipt.CoreTenantID + "\x00" + identity
	digest := sha256.Sum256([]byte(seed))
	receipt.Schema = usagev1.Schema
	receipt.ID = hex.EncodeToString(digest[:])
	receipt.Quantity = 1
	receipt.OccurredAt = at.UTC().Truncate(time.Microsecond)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO idenqa.usage_receipts(tenant_id,id,receipt,created_at) VALUES($1,$2,$3,$4)`, receipt.CoreTenantID, receipt.ID, raw, receipt.OccurredAt); err != nil {
		return fmt.Errorf("append usage source: %w", err)
	}
	return nil
}

func sourceClock(ctx context.Context, tx pg.Transaction, tenant string) (time.Time, error) {
	var at time.Time
	err := tx.QueryRow(ctx, `WITH observation AS (SELECT clock_timestamp() AS at)
INSERT INTO idenqa.usage_source_clocks(tenant_id,through,covered_from)
SELECT $1,at,at FROM observation
ON CONFLICT(tenant_id) DO UPDATE SET through=greatest(usage_source_clocks.through+interval '1 microsecond',excluded.through)
RETURNING through`, tenant).Scan(&at)
	return at, err
}
