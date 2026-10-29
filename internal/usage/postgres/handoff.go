package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"

	usagev1 "github.com/Mujhtech/idenqa/contracts/usage/v1"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
)

var handoffToken = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

// Handoff binds a privileged ownership/routing transition to exact prior state.
type Handoff struct {
	Tenant, ExpectedEpoch, ExpectedConsumer, ExpectedRouting string
	Epoch, Consumer, Routing, Actor, ReasonReference         string
	CarryPending                                             bool
}

// HandoffCoverage returns the new floor and any proven preceding coverage.
type HandoffCoverage struct {
	usagev1.Coverage
	Previous usagev1.Coverage `json:"previous"`
}

// Handoff starts a new, nonhistorical coverage epoch after an exact privileged
// compare-and-swap. Pending-preserving mode keeps the same owned consumer;
// changing signing consumers requires a drained outbox.
// No receipt is deleted, re-acknowledged or reassigned to manufacture history.
func (s *Store) Handoff(ctx context.Context, in Handoff) (HandoffCoverage, error) {
	if !handoffToken.MatchString(in.Tenant) || !handoffToken.MatchString(in.ExpectedEpoch) || !handoffToken.MatchString(in.Epoch) || in.ExpectedEpoch == in.Epoch || !subscriptionID.MatchString(in.Consumer) || !subscriptionID.MatchString(in.Routing) || !handoffToken.MatchString(in.Actor) || !handoffToken.MatchString(in.ReasonReference) || (in.ExpectedConsumer != "" && !subscriptionID.MatchString(in.ExpectedConsumer)) || (in.ExpectedRouting != "" && !subscriptionID.MatchString(in.ExpectedRouting)) || (in.ExpectedConsumer == "") != (in.ExpectedRouting == "") {
		return HandoffCoverage{}, errors.New("invalid usage source handoff")
	}
	out := HandoffCoverage{Coverage: usagev1.Coverage{Schema: "idenqa-core.usage-coverage.v1", CoreTenantID: in.Tenant, ConsumerID: in.Consumer, RoutingDigest: in.Routing, Epoch: in.Epoch}}
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		var privileged bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=current_user AND (rolsuper OR rolbypassrls))`).Scan(&privileged); err != nil {
			return err
		}
		if !privileged {
			return errors.New("usage handoff requires dedicated migration identity")
		}
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, in.Tenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('idenqa.usage:'||$1,0))`, in.Tenant); err != nil {
			return err
		}
		var epoch string
		var consumer, routing *string
		out.Previous = usagev1.Coverage{Schema: "idenqa-core.usage-coverage.v1", CoreTenantID: in.Tenant}
		if err := tx.QueryRow(ctx, `SELECT epoch,consumer_id,routing_digest,covered_from FROM idenqa.usage_source_clocks WHERE tenant_id=$1 FOR UPDATE`, in.Tenant).Scan(&epoch, &consumer, &routing, &out.Previous.From); err != nil {
			return err
		}
		value := func(v *string) string {
			if v == nil {
				return ""
			}
			return *v
		}
		if epoch != in.ExpectedEpoch || value(consumer) != in.ExpectedConsumer || value(routing) != in.ExpectedRouting {
			return errors.New("usage source handoff expectation conflict")
		}
		var pending int64
		digest := sha256.New()
		rows, err := tx.Query(ctx, `SELECT id,encode(sha256(convert_to(receipt::text,'UTF8')),'hex') FROM idenqa.usage_receipts WHERE tenant_id=$1 AND delivered_at IS NULL ORDER BY id`, in.Tenant)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, meaning string
			if err = rows.Scan(&id, &meaning); err != nil {
				break
			}
			_, _ = digest.Write([]byte(id + "\x00" + meaning + "\n"))
			pending++
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if pending > 0 && (!in.CarryPending || (consumer != nil && *consumer != in.Consumer)) {
			return errors.New("usage source must be drained before handoff")
		}
		if in.CarryPending {
			if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.usage_source_pending_transfer','true',true)`); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `WITH boundary AS (SELECT greatest(through+interval '1 microsecond',clock_timestamp()) AS at FROM idenqa.usage_source_clocks WHERE tenant_id=$1)
UPDATE idenqa.usage_source_clocks SET covered_from=boundary.at,through=boundary.at,epoch=$2,consumer_id=$3,routing_digest=$4 FROM boundary WHERE tenant_id=$1 RETURNING covered_from,through`, in.Tenant, in.Epoch, in.Consumer, in.Routing).Scan(&out.From, &out.Through); err != nil {
			return err
		}
		out.Previous.ConsumerID = value(consumer)
		out.Previous.RoutingDigest = value(routing)
		out.Previous.Epoch = epoch
		if pending == 0 {
			out.Previous.Through = out.From
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.usage_source_handoffs(tenant_id,epoch,previous_consumer,previous_routing,consumer_id,routing_digest,covered_from,actor_id,reason_reference,previous_from,pending_carried,pending_digest) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`, in.Tenant, in.Epoch, consumer, routing, in.Consumer, in.Routing, out.From, in.Actor, in.ReasonReference, out.Previous.From, pending, hex.EncodeToString(digest.Sum(nil)))
		return err
	})
	return out, err
}
