package authority_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestUploadServiceIssueResolvesRequirementPolicyAndAuthority(t *testing.T) {
	t.Parallel()

	workflow := newUploadWorkflow(t, uploadProfileRequirement())
	upload, err := workflow.service.Issue(
		context.Background(), workflow.captureContext, "upload-selfie-jpeg", workflow.request,
	)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	record := upload.Record()
	mutation := workflow.repository.mutation
	if mutation.Upload.ID() != upload.ID() ||
		mutation.Idempotency.Principal().String() != workflow.captureContext.TokenID().String() ||
		mutation.Idempotency.Operation() != evidence.OperationCreateUpload ||
		mutation.Idempotency.Key() != "upload-selfie-jpeg" {
		t.Fatalf("upload mutation = %+v", mutation)
	}
	if record.SubjectID != workflow.fixture.authority.SubjectID() ||
		record.AuthorityID != workflow.fixture.authority.ID() ||
		record.ResponseID != workflow.fixture.response.Record().ID ||
		record.RetentionClass != workflow.fixture.authority.Record().RetentionReference ||
		record.MaximumBytes != 8<<20 ||
		!slices.Equal(record.AllowedMediaTypes, []string{evidence.MediaTypeJPEG}) ||
		!slices.Equal(record.Assurances, []evidence.Name{
			evidence.AssuranceFreshness, evidence.AssuranceLiveCapture,
		}) {
		t.Fatalf("resolved upload = %+v", record)
	}
	if !record.ExpiresAt.Equal(workflow.fixture.now.Add(evidence.DefaultUploadIntentLifetime)) {
		t.Fatalf("ExpiresAt = %v", record.ExpiresAt)
	}
}

func TestUploadServiceIssueFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(*testing.T, *uploadWorkflow)
		want   error
	}{
		{
			name: "method outside selected acquisition",
			change: func(_ *testing.T, workflow *uploadWorkflow) {
				workflow.request.AcquisitionMethod = evidence.MethodFileUpload
			},
			want: authority.ErrProcessingNotPermitted,
		},
		{
			name: "artefact outside requirement",
			change: func(_ *testing.T, workflow *uploadWorkflow) {
				workflow.request.Artefact = evidence.ArtefactDocumentFront
			},
			want: authority.ErrProcessingNotPermitted,
		},
		{
			name: "unapproved fallback condition",
			change: func(_ *testing.T, workflow *uploadWorkflow) {
				workflow.request.FallbackCondition = verification.FallbackCaptureFailed
			},
			want: authority.ErrProcessingNotPermitted,
		},
		{
			name: "region outside authority",
			change: func(_ *testing.T, workflow *uploadWorkflow) {
				workflow.request.Region = "idenqa.region.other"
			},
			want: authority.ErrProcessingNotPermitted,
		},
		{
			name: "missing subject response",
			change: func(_ *testing.T, workflow *uploadWorkflow) {
				workflow.repository.snapshot.Response = nil
			},
			want: authority.ErrSubjectResponseRequired,
		},
		{
			name: "restricted current authority",
			change: func(t *testing.T, workflow *uploadWorkflow) {
				t.Helper()
				current := workflow.repository.snapshot.Authority
				if err := current.Restrict(workflow.fixture.now); err != nil {
					t.Fatalf("Restrict() error = %v", err)
				}
				workflow.repository.snapshot.Authority = current
			},
			want: authority.ErrProcessingNotPermitted,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			workflow := newUploadWorkflow(t, uploadProfileRequirement())
			test.change(t, &workflow)
			_, err := workflow.service.Issue(
				context.Background(), workflow.captureContext, "upload-selfie-jpeg", workflow.request,
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("Issue() error = %v, want %v", err, test.want)
			}
			if !workflow.repository.mutation.Upload.ID().IsZero() {
				t.Fatal("Issue() persisted a denied upload")
			}
		})
	}
}

func TestUploadServiceIssueRejectsProfilePolicyExpansion(t *testing.T) {
	t.Parallel()

	requirement := uploadProfileRequirement()
	requirement.Constraints[1].Value.Integer = evidence.DefaultUploadMaximumBytes + 1
	workflow := newUploadWorkflow(t, requirement)
	if _, err := workflow.service.Issue(
		context.Background(), workflow.captureContext, "oversized-profile", workflow.request,
	); err == nil {
		t.Fatal("Issue() error = nil")
	}
	if !workflow.repository.mutation.Upload.ID().IsZero() {
		t.Fatal("Issue() persisted a profile policy expansion")
	}
}

func TestUploadServiceIssueUsesPolicyApprovedFallback(t *testing.T) {
	t.Parallel()

	requirement := uploadProfileRequirement()
	requirement.RequiredAssurances = nil
	requirement.Fallbacks = []verification.Fallback{{
		On: []verification.FallbackCondition{verification.FallbackCaptureFailed},
		Acquisition: verification.Acquisition{
			Strategy: verification.StrategyAnyOf,
			Methods:  []evidence.Name{evidence.MethodFileUpload},
		},
	}}
	workflow := newUploadWorkflow(t, requirement)
	workflow.request.AcquisitionMethod = evidence.MethodFileUpload
	workflow.request.FallbackCondition = verification.FallbackCaptureFailed

	upload, err := workflow.service.Issue(
		context.Background(), workflow.captureContext, "fallback-selfie", workflow.request,
	)
	if err != nil {
		t.Fatalf("Issue(fallback) error = %v", err)
	}
	if record := upload.Record(); record.AcquisitionMethod != evidence.MethodFileUpload || len(record.Assurances) != 0 {
		t.Fatalf("fallback upload = %+v", record)
	}
}

func TestUploadServiceIssueDoesNotOverclaimAllOfAssurance(t *testing.T) {
	t.Parallel()

	requirement := uploadProfileRequirement()
	requirement.Acquisition = verification.Acquisition{
		Strategy: verification.StrategyAllOf,
		Methods:  []evidence.Name{evidence.MethodFileUpload, evidence.MethodLiveCamera},
	}
	workflow := newUploadWorkflow(t, requirement)
	workflow.request.AcquisitionMethod = evidence.MethodFileUpload

	upload, err := workflow.service.Issue(
		context.Background(), workflow.captureContext, "all-of-file", workflow.request,
	)
	if err != nil {
		t.Fatalf("Issue(all_of file) error = %v", err)
	}
	if assurances := upload.Record().Assurances; len(assurances) != 0 {
		t.Fatalf("file upload assurances = %v, want none", assurances)
	}
}

type uploadWorkflow struct {
	fixture        fixture
	service        *authority.UploadService
	repository     *uploadServiceRepository
	captureContext verification.CaptureContext
	request        authority.UploadRequest
}

type uploadServiceRepository struct {
	snapshot authority.Snapshot
	mutation evidence.UploadCreateMutation
}

func (repository *uploadServiceRepository) CaptureSnapshot(
	context.Context,
	tenant.Scope,
	id.Verification,
) (authority.Snapshot, error) {
	return repository.snapshot, nil
}

func (repository *uploadServiceRepository) CreateUpload(
	_ context.Context,
	_ tenant.Scope,
	mutation evidence.UploadCreateMutation,
) (evidence.Upload, error) {
	repository.mutation = mutation

	return mutation.Upload, nil
}

type captureUploadRepository struct{ creation verification.SessionCreation }

func (repository captureUploadRepository) FindForCapture(
	context.Context,
	access.CaptureTokenClaims,
) (verification.SessionCreation, error) {
	return repository.creation, nil
}

func newUploadWorkflow(t *testing.T, requirement verification.Requirement) uploadWorkflow {
	t.Helper()
	fixture := newFixture(t, false)
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	profile, err := verification.NewProfile(registry, []verification.Requirement{requirement})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}
	digest, err := verification.Digest(profile, registry)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	profileID := mustProfile(t, "prf_01ARZ3NDEKTSV4RRFFQ69G5FB5")
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	session, err := verification.RestoreSession(
		fixture.request.VerificationID, fixture.request.TenantID,
		verification.SessionStateCollecting, 1, profileID, 1, digest, profile,
		"local",
		policyID,
		fixture.now.Add(-time.Minute), fixture.now.Add(-time.Minute), fixture.now.Add(time.Hour), registry,
	)
	if err != nil {
		t.Fatalf("RestoreSession() error = %v", err)
	}
	credential, err := access.NewCaptureCredential(
		fixture.tokenID, session.TenantID(), session.ID(), 1,
		fixture.now.Add(-time.Minute), fixture.now.Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewCaptureCredential() error = %v", err)
	}
	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{7}, 32),
	})
	if err != nil {
		t.Fatalf("NewCaptureTokenKeyring() error = %v", err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, authorityClock{now: fixture.now})
	if err != nil {
		t.Fatalf("NewCaptureTokenSigner() error = %v", err)
	}
	presented, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	authenticator, err := verification.NewCaptureAuthenticator(
		captureUploadRepository{creation: verification.SessionCreation{
			Session: session, Credential: credential,
		}}, signer, authorityClock{now: fixture.now},
	)
	if err != nil {
		t.Fatalf("NewCaptureAuthenticator() error = %v", err)
	}
	captureContext, err := authenticator.Authenticate(context.Background(), presented.Reveal())
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	identifiers, err := id.NewGenerator(
		authorityClock{now: fixture.now}, bytes.NewReader(bytes.Repeat([]byte{11}, 256)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	repository := &uploadServiceRepository{snapshot: authority.Snapshot{
		Authority: fixture.authority, Notice: fixture.notice, Response: fixture.response,
	}}
	service, err := authority.NewUploadService(
		repository, repository, identifiers, authorityClock{now: fixture.now},
		catalog, evidence.DefaultUploadPolicy(), 24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewUploadService() error = %v", err)
	}
	body := []byte("private selfie")

	return uploadWorkflow{
		fixture: fixture, service: service, repository: repository, captureContext: captureContext,
		request: authority.UploadRequest{
			RequirementKey: requirement.Key, Artefact: evidence.ArtefactSelfieImage,
			AcquisitionMethod: evidence.MethodLiveCamera, ExpectedBytes: int64(len(body)),
			ExpectedDigest: string(platformcrypto.Sum(body)), MediaType: evidence.MediaTypeJPEG,
			Region: "idenqa.region.synthetic",
		},
	}
}

func uploadProfileRequirement() verification.Requirement {
	return verification.Requirement{
		Key: "selfie", Purpose: evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceSelfieImage,
		Artefacts:    []evidence.Name{evidence.ArtefactSelfieImage},
		Acquisition: verification.Acquisition{
			Strategy: verification.StrategyAnyOf,
			Methods:  []evidence.Name{evidence.MethodLiveCamera},
		},
		RequiredAssurances: []evidence.Name{
			evidence.AssuranceFreshness, evidence.AssuranceLiveCapture,
		},
		Constraints: []verification.Constraint{
			{
				Name: evidence.ConstraintAllowedMedia,
				Value: verification.ConstraintValue{
					Kind: evidence.ValueStringList, StringList: []string{evidence.MediaTypeJPEG},
				},
			},
			{
				Name:  evidence.ConstraintMaximumBytes,
				Value: verification.ConstraintValue{Kind: evidence.ValueInteger, Integer: 8 << 20},
			},
		},
	}
}

func mustProfile(t *testing.T, value string) id.Profile {
	t.Helper()
	identifier, err := id.ParseProfile(value)
	if err != nil {
		t.Fatal(err)
	}

	return identifier
}
