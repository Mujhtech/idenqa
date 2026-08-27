package postgres_test

import (
	"testing"

	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
)

func TestNewRejectsMissingPool(t *testing.T) {
	t.Parallel()

	if _, err := accesspostgres.New(nil); err == nil {
		t.Error("New(nil) error = nil")
	}
}
