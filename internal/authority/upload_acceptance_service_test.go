package authority_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestUploadAcceptanceServiceAcceptsOnlyAfterLiveAuthorityEvaluation(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	accepted, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(workflow.body),
	)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if accepted.ID() != workflow.claimed.ID() || workflow.stager.discards != 0 ||
		workflow.committer.calls != 1 || workflow.authorities.calls != 1 ||
		workflow.reconciler.creates != 1 || workflow.failures.retries != 0 ||
		workflow.failures.rejections != 0 {
		t.Fatalf("accepted=%s discard=%d commits=%d authority_calls=%d",
			accepted.ID(), workflow.stager.discards, workflow.committer.calls, workflow.authorities.calls)
	}
	mutation := workflow.committer.mutation
	if mutation.UploadID != workflow.claimed.ID() || mutation.Attempt != workflow.claimed.Attempt() ||
		mutation.ExpectedVersion != workflow.claimed.Version() ||
		mutation.CaptureTokenID != workflow.captureContext.TokenID() ||
		mutation.PlaintextBytes != int64(len(workflow.body)) || mutation.EventID.IsZero() {
		t.Fatalf("acceptance mutation = %+v", mutation)
	}
	input := workflow.stager.input
	if input.ID != workflow.claimed.Record().EvidenceID ||
		input.AcquisitionMethod != workflow.claimed.Record().AcquisitionMethod ||
		input.ContentRevision != 1 || input.Plaintext == nil {
		t.Fatalf("protection input = %+v", input)
	}
}

func TestUploadAcceptanceServiceReplaysAcceptedResultWithoutReadingBody(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	record := workflow.claimed.Record()
	record.State = evidence.UploadStateAccepted
	record.Version++
	record.UpdatedAt = workflow.now
	record.LeaseExpiresAt = nil
	record.AcceptedAt = &workflow.now
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	accepted, err := evidence.RestoreUpload(record, registry)
	if err != nil {
		t.Fatalf("RestoreUpload() error = %v", err)
	}
	workflow.preflight.upload = accepted
	body := &acceptanceBodyProbe{}

	replayed, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, accepted.ID(), workflow.metadata, body,
	)
	if err != nil {
		t.Fatalf("Accept() error = %v", err)
	}
	if replayed.State() != evidence.UploadStateAccepted || body.reads != 0 ||
		workflow.stager.input.Plaintext != nil || workflow.committer.calls != 0 ||
		workflow.authorities.calls != 0 {
		t.Fatalf(
			"state=%q reads=%d commits=%d authority=%d",
			replayed.State(), body.reads, workflow.committer.calls, workflow.authorities.calls,
		)
	}
}

type acceptanceBodyProbe struct{ reads int }

func (probe *acceptanceBodyProbe) Read([]byte) (int, error) {
	probe.reads++

	return 0, io.EOF
}

func TestUploadAcceptanceServiceFailsClosedAndDiscardsPreparedCiphertext(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		change     func(*testing.T, *uploadAcceptanceWorkflow)
		want       error
		wantReject bool
	}{
		{
			name: "current authority restricted",
			change: func(t *testing.T, workflow *uploadAcceptanceWorkflow) {
				t.Helper()
				current := workflow.authorities.snapshot.Authority
				if err := current.Restrict(workflow.now); err != nil {
					t.Fatalf("Restrict() error = %v", err)
				}
				workflow.authorities.snapshot.Authority = current
			},
			want: authority.ErrProcessingNotPermitted, wantReject: true,
		},
		{
			name: "pinned response no longer current",
			change: func(_ *testing.T, workflow *uploadAcceptanceWorkflow) {
				workflow.authorities.snapshot.Response = nil
			},
			want: authority.ErrProcessingNotPermitted, wantReject: true,
		},
		{
			name: "event identity generation fails",
			change: func(_ *testing.T, workflow *uploadAcceptanceWorkflow) {
				workflow.identifiers.err = errors.New("entropy unavailable")
			},
		},
		{
			name: "definite acceptance failure",
			change: func(_ *testing.T, workflow *uploadAcceptanceWorkflow) {
				workflow.committer.err = errors.New("transaction rolled back")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			workflow := newUploadAcceptanceWorkflow(t)
			test.change(t, &workflow)
			_, err := workflow.service.Accept(
				context.Background(), workflow.captureContext, workflow.claimed.ID(),
				workflow.metadata, bytes.NewReader(workflow.body),
			)
			if err == nil || (test.want != nil && !errors.Is(err, test.want)) {
				t.Fatalf("Accept() error = %v, want %v", err, test.want)
			}
			if workflow.stager.discards != 1 {
				t.Fatalf("Discard() calls = %d, want 1", workflow.stager.discards)
			}
			if test.wantReject && (workflow.failures.rejections != 1 || workflow.failures.retries != 0) {
				t.Fatalf("rejections=%d retries=%d", workflow.failures.rejections, workflow.failures.retries)
			}
			if !test.wantReject && (workflow.failures.retries != 1 || workflow.failures.rejections != 0) {
				t.Fatalf("retries=%d rejections=%d", workflow.failures.retries, workflow.failures.rejections)
			}
		})
	}
}

func TestUploadAcceptanceServicePreservesBodyClassification(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	corrupt := append(bytes.Clone(workflow.body), 0x01)
	_, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(corrupt),
	)
	if !errors.Is(err, evidence.ErrUploadBodyLength) {
		t.Fatalf("Accept(corrupt body) error = %v, want ErrUploadBodyLength", err)
	}
	if workflow.authorities.calls != 0 || workflow.committer.calls != 0 || workflow.stager.discards != 0 {
		t.Fatalf("authority=%d commits=%d discards=%d after body rejection",
			workflow.authorities.calls, workflow.committer.calls, workflow.stager.discards)
	}
	if workflow.failures.retries != 1 || workflow.failures.rejections != 0 {
		t.Fatalf("retries=%d rejections=%d", workflow.failures.retries, workflow.failures.rejections)
	}
}

func TestUploadAcceptanceServiceTerminallyRejectsInvalidMediaSignature(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	invalid := bytes.Repeat([]byte("not-a-jpeg"), 8)
	_, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(invalid),
	)
	if !errors.Is(err, evidence.ErrUploadSignature) {
		t.Fatalf("Accept(invalid signature) error = %v, want ErrUploadSignature", err)
	}
	if workflow.failures.rejections != 1 || workflow.failures.retries != 0 ||
		workflow.failures.reason != "evidence.upload.signature_mismatch" || workflow.stager.discards != 0 {
		t.Fatalf("rejections=%d retries=%d reason=%q discards=%d",
			workflow.failures.rejections, workflow.failures.retries,
			workflow.failures.reason, workflow.stager.discards)
	}
}

func TestUploadAcceptanceServiceRetainsCiphertextForUnknownCommitOutcome(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	workflow.committer.err = evidence.ErrUploadAcceptanceOutcomeUnknown
	_, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(workflow.body),
	)
	if !errors.Is(err, evidence.ErrAcceptanceReconciliationRequired) ||
		!errors.Is(err, evidence.ErrUploadAcceptanceOutcomeUnknown) {
		t.Fatalf("Accept(unknown commit) error = %v, want reconciliation and cause", err)
	}
	if workflow.stager.discards != 0 {
		t.Fatalf("Discard() calls = %d, want 0 for unknown commit", workflow.stager.discards)
	}
	if workflow.failures.retries != 0 || workflow.failures.rejections != 0 {
		t.Fatalf("unknown commit retries=%d rejections=%d", workflow.failures.retries, workflow.failures.rejections)
	}
}

func TestUploadAcceptanceServiceRechecksSessionAfterStreaming(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	source := &acceptanceClock{times: []time.Time{workflow.now, workflow.now.Add(2 * time.Hour)}}
	service, err := authority.NewUploadAcceptanceService(
		workflow.authorities, workflow.preflight, workflow.stager, workflow.reconciler,
		workflow.failures, workflow.committer, workflow.identifiers, source,
	)
	if err != nil {
		t.Fatalf("NewUploadAcceptanceService() error = %v", err)
	}
	_, err = service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(workflow.body),
	)
	if !errors.Is(err, authority.ErrProcessingNotPermitted) {
		t.Fatalf("Accept(expired after stream) error = %v, want ErrProcessingNotPermitted", err)
	}
	if workflow.stager.discards != 1 || workflow.authorities.calls != 0 || workflow.committer.calls != 0 {
		t.Fatalf("discards=%d authority=%d commits=%d",
			workflow.stager.discards, workflow.authorities.calls, workflow.committer.calls)
	}
	if workflow.failures.retries != 1 || workflow.failures.rejections != 0 {
		t.Fatalf("expired retries=%d rejections=%d", workflow.failures.retries, workflow.failures.rejections)
	}
}

func TestUploadAcceptanceServiceJoinsCleanupFailure(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	acceptanceErr := errors.New("transaction rolled back")
	cleanupErr := evidence.ErrCleanupRequired
	workflow.committer.err = acceptanceErr
	workflow.stager.discardErr = cleanupErr
	_, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(workflow.body),
	)
	if !errors.Is(err, acceptanceErr) || !errors.Is(err, cleanupErr) {
		t.Fatalf("Accept(cleanup failure) error = %v, want both causes", err)
	}
	if workflow.failures.retries != 1 {
		t.Fatalf("failure persistence retries = %d, want 1", workflow.failures.retries)
	}
}

func TestUploadAcceptanceServiceRecoversUnrecordedCleanupFailureBeforeReleasingAttempt(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	obligationErr := errors.New("database temporarily unavailable")
	workflow.reconciler.createErrors = []error{obligationErr, nil}
	workflow.stager.discardErr = evidence.ErrCleanupRequired
	_, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(workflow.body),
	)
	if !errors.Is(err, obligationErr) || !errors.Is(err, evidence.ErrCleanupRequired) {
		t.Fatalf("Accept(unrecorded cleanup) error = %v", err)
	}
	if workflow.reconciler.creates != 2 || workflow.failures.retries != 1 ||
		workflow.committer.calls != 0 {
		t.Fatalf("reconciliation creates=%d retries=%d commits=%d",
			workflow.reconciler.creates, workflow.failures.retries, workflow.committer.calls)
	}
}

func TestUploadAcceptanceServiceJoinsFailurePersistenceError(t *testing.T) {
	t.Parallel()

	workflow := newUploadAcceptanceWorkflow(t)
	acceptanceErr := errors.New("transaction rolled back")
	persistenceErr := errors.New("failure transition unavailable")
	workflow.committer.err = acceptanceErr
	workflow.failures.err = persistenceErr
	_, err := workflow.service.Accept(
		context.Background(), workflow.captureContext, workflow.claimed.ID(),
		workflow.metadata, bytes.NewReader(workflow.body),
	)
	if !errors.Is(err, acceptanceErr) || !errors.Is(err, persistenceErr) {
		t.Fatalf("Accept(failure persistence) error = %v", err)
	}
}

type uploadAcceptanceWorkflow struct {
	now            time.Time
	service        *authority.UploadAcceptanceService
	captureContext verification.CaptureContext
	claimed        evidence.Upload
	metadata       evidence.UploadMetadata
	body           []byte
	authorities    *acceptanceAuthorityStub
	preflight      *acceptancePreflightStub
	stager         *acceptanceStagerStub
	committer      *acceptanceCommitterStub
	reconciler     *acceptanceReconcilerStub
	failures       *acceptanceFailureStub
	identifiers    *acceptanceIDStub
}

type acceptanceAuthorityStub struct {
	snapshot authority.Snapshot
	calls    int
}

func (stub *acceptanceAuthorityStub) CaptureSnapshot(
	context.Context,
	tenant.Scope,
	id.Verification,
) (authority.Snapshot, error) {
	stub.calls++

	return stub.snapshot, nil
}

type acceptancePreflightStub struct{ upload evidence.Upload }

func (stub *acceptancePreflightStub) Begin(
	context.Context,
	evidence.UploadPrincipal,
	id.Upload,
	evidence.UploadMetadata,
) (evidence.Upload, error) {
	return stub.upload, nil
}

type acceptanceStagerStub struct {
	input      evidence.ProtectionInput
	discards   int
	discardErr error
}

func (stub *acceptanceStagerStub) Prepare(
	_ context.Context,
	_ tenant.Scope,
	input evidence.ProtectionInput,
) (evidence.PreparedEvidence, error) {
	stub.input = input
	if _, err := io.Copy(io.Discard, input.Plaintext); err != nil {
		return evidence.PreparedEvidence{}, fmt.Errorf("consume protected plaintext: %w", err)
	}

	return evidence.PreparedEvidence{}, nil
}

func (stub *acceptanceStagerStub) Discard(context.Context, evidence.PreparedEvidence) error {
	stub.discards++

	return stub.discardErr
}

type acceptanceCommitterStub struct {
	result   evidence.Upload
	mutation evidence.UploadAcceptance
	err      error
	calls    int
}

type acceptanceReconcilerStub struct {
	creates      int
	resolves     int
	createErrors []error
	resolveErr   error
}

func (stub *acceptanceReconcilerStub) CreateObjectReconciliation(
	context.Context,
	tenant.Scope,
	evidence.Upload,
	evidence.PreparedEvidence,
	time.Time,
) error {
	stub.creates++
	return stub.nextCreateError()
}

func (stub *acceptanceReconcilerStub) CreateObjectReconciliationForObject(
	context.Context,
	tenant.Scope,
	evidence.Upload,
	objectstore.Object,
	time.Time,
) error {
	stub.creates++
	return stub.nextCreateError()
}

func (stub *acceptanceReconcilerStub) ResolveObjectReconciliation(
	context.Context,
	tenant.Scope,
	id.Upload,
	uint32,
	objectstore.Object,
	evidence.ReconciliationState,
	time.Time,
) error {
	stub.resolves++
	return stub.resolveErr
}

func (stub *acceptanceReconcilerStub) nextCreateError() error {
	index := stub.creates - 1
	if index < len(stub.createErrors) {
		return stub.createErrors[index]
	}
	return nil
}

type acceptanceFailureStub struct {
	retries    int
	rejections int
	reason     string
	err        error
}

func (stub *acceptanceFailureStub) FailUploadAttempt(
	context.Context,
	tenant.Scope,
	id.CaptureToken,
	id.Upload,
	int64,
	uint32,
	time.Time,
) (evidence.Upload, error) {
	stub.retries++
	return evidence.Upload{}, stub.err
}

func (stub *acceptanceFailureStub) RejectUploadAttempt(
	_ context.Context,
	_ tenant.Scope,
	_ id.CaptureToken,
	_ id.Upload,
	_ int64,
	_ uint32,
	reason string,
	_ time.Time,
) (evidence.Upload, error) {
	stub.rejections++
	stub.reason = reason
	return evidence.Upload{}, stub.err
}

func (stub *acceptanceCommitterStub) AcceptUpload(
	_ context.Context,
	_ tenant.Scope,
	mutation evidence.UploadAcceptance,
) (evidence.Upload, error) {
	stub.calls++
	stub.mutation = mutation

	return stub.result, stub.err
}

type acceptanceIDStub struct{ err error }

func (stub *acceptanceIDStub) NewEvent() (id.Event, error) {
	if stub.err != nil {
		return id.Event{}, stub.err
	}

	return id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
}

type acceptanceClock struct {
	times []time.Time
	next  int
}

func (source *acceptanceClock) Now() time.Time {
	if source.next >= len(source.times) {
		return source.times[len(source.times)-1]
	}
	value := source.times[source.next]
	source.next++

	return value
}

func newUploadAcceptanceWorkflow(t *testing.T) uploadAcceptanceWorkflow {
	t.Helper()
	issuance := newUploadWorkflow(t, uploadProfileRequirement())
	body := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("selfie"), 8)...)
	issuance.request.ExpectedBytes = int64(len(body))
	issuance.request.ExpectedDigest = string(platformcrypto.Sum(body))
	issued, err := issuance.service.Issue(
		context.Background(), issuance.captureContext, "accept-selfie", issuance.request,
	)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	claimed, err := issued.ClaimAttempt(issued.Version(), issuance.fixture.now)
	if err != nil {
		t.Fatalf("ClaimAttempt() error = %v", err)
	}
	authorities := &acceptanceAuthorityStub{snapshot: issuance.repository.snapshot}
	preflight := &acceptancePreflightStub{upload: claimed}
	stager := &acceptanceStagerStub{}
	committer := &acceptanceCommitterStub{result: claimed}
	reconciler := &acceptanceReconcilerStub{}
	failures := &acceptanceFailureStub{}
	identifiers := &acceptanceIDStub{}
	service, err := authority.NewUploadAcceptanceService(
		authorities, preflight, stager, reconciler, failures, committer, identifiers,
		authorityClock{now: issuance.fixture.now},
	)
	if err != nil {
		t.Fatalf("NewUploadAcceptanceService() error = %v", err)
	}

	return uploadAcceptanceWorkflow{
		now: issuance.fixture.now, service: service, captureContext: issuance.captureContext,
		claimed: claimed, body: body, authorities: authorities, preflight: preflight,
		stager: stager, committer: committer, reconciler: reconciler, failures: failures,
		identifiers: identifiers,
		metadata: evidence.UploadMetadata{
			ExpectedVersion: issued.Version(), ContentLength: int64(len(body)),
			MediaType: evidence.MediaTypeJPEG, Digest: platformcrypto.Sum(body),
		},
	}
}
