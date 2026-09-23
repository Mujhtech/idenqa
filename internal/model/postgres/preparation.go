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
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
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
	Wrapper  platformcrypto.KeyWrapper
}

// Prepare returns a request-bound provenance and a transaction-local append closure.
func (preparation *Preparation) Prepare(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verificationID id.Verification, checkID id.Check, attemptID id.Attempt, definition verification.PlannedCheck, now, deadline time.Time) (verification.Provenance, func(context.Context, verification.Check) error, error) {
	fail := func(err error) (verification.Provenance, func(context.Context, verification.Check) error, error) {
		return verification.Provenance{}, nil, err
	}
	plan := preparation.Plan
	if plan == nil || preparation.Requests == nil || preparation.IDs == nil || preparation.Clock == nil || scope.ID().String() != plan.Binding.TenantID || definition.Name != plan.CheckName() {
		return fail(model.ErrRequestUnavailable)
	}
	if err := ValidateSelection(ctx, tx, scope, plan); err != nil {
		return fail(err)
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
	targets := []target{{plan.Binding.Requirement, "idenqa.evidence.selfie_image", "idenqa.artefact.selfie_image", "selfie"}}
	if plan.Binding.DocumentRequirement != "" {
		targets = append(targets, target{plan.Binding.DocumentRequirement, "idenqa.evidence.document_image", "idenqa.artefact.document_front", "document.front"})
	}
	var references []modelv1.EvidenceGrantReference
	var sequence *modelv1.EvidenceSequence
	for _, target := range targets {
		selected, err := selectEvidence(ctx, tx, scope, verificationID, plan, target)
		if err != nil || len(selected) == 0 {
			return fail(model.ErrRequestUnavailable)
		}
		for _, candidate := range selected {
			evidenceID, err := id.ParseEvidence(candidate.evidenceID)
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
			if candidate.sequenceDigest != "" {
				if sequence == nil {
					sequence = &modelv1.EvidenceSequence{SequenceDigest: candidate.sequenceDigest}
				} else if sequence.SequenceDigest != candidate.sequenceDigest {
					return fail(model.ErrRequestUnavailable)
				}
				sequence.Frames = append(sequence.Frames, modelv1.EvidenceSequenceFrame{
					GrantID: grant.ID().String(), ChallengeID: candidate.challengeID, Index: candidate.index,
					CapturedAt: candidate.capturedAt, PreviousDigest: candidate.previousDigest, ContentDigest: candidate.contentDigest,
				})
			}
		}
	}
	var sequences []modelv1.EvidenceSequence
	if sequence != nil {
		sequences = []modelv1.EvidenceSequence{*sequence}
	}
	request := modelv1.Request{
		Contract:       plan.Manifest.Provenance.Contract,
		AttemptID:      attemptID.String(),
		ModelID:        plan.Binding.Configuration.ModelID,
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Evaluation:     plan.Capability.Evaluation,
		IdempotencyKey: attemptID.String(),
		Provenance:     plan.Manifest.Provenance,
		Capability:     plan.Capability,
		Restrictions:   plan.Manifest.Restrictions,
		Configuration:  plan.Binding.Configuration,
		Deadline:       deadline,
		Evidence:       references,
		Sequences:      sequences,
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

type evidenceTarget struct{ requirement, evidenceType, artefact, variant string }

type selectedEvidence struct {
	evidenceID, sequenceDigest, challengeID, previousDigest, contentDigest string
	index                                                                  uint16
	capturedAt                                                             time.Time
}

func selectEvidence(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verificationID id.Verification, plan *model.Plan, target struct{ requirement, evidenceType, artefact, variant string }) ([]selectedEvidence, error) {
	base := []any{scope.ID().String(), verificationID.String(), target.requirement, plan.Binding.Region, target.evidenceType, target.artefact}
	if !plan.Capability.TemporalEvidence || target.variant != "selfie" {
		rows, err := tx.Query(ctx, `SELECT assets.id FROM idenqa.evidence_assets assets JOIN idenqa.evidence_upload_intents uploads ON uploads.tenant_id=assets.tenant_id AND uploads.evidence_id=assets.id WHERE assets.tenant_id=$1 AND assets.verification_id=$2 AND assets.requirement_key=$3 AND assets.region=$4 AND assets.evidence_type=$5 AND uploads.state='accepted' AND uploads.artefact=$6 AND assets.state='available' AND assets.integrity='verified' ORDER BY assets.id LIMIT 2`, base...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var result []selectedEvidence
		for rows.Next() {
			var candidate selectedEvidence
			if err := rows.Scan(&candidate.evidenceID); err != nil {
				return nil, err
			}
			result = append(result, candidate)
		}
		if err := rows.Err(); err != nil || len(result) != 1 {
			return nil, model.ErrRequestUnavailable
		}
		return result, nil
	}
	rows, err := tx.Query(ctx, `SELECT assets.id,frames.sequence_digest,frames.frame_index,frames.challenge_id,frames.captured_at,COALESCE(frames.previous_digest,''),frames.content_digest FROM idenqa.evidence_assets assets JOIN idenqa.evidence_upload_intents uploads ON uploads.tenant_id=assets.tenant_id AND uploads.evidence_id=assets.id JOIN idenqa.evidence_temporal_frames frames ON frames.tenant_id=assets.tenant_id AND frames.evidence_id=assets.id WHERE assets.tenant_id=$1 AND assets.verification_id=$2 AND assets.requirement_key=$3 AND assets.region=$4 AND assets.evidence_type=$5 AND uploads.state='accepted' AND uploads.artefact=$6 AND assets.state='available' AND assets.integrity='verified' ORDER BY frames.frame_index LIMIT 33`, base...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []selectedEvidence
	for rows.Next() {
		var candidate selectedEvidence
		var index int32
		if err := rows.Scan(&candidate.evidenceID, &candidate.sequenceDigest, &index, &candidate.challengeID, &candidate.capturedAt, &candidate.previousDigest, &candidate.contentDigest); err != nil || index < 0 || index > 31 {
			return nil, model.ErrRequestUnavailable
		}
		candidate.index = uint16(index)
		result = append(result, candidate)
	}
	if err := rows.Err(); err != nil || len(result) < 2 || len(result) > int(plan.Manifest.Restrictions.MaximumGrants) {
		return nil, model.ErrRequestUnavailable
	}
	return result, nil
}

type preparationClock struct{ now time.Time }

func (source preparationClock) Now() time.Time { return source.now }
