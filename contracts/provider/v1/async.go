package provider

import (
	"context"
	"regexp"
)

// Progress is the optional asynchronous v1 extension. A nil Result means the
// same job is still pending; it is never a terminal verification failure.
// ReplayID is the provider-owned stable identity for the delivered operation;
// a callback and a status-only result carrying the same ReplayID are exact
// duplicates.
type Progress struct {
	ProviderJobID string  `json:"provider_job_id,omitempty"`
	ReplayID      string  `json:"replay_id,omitempty"`
	Result        *Result `json:"result,omitempty"`
}

var (
	jobReferencePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
	replayPattern       = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,200}$`)
)

// ValidateForRequest rejects unbounded references and mismatched final results.
func (progress Progress) ValidateForRequest(request Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if progress.ProviderJobID != "" && !jobReferencePattern.MatchString(progress.ProviderJobID) {
		return invalid("progress.provider_job_id", "is invalid")
	}
	if progress.ReplayID != "" && !replayPattern.MatchString(progress.ReplayID) {
		return invalid("progress.replay_id", "is invalid")
	}
	if progress.Result != nil {
		return progress.Result.ValidateForRequest(request)
	}
	return nil
}

// Advancer separates one initial submission from status-only recovery. Resume
// must never submit a job, upload evidence, or consume an evidence grant.
type Advancer interface {
	Advance(context.Context, Request, bool) (Progress, error)
}
