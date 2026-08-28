package evidence_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestGrantIssuerPinsLiveAuthorityAndAssetScope(t *testing.T) {
	t.Parallel()

	assetRecord, registry := validAssetRecord(t)
	asset, err := evidence.NewAvailable(assetRecord, registry)
	if err != nil {
		t.Fatalf("NewAvailable() error = %v", err)
	}
	scope, err := tenant.NewScope(assetRecord.TenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	grant, _, _ := validGrant(t)
	decision := evidence.AuthorizationDecision{
		AuthorityID: grant.Record().AuthorityID, ResponseID: grant.Record().ResponseID,
		PolicyReference: "tenant.policy.synthetic_v1",
	}
	repository := &grantRepositoryStub{}
	authorizer := &readAuthorizerStub{decision: decision}
	issuer, err := evidence.NewGrantIssuer(
		assetStoreStub{asset: asset}, authorizer, repository,
		grantIDGeneratorStub{identifier: grant.ID()}, mustCatalog(t, registry),
		assetClock{now: assetRecord.CreatedAt.Add(time.Hour)}, 10*time.Minute,
	)
	if err != nil {
		t.Fatalf("NewGrantIssuer() error = %v", err)
	}
	issued, err := issuer.Issue(context.Background(), scope, evidence.GrantInput{
		EvidenceID: asset.ID(), CheckReference: "check.selfie.match",
		Runner: grant.Record().Runner, Purpose: evidence.PurposeIdentityVerification,
		PermittedVariants:  []string{evidence.VariantOriginal},
		RecipientReference: "tenant.recipient.primary", OutputDestination: "workflow.result.normalized",
		MaximumUses: 1, TTL: 5 * time.Minute,
		Attribution: validGrantAttribution(),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	issuedRecord := issued.Record()
	if repository.created.ID() != issued.ID() || issuedRecord.EvidenceID != asset.ID() ||
		issuedRecord.AuthorityID != decision.AuthorityID || issuedRecord.ResponseID != decision.ResponseID ||
		issuedRecord.Region != assetRecord.Region {
		t.Fatalf("issued grant = %+v", issuedRecord)
	}
	if authorizer.request.EvidenceID != asset.ID() || authorizer.request.EvidenceType != assetRecord.EvidenceType {
		t.Fatalf("authorization request = %+v", authorizer.request)
	}
}

func TestReaderCommitsOnlyAuthenticatedAuditedPlaintext(t *testing.T) {
	t.Parallel()

	fixture := newReadFixture(t, []byte("private selfie evidence"))
	receiver := &transactionalReceiver{binding: fixture.binding}
	if err := fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver); err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if got := receiver.committed.String(); got != "private selfie evidence" {
		t.Fatalf("committed plaintext = %q", got)
	}
	if len(fixture.outcomes.outcomes) != 1 || fixture.outcomes.outcomes[0] != evidence.GrantOutcomeSucceeded {
		t.Fatalf("outcomes = %v, want succeeded", fixture.outcomes.outcomes)
	}
	if _, ok := fixture.outcomes.redemptions[fixture.input.RedemptionID]; !ok {
		t.Fatalf("claimed redemption %s was not persisted", fixture.input.RedemptionID)
	}
}

func TestReaderShortCircuitsCompletedRedemptionReplay(t *testing.T) {
	t.Parallel()

	fixture := newReadFixture(t, []byte("private selfie evidence"))
	receiver := &transactionalReceiver{binding: fixture.binding}
	for attempt := 0; attempt < 2; attempt++ {
		if err := fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver); err != nil {
			t.Fatalf("Read(attempt %d) error = %v", attempt+1, err)
		}
	}
	if receiver.productions != 1 || receiver.committed.String() != "private selfie evidence" {
		t.Fatalf("productions = %d, committed = %q", receiver.productions, receiver.committed.String())
	}
	if len(fixture.outcomes.outcomes) != 1 {
		t.Fatalf("outcomes = %v, want one terminal outcome", fixture.outcomes.outcomes)
	}
}

func TestReaderResumesOutcomeAfterCommittedDelivery(t *testing.T) {
	t.Parallel()

	fixture := newReadFixture(t, []byte("private selfie evidence"))
	receiver := &transactionalReceiver{binding: fixture.binding}
	fixture.outcomes.outcomeErr = errors.New("synthetic outcome failure")
	if err := fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver); err == nil {
		t.Fatal("Read(first attempt) error = nil")
	}
	fixture.outcomes.outcomeErr = nil
	if err := fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver); err != nil {
		t.Fatalf("Read(retry) error = %v", err)
	}
	if receiver.productions != 1 || receiver.committed.String() != "private selfie evidence" {
		t.Fatalf("productions = %d, committed = %q", receiver.productions, receiver.committed.String())
	}
	if len(fixture.outcomes.outcomes) != 1 || fixture.outcomes.outcomes[0] != evidence.GrantOutcomeSucceeded {
		t.Fatalf("outcomes = %v, want succeeded", fixture.outcomes.outcomes)
	}
}

func TestReaderRollsBackPartialPlaintextOnOpenFailure(t *testing.T) {
	t.Parallel()

	fixture := newReadFixtureWithOpener(t, []byte("private selfie evidence"), partialFailingOpener{})
	receiver := &transactionalReceiver{binding: fixture.binding}
	if err := fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver); err == nil {
		t.Fatal("Read() error = nil")
	}
	if receiver.committed.Len() != 0 {
		t.Fatalf("committed partial plaintext = %q", receiver.committed.String())
	}
	if got := fixture.outcomes.outcomes; len(got) != 1 || got[0] != evidence.GrantOutcomeFailed {
		t.Fatalf("outcomes = %v, want failed", got)
	}
}

func TestReaderQuarantinesPlaintextDigestMismatch(t *testing.T) {
	t.Parallel()

	fixture := newReadFixture(t, []byte("private selfie evidence"))
	record := fixture.asset.Record()
	record.Content.PlaintextDigest = string(platformcrypto.Sum([]byte("different plaintext")))
	asset, err := evidence.RestoreAsset(record, fixture.registry)
	if err != nil {
		t.Fatalf("RestoreAsset() error = %v", err)
	}
	fixture.assets.asset = asset
	receiver := &transactionalReceiver{binding: fixture.binding}
	err = fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver)
	if !errors.Is(err, evidence.ErrContentIntegrity) {
		t.Fatalf("Read() error = %v, want ErrContentIntegrity", err)
	}
	if receiver.committed.Len() != 0 || fixture.lifecycle.updated.State() != evidence.StateQuarantined {
		t.Fatalf("committed = %d, lifecycle state = %q", receiver.committed.Len(), fixture.lifecycle.updated.State())
	}
	if got := fixture.outcomes.outcomes; len(got) != 1 || got[0] != evidence.GrantOutcomeIntegrityFailed {
		t.Fatalf("outcomes = %v, want integrity_failed", got)
	}
}

func TestReaderDeniesQuarantinedEvidenceAfterClaim(t *testing.T) {
	t.Parallel()

	fixture := newReadFixture(t, []byte("private selfie evidence"))
	quarantined, err := fixture.asset.Quarantine(
		fixture.asset.Version(), "policy.subject_request", fixture.now.Add(time.Minute),
	)
	if err != nil {
		t.Fatalf("Quarantine() error = %v", err)
	}
	fixture.assets.asset = quarantined
	receiver := &transactionalReceiver{binding: fixture.binding}
	if err := fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver); !errors.Is(err, evidence.ErrReadDenied) {
		t.Fatalf("Read() error = %v, want ErrReadDenied", err)
	}
	if receiver.committed.Len() != 0 || fixture.authorizer.calls != 0 {
		t.Fatalf("committed = %d, authorizer calls = %d", receiver.committed.Len(), fixture.authorizer.calls)
	}
	if got := fixture.outcomes.outcomes; len(got) != 1 || got[0] != evidence.GrantOutcomeDenied {
		t.Fatalf("outcomes = %v, want denied", got)
	}
}

func TestReaderDeniesReceiverOutsideGrantBinding(t *testing.T) {
	t.Parallel()

	fixture := newReadFixture(t, []byte("private selfie evidence"))
	binding := fixture.binding
	binding.OutputDestination = "workflow.result.other"
	receiver := &transactionalReceiver{binding: binding}
	if err := fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver); !errors.Is(err, evidence.ErrReadDenied) {
		t.Fatalf("Read() error = %v, want ErrReadDenied", err)
	}
	if receiver.committed.Len() != 0 || fixture.authorizer.calls != 0 {
		t.Fatalf("committed = %d, authorizer calls = %d", receiver.committed.Len(), fixture.authorizer.calls)
	}
}

func TestReaderReportsIncompleteIntegrityQuarantine(t *testing.T) {
	t.Parallel()

	fixture := newReadFixture(t, []byte("private selfie evidence"))
	record := fixture.asset.Record()
	record.Content.PlaintextDigest = string(platformcrypto.Sum([]byte("different plaintext")))
	asset, err := evidence.RestoreAsset(record, fixture.registry)
	if err != nil {
		t.Fatalf("RestoreAsset() error = %v", err)
	}
	fixture.assets.asset = asset
	fixture.lifecycle.err = errors.New("synthetic durable quarantine failure")
	receiver := &transactionalReceiver{binding: fixture.binding}
	err = fixture.reader.Read(context.Background(), fixture.scope, fixture.input, receiver)
	if !errors.Is(err, evidence.ErrContentIntegrity) ||
		!errors.Is(err, evidence.ErrQuarantineIncomplete) {
		t.Fatalf("Read() error = %v, want integrity and incomplete quarantine", err)
	}
	if got := fixture.outcomes.outcomes; len(got) != 1 || got[0] != evidence.GrantOutcomeFailed {
		t.Fatalf("outcomes = %v, want failed", got)
	}
}

func TestReaderQuarantinesIntegrityFailureAfterRequestCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fixture := newReadFixtureWithOpener(
		t, []byte("private selfie evidence"), cancellingIntegrityOpener{cancel: cancel},
	)
	receiver := &transactionalReceiver{binding: fixture.binding}
	err := fixture.reader.Read(ctx, fixture.scope, fixture.input, receiver)
	if !errors.Is(err, evidence.ErrContentIntegrity) {
		t.Fatalf("Read() error = %v, want ErrContentIntegrity", err)
	}
	if fixture.lifecycle.ctxErr != nil || fixture.lifecycle.updated.State() != evidence.StateQuarantined {
		t.Fatalf("quarantine context error = %v, state = %q", fixture.lifecycle.ctxErr, fixture.lifecycle.updated.State())
	}
}

type readFixture struct {
	reader     *evidence.Reader
	scope      tenant.Scope
	input      evidence.ReadInput
	asset      evidence.Asset
	registry   evidence.Registry
	now        time.Time
	assets     *assetStoreStub
	lifecycle  *assetLifecycleStub
	authorizer *readAuthorizerStub
	outcomes   *grantRepositoryStub
	binding    evidence.ReceiverBinding
}

func newReadFixture(t *testing.T, plaintext []byte) readFixture {
	t.Helper()
	return newReadFixtureWithOpener(t, plaintext, nil)
}

func newReadFixtureWithOpener(
	t *testing.T,
	plaintext []byte,
	opener evidence.ContentOpener,
) readFixture {
	t.Helper()
	protection := newProtectionFixture(t)
	input := protection.input(bytes.NewReader(plaintext))
	asset, err := protection.protector.Protect(context.Background(), protection.scope, input)
	if err != nil {
		t.Fatalf("Protect() error = %v", err)
	}
	grantTemplate, _, _ := validGrant(t)
	template := grantTemplate.Record()
	now := input.CreatedAt.Add(time.Hour)
	grant, err := evidence.NewGrant(evidence.GrantRecord{
		ID: template.ID, TenantID: protection.scope.ID(), SubjectID: input.SubjectID,
		VerificationID: input.VerificationID, EvidenceID: input.ID,
		RequirementKey: input.RequirementKey,
		AuthorityID:    template.AuthorityID, ResponseID: template.ResponseID,
		CheckReference: template.CheckReference, Runner: template.Runner,
		Purpose: template.Purpose, Operation: evidence.OperationPlaintextRead,
		PermittedVariants: []string{evidence.VariantOriginal}, Region: input.Region,
		RecipientReference: template.RecipientReference, OutputDestination: template.OutputDestination,
		PolicyReference: template.PolicyReference, MaximumUses: 1,
		CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(59 * time.Minute),
	})
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	repository := &grantRepositoryStub{grant: grant}
	assets := &assetStoreStub{asset: asset}
	lifecycle := &assetLifecycleStub{}
	authorizer := &readAuthorizerStub{decision: evidence.AuthorizationDecision{
		AuthorityID: template.AuthorityID, ResponseID: template.ResponseID,
		PolicyReference: template.PolicyReference,
	}}
	if opener == nil {
		opener = protection.streaming
	}
	reader, err := evidence.NewReader(
		repository, repository, assets, lifecycle, authorizer,
		opener, protection.objects, assetClock{now: now},
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewReader() error = %v", err)
	}
	redemptionID, err := id.ParseRedemption("rdm_01ARZ3NDEKTSV4RRFFQ69G5FB4")
	if err != nil {
		t.Fatalf("ParseRedemption() error = %v", err)
	}
	binding := evidence.ReceiverBinding{
		Runner: template.Runner, CheckReference: template.CheckReference,
		OutputDestination: template.OutputDestination,
	}
	return readFixture{
		reader: reader, scope: protection.scope,
		input: evidence.ReadInput{
			GrantID: grant.ID(), RedemptionID: redemptionID, Runner: template.Runner,
		},
		asset: asset, registry: protection.registry, now: now, assets: assets,
		lifecycle: lifecycle, authorizer: authorizer, outcomes: repository, binding: binding,
	}
}

type assetStoreStub struct {
	asset evidence.Asset
	err   error
}

func (store assetStoreStub) Find(context.Context, tenant.Scope, id.Evidence) (evidence.Asset, error) {
	return store.asset, store.err
}

type assetLifecycleStub struct {
	updated evidence.Asset
	err     error
	ctxErr  error
}

func (store *assetLifecycleStub) QuarantineIntegrity(
	ctx context.Context,
	_ tenant.Scope,
	asset evidence.Asset,
	_ int64,
) error {
	store.ctxErr = ctx.Err()
	store.updated = asset
	return store.err
}

type readAuthorizerStub struct {
	decision evidence.AuthorizationDecision
	request  evidence.ReadAuthorization
	err      error
	calls    int
}

func (authorizer *readAuthorizerStub) AuthorizeEvidence(
	_ context.Context,
	_ tenant.Scope,
	request evidence.ReadAuthorization,
) (evidence.AuthorizationDecision, error) {
	authorizer.calls++
	authorizer.request = request
	return authorizer.decision, authorizer.err
}

type grantRepositoryStub struct {
	grant       evidence.Grant
	created     evidence.Grant
	outcomes    []evidence.GrantOutcome
	redemptions map[id.Redemption]evidence.Redemption
	err         error
	outcomeErr  error
}

func (repository *grantRepositoryStub) CreateGrant(
	_ context.Context,
	_ tenant.Scope,
	grant evidence.Grant,
	_ evidence.CommandAttribution,
) error {
	repository.created = grant
	return repository.err
}

func validGrantAttribution() evidence.CommandAttribution {
	return evidence.CommandAttribution{
		Principal:   evidence.Actor{Type: "internal.service", ID: "workflow.core"},
		TenantActor: evidence.Actor{Type: "tenant.service", ID: "tenant.api"},
		Reason:      "issue evidence processing grant",
	}
}

func (repository *grantRepositoryStub) ClaimGrant(
	_ context.Context,
	_ tenant.Scope,
	_ id.Grant,
	redemptionID id.Redemption,
	runner evidence.Runner,
	now time.Time,
) (evidence.Redemption, error) {
	if repository.err != nil {
		return evidence.Redemption{}, repository.err
	}
	if existing, ok := repository.redemptions[redemptionID]; ok {
		return existing, nil
	}
	claimed, err := repository.grant.Claim(runner, now)
	if err == nil {
		repository.grant = claimed
	}
	redemption, restoreErr := evidence.NewRedemption(redemptionID, claimed)
	if restoreErr != nil {
		return evidence.Redemption{}, restoreErr
	}
	if repository.redemptions == nil {
		repository.redemptions = make(map[id.Redemption]evidence.Redemption)
	}
	repository.redemptions[redemptionID] = redemption
	return redemption, err
}

func (repository *grantRepositoryStub) RecordGrantOutcome(
	_ context.Context,
	_ tenant.Scope,
	redemption evidence.Redemption,
	outcome evidence.GrantOutcome,
	_ time.Time,
) error {
	if repository.outcomeErr != nil {
		return repository.outcomeErr
	}
	repository.outcomes = append(repository.outcomes, outcome)
	completed, err := redemption.Complete(outcome)
	if err != nil {
		return err
	}
	repository.redemptions[redemption.ID()] = completed
	return repository.err
}

type grantIDGeneratorStub struct{ identifier id.Grant }

func (generator grantIDGeneratorStub) NewGrant() (id.Grant, error) { return generator.identifier, nil }

type transactionalReceiver struct {
	binding     evidence.ReceiverBinding
	committed   bytes.Buffer
	redemptions map[id.Redemption]struct{}
	productions int
}

func (receiver *transactionalReceiver) Binding() evidence.ReceiverBinding {
	return receiver.binding
}

func (receiver *transactionalReceiver) Receive(
	_ context.Context,
	redemptionID id.Redemption,
	_ string,
	produce func(io.Writer) error,
	afterCommit func() error,
) error {
	if _, committed := receiver.redemptions[redemptionID]; committed {
		return afterCommit()
	}
	var staged bytes.Buffer
	receiver.productions++
	if err := produce(&staged); err != nil {
		return err
	}
	if _, err := receiver.committed.Write(staged.Bytes()); err != nil {
		return err
	}
	if receiver.redemptions == nil {
		receiver.redemptions = make(map[id.Redemption]struct{})
	}
	receiver.redemptions[redemptionID] = struct{}{}
	return afterCommit()
}

type partialFailingOpener struct{}

func (partialFailingOpener) Open(
	_ context.Context,
	destination io.Writer,
	_ io.Reader,
	_ platformcrypto.Envelope,
	_ platformcrypto.Context,
) error {
	if _, err := destination.Write([]byte("partial plaintext")); err != nil {
		return err
	}
	return errors.New("synthetic open failure")
}

type cancellingIntegrityOpener struct{ cancel context.CancelFunc }

func (opener cancellingIntegrityOpener) Open(
	_ context.Context,
	_ io.Writer,
	_ io.Reader,
	_ platformcrypto.Envelope,
	_ platformcrypto.Context,
) error {
	opener.cancel()
	return platformcrypto.ErrCiphertextIntegrity
}

func mustCatalog(t *testing.T, registry evidence.Registry) evidence.Catalog {
	t.Helper()
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	return catalog
}
