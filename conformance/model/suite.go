// Package model supplies public conformance checks for model adapters.
package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

// Fixture is one valid, deterministic model-adapter conformance input.
type Fixture struct {
	Configuration modelv1.ConfigurationReference
	Request       modelv1.Request
}

// Check validates advertised capability, safe lifecycle responses, and exact
// execution-result binding. Runner transport checks are intentionally separate.
func Check(ctx context.Context, adapter modelv1.Adapter, fixture Fixture) error {
	if adapter == nil {
		return errors.New("model conformance: adapter is required")
	}
	if err := fixture.Request.Validate(); err != nil {
		return fmt.Errorf("model conformance: fixture request: %w", err)
	}
	if fixture.Configuration != fixture.Request.Configuration {
		return errors.New("model conformance: fixture configuration does not match request")
	}
	manifest, err := adapter.Manifest(ctx)
	if err != nil {
		return fmt.Errorf("model conformance: manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("model conformance: manifest: %w", err)
	}
	if manifest.Provenance != fixture.Request.Provenance || manifest.Restrictions != fixture.Request.Restrictions {
		return errors.New("model conformance: request does not pin advertised manifest")
	}
	capabilityFound := false
	for _, capability := range manifest.Capabilities {
		if reflect.DeepEqual(capability, fixture.Request.Capability) {
			capabilityFound = true
			break
		}
	}
	if !capabilityFound {
		return errors.New("model conformance: request capability is not advertised")
	}
	if err := adapter.ValidateConfiguration(ctx, fixture.Configuration); err != nil {
		return fmt.Errorf("model conformance: valid configuration rejected: %w", err)
	}
	health, err := adapter.Health(ctx)
	if err != nil {
		return fmt.Errorf("model conformance: health: %w", err)
	}
	if err := health.Validate(); err != nil {
		return fmt.Errorf("model conformance: health: %w", err)
	}
	result, err := adapter.Execute(ctx, fixture.Request)
	if err != nil {
		return fmt.Errorf("model conformance: execute: %w", err)
	}
	if err := result.ValidateForRequest(fixture.Request); err != nil {
		return fmt.Errorf("model conformance: result: %w", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("model conformance: encode result: %w", err)
	}
	if len(encoded) > int(fixture.Request.Restrictions.MaximumResultSize) {
		return errors.New("model conformance: result exceeds pinned size restriction")
	}
	return nil
}
