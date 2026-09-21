package api

import (
	"context"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/identity"
	identitypostgres "github.com/Mujhtech/idenqa/internal/identity/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
)

// privacySubjectBundle adapts the tenant-export subject stream to the privacy
// request effect port. The bundle never reaches ordinary logs or responses.
type privacySubjectBundle struct {
	exporter *tenantexport.SubjectExporter
}

// ExportSubjectBundle streams the subject bundle to a discard sink and
// returns only its canonical digest and byte count.
func (bundle privacySubjectBundle) ExportSubjectBundle(ctx context.Context, scope tenant.Scope, subjectID string, structured bool) (privacy.SubjectBundle, error) {
	if bundle.exporter == nil {
		return privacy.SubjectBundle{}, privacy.ErrUnavailable
	}
	selection := tenantexport.AccessSubjectCollections()
	if structured {
		selection = tenantexport.PortabilitySubjectCollections()
	}
	result, err := bundle.exporter.Export(ctx, scope, subjectID, selection, func([]byte) error { return nil })
	if err != nil {
		return privacy.SubjectBundle{}, err
	}
	return privacy.SubjectBundle{Digest: result.Digest, Bytes: result.Bytes}, nil
}

// privacySubjectDeletion adapts the existing identity deletion mechanism,
// which plans identity and evidence targets and creates the deletion request.
type privacySubjectDeletion struct {
	store *identitypostgres.Store
	now   func() time.Time
}

// RequestSubjectDeletion routes erasure through identity deletion semantics.
func (deletion privacySubjectDeletion) RequestSubjectDeletion(ctx context.Context, scope tenant.Scope, actor privacy.Actor, subjectID, region string) (string, error) {
	if deletion.store == nil {
		return "", privacy.ErrUnavailable
	}
	key, err := id.ParseAPIKey(actor.ID)
	if err != nil {
		return "", privacy.ErrInvalid
	}
	result, err := deletion.store.Execute(ctx, scope, identity.Command{
		Operation: "delete", SubjectID: subjectID, Actor: key, At: deletion.now().UTC(), Region: region,
	})
	if err != nil {
		return "", err
	}
	return result.DeletionID, nil
}

// privacyCorrection routes approved corrections to the existing identity
// successor or review correction intake mechanisms.
type privacyCorrection struct {
	identity  *identitypostgres.Store
	followup  *reviewpostgres.FollowupStore
	now       func() time.Time
	retention time.Duration
}

// ExecuteCorrection calls the owning mechanism and returns its reference.
func (correction privacyCorrection) ExecuteCorrection(ctx context.Context, scope tenant.Scope, actor privacy.Actor, request privacy.Request, instruction privacy.CorrectionInstruction) (string, error) {
	if correction.identity == nil || correction.followup == nil {
		return "", privacy.ErrUnavailable
	}
	key, err := id.ParseAPIKey(actor.ID)
	if err != nil {
		return "", privacy.ErrInvalid
	}
	if instruction.DecisionID != "" {
		decisionID, err := id.ParseDecision(instruction.DecisionID)
		if err != nil {
			return "", privacy.ErrInvalid
		}
		retry, err := idempotency.NewRequest(scope.ID(), key, "privacy.correction.intake", request.ID.String(),
			[]byte(instruction.DecisionID), correction.now().UTC(), correction.retention)
		if err != nil {
			return "", privacy.ErrInvalid
		}
		result, err := correction.followup.OpenCorrection(ctx, scope, decisionID, review.Actor{ID: actor.ID}, retry)
		if err != nil {
			return "", err
		}
		if result.CaseID != "" {
			return result.CaseID, nil
		}
		return instruction.DecisionID, nil
	}
	if request.VerificationID == "" {
		return "", privacy.ErrUnavailable
	}
	subject, err := correction.identity.Read(ctx, scope, identity.Query{Kind: "subject", SubjectID: instruction.SubjectID, Limit: 1})
	if err != nil || subject.Subject == nil {
		return "", errors.Join(privacy.ErrInvalid, err)
	}
	now := correction.now().UTC().Truncate(time.Microsecond)
	result, err := correction.identity.Execute(ctx, scope, identity.Command{
		Operation: "record", SubjectID: instruction.SubjectID, ExpectedVersion: subject.Subject.Version,
		Actor: key, At: now, Region: subject.Subject.Region, Key: request.ID.String(),
		Record: &identity.RecordInput{
			Kind: instruction.Kind, Name: instruction.Name, VerificationID: request.VerificationID,
			Value: &identity.Value{Type: "string", Text: instruction.Value}, Supersedes: instruction.RecordID,
			Normalization: instruction.Normalization, SourceName: "subject.privacy_request", InputVersion: "privacy.request.v1",
			CollectedAt: now, ObservedAt: now, ValidFrom: now, ValidUntil: now.Add(365 * 24 * time.Hour),
			RetainUntil: now.Add(30 * 24 * time.Hour),
		},
	})
	if err != nil {
		return "", err
	}
	if result.Record == nil {
		return "", privacy.ErrInvalid
	}
	return result.Record.ID, nil
}
