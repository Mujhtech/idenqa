package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

const decisionBundleMediaType = "application/vnd.idenqa.decision-bundle.v1+json"

// DecisionReader is the tenant-authorised decision capability consumed by HTTP.
type DecisionReader interface {
	Find(context.Context, access.Context, id.Decision) (policy.ReproductionReport, error)
	FindLatest(context.Context, access.Context, id.Verification) (policy.ReproductionReport, error)
	Export(context.Context, access.Context, id.Decision) (policy.DecisionBundle, policy.ReproductionReport, error)
}

// DecisionRoutes adapts safe decision reads and portable export to HTTP.
type DecisionRoutes struct {
	access *AccessMiddleware
	reader DecisionReader
	logger *slog.Logger
}

// NewDecisionRoutes constructs the protected decision HTTP surface.
func NewDecisionRoutes(
	accessMiddleware *AccessMiddleware,
	reader DecisionReader,
	logger *slog.Logger,
) (*DecisionRoutes, error) {
	if accessMiddleware == nil || reader == nil || logger == nil {
		return nil, errors.New("decision route dependencies are required")
	}

	return &DecisionRoutes{access: accessMiddleware, reader: reader, logger: logger}, nil
}

// Register adds exact, latest, and byte-canonical export endpoints.
func (routes *DecisionRoutes) Register(router chi.Router) {
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionDecisionsRead),
	).Get("/decisions/{decisionID}", routes.find)
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionDecisionsRead),
	).Get("/verifications/{verificationID}/decision", routes.findLatest)
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionDecisionsExport),
	).Get("/decisions/{decisionID}/bundle", routes.export)
}

func (routes *DecisionRoutes) find(writer http.ResponseWriter, request *http.Request) {
	decisionID, err := id.ParseDecision(chi.URLParam(request, "decisionID"))
	if err != nil {
		routes.writeFailure(writer, request, policy.ErrDecisionNotFound)

		return
	}
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)

		return
	}
	report, err := routes.reader.Find(request.Context(), authority, decisionID)
	if err != nil {
		routes.writeFailure(writer, request, err)

		return
	}
	routes.writeReport(writer, request, report)
}

func (routes *DecisionRoutes) findLatest(writer http.ResponseWriter, request *http.Request) {
	verificationID, err := id.ParseVerification(chi.URLParam(request, "verificationID"))
	if err != nil {
		routes.writeFailure(writer, request, policy.ErrDecisionNotFound)

		return
	}
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)

		return
	}
	report, err := routes.reader.FindLatest(request.Context(), authority, verificationID)
	if err != nil {
		routes.writeFailure(writer, request, err)

		return
	}
	routes.writeReport(writer, request, report)
}

func (routes *DecisionRoutes) export(writer http.ResponseWriter, request *http.Request) {
	decisionID, err := id.ParseDecision(chi.URLParam(request, "decisionID"))
	if err != nil {
		routes.writeFailure(writer, request, policy.ErrDecisionNotFound)

		return
	}
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)

		return
	}
	bundle, _, err := routes.reader.Export(request.Context(), authority, decisionID)
	if err != nil {
		routes.writeFailure(writer, request, err)

		return
	}
	etag := decisionETag(bundle.Digest())
	setDecisionCacheHeaders(writer, etag)
	if matchesEntityTag(request.Header.Values("If-None-Match"), etag) {
		writer.WriteHeader(http.StatusNotModified)

		return
	}
	if err := respond.CanonicalJSON(
		writer,
		request,
		http.StatusOK,
		decisionBundleMediaType,
		bundle.Canonical(),
	); err != nil {
		routes.logger.ErrorContext(request.Context(), "write canonical decision bundle response")
	}
}

func (routes *DecisionRoutes) writeReport(
	writer http.ResponseWriter,
	request *http.Request,
	report policy.ReproductionReport,
) {
	etag := decisionETag(report.DecisionDigest)
	setDecisionCacheHeaders(writer, etag)
	if matchesEntityTag(request.Header.Values("If-None-Match"), etag) {
		writer.WriteHeader(http.StatusNotModified)

		return
	}
	resource := decisionReportResource(report)
	if err := respond.JSON(writer, request, http.StatusOK, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write decision report response")
	}
}

func (routes *DecisionRoutes) writeFailure(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(
		writer,
		request,
		err,
		requestIDString(request.Context()),
	); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write decision failure response")
	}
}

func decisionReportResource(report policy.ReproductionReport) openapiv1.PolicyDecisionReport {
	var supersedes *openapiv1.PolicyDecisionID
	if report.Supersedes != "" {
		value := report.Supersedes
		supersedes = &value
	}

	return openapiv1.PolicyDecisionReport{
		TypedAssurance: assuranceSummaryResource(report.TypedAssurance),
		SchemaMajor:    int(report.SchemaMajor), SchemaMinor: int(report.SchemaMinor),
		DecisionID: report.DecisionID, TenantID: report.TenantID,
		VerificationID: report.VerificationID, PolicyID: report.PolicyID,
		PolicyRevision: int(report.PolicyRevision), PolicyDigest: report.PolicyDigest,
		EvaluatorMajor: int(report.EvaluatorMajor), EvaluatorMinor: int(report.EvaluatorMinor),
		EvaluatorDigest: report.EvaluatorDigest, SnapshotDigest: report.SnapshotDigest,
		EvaluationDigest: report.EvaluationDigest, DecisionDigest: report.DecisionDigest,
		BundleDigest: report.BundleDigest, Directive: openapiv1.PolicyDirective(report.Directive),
		Outcome: openapiv1.PolicyOutcome(report.Outcome), Assurance: report.Assurance,
		Actor: openapiv1.PolicyActor(report.Actor), Supersedes: supersedes,
		FactCount: report.FactCount, RequirementCount: report.RequirementCount,
		EvaluatedAt: report.EvaluatedAt, DecidedAt: report.DecidedAt,
		Reproduced: openapiv1.PolicyDecisionReportReproduced(report.Reproduced),
	}
}

func decisionETag(digest string) string { return strconv.Quote("sha256:" + digest) }

func setDecisionCacheHeaders(writer http.ResponseWriter, etag string) {
	writer.Header().Set("ETag", etag)
	writer.Header().Set("Cache-Control", "private, no-cache")
}

func assuranceSummaryResource(value *policy.AssuranceSummary) *openapiv1.AssuranceSummary {
	if value == nil {
		return nil
	}
	result := &openapiv1.AssuranceSummary{Achieved: value.Achieved, Dimensions: []openapiv1.AssuranceDimensionSummary{}}
	if value.Requested != nil {
		result.Requested = &openapiv1.AssuranceSelection{Name: value.Requested.Name, Revision: int(value.Requested.Revision), Digest: value.Requested.Digest}
	}
	for _, d := range value.Dimensions {
		result.Dimensions = append(result.Dimensions, openapiv1.AssuranceDimensionSummary{Dimension: openapiv1.AssuranceDimension(d.Dimension), Property: d.Property, Satisfied: d.Satisfied, IndependentSources: d.IndependentSources})
	}
	return result
}
