package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestEvidenceUploadRoutesIssueAndStreamExactIntent(t *testing.T) {
	t.Parallel()

	fixture := newEvidenceUploadHTTPFixture(t)
	routes, err := NewEvidenceUploadRoutes(
		fixture.capture,
		fixture.service,
		fixture.service,
		fixture.service,
		evidence.DefaultUploadPolicy(),
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewEvidenceUploadRoutes() error = %v", err)
	}
	router := versionedRouter(t, routes)

	createBody, err := json.Marshal(openapiv1.EvidenceUploadCreate{
		RequirementKey: "selfie", Artefact: string(evidence.ArtefactSelfieImage),
		AcquisitionMethod: string(evidence.MethodFileUpload), ExpectedBytes: 4,
		ExpectedDigest: fixture.upload.Record().ExpectedDigest,
		MediaType:      openapiv1.EvidenceUploadCreateMediaTypeImageJpeg,
		Region:         "idenqa.region.synthetic",
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPost, "/v1/evidence-uploads", bytes.NewReader(createBody),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", `"upload-1"`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("issue status=%d body=%s", response.Code, response.Body)
	}
	if fixture.service.key != "upload-1" || fixture.service.issueInput.Artefact != evidence.ArtefactSelfieImage ||
		fixture.service.issueInput.ExpectedDigest != fixture.upload.Record().ExpectedDigest {
		t.Fatalf("issue key=%q input=%+v", fixture.service.key, fixture.service.issueInput)
	}
	if response.Header().Get("ETag") != `"1"` ||
		response.Header().Get("Location") != "/v1/evidence-uploads/"+fixture.upload.ID().String() {
		t.Fatalf("issue headers=%v", response.Header())
	}

	request = httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/v1/evidence-uploads/"+fixture.upload.ID().String(), nil,
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"1"` {
		t.Fatalf("find status=%d headers=%v body=%s", response.Code, response.Header(), response.Body)
	}
	if fixture.service.principal.CaptureTokenID.IsZero() ||
		fixture.service.principal.VerificationID != fixture.upload.Record().VerificationID {
		t.Fatalf("find principal=%+v", fixture.service.principal)
	}

	digest := sha256.Sum256([]byte("jpeg"))
	request = httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/v1/evidence-uploads/"+fixture.upload.ID().String(),
		bytes.NewReader([]byte("jpeg")),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Content-Type", evidence.MediaTypeJPEG)
	request.Header.Set("Content-Digest", "sha-256=:"+base64.StdEncoding.EncodeToString(digest[:])+":")
	request.Header.Set("If-Match", `"1"`)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%s", response.Code, response.Body)
	}
	if fixture.service.uploadID != fixture.upload.ID() || fixture.service.body != "jpeg" ||
		fixture.service.metadata.ContentLength != 4 || fixture.service.metadata.ExpectedVersion != 1 {
		t.Fatalf("upload id=%q metadata=%+v body=%q", fixture.service.uploadID, fixture.service.metadata, fixture.service.body)
	}
	var result openapiv1.EvidenceUpload
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.ID != fixture.upload.ID().String() || result.ExpectedBytes != 4 ||
		len(result.AllowedMediaTypes) != 1 || result.AcceptedAt != nil {
		t.Fatalf("result=%+v", result)
	}
}

func TestEvidenceUploadRoutesRejectBeforeApplication(t *testing.T) {
	t.Parallel()

	fixture := newEvidenceUploadHTTPFixture(t)
	routes, err := NewEvidenceUploadRoutes(
		fixture.capture,
		fixture.service,
		fixture.service,
		fixture.service,
		evidence.DefaultUploadPolicy(),
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewEvidenceUploadRoutes() error = %v", err)
	}
	router := versionedRouter(t, routes)
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodPut, "/v1/evidence-uploads/"+fixture.upload.ID().String(),
		bytes.NewReader([]byte("jpeg")),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Content-Type", "image/jpeg; charset=binary")
	request.Header.Set("If-Match", `"1"`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || fixture.service.accepts != 0 {
		t.Fatalf("status=%d accepts=%d body=%s", response.Code, fixture.service.accepts, response.Body)
	}
}

type evidenceUploadHTTPService struct {
	upload     evidence.Upload
	key        string
	issueInput authority.UploadRequest
	uploadID   id.Upload
	metadata   evidence.UploadMetadata
	body       string
	accepts    int
	principal  evidence.UploadPrincipal
}

func (service *evidenceUploadHTTPService) Issue(
	_ context.Context,
	_ verification.CaptureContext,
	key string,
	input authority.UploadRequest,
) (evidence.Upload, error) {
	service.key, service.issueInput = key, input

	return service.upload, nil
}

func (service *evidenceUploadHTTPService) Accept(
	_ context.Context,
	_ verification.CaptureContext,
	uploadID id.Upload,
	metadata evidence.UploadMetadata,
	body io.Reader,
) (evidence.Upload, error) {
	service.accepts++
	service.uploadID, service.metadata = uploadID, metadata
	value, err := io.ReadAll(body)
	if err != nil {
		return evidence.Upload{}, err
	}
	service.body = string(value)

	return service.upload, nil
}

func (service *evidenceUploadHTTPService) Find(
	_ context.Context,
	principal evidence.UploadPrincipal,
	uploadID id.Upload,
) (evidence.Upload, error) {
	service.principal = principal
	service.uploadID = uploadID

	return service.upload, nil
}

type evidenceUploadHTTPFixture struct {
	capture *CaptureAccessMiddleware
	service *evidenceUploadHTTPService
	upload  evidence.Upload
	token   string
	logger  *slog.Logger
}

func newEvidenceUploadHTTPFixture(t *testing.T) evidenceUploadHTTPFixture {
	t.Helper()

	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatal(err)
	}
	profile := verification.Profile{
		SchemaVersion: verification.ProfileSchemaVersion,
		Registry:      registry.Reference(),
		Requirements: []verification.Requirement{{
			Key: "selfie", Purpose: evidence.PurposeIdentityVerification,
			EvidenceType: evidence.EvidenceSelfieImage, Artefacts: []evidence.Name{evidence.ArtefactSelfieImage},
			Acquisition: verification.Acquisition{
				Strategy: verification.StrategyAnyOf, Methods: []evidence.Name{evidence.MethodFileUpload},
			},
		}},
	}
	digest, err := verification.Digest(profile, registry)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	profileID, _ := id.ParseProfile("prf_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	tokenID, _ := id.ParseCaptureToken("ctk_01ARZ3NDEKTSV4RRFFQ69G5FAY")
	session, err := verification.RestoreSession(
		verificationID, tenantID, verification.SessionStateCollecting, 1, profileID, 1, digest,
		profile, "local", policyID, now, now, now.Add(time.Hour), registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := access.NewCaptureCredential(tokenID, tenantID, verificationID, 1, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{1: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, httpAccessClock{now: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	presented, err := signer.Sign(credential)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := verification.NewCaptureAuthenticator(
		httpCaptureRepository{creation: verification.SessionCreation{Session: session, Credential: credential}},
		signer, httpAccessClock{now: now.Add(time.Minute)},
	)
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	capture, err := NewCaptureAccessMiddleware(authenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	uploadID, _ := id.ParseUpload("upl_01ARZ3NDEKTSV4RRFFQ69G5FAZ")
	evidenceID, _ := id.ParseEvidence("evd_01ARZ3NDEKTSV4RRFFQ69G5FB0")
	subjectID, _ := id.ParseSubject("sub_01ARZ3NDEKTSV4RRFFQ69G5FB1")
	authorityID, _ := id.ParseAuthority("aut_01ARZ3NDEKTSV4RRFFQ69G5FB2")
	responseID, _ := id.ParseAcknowledgement("ack_01ARZ3NDEKTSV4RRFFQ69G5FB3")
	upload, err := evidence.NewUpload(evidence.UploadInput{
		ID: uploadID, TenantID: tenantID, CaptureTokenID: tokenID, SubjectID: subjectID,
		VerificationID: verificationID, EvidenceID: evidenceID, AuthorityID: authorityID,
		ResponseID: responseID, ProfileID: profileID, ProfileRevision: 1, ProfileDigest: digest,
		RequirementKey: "selfie", Purpose: evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceSelfieImage, Artefact: evidence.ArtefactSelfieImage,
		AcquisitionMethod: evidence.MethodFileUpload, AllowedMediaTypes: []string{evidence.MediaTypeJPEG},
		MaximumBytes: evidence.DefaultUploadMaximumBytes, ExpectedBytes: 4,
		ExpectedDigest: string(platformcrypto.Sum([]byte("jpeg"))), MediaType: evidence.MediaTypeJPEG,
		Region: "idenqa.region.synthetic", RetentionClass: "tenant.retention.synthetic",
		CreatedAt: now, SessionExpiresAt: now.Add(time.Hour),
	}, registry, evidence.DefaultUploadPolicy())
	if err != nil {
		t.Fatal(err)
	}
	service := &evidenceUploadHTTPService{upload: upload}

	return evidenceUploadHTTPFixture{
		capture: capture, service: service, upload: upload, token: presented.Reveal(), logger: logger,
	}
}
