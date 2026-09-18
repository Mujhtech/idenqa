package config

import (
	"context"
	"time"

	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ReviewAuthorityFile is an operator-managed, reference-only assignment file.
// It is reread for each action so removal, expiry and invalid replacement fail closed.
type ReviewAuthorityFile struct{ Path string }

// Validate checks mounted configuration at startup. An absent path disables review writes.
func (file ReviewAuthorityFile) Validate() error {
	if file.Path == "" {
		return nil
	}
	_, err := file.load()
	return err
}

func (file ReviewAuthorityFile) load() (*review.Registry, error) {
	if file.Path == "" {
		return nil, review.ErrForbidden
	}
	var document struct {
		Assignments []review.Assignment `json:"assignments"`
	}
	if err := ReadClosedFile(file.Path, &document, 256<<10); err != nil {
		return nil, review.ErrForbidden
	}
	return review.NewRegistry(document.Assignments)
}

// ResolveReviewer reloads authority and never accepts privileges from a request body.
func (file ReviewAuthorityFile) ResolveReviewer(ctx context.Context, scope tenant.Scope, actor review.Actor, region string, now time.Time) (review.Principal, error) {
	registry, err := file.load()
	if err != nil {
		return review.Principal{}, review.ErrForbidden
	}
	return registry.ResolveReviewer(ctx, scope, actor, region, now)
}

// LoadReviewRouting reads bounded explicit case requirements for exact policy revisions.
func LoadReviewRouting(path string) ([]review.RoutingRule, error) {
	if path == "" {
		return nil, nil
	}
	var document struct {
		Rules []review.RoutingRule `json:"rules"`
	}
	if err := ReadClosedFile(path, &document, 256<<10); err != nil {
		return nil, err
	}
	return document.Rules, nil
}
