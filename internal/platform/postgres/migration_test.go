package postgres

import (
	"context"
	"errors"
	"testing"
)

func TestMigratorDownOneRequiresEnvironmentAndConfirmation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		guard RollbackGuard
	}{
		{name: "production confirmed", guard: RollbackGuard{Environment: "production", Confirmed: true}},
		{name: "development unconfirmed", guard: RollbackGuard{Environment: "development"}},
		{name: "test unconfirmed", guard: RollbackGuard{Environment: "test"}},
		{name: "unknown environment confirmed", guard: RollbackGuard{Environment: "local", Confirmed: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := (&Migrator{}).DownOne(context.Background(), test.guard)
			if !errors.Is(err, ErrRollbackForbidden) {
				t.Fatalf("DownOne() error = %v, want ErrRollbackForbidden", err)
			}
		})
	}
}
