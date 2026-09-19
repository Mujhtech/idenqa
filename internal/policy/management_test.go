package policy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
)

type unreachableManagementStore struct{ policy.ManagementRepository }

func TestManagementRequiresApplicationAuthorityBeforeRepositoryAccess(t *testing.T) {
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	service, err := policy.NewManagement(unreachableManagementStore{}, policycel.Compiler{}, ids, func() time.Time { return time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC) }, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		run  func(context.Context) error
	}{
		{"create", func(ctx context.Context) error {
			_, err := service.Execute(ctx, access.Context{}, "key", policy.Command{Operation: "create"})
			return err
		}},
		{"activate", func(ctx context.Context) error {
			_, err := service.Execute(ctx, access.Context{}, "key", policy.Command{Operation: "activate"})
			return err
		}},
		{"diff", func(ctx context.Context) error {
			_, err := service.Diff(ctx, access.Context{}, id.Policy{}, 1, 2)
			return err
		}},
		{"rollback", func(ctx context.Context) error {
			_, err := service.Execute(ctx, access.Context{}, "key", policy.Command{Operation: "rollback"})
			return err
		}},
		{"validate", func(ctx context.Context) error {
			_, err := service.Validate(ctx, access.Context{}, policy.Definition{})
			return err
		}},
		{"get", func(ctx context.Context) error { _, err := service.Get(ctx, access.Context{}, id.Policy{}); return err }},
		{"source", func(ctx context.Context) error {
			_, err := service.GetRevision(ctx, access.Context{}, id.Policy{}, 1)
			return err
		}},
		{"list", func(ctx context.Context) error { _, err := service.List(ctx, access.Context{}, "", 25); return err }},
		{"revisions", func(ctx context.Context) error {
			_, _, _, err := service.Revisions(ctx, access.Context{}, id.Policy{}, 0, 25)
			return err
		}},
		{"activations", func(ctx context.Context) error {
			_, _, _, err := service.Activations(ctx, access.Context{}, id.Policy{}, 0, 25)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(t.Context()); !errors.Is(err, access.ErrInsufficientScope) {
				t.Fatalf("authority failure=%v", err)
			}
		})
	}
}
