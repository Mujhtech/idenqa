package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

// ReviewQueue is a tenant-scoped operational metadata port.
type ReviewQueue interface {
	ListQueue(context.Context, tenant.Scope, review.QueueQuery) ([]review.QueueItem, error)
}

// RegisterQueue mounts a tenant-scoped queue with bound continuation cursors.
func (routes *ReviewRoutes) RegisterQueue(router chi.Router, queue ReviewQueue, cursors ProfileCursor) {
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionReviewsRead)).Get("/review-cases", func(w http.ResponseWriter, req *http.Request) {
		auth, _, ok := routes.authority(req)
		if !ok {
			routes.problem(w, req, access.ErrInvalidCredential)
			return
		}
		values := req.URL.Query()
		allowed := map[string]bool{"state": true, "region": true, "reviewer": true, "language": true, "reason": true, "assurance": true, "risk": true, "certificate": true, "sampled": true, "overdue": true, "limit": true, "cursor": true}
		for key, v := range values {
			if !allowed[key] || len(v) != 1 || len(v[0]) > 4096 {
				routes.problem(w, req, review.ErrInvalid)
				return
			}
		}
		limit := 25
		var err error
		if values.Get("limit") != "" {
			limit, err = strconv.Atoi(values.Get("limit"))
			if err != nil || limit < 1 || limit > 100 {
				routes.problem(w, req, review.ErrInvalid)
				return
			}
		}
		for _, key := range []string{"sampled", "overdue"} {
			if v := values.Get(key); v != "" && v != "true" && v != "false" {
				routes.problem(w, req, review.ErrInvalid)
				return
			}
		}
		token := values.Get("cursor")
		values.Del("cursor")
		values.Set("limit", strconv.Itoa(limit))
		query := "reviews.queue;" + values.Encode()
		q := review.QueueQuery{State: values.Get("state"), Region: values.Get("region"), Reviewer: values.Get("reviewer"), Language: values.Get("language"), Reason: values.Get("reason"), Assurance: values.Get("assurance"), Risk: values.Get("risk"), Certificate: values.Get("certificate"), Sampled: optionalReviewBoolean(values.Get("sampled")), Overdue: optionalReviewBoolean(values.Get("overdue")), Limit: limit, At: time.Now().UTC()}
		type position struct {
			Priority   int       `json:"priority"`
			At         time.Time `json:"at"`
			ID         string    `json:"id"`
			ObservedAt time.Time `json:"observed_at"`
		}
		if token != "" {
			claims, err := cursors.Decode(token, auth.TenantScope().ID(), query)
			if err != nil {
				routes.problem(w, req, review.ErrInvalid)
				return
			}
			var p position
			if decodeStrictJSON(claims.Position, &p) != nil || p.ID == "" || p.At.IsZero() || p.ObservedAt.IsZero() {
				routes.problem(w, req, review.ErrInvalid)
				return
			}
			q.AfterPriority, q.AfterDue, q.AfterID, q.At = p.Priority, p.At, p.ID, p.ObservedAt
		}
		items, err := queue.ListQueue(req.Context(), auth.TenantScope(), q)
		if err != nil {
			routes.problem(w, req, err)
			return
		}
		hasMore := len(items) > limit
		if hasMore {
			items = items[:limit]
		}
		type resource struct {
			ID                string           `json:"id"`
			VerificationID    string           `json:"verification_id"`
			State             review.CaseState `json:"state"`
			Region            string           `json:"region"`
			Reviewer          string           `json:"assigned_reviewer,omitempty"`
			Certificate       string           `json:"required_certificate"`
			Version           int64            `json:"version"`
			OperationsVersion int64            `json:"operations_version"`
			Priority          int              `json:"priority"`
			DueAt             *time.Time       `json:"due_at,omitempty"`
			Language          string           `json:"language"`
			Reason            string           `json:"reason"`
			Assurance         string           `json:"assurance"`
			Risk              string           `json:"risk"`
			Sampled           bool             `json:"sampled"`
		}
		output := make([]resource, 0, len(items))
		for _, item := range items {
			c := item.Case
			output = append(output, resource{c.ID.String(), c.VerificationID.String(), c.State, c.Region, c.AssignedReviewer, c.RequiredCertificate, c.Version, item.Version, item.Priority, item.DueAt, item.Language, item.Reason, item.Assurance, item.Risk, item.Sampled})
		}
		next := ""
		if hasMore {
			last := items[len(items)-1]
			encoded, err := json.Marshal(position{last.Priority, last.SortAt, last.Case.ID.String(), q.At})
			if err != nil {
				routes.problem(w, req, err)
				return
			}
			next, err = cursors.Encode(auth.TenantScope().ID(), query, encoded)
			if err != nil {
				routes.problem(w, req, err)
				return
			}
		}
		routes.write(w, req, http.StatusOK, struct {
			Items      []resource `json:"items"`
			HasMore    bool       `json:"has_more"`
			NextCursor string     `json:"next_cursor,omitempty"`
		}{output, hasMore, next})
	})
}

func optionalReviewBoolean(value string) *bool {
	if value == "" {
		return nil
	}
	result := value == "true"
	return &result
}
