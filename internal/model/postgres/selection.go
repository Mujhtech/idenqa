package postgres

import (
	"context"
	"reflect"

	"github.com/Mujhtech/idenqa/internal/model"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ValidateSelection fences future attempts under the same root lock as activation.
// Existing saved requests retain their original dispatch meaning after retirement.
func ValidateSelection(ctx context.Context, tx pg.Transaction, scope tenant.Scope, plan *model.Plan) error {
	pin := plan.Binding.Registry
	if pin == nil {
		return nil
	}
	if pin.Validate() != nil {
		return model.ErrRegistryInvalid
	}
	state, err := readRegistry(ctx, tx, scope, pin.Name, true)
	if err != nil {
		return err
	}
	expected := model.Deployment{ModelRevision: pin.ModelRevision, ThresholdRevision: pin.ThresholdRevision, Region: plan.Binding.Region}
	if state.Active == nil || *state.Active != expected {
		return model.ErrRegistryConflict
	}
	registration, err := readRegistryRevision(ctx, tx, scope, pin.Name, "model", pin.ModelRevision)
	if err != nil {
		return err
	}
	thresholds, err := readRegistryRevision(ctx, tx, scope, pin.Name, "threshold", pin.ThresholdRevision)
	if err != nil {
		return err
	}
	if registration.Digest != pin.ModelDigest || thresholds.Digest != pin.ThresholdDigest || registration.Registration.Configuration != plan.Binding.Configuration || !reflect.DeepEqual(registration.Registration.Manifest, plan.Manifest) {
		return model.ErrRegistryConflict
	}
	return model.ValidateDeployment(expected, *registration.Registration, *thresholds.Thresholds)
}
