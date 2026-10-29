package postgres

import (
	"context"
	"errors"
	"regexp"
	"time"

	usagev1 "github.com/Mujhtech/idenqa/contracts/usage/v1"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
)

var subscriptionID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// ForSubscription returns a copy bound to one consumer and immutable route digest.
func (s *Store) ForSubscription(consumer, routing string) (*Store, error) {
	if s == nil || !subscriptionID.MatchString(consumer) || !subscriptionID.MatchString(routing) {
		return nil, errors.New("invalid usage subscription")
	}
	bound := *s
	bound.consumer = consumer
	bound.routing = routing
	return &bound, nil
}

func (s *Store) authorizeSource(ctx context.Context, tx pg.Transaction, tenant string) (time.Time, string, error) {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('idenqa.usage:'||$1,0))`, tenant); err != nil {
		return time.Time{}, "", err
	}
	if _, err := sourceClock(ctx, tx, tenant); err != nil {
		return time.Time{}, "", err
	}
	var from time.Time
	var epoch string
	var consumer, routing *string
	if err := tx.QueryRow(ctx, `SELECT covered_from,epoch,consumer_id,routing_digest FROM idenqa.usage_source_clocks WHERE tenant_id=$1 FOR UPDATE`, tenant).Scan(&from, &epoch, &consumer, &routing); err != nil {
		return from, epoch, err
	}
	if s.consumer == "" && s.routing == "" {
		if consumer != nil {
			return from, epoch, errors.New("usage source has an owned consumer")
		}
		return from, epoch, nil
	}
	if !subscriptionID.MatchString(s.consumer) || !subscriptionID.MatchString(s.routing) {
		return from, epoch, errors.New("invalid usage subscription")
	}
	if consumer != nil {
		if *consumer != s.consumer || routing == nil || *routing != s.routing {
			return from, epoch, errors.New("usage source consumer or routing conflict")
		}
		return from, epoch, nil
	}
	var delivered bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.usage_receipts WHERE tenant_id=$1 AND delivered_at IS NOT NULL)`, tenant).Scan(&delivered); err != nil {
		return from, epoch, err
	}
	if delivered {
		return from, epoch, errors.New("usage source needs an explicit historical handoff")
	}
	_, err := tx.Exec(ctx, `UPDATE idenqa.usage_source_clocks SET consumer_id=$2,routing_digest=$3 WHERE tenant_id=$1`, tenant, s.consumer, s.routing)
	return from, epoch, err
}

// ReadSubscription reads a bounded page under exact consumer ownership.
func (s *Store) ReadSubscription(ctx context.Context, tenant string, limit int, consumer, routing string) ([]usagev1.Receipt, error) {
	bound := *s
	bound.consumer = consumer
	bound.routing = routing
	return bound.Read(ctx, tenant, limit)
}

// AcknowledgeSubscription acknowledges an exact receipt under consumer ownership.
func (s *Store) AcknowledgeSubscription(ctx context.Context, tenant, id, digest string, at time.Time, consumer, routing string) error {
	bound := *s
	bound.consumer = consumer
	bound.routing = routing
	return bound.Acknowledge(ctx, tenant, id, digest, at)
}

// CoverageSubscription reads a serialized coverage barrier for one consumer.
func (s *Store) CoverageSubscription(ctx context.Context, tenant, consumer, routing string) (usagev1.Coverage, error) {
	bound := *s
	bound.consumer = consumer
	bound.routing = routing
	return bound.Coverage(ctx, tenant)
}
