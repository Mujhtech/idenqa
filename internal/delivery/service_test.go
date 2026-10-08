package delivery_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

func TestCreateDeliveryDoesNotPersistWhenBodyProtectionFails(t *testing.T) {
	t.Parallel()
	wrapErr := errors.New("body wrapping unavailable")
	for _, test := range []struct {
		name    string
		failure error
		want    error
	}{
		{name: "wrapping failure", failure: wrapErr, want: wrapErr},
		{name: "empty wrapped output", want: delivery.ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
			scope := mustScope(t, "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
			ids := &identifiers{endpoint: mustEndpoint(t), deliveries: []id.Delivery{mustDelivery(t, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV")}}
			repo := newMemoryRepository()
			manager, err := delivery.NewManager(repo, ids, failingBodyWrapper{failure: test.failure}, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			endpoint, secret, err := manager.CreateEndpoint(t.Context(), scope, "https://hooks.example.com/webhook")
			if err != nil {
				t.Fatal(err)
			}
			defer clear(secret)
			_, err = manager.CreateDelivery(t.Context(), scope, endpoint.ID, mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV"), "verification.completed", []byte(`{"outcome":"verified"}`))
			if !errors.Is(err, test.want) || len(repo.deliveries) != 0 {
				t.Fatalf("protection failure: %v, stored=%d", err, len(repo.deliveries))
			}
		})
	}
}

type failingBodyWrapper struct {
	protector
	failure error
}

func (wrapper failingBodyWrapper) Wrap(ctx context.Context, purpose kms.Purpose, plaintext, binding []byte) (kms.WrappedKey, error) {
	if purpose == delivery.BodyPurpose() {
		return kms.WrappedKey{}, wrapper.failure
	}
	return wrapper.protector.Wrap(ctx, purpose, plaintext, binding)
}
