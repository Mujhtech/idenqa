package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

// AdmitResourceWithin reserves a dependent resource in its owning transaction.
// Accepted verification work uses its immutable admission revision. A later
// suspension cannot strip that accepted work of its original dependent rules.
func (s *Store) AdmitResourceWithin(ctx context.Context, tx pg.Transaction, resource admission.Resource) error {
	if _, err := id.ParseVerification(resource.VerificationID); err != nil {
		return admission.ErrInvalid
	}
	if err := runtime(ctx, tx, resource.TenantID); err != nil {
		return err
	}
	meaning, err := digest(resource)
	if err != nil {
		return err
	}
	operationID := resource.Kind + ":" + resource.ID
	var replayDigest string
	err = tx.QueryRow(ctx, `SELECT digest FROM idenqa.admission_operations WHERE tenant_id=$1 AND id=$2`, resource.TenantID, operationID).Scan(&replayDigest)
	if err == nil {
		if replayDigest != meaning {
			return admission.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	ruleName, expectedBytes, err := resourceRule(ctx, tx, resource)
	if err != nil {
		return err
	}
	if err := lock(ctx, tx, resource.TenantID); err != nil {
		return err
	}
	operation := admission.Operation{TenantID: resource.TenantID, ID: resource.Kind + ":" + resource.ID, Rule: ruleName}
	var stored string
	err = tx.QueryRow(ctx, `SELECT digest FROM idenqa.admission_operations WHERE tenant_id=$1 AND id=$2`, resource.TenantID, operation.ID).Scan(&stored)
	if err == nil {
		if stored != meaning {
			return admission.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var raw []byte
	var acceptedAt time.Time
	var state string
	var workflowRule string
	var now time.Time
	err = tx.QueryRow(ctx, `SELECT s.body,o.accepted_at,v.state,clock_timestamp(),o.rule FROM idenqa.admission_operations o JOIN idenqa.admission_snapshots s ON s.tenant_id=o.tenant_id AND s.revision=o.snapshot_revision JOIN idenqa.verification_sessions v ON v.tenant_id=o.tenant_id AND v.id=o.id WHERE o.tenant_id=$1 AND o.id=$2 AND o.state='active'`, resource.TenantID, resource.VerificationID).Scan(&raw, &acceptedAt, &state, &now, &workflowRule)
	if errors.Is(err, pgx.ErrNoRows) {
		return admission.ErrUnavailable
	}
	if err != nil {
		return err
	}
	if !slices.Contains([]string{"created", "collecting", "processing"}, state) {
		return admission.ErrConflict
	}
	ruleName, err = admissionv1.DependentOperation(workflowRule, ruleName)
	if err != nil {
		return admission.ErrInvalid
	}
	operation.Rule = ruleName
	var snapshot admissionv1.Snapshot
	if json.Unmarshal(raw, &snapshot) != nil || snapshot.Scope.TenantID != resource.TenantID || s.policy.Verify(snapshot, snapshot.IssuedAt) != nil {
		return admission.ErrInvalid
	}
	if acceptedAt.Before(snapshot.ValidFrom) || !acceptedAt.Before(snapshot.ValidUntil) || (snapshot.Status != "active" && snapshot.Status != "grace") {
		return admission.ErrInvalid
	}
	var selected *admissionv1.Rule
	for i := range snapshot.Rules {
		if snapshot.Rules[i].Operation == ruleName {
			selected = &snapshot.Rules[i]
			break
		}
	}
	if selected == nil {
		return admission.ErrEntitlement
	}
	for capability, version := range selected.Capabilities {
		if snapshot.Capabilities[capability] != version {
			return admission.ErrEntitlement
		}
	}
	// Evidence reserves the complete physical ciphertext bound before upload.
	if resource.Kind == "evidence" {
		expectedBytes, err = platformcrypto.MaximumCiphertextBytes(1, "AES256_GCM_HKDF_1MB", expectedBytes)
		if err != nil {
			return admission.ErrInvalid
		}
	}
	// Evidence reserves the signed per-object upper bound before an HTTP upload.
	// The complete v1 ciphertext must fit; this is not billable byte usage.
	for _, budget := range selected.Budgets {
		if resource.Kind == "evidence" && budget.Kind == "storage" && expectedBytes > budget.Quantity {
			return admission.ErrCeiling
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.admission_operations(tenant_id,id,rule,digest,snapshot_revision,state,accepted_at) VALUES($1,$2,$3,$4,$5,'active',$6)`, resource.TenantID, operation.ID, ruleName, meaning, snapshot.Revision, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.admission_resource_sources(tenant_id,operation_id,verification_id,kind,source_id) VALUES($1,$2,$3,$4,$5)`, resource.TenantID, operation.ID, resource.VerificationID, resource.Kind, resource.ID); err != nil {
		return err
	}
	if err := reserveBudgets(ctx, tx, operation, snapshot, *selected, now); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.admission_events(tenant_id,kind,reference,revision) VALUES($1,'accepted',$2,$3)`, resource.TenantID, operation.ID, snapshot.Revision)
	return err
}

// resourceRule reads immutable authoritative source fields, never caller rules.
func resourceRule(ctx context.Context, tx pg.Transaction, r admission.Resource) (string, int64, error) {
	var selectors []string
	var size int64
	switch r.Kind {
	case "provider":
		var providerID, version, packageDigest, check string
		err := tx.QueryRow(ctx, `SELECT request_body->>'provider_id',request_body->'adapter'->>'adapter_version',request_body->'adapter'->>'package_digest',request_body->>'check' FROM idenqa.provider_requests WHERE tenant_id=$1 AND attempt_id=$2 AND verification_id=$3 AND EXISTS(SELECT 1 FROM idenqa.verification_attempts a WHERE a.tenant_id=$1 AND a.id=$2 AND a.verification_id=$3 AND a.runner_kind='provider' AND a.state='running')`, r.TenantID, r.ID, r.VerificationID).Scan(&providerID, &version, &packageDigest, &check)
		if err != nil {
			return "", 0, err
		}
		selectors = []string{providerID, version, packageDigest, check}
	case "model":
		var modelID, version, modelDigest, evaluation string
		err := tx.QueryRow(ctx, `SELECT request_body->>'model_registration_id',request_body->'provenance'->>'model_version',request_body->'provenance'->>'model_digest',request_body->>'evaluation' FROM idenqa.model_requests WHERE tenant_id=$1 AND attempt_id=$2 AND verification_id=$3 AND EXISTS(SELECT 1 FROM idenqa.verification_attempts a WHERE a.tenant_id=$1 AND a.id=$2 AND a.verification_id=$3 AND a.runner_kind='model' AND a.state='running')`, r.TenantID, r.ID, r.VerificationID).Scan(&modelID, &version, &modelDigest, &evaluation)
		if err != nil {
			return "", 0, err
		}
		selectors = []string{modelID, version, modelDigest, evaluation}
	case "evidence":
		var evidenceType, artefact, method, mediaType, retention string
		err := tx.QueryRow(ctx, `SELECT evidence_type,artefact,acquisition_method,media_type,retention_class,expected_bytes FROM idenqa.evidence_upload_intents WHERE tenant_id=$1 AND id=$2 AND verification_id=$3 AND state='issued' FOR UPDATE`, r.TenantID, r.ID, r.VerificationID).Scan(&evidenceType, &artefact, &method, &mediaType, &retention, &size)
		if err != nil {
			return "", 0, err
		}
		selectors = []string{evidenceType, artefact, method, mediaType, retention}
	case "review":
		var certificate, oversight string
		err := tx.QueryRow(ctx, `SELECT required_certification,oversight FROM idenqa.review_cases WHERE tenant_id=$1 AND id=$2 AND verification_id=$3 AND state='open' FOR UPDATE`, r.TenantID, r.ID, r.VerificationID).Scan(&certificate, &oversight)
		if err != nil {
			return "", 0, err
		}
		selectors = []string{certificate, oversight}
	default:
		return "", 0, admission.ErrInvalid
	}
	name, err := admissionv1.ResourceOperation(r.Kind, selectors...)
	return name, size, err
}
