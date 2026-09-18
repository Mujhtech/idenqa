package task

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
)

type coordinationClock struct{ at time.Time }

func (source coordinationClock) Now() time.Time { return source.at }

type coordinationRepository struct{ targets []DeliveryTarget }

func (source coordinationRepository) ListReadyDeliveries(context.Context, time.Time, int) ([]DeliveryTarget, error) {
	return source.targets, nil
}

type coordinationQueue struct{ intents []platformtask.Intent }

func (queue *coordinationQueue) Enqueue(_ context.Context, intents ...platformtask.Intent) error {
	queue.intents = append(queue.intents, intents...)
	return nil
}

type coordinationIDs struct{ taskID id.Task }

func (source coordinationIDs) NewTask() (id.Task, error) { return source.taskID, nil }

func TestCoordinatorRecoversPendingAttemptAndExpiresOriginalWindow(t *testing.T) {
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	deliveryID, _ := id.ParseDelivery("dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	taskID, _ := id.ParseTask("tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	created := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name   string
		age    time.Duration
		expiry bool
	}{
		{name: "pending retry", age: time.Hour},
		{name: "elapsed original deadline", age: 25 * time.Hour, expiry: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := created.Add(test.age)
			repository := coordinationRepository{targets: []DeliveryTarget{{TenantID: tenantID, DeliveryID: deliveryID, AttemptNumber: 2, DueAt: created.Add(time.Minute), CreatedAt: created}}}
			queue := &coordinationQueue{}
			coordinator, err := NewCoordinator(repository, coordinationIDs{taskID}, queue, coordinationClock{now}, 100)
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if count, err := coordinator.ScheduleReadyDeliveries(t.Context()); err != nil || count != 1 {
					t.Fatalf("schedule=%d,%v", count, err)
				}
			}
			first, second := queue.intents[0], queue.intents[1]
			if first.IdempotencyKey() != second.IdempotencyKey() {
				t.Fatal("rediscovery changed the logical attempt key")
			}
			gotID, number, err := decodeAttempt(first.Payload())
			if err != nil || gotID != deliveryID || number != 2 || first.Key() != DeliverKey {
				t.Fatalf("attempt contract: %s %d %v", gotID, number, err)
			}
			if test.expiry {
				if first.IdempotencyKey() != "webhook.deliver:"+deliveryID.String()+":attempt:2:expiry:"+strconv.FormatInt(now.Truncate(time.Hour).Unix(), 10) || !first.ScheduledAt().Equal(now) {
					t.Fatal("expired delivery did not receive distinct cleanup work")
				}
			} else if !first.Deadline().Equal(created.Add(MaximumDeliveryDuration)) || !first.ScheduledAt().Equal(repository.targets[0].DueAt) {
				t.Fatal("recovery moved the original deadline or retry due time")
			}
		})
	}
}

func TestCallbackBackoffPreservesRetryAfterAndBoundsStableJitter(t *testing.T) {
	deliveryID, _ := id.ParseDelivery("dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	for attempt := int32(1); attempt <= 20; attempt++ {
		base := min(time.Second<<min(attempt-1, 12), time.Hour)
		got := callbackBackoff(deliveryID, attempt, 0)
		if got < base*80/100 || got > min(base*120/100, time.Hour) || callbackBackoff(deliveryID, attempt, 0) != got {
			t.Fatalf("attempt=%d delay=%s base=%s", attempt, got, base)
		}
		if got := callbackBackoff(deliveryID, attempt, 5*time.Minute); got != 5*time.Minute {
			t.Fatalf("Retry-After changed: %s", got)
		}
	}
}
