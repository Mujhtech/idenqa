package model_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

type unreachableRegistry struct{ model.RegistryRepository }

func TestRegistryApplicationAuthorityPrecedesReplay(t *testing.T) {
	t.Parallel()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	service, err := model.NewManagement(unreachableRegistry{}, ids, time.Now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"register", "threshold", "activate", "rollback", "retire"} {
		t.Run(operation, func(t *testing.T) {
			if _, err := service.Execute(t.Context(), access.Context{}, "existing-key", model.RegistryCommand{Operation: operation}); !errors.Is(err, access.ErrInsufficientScope) {
				t.Fatal(err)
			}
		})
	}
	if _, err := service.Get(t.Context(), access.Context{}, "pad"); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatal(err)
	}
}
