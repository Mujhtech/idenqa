package dojah

import (
	"context"
	"encoding/base64"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

// documentCheck consumes only explicitly granted document sides. All references
// are checked before redemption, and all reads finish before external submission.
// A back-side grant is optional; its presence never establishes side correspondence.
func (adapter *Adapter) documentCheck(ctx context.Context, request providerv1.Request, configuration Config) (providerv1.Result, error) {
	grants := make(map[string]providerv1.EvidenceGrantReference, 2)
	for _, grant := range request.Evidence {
		if (grant.Variant != "document.front" && grant.Variant != "document.back") || grants[grant.Variant].GrantID != "" {
			return failed(request, adapter.now, providerv1.FailureInvalidRequest, "document_evidence_ambiguous", providerv1.RetryNever, 0), nil
		}
		grants[grant.Variant] = grant
	}
	if grants["document.front"].GrantID == "" {
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "evidence_unavailable", providerv1.RetryNever, 0), nil
	}
	body := map[string]string{"input_type": "base64"}
	for _, side := range []struct{ variant, field string }{
		{"document.front", "imagefrontside"},
		{"document.back", "imagebackside"},
	} {
		grant, exists := grants[side.variant]
		if !exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return providerv1.Result{}, err
		}
		image, err := adapter.evidence.ReadProviderEvidence(ctx, grant, maximumEvidence)
		if err != nil || len(image) == 0 || len(image) > maximumEvidence {
			wipe(image)
			//nolint:nilerr // reader details are replaced by a stable contract failure.
			return failed(request, adapter.now, providerv1.FailureInvalidRequest, "evidence_unavailable", providerv1.RetryNever, 0), nil
		}
		body[side.field] = base64.StdEncoding.EncodeToString(image)
		wipe(image)
	}
	if err := ctx.Err(); err != nil {
		return providerv1.Result{}, err
	}
	response, status, retryAfter, err := adapter.post(ctx, configuration, "/api/v1/document/analysis", body)
	if err != nil {
		//nolint:nilerr // external errors are classified without leaking provider details.
		return transportFailure(request, adapter.now, status, retryAfter), nil
	}
	outcome := documentOutcome(response)
	result := completed(request, adapter.now, "idenqa.signal.document_quality", outcome)
	result.Document = documentObservation(response, outcome)
	return result, nil
}
