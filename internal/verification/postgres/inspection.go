package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// Inspect returns a bounded metadata-only operational projection.
func (store *SessionStore) Inspect(ctx context.Context, scope tenant.Scope, identifier id.Verification) (verification.Inspection, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return verification.Inspection{}, verification.ErrSessionNotFound
	}
	result := verification.Inspection{
		Evidence: []verification.EvidenceInspection{}, Checks: []verification.CheckInspection{},
		Attempts: []verification.AttemptInspection{}, Decisions: []verification.DecisionInspection{},
		Retention: []verification.RetentionInspection{}, Webhooks: []verification.WebhookInspection{},
	}
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		tenantID, verificationID := scope.ID().String(), identifier.String()
		if _, err := queries.FindVerificationInspectionSession(ctx, sqlgen.FindVerificationInspectionSessionParams{TenantID: tenantID, ID: verificationID}); errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		} else if err != nil {
			return fmt.Errorf("find verification inspection session: %w", err)
		}
		evidenceRows, err := queries.ListVerificationInspectionEvidence(ctx, sqlgen.ListVerificationInspectionEvidenceParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification inspection evidence: %w", err)
		}
		for _, row := range evidenceRows {
			result.Evidence = append(result.Evidence, verification.EvidenceInspection{ID: row.ID, RequirementKey: row.RequirementKey, EvidenceType: row.EvidenceType, Artefact: row.Artefact, AcquisitionMethod: row.AcquisitionMethod, Assurances: row.Assurances, State: row.State, Integrity: row.Integrity, RetentionClass: row.RetentionClass, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time})
		}
		checkRows, err := queries.ListVerificationInspectionChecks(ctx, sqlgen.ListVerificationInspectionChecksParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification inspection checks: %w", err)
		}
		for _, row := range checkRows {
			result.Checks = append(result.Checks, verification.CheckInspection{ID: row.ID, Name: row.Name, State: row.State, Outcome: row.Outcome, AttemptCount: int(row.AttemptCount), CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time})
		}
		attemptRows, err := queries.ListVerificationInspectionAttempts(ctx, sqlgen.ListVerificationInspectionAttemptsParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification inspection attempts: %w", err)
		}
		for _, row := range attemptRows {
			result.Attempts = append(result.Attempts, verification.AttemptInspection{
				ID: row.ID, CheckID: row.CheckID, Number: int(row.AttemptNumber), RunnerKind: row.RunnerKind,
				RunnerID: row.RunnerID, RunnerVersion: row.RunnerVersion, PackageDigest: row.PackageDigest,
				ContractMajor: int(row.ContractMajor), ContractMinor: int(row.ContractMinor), RequestDigest: row.RequestDigest,
				ConfigurationDigest: row.ConfigurationDigest, State: row.State, StartedAt: row.StartedAt.Time,
				Deadline: row.Deadline.Time, FinishedAt: timePointer(row.FinishedAt), FailureClass: row.FailureClass,
				FailureCode: row.FailureCode, RetryDisposition: row.RetryDisposition,
				RetryAfterMilliseconds: row.RetryAfterMilliseconds, ResultDigest: row.ResultDigest,
			})
		}
		decisionRows, err := queries.ListVerificationInspectionDecisions(ctx, sqlgen.ListVerificationInspectionDecisionsParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification inspection decisions: %w", err)
		}
		for _, row := range decisionRows {
			result.Decisions = append(result.Decisions, verification.DecisionInspection{ID: row.ID, DecisionDigest: row.DecisionDigest, Selected: row.Selected, Outcome: row.Outcome, Actor: row.Actor, SupersedesID: row.SupersedesID, DecidedAt: row.DecidedAt.Time})
		}
		retentionRows, err := queries.ListVerificationInspectionRetention(ctx, sqlgen.ListVerificationInspectionRetentionParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification inspection retention: %w", err)
		}
		for _, row := range retentionRows {
			result.Retention = append(result.Retention, verification.RetentionInspection{DataClass: row.DataClass, Region: row.Region, PolicyDigest: row.PolicyDigest, ExpiresAt: row.ExpiresAt.Time})
		}
		result.LegalHold, err = queries.VerificationInspectionHasLegalHold(ctx, sqlgen.VerificationInspectionHasLegalHoldParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("find verification inspection legal hold: %w", err)
		}
		webhookRows, err := queries.ListVerificationInspectionWebhooks(ctx, sqlgen.ListVerificationInspectionWebhooksParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification inspection webhooks: %w", err)
		}
		for _, row := range webhookRows {
			result.Webhooks = append(result.Webhooks, verification.WebhookInspection{EventID: row.EventID, EventType: row.EventType, EventState: row.EventState, DeliveryID: row.DeliveryID, DeliveryState: row.DeliveryState, CreatedAt: row.CreatedAt.Time})
		}
		return nil
	})
	return result, err
}
