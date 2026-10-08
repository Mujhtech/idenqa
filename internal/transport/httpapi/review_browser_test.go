package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/reviewbrowser"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
)

func TestReviewBrowserProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"expired", reviewbrowser.ErrExpired, http.StatusUnauthorized, apierror.CodeReviewSessionExpired},
		{"replayed", reviewbrowser.ErrReplay, http.StatusConflict, apierror.CodeReviewSessionReplayed},
		{"stale case", review.ErrConflict, http.StatusPreconditionFailed, apierror.CodeReviewCaseStale},
		{"revoked authority", review.ErrForbidden, http.StatusForbidden, apierror.CodeReviewAuthorityRevoked},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			failure := &apierror.Error{}
			ok := errors.As(reviewBrowserProblem(test.err), &failure)
			if !ok {
				t.Fatalf("reviewBrowserProblem() type = %T, want *apierror.Error", reviewBrowserProblem(test.err))
			}
			if failure.Status() != test.wantStatus || failure.Code() != test.wantCode || !errors.Is(failure, test.err) {
				t.Fatalf("reviewBrowserProblem() = status %d code %q, want status %d code %q", failure.Status(), failure.Code(), test.wantStatus, test.wantCode)
			}
		})
	}
}
