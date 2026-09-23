package postgres

import (
	"context"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

type boundTransaction struct{ tx pg.Transaction }

func (bound boundTransaction) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return work(ctx, bound.tx)
}

// Preparation creates request and grant meaning inside the planning transaction.
type Preparation struct {
	Plan     *provider.Plan
	Requests *RequestStore
	IDs      *id.Generator
	Catalog  evidence.Catalog
	Clock    clock.Clock
	Wrapper  platformcrypto.KeyWrapper
}

// Prepare returns a request-bound provenance and a transaction-local append closure.
func (preparation *Preparation) Prepare(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verificationID id.Verification, checkID id.Check, attemptID id.Attempt, definition verification.PlannedCheck, now, deadline time.Time) (verification.Provenance, func(context.Context, verification.Check) error, error) {
	fail := func(err error) (verification.Provenance, func(context.Context, verification.Check) error, error) {
		return verification.Provenance{}, nil, err
	}
	plan := preparation.Plan
	if plan == nil || preparation.Requests == nil || preparation.IDs == nil || preparation.Clock == nil || scope.ID().String() != plan.Binding.TenantID || definition.Name != plan.Capability.Check {
		return fail(provider.ErrRequestUnavailable)
	}
	bound := boundTransaction{tx}
	assets, err := evidencepostgres.New(bound, preparation.Wrapper, preparation.Catalog)
	if err != nil {
		return fail(err)
	}
	authorities, err := authoritypostgres.New(bound, preparation.Wrapper)
	if err != nil {
		return fail(err)
	}
	sessions, err := verificationpostgres.NewSessionStore(bound, preparation.Wrapper, preparation.Catalog)
	if err != nil {
		return fail(err)
	}
	authorizer, err := authority.NewService(authorities, authorities, authorities, sessions, preparation.IDs, preparation.Clock, 24*time.Hour)
	if err != nil {
		return fail(err)
	}
	issuer, err := evidence.NewGrantIssuer(assets, authorizer, assets, preparation.IDs, preparation.Catalog, preparationClock{now}, 10*time.Minute)
	if err != nil {
		return fail(err)
	}
	type target struct{ requirement, evidenceType, artefact, variant string }
	targets := []target{{plan.Binding.Requirement, "idenqa.evidence.document_image", "idenqa.artefact.document_front", "document.front"}}
	if plan.Manifest.Package.AdapterID == providerv1.AdapterDojah {
		session, err := sessions.FindSession(ctx, scope, verificationID)
		if err != nil {
			return fail(err)
		}
		if session.ProfileDigest() != plan.Binding.ProfileDigest {
			return fail(provider.ErrRequestUnavailable)
		}
		artefacts, err := documentArtefacts(session.Requirements(), session.DocumentSelections(), plan.Binding)
		if err != nil {
			return fail(err)
		}
		for _, artefact := range artefacts {
			if artefact == evidence.ArtefactDocumentBack {
				targets = append(targets, target{plan.Binding.Requirement, "idenqa.evidence.document_image", string(artefact), "document.back"})
			}
		}
	}
	if plan.Manifest.Package.AdapterID == providerv1.AdapterSmileID {
		targets = append(targets, target{plan.Binding.SelfieRequirement, "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image", "selfie"})
	}
	var references []providerv1.EvidenceGrantReference
	for _, target := range targets {
		rows, err := tx.Query(ctx, `SELECT assets.id FROM idenqa.evidence_assets assets JOIN idenqa.evidence_upload_intents uploads ON uploads.tenant_id=assets.tenant_id AND uploads.evidence_id=assets.id WHERE assets.tenant_id=$1 AND assets.verification_id=$2 AND assets.requirement_key=$3 AND assets.region=$4 AND assets.evidence_type=$5 AND uploads.state='accepted' AND uploads.artefact=$6 AND assets.state='available' AND assets.integrity='verified' ORDER BY assets.id LIMIT 2`, scope.ID().String(), verificationID.String(), target.requirement, plan.Binding.Region, target.evidenceType, target.artefact)
		if err != nil {
			return fail(err)
		}
		var selected []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				rows.Close()
				return fail(err)
			}
			selected = append(selected, value)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return fail(err)
		}
		if len(selected) != 1 {
			return fail(provider.ErrRequestUnavailable)
		}
		evidenceID, err := id.ParseEvidence(selected[0])
		if err != nil {
			return fail(err)
		}
		grant, err := issuer.Issue(ctx, scope, evidence.GrantInput{EvidenceID: evidenceID, CheckReference: definition.Name, Runner: evidence.Runner{Identity: plan.Manifest.Package.AdapterID, WorkloadVersion: plan.Manifest.Package.AdapterVersion}, Purpose: evidence.Name(plan.Binding.Purpose), PermittedVariants: []string{evidence.VariantOriginal}, RecipientReference: plan.Binding.Recipient, OutputDestination: "provider." + plan.Manifest.Package.AdapterID, MaximumUses: 1, TTL: deadline.Sub(now), Attribution: evidence.CommandAttribution{Principal: evidence.Actor{Type: "internal.service", ID: "worker.provider"}, TenantActor: evidence.Actor{Type: "tenant.service", ID: scope.ID().String()}, Reason: "provider.document_analysis"}})
		if err != nil {
			return fail(err)
		}
		redemptionID, err := preparation.IDs.NewRedemption()
		if err != nil {
			return fail(err)
		}

		references = append(references, providerv1.EvidenceGrantReference{GrantID: grant.ID().String(), RedemptionID: redemptionID.String(), EvidenceID: evidenceID.String(), Purpose: plan.Binding.Purpose, Variant: target.variant, ExpiresAt: grant.Record().ExpiresAt})
	}
	callbackReference, err := preparation.IDs.NewProviderCallback()
	if err != nil {
		return fail(err)
	}
	request := providerv1.Request{Contract: plan.Manifest.Package.Contract, AttemptID: attemptID.String(), ProviderID: plan.Binding.Configuration.ProviderID, TenantID: scope.ID().String(), VerificationID: verificationID.String(), Check: definition.Name, IdempotencyKey: attemptID.String(), CallbackReference: callbackReference.String(), Adapter: plan.Manifest.Package, Capability: plan.Capability, Restrictions: plan.Manifest.Restrictions, Configuration: plan.Binding.Configuration, Deadline: deadline, Inputs: plan.Binding.Inputs, Evidence: references}
	digest, err := provider.RequestDigest(request)
	if err != nil {
		return fail(err)
	}
	provenance := definition.Provenance
	provenance.RequestDigest = digest
	save := func(ctx context.Context, check verification.Check) error {
		if check.ID != checkID {
			return provider.ErrRequestUnavailable
		}
		return preparation.Requests.SaveWithin(ctx, tx, check, request)
	}
	return provenance, save, nil
}

type preparationClock struct{ now time.Time }

func (source preparationClock) Now() time.Time { return source.now }
