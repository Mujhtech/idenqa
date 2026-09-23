package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// EvidenceAdministrationRoutes exposes safe evidence and grant metadata only.
type EvidenceAdministrationRoutes struct {
	handlerBase
	access  *AccessMiddleware
	service *evidence.Administration
	grants  *evidence.GrantAdministration
}

// WithGrantIssuer enables idempotent public processing-grant issuance.
func (routes *EvidenceAdministrationRoutes) WithGrantIssuer(service *evidence.GrantAdministration) *EvidenceAdministrationRoutes {
	if routes != nil {
		routes.grants = service
	}
	return routes
}

// NewEvidenceAdministrationRoutes constructs the tenant evidence administration routes.
func NewEvidenceAdministrationRoutes(a *AccessMiddleware, service *evidence.Administration, logger *slog.Logger) (*EvidenceAdministrationRoutes, error) {
	if a == nil || service == nil || logger == nil {
		return nil, errors.New("evidence administration routes require dependencies")
	}
	return &EvidenceAdministrationRoutes{
		access:      a,
		service:     service,
		handlerBase: newHandlerBase(logger, "evidence administration"),
	}, nil
}

// Register mounts safe metadata reads and irreversible grant revocation.
func (routes *EvidenceAdministrationRoutes) Register(router chi.Router) {
	readEvidence := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionEvidenceRead)}
	readGrants := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionEvidenceGrantsRead)}
	writeGrants := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionEvidenceGrantsWrite)}
	router.With(readEvidence...).Get("/evidence/{evidenceID}", routes.find)
	router.With(readEvidence...).Get("/evidence/{evidenceID}/lifecycle", routes.history)
	router.With(readGrants...).Get("/evidence-access-grants/{grantID}", routes.findGrant)
	if routes.grants != nil {
		router.With(writeGrants...).Post("/evidence-access-grants/{grantID}/revoke", routes.revokeGrant)
		router.With(writeGrants...).Post("/evidence/{evidenceID}/access-grants", routes.issueGrant)
	}
}

type grantCreateRequest struct {
	CheckReference     string   `json:"check_reference"`
	RunnerIdentity     string   `json:"runner_identity"`
	WorkloadVersion    string   `json:"workload_version"`
	Purpose            string   `json:"purpose"`
	PermittedVariants  []string `json:"permitted_variants"`
	RecipientReference string   `json:"recipient_reference"`
	OutputDestination  string   `json:"output_destination"`
	MaximumUses        uint32   `json:"maximum_uses"`
	TTLSeconds         int64    `json:"ttl_seconds"`
	Reason             string   `json:"reason"`
}

type safeEvidence struct {
	ID                string     `json:"id"`
	SubjectID         string     `json:"subject_id"`
	VerificationID    string     `json:"verification_id"`
	RequirementKey    string     `json:"requirement_key"`
	EvidenceType      string     `json:"evidence_type"`
	Artefact          string     `json:"artefact"`
	AcquisitionMethod string     `json:"acquisition_method"`
	Assurances        []string   `json:"assurances"`
	RegistryRevision  uint32     `json:"registry_revision"`
	Region            string     `json:"region"`
	RetentionClass    string     `json:"retention_class"`
	ContentRevision   uint32     `json:"content_revision"`
	Integrity         string     `json:"integrity"`
	State             string     `json:"state"`
	Version           int64      `json:"version"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	QuarantineReason  string     `json:"quarantine_reason,omitempty"`
	QuarantinedAt     *time.Time `json:"quarantined_at,omitempty"`
}

type safeLifecycleEvent struct {
	Version    int64     `json:"version"`
	Action     string    `json:"action"`
	Reason     string    `json:"reason,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

type safeGrant struct {
	ID                 string     `json:"id"`
	SubjectID          string     `json:"subject_id"`
	VerificationID     string     `json:"verification_id"`
	EvidenceID         string     `json:"evidence_id"`
	RequirementKey     string     `json:"requirement_key"`
	AuthorityID        string     `json:"authority_id"`
	ResponseID         string     `json:"response_id"`
	CheckReference     string     `json:"check_reference"`
	RunnerIdentity     string     `json:"runner_identity"`
	WorkloadVersion    string     `json:"workload_version"`
	Purpose            string     `json:"purpose"`
	Operation          string     `json:"operation"`
	PermittedVariants  []string   `json:"permitted_variants"`
	Region             string     `json:"region"`
	RecipientReference string     `json:"recipient_reference"`
	OutputDestination  string     `json:"output_destination"`
	PolicyReference    string     `json:"policy_reference"`
	MaximumUses        uint32     `json:"maximum_uses"`
	Uses               uint32     `json:"uses"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          time.Time  `json:"expires_at"`
	RevokedAt          *time.Time `json:"revoked_at,omitempty"`
}

func (routes *EvidenceAdministrationRoutes) find(w http.ResponseWriter, request *http.Request) {
	identifier, err := id.ParseEvidence(chi.URLParam(request, "evidenceID"))
	if err != nil {
		routes.reply(w, request, nil, evidence.ErrNotFound)
		return
	}
	auth, _ := AccessContext(request.Context())
	asset, err := routes.service.Find(request.Context(), auth, identifier)
	if err != nil {
		routes.reply(w, request, nil, err)
		return
	}
	routes.reply(w, request, projectEvidence(asset), nil)
}

func (routes *EvidenceAdministrationRoutes) history(w http.ResponseWriter, request *http.Request) {
	identifier, err := id.ParseEvidence(chi.URLParam(request, "evidenceID"))
	if err != nil {
		routes.reply(w, request, nil, evidence.ErrNotFound)
		return
	}
	limit := 50
	if value := request.URL.Query().Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
	}
	if err != nil || limit < 1 || limit > 100 {
		routes.reply(w, request, nil, invalidRequest(errors.New("limit must be from 1 to 100")))
		return
	}
	auth, _ := AccessContext(request.Context())
	events, err := routes.service.History(request.Context(), auth, identifier, limit)
	if err != nil {
		routes.reply(w, request, nil, err)
		return
	}
	projected := make([]safeLifecycleEvent, 0, len(events))
	for _, event := range events {
		projected = append(projected, safeLifecycleEvent{Version: event.Version, Action: event.Action, Reason: event.Reason, OccurredAt: event.OccurredAt})
	}
	routes.reply(w, request, map[string]any{"data": projected}, nil)
}

func (routes *EvidenceAdministrationRoutes) findGrant(w http.ResponseWriter, request *http.Request) {
	identifier, err := id.ParseGrant(chi.URLParam(request, "grantID"))
	if err != nil {
		routes.reply(w, request, nil, evidence.ErrGrantDenied)
		return
	}
	auth, _ := AccessContext(request.Context())
	grant, err := routes.service.FindGrant(request.Context(), auth, identifier)
	if err != nil {
		routes.reply(w, request, nil, err)
		return
	}
	routes.reply(w, request, projectGrant(grant), nil)
}

func (routes *EvidenceAdministrationRoutes) revokeGrant(w http.ResponseWriter, request *http.Request) {
	identifier, err := id.ParseGrant(chi.URLParam(request, "grantID"))
	if err != nil {
		routes.reply(w, request, nil, evidence.ErrGrantDenied)
		return
	}
	body, err := decodeJSONBody[struct {
		Reason string `json:"reason"`
	}](request)
	if err != nil {
		routes.reply(w, request, nil, invalidRequest(err))
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.reply(w, request, nil, err)
		return
	}
	auth, _ := AccessContext(request.Context())
	grant, err := routes.grants.Revoke(request.Context(), auth, identifier, key, body.Reason)
	if err != nil {
		routes.reply(w, request, nil, err)
		return
	}
	routes.reply(w, request, projectGrant(grant), nil)
}

func (routes *EvidenceAdministrationRoutes) issueGrant(w http.ResponseWriter, request *http.Request) {
	identifier, err := id.ParseEvidence(chi.URLParam(request, "evidenceID"))
	if err != nil {
		routes.reply(w, request, nil, evidence.ErrNotFound)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.reply(w, request, nil, err)
		return
	}
	body, err := decodeJSONBody[grantCreateRequest](request)
	if err != nil {
		routes.reply(w, request, nil, invalidRequest(err))
		return
	}
	purpose, err := evidence.ParseName(evidence.KindPurpose, body.Purpose)
	if err != nil {
		routes.reply(w, request, nil, invalidRequest(err))
		return
	}
	auth, _ := AccessContext(request.Context())
	grant, err := routes.grants.Issue(request.Context(), auth, key, evidence.GrantInput{EvidenceID: identifier, CheckReference: body.CheckReference, Runner: evidence.Runner{Identity: body.RunnerIdentity, WorkloadVersion: body.WorkloadVersion}, Purpose: purpose, PermittedVariants: body.PermittedVariants, RecipientReference: body.RecipientReference, OutputDestination: body.OutputDestination, MaximumUses: body.MaximumUses, TTL: time.Duration(body.TTLSeconds) * time.Second, Attribution: evidence.CommandAttribution{Reason: body.Reason}})
	if err != nil {
		routes.reply(w, request, nil, err)
		return
	}
	w.Header().Set("Location", "/v1/evidence-access-grants/"+grant.ID().String())
	w.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(w, request, http.StatusCreated, projectGrant(grant)); err != nil {
		routes.logger.ErrorContext(request.Context(), "write evidence grant response")
	}
}

func projectEvidence(asset evidence.Asset) safeEvidence {
	record := asset.Record()
	assurances := make([]string, len(record.Assurances))
	for index, assurance := range record.Assurances {
		assurances[index] = string(assurance)
	}
	return safeEvidence{ID: record.ID.String(), SubjectID: record.SubjectID.String(), VerificationID: record.VerificationID.String(), RequirementKey: record.RequirementKey, EvidenceType: string(record.EvidenceType), Artefact: string(record.Artefact), AcquisitionMethod: string(record.AcquisitionMethod), Assurances: assurances, RegistryRevision: record.Registry.Revision, Region: record.Region, RetentionClass: record.RetentionClass, ContentRevision: record.ContentRevision, Integrity: string(record.Integrity), State: string(record.State), Version: record.Version, CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt, QuarantineReason: record.QuarantineReason, QuarantinedAt: record.QuarantinedAt}
}

func projectGrant(grant evidence.Grant) safeGrant {
	record := grant.Record()
	return safeGrant{ID: record.ID.String(), SubjectID: record.SubjectID.String(), VerificationID: record.VerificationID.String(), EvidenceID: record.EvidenceID.String(), RequirementKey: record.RequirementKey, AuthorityID: record.AuthorityID.String(), ResponseID: record.ResponseID.String(), CheckReference: record.CheckReference, RunnerIdentity: record.Runner.Identity, WorkloadVersion: record.Runner.WorkloadVersion, Purpose: string(record.Purpose), Operation: record.Operation, PermittedVariants: record.PermittedVariants, Region: record.Region, RecipientReference: record.RecipientReference, OutputDestination: record.OutputDestination, PolicyReference: record.PolicyReference, MaximumUses: record.MaximumUses, Uses: record.Uses, CreatedAt: record.CreatedAt, ExpiresAt: record.ExpiresAt, RevokedAt: record.RevokedAt}
}
