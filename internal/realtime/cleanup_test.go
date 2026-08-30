package realtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type cleanupRepositoryStub struct {
	observedAt time.Time
	batchSize  int32
	deleted    int
}

func (repository *cleanupRepositoryStub) DeleteExpiredUnredeemed(
	_ context.Context,
	_ tenant.Scope,
	observedAt time.Time,
	batchSize int32,
) (int, error) {
	repository.observedAt = observedAt
	repository.batchSize = batchSize

	return repository.deleted, nil
}

func TestCleanupServiceUsesBoundedTenantBatch(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	repository := &cleanupRepositoryStub{deleted: 7}
	service, err := realtime.NewCleanupService(repository, fixedClock{now: fixture.now})
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := service.CleanupExpired(t.Context(), fixture.scope, 25)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 7 || repository.batchSize != 25 || !repository.observedAt.Equal(fixture.now) {
		t.Fatalf("cleanup = %d, batch = %d, observed = %s", deleted, repository.batchSize, repository.observedAt)
	}
	if _, err := service.CleanupExpired(t.Context(), fixture.scope, 1001); err == nil {
		t.Fatal("CleanupExpired(oversized batch) error = nil")
	}
}
