package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	plaintextDigestFailureReason = "evidence.integrity.plaintext_digest"
	ciphertextFailureReason      = "evidence.integrity.ciphertext_object"
)

var (
	// ErrContentIntegrity means authenticated evidence did not match its durable integrity metadata.
	ErrContentIntegrity = errors.New("evidence: content integrity failure")
	// ErrQuarantineIncomplete means a proven integrity failure was not durably blocked.
	ErrQuarantineIncomplete = errors.New("evidence: integrity quarantine incomplete")
)

// GrantInput requests one durable, narrow evidence-processing capability.
type GrantInput struct {
	EvidenceID         id.Evidence
	CheckReference     string
	Runner             Runner
	Purpose            Name
	PermittedVariants  []string
	RecipientReference string
	OutputDestination  string
	MaximumUses        uint32
	TTL                time.Duration
	Attribution        CommandAttribution
}

// GrantIssuer validates current evidence and processing authority before
// durably recording a scoped grant.
type GrantIssuer struct {
	assets      AssetFinder
	authorizer  ReadAuthorizer
	repository  GrantCreator
	identifiers GrantIDGenerator
	catalog     Catalog
	clock       clock.Clock
	maximumTTL  time.Duration
}

// NewGrantIssuer constructs a processing-grant issuer.
func NewGrantIssuer(
	assets AssetFinder,
	authorizer ReadAuthorizer,
	repository GrantCreator,
	identifiers GrantIDGenerator,
	catalog Catalog,
	source clock.Clock,
	maximumTTL time.Duration,
) (*GrantIssuer, error) {
	if assets == nil || authorizer == nil || repository == nil || identifiers == nil ||
		catalog.IsZero() || source == nil || maximumTTL <= 0 || maximumTTL > maxGrantLifetime {
		return nil, errors.New("evidence: grant issuer dependencies are invalid")
	}
	return &GrantIssuer{
		assets: assets, authorizer: authorizer, repository: repository,
		identifiers: identifiers, catalog: catalog, clock: source, maximumTTL: maximumTTL,
	}, nil
}

// Issue creates one unredeemed grant after a live authority decision.
func (issuer *GrantIssuer) Issue(
	ctx context.Context,
	scope tenant.Scope,
	input GrantInput,
) (Grant, error) {
	if ctx == nil || scope.ID().IsZero() || input.EvidenceID.IsZero() || input.TTL <= 0 ||
		!input.Attribution.Valid() ||
		input.TTL > issuer.maximumTTL {
		return Grant{}, ErrGrantDenied
	}
	if err := ctx.Err(); err != nil {
		return Grant{}, fmt.Errorf("issue evidence grant: %w", err)
	}
	asset, err := issuer.assets.Find(ctx, scope, input.EvidenceID)
	if err != nil || !asset.CanRead() {
		return Grant{}, ErrReadDenied
	}
	assetRecord := asset.Record()
	registry, err := issuer.catalog.Resolve(assetRecord.Registry)
	if err != nil || !registry.Has(KindPurpose, input.Purpose) {
		return Grant{}, ErrReadDenied
	}
	authorization := ReadAuthorization{
		TenantID: scope.ID(), SubjectID: assetRecord.SubjectID,
		VerificationID: assetRecord.VerificationID, EvidenceID: assetRecord.ID,
		RequirementKey: assetRecord.RequirementKey,
		Purpose:        input.Purpose, EvidenceType: assetRecord.EvidenceType,
		RecipientReference: input.RecipientReference, Region: assetRecord.Region,
	}
	decision, err := issuer.authorizer.AuthorizeEvidence(ctx, scope, authorization)
	if err != nil {
		return Grant{}, ErrReadDenied
	}
	identifier, err := issuer.identifiers.NewGrant()
	if err != nil {
		return Grant{}, fmt.Errorf("generate evidence grant id: %w", err)
	}
	now := issuer.clock.Now().UTC()
	grant, err := NewGrant(GrantRecord{
		ID: identifier, TenantID: scope.ID(), SubjectID: assetRecord.SubjectID,
		VerificationID: assetRecord.VerificationID, EvidenceID: assetRecord.ID,
		RequirementKey: assetRecord.RequirementKey,
		AuthorityID:    decision.AuthorityID, ResponseID: decision.ResponseID,
		CheckReference: input.CheckReference, Runner: input.Runner, Purpose: input.Purpose,
		Operation: OperationPlaintextRead, PermittedVariants: input.PermittedVariants,
		Region: assetRecord.Region, RecipientReference: input.RecipientReference,
		OutputDestination: input.OutputDestination, PolicyReference: decision.PolicyReference,
		MaximumUses: input.MaximumUses, CreatedAt: now, ExpiresAt: now.Add(input.TTL),
	})
	if err != nil {
		return Grant{}, err
	}
	if err := issuer.repository.CreateGrant(ctx, scope, grant, input.Attribution); err != nil {
		return Grant{}, fmt.Errorf("persist evidence grant: %w", err)
	}
	return grant, nil
}

// ReadInput identifies one separately authenticated grant redemption.
type ReadInput struct {
	GrantID      id.Grant
	RedemptionID id.Redemption
	Runner       Runner
}

// Reader redeems grants into transactional plaintext receivers.
type Reader struct {
	grants         GrantClaimer
	outcomes       GrantOutcomeRecorder
	assets         AssetFinder
	quarantine     IntegrityQuarantiner
	authorizer     ReadAuthorizer
	opener         ContentOpener
	objects        ObjectReader
	clock          clock.Clock
	outcomeTimeout time.Duration
}

// NewReader constructs the controlled evidence-read workflow.
func NewReader(
	grants GrantClaimer,
	outcomes GrantOutcomeRecorder,
	assets AssetFinder,
	quarantine IntegrityQuarantiner,
	authorizer ReadAuthorizer,
	opener ContentOpener,
	objects ObjectReader,
	source clock.Clock,
	outcomeTimeout time.Duration,
) (*Reader, error) {
	if grants == nil || outcomes == nil || assets == nil || quarantine == nil || authorizer == nil ||
		opener == nil || objects == nil || source == nil || outcomeTimeout <= 0 {
		return nil, errors.New("evidence: reader dependencies are invalid")
	}
	return &Reader{
		grants: grants, outcomes: outcomes, assets: assets, quarantine: quarantine,
		authorizer: authorizer, opener: opener, objects: objects, clock: source,
		outcomeTimeout: outcomeTimeout,
	}, nil
}

// Read claims one use, re-evaluates live authority, authenticates the complete
// ciphertext and plaintext digest, commits the bound receiver, then audits success.
func (reader *Reader) Read(
	ctx context.Context,
	scope tenant.Scope,
	input ReadInput,
	receiver PlaintextReceiver,
) error {
	if ctx == nil || scope.ID().IsZero() || input.GrantID.IsZero() ||
		input.RedemptionID.IsZero() || receiver == nil {
		return ErrReadDenied
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("read evidence: %w", err)
	}
	now := reader.clock.Now().UTC()
	redemption, err := reader.grants.ClaimGrant(
		ctx, scope, input.GrantID, input.RedemptionID, input.Runner, now,
	)
	if err != nil {
		return ErrReadDenied
	}
	grant := redemption.Grant()
	record := grant.Record()
	if redemption.ID() != input.RedemptionID || record.TenantID != scope.ID() ||
		!redemption.ValidFor(input.GrantID, input.Runner, now) ||
		!receiver.Binding().Binds(grant) {
		return reader.finish(ctx, scope, redemption, GrantOutcomeDenied, ErrReadDenied)
	}
	if outcome, terminal := redemption.TerminalOutcome(); terminal {
		if outcome == GrantOutcomeSucceeded {
			return nil
		}
		return ErrReadDenied
	}
	asset, err := reader.assets.Find(ctx, scope, record.EvidenceID)
	if err != nil || !asset.CanRead() || !grant.Binds(asset) {
		return reader.finish(ctx, scope, redemption, GrantOutcomeDenied, ErrReadDenied)
	}
	assetRecord := asset.Record()
	decision, err := reader.authorizer.AuthorizeEvidence(ctx, scope, ReadAuthorization{
		TenantID: scope.ID(), SubjectID: assetRecord.SubjectID,
		VerificationID: assetRecord.VerificationID, EvidenceID: assetRecord.ID,
		RequirementKey: assetRecord.RequirementKey,
		Purpose:        record.Purpose, EvidenceType: assetRecord.EvidenceType,
		RecipientReference: record.RecipientReference, Region: assetRecord.Region,
	})
	if err != nil || decision.AuthorityID != record.AuthorityID ||
		decision.ResponseID != record.ResponseID || decision.PolicyReference != record.PolicyReference {
		return reader.finish(ctx, scope, redemption, GrantOutcomeDenied, ErrReadDenied)
	}
	ciphertext, err := reader.objects.Open(ctx, asset.Content().Object())
	if err != nil {
		return reader.failed(ctx, scope, redemption, asset, err)
	}
	closed := false
	closeCiphertext := func() error {
		if closed {
			return nil
		}
		closed = true
		return ciphertext.Close()
	}
	defer func() { _ = closeCiphertext() }()

	receiverCommitted := false
	err = receiver.Receive(ctx, redemption.ID(), assetRecord.Content.MediaType, func(destination io.Writer) error {
		if destination == nil {
			return errors.New("evidence: plaintext receiver destination is invalid")
		}
		digest := sha256.New()
		authenticatedContext, contextErr := AuthenticatedContext(assetRecord)
		if contextErr != nil {
			return contextErr
		}
		if openErr := reader.opener.Open(
			ctx, io.MultiWriter(destination, digest), ciphertext,
			asset.Content().Envelope(), authenticatedContext,
		); openErr != nil {
			return openErr
		}
		if closeErr := closeCiphertext(); closeErr != nil {
			return closeErr
		}
		actualDigest := "sha256:" + hex.EncodeToString(digest.Sum(nil))
		if actualDigest != assetRecord.Content.PlaintextDigest {
			return ErrContentIntegrity
		}
		return nil
	}, func() error {
		receiverCommitted = true
		if outcomeErr := reader.recordOutcome(ctx, scope, redemption, GrantOutcomeSucceeded); outcomeErr != nil {
			return fmt.Errorf("record evidence grant success: %w", outcomeErr)
		}
		return nil
	})
	if err == nil {
		return nil
	}
	if receiverCommitted {
		// The durable pending redemption remains reconciliation evidence. Do not
		// misclassify an already released destination as a terminal failure.
		return fmt.Errorf("finalize committed evidence plaintext delivery: %w", err)
	}
	return reader.failed(ctx, scope, redemption, asset, err)
}

func (reader *Reader) failed(
	ctx context.Context,
	scope tenant.Scope,
	redemption Redemption,
	asset Asset,
	cause error,
) error {
	if errors.Is(cause, platformcrypto.ErrCiphertextIntegrity) || errors.Is(cause, ErrContentIntegrity) {
		reason := plaintextDigestFailureReason
		if errors.Is(cause, platformcrypto.ErrCiphertextIntegrity) {
			reason = ciphertextFailureReason
		}
		failed, err := asset.FailIntegrity(asset.Version(), reason, reader.clock.Now().UTC())
		if err == nil {
			quarantineContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx), reader.outcomeTimeout,
			)
			err = reader.quarantine.QuarantineIntegrity(
				quarantineContext, scope, failed, asset.Version(),
			)
			cancel()
		}
		if err != nil {
			cause = errors.Join(ErrContentIntegrity, ErrQuarantineIncomplete, err)
			return reader.finish(ctx, scope, redemption, GrantOutcomeFailed, cause)
		}
		return reader.finish(ctx, scope, redemption, GrantOutcomeIntegrityFailed, ErrContentIntegrity)
	}
	return reader.finish(ctx, scope, redemption, GrantOutcomeFailed, cause)
}

func (reader *Reader) finish(
	ctx context.Context,
	scope tenant.Scope,
	redemption Redemption,
	outcome GrantOutcome,
	cause error,
) error {
	if err := reader.recordOutcome(ctx, scope, redemption, outcome); err != nil {
		return errors.Join(cause, fmt.Errorf("record evidence grant outcome: %w", err))
	}
	return cause
}

func (reader *Reader) recordOutcome(
	ctx context.Context,
	scope tenant.Scope,
	redemption Redemption,
	outcome GrantOutcome,
) error {
	outcomeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), reader.outcomeTimeout)
	defer cancel()
	return reader.outcomes.RecordGrantOutcome(
		outcomeContext, scope, redemption, outcome, reader.clock.Now().UTC(),
	)
}
