package postgres

import (
	"context"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
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
	Plan     *model.Plan
	Requests *RequestStore
	IDs      *id.Generator
	Catalog  evidence.Catalog
	Clock    clock.Clock
}

// Prepare returns a request-bound provenance and a transaction-local append closure.
func (preparation *Preparation) Prepare(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verificationID id.Verification, checkID id.Check, attemptID id.Attempt, definition verification.PlannedCheck, now, deadline time.Time) (verification.Provenance, func(context.Context, verification.Check) error, error) {
	fail := func(err error) (verification.Provenance, func(context.Context, verification.Check) error, error) {
		return verification.Provenance{}, nil, err
	}
	plan := preparation.Plan
	if plan == nil || preparation.Requests == nil || preparation.IDs == nil || preparation.Clock == nil || scope.ID().String() != plan.Binding.TenantID || definition.Name != plan.Capability.Evaluation {
		return fail(model.ErrRequestUnavailable)
	}
	if err := ValidateSelection(ctx, tx, scope, plan); err != nil {
		return fail(err)
	}
	bound := boundTransaction{tx}
	assets, err := evidencepostgres.New(bound, preparation.Catalog)
	if err != nil {
		return fail(err)
	}
	authorities, err := authoritypostgres.New(bound)
	if err != nil {
		return fail(err)
	}
	sessions, err := verificationpostgres.NewSessionStore(bound, preparation.Catalog)
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
	targets := []target{{plan.Binding.Requirement, "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image", "selfie"}}
	if plan.Capability.Evaluation == "idenqa.check.face_match_1to1" {
		targets = append(targets, target{plan.Binding.DocumentRequirement, "idenqa.evidence.document_image", "idenqa.artefact.document_front", "document.front"})
	}
	var references []modelv1.EvidenceGrantReference
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
			return fail(model.ErrRequestUnavailable)
		}
		evidenceID, err := id.ParseEvidence(selected[0])
		if err != nil {
			return fail(err)
		}
		grant, err := issuer.Issue(ctx, scope, evidence.GrantInput{
			EvidenceID:     evidenceID,
			CheckReference: definition.Name,
			Runner: evidence.Runner{
				Identity:        plan.Manifest.Provenance.ModelID,
				WorkloadVersion: plan.Manifest.Provenance.ModelVersion,
			},
			Purpose:            evidence.Name(plan.Binding.Purpose),
			PermittedVariants:  []string{evidence.VariantOriginal},
			RecipientReference: plan.Binding.Recipient,
			OutputDestination:  plan.OutputDestination(),
			MaximumUses:        1,
			TTL:                deadline.Sub(now),
			Attribution: evidence.CommandAttribution{
				Principal:   evidence.Actor{Type: "internal.service", ID: "worker.model"},
				TenantActor: evidence.Actor{Type: "tenant.service", ID: scope.ID().String()},
				Reason:      plan.OutputDestination(),
			},
		})
		if err != nil {
			return fail(err)
		}
		redemptionID, err := preparation.IDs.NewRedemption()
		if err != nil {
			return fail(err)
		}

		references = append(references, modelv1.EvidenceGrantReference{
			GrantID:      grant.ID().String(),
			RedemptionID: redemptionID.String(),
			EvidenceID:   evidenceID.String(),
			Purpose:      plan.Binding.Purpose,
			Variant:      target.variant,
			ExpiresAt:    grant.Record().ExpiresAt,
		})
	}
	request := modelv1.Request{
		Contract:       plan.Manifest.Provenance.Contract,
		AttemptID:      attemptID.String(),
		ModelID:        plan.Binding.Configuration.ModelID,
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Evaluation:     definition.Name,
		IdempotencyKey: attemptID.String(),
		Provenance:     plan.Manifest.Provenance,
		Capability:     plan.Capability,
		Restrictions:   plan.Manifest.Restrictions,
		Configuration:  plan.Binding.Configuration,
		Deadline:       deadline,
		Evidence:       references,
	}
	digest, err := model.RequestDigest(request)
	if err != nil {
		return fail(err)
	}
	provenance := definition.Provenance
	provenance.RequestDigest = digest
	save := func(ctx context.Context, check verification.Check) error {
		if check.ID != checkID {
			return model.ErrRequestUnavailable
		}
		return preparation.Requests.saveWithin(ctx, tx, check, request, plan)
	}
	return provenance, save, nil
}

type preparationClock struct{ now time.Time }

func (source preparationClock) Now() time.Time { return source.now }
