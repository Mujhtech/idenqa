package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/jackc/pgx/v5"
)

type boundTransaction struct{ tx pg.Transaction }

func (b boundTransaction) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return work(ctx, b.tx)
}

// EvidenceStore composes current case authority, scoped grants and audited byte reads.
type EvidenceStore struct {
	pool      transactionRunner
	authority review.Authority
	catalog   evidence.Catalog
	ids       *id.Generator
	clock     clock.Clock
	wrapper   platformcrypto.KeyWrapper
	opener    evidence.ContentOpener
	objects   evidence.ObjectReader
}

// NewEvidenceStore constructs the transactional evidence access adapter.
func NewEvidenceStore(pool transactionRunner, wrapper platformcrypto.KeyWrapper, authority review.Authority, catalog evidence.Catalog, ids *id.Generator, source clock.Clock, opener evidence.ContentOpener, objects evidence.ObjectReader) (*EvidenceStore, error) {
	if pool == nil || authority == nil || catalog.IsZero() || ids == nil || source == nil || opener == nil || objects == nil {
		return nil, review.ErrInvalid
	}
	return &EvidenceStore{pool: pool, authority: authority, catalog: catalog, ids: ids, clock: source, wrapper: wrapper, opener: opener, objects: objects}, nil
}
func (s *EvidenceStore) eligible(ctx context.Context, scope tenant.Scope, tx pg.Transaction, actor review.Actor, caseID id.ReviewCase, version int64) (review.Case, review.Principal, error) {
	cases, err := NewWithClock(boundTransaction{tx}, s.wrapper, s.clock)
	if err != nil {
		return review.Case{}, review.Principal{}, err
	}
	value, err := cases.findCaseWithin(ctx, scope, tx, caseID)
	if err != nil {
		return value, review.Principal{}, err
	}
	if err := validateCaseProcessing(ctx, tx, scope, value, s.clock.Now().UTC(), s.clock); err != nil {
		return value, review.Principal{}, err
	}
	principal, err := resolveWithin(ctx, tx, s.authority, scope, actor, value.Region, s.clock.Now().UTC())
	if err != nil {
		return value, principal, err
	}
	if !value.ChallengedDecision.IsZero() {
		if err := independentOfDecision(ctx, tx, scope, value.ChallengedDecision, principal.ID); err != nil {
			return value, principal, err
		}
	}
	if value.State == review.CaseEscalated {
		var config []byte
		if err := tx.QueryRow(ctx, `SELECT configuration FROM idenqa.review_case_settings WHERE tenant_id=$1 AND case_id=$2`, scope.ID().String(), value.ID.String()).Scan(&config); err != nil {
			return value, principal, err
		}
		var settings review.PolicySettings
		if json.Unmarshal(config, &settings) != nil || !slices.Contains(principal.Certifications, settings.EscalationCertificate) {
			return value, principal, review.ErrForbidden
		}
	}

	if err := value.CanReadEvidence(principal, version); err != nil {
		if value.Version != version || !slices.Contains(principal.Permissions, review.PermissionAppeal) || !slices.Contains(principal.Certifications, value.RequiredCertificate) {
			return value, principal, err
		}
		var assigned bool
		if queryErr := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.appeals WHERE tenant_id=$1 AND case_id=$2 AND assigned_reviewer=$3 AND state IN ('independent_review','awaiting_input') AND deadline>$4)`, scope.ID().String(), caseID.String(), principal.ID, s.clock.Now().UTC()).Scan(&assigned); queryErr != nil {
			return value, principal, queryErr
		}
		if !assigned {
			return value, principal, review.ErrForbidden
		}
		if err := independentOfCase(ctx, tx, scope, value, principal.ID); err != nil {
			return value, principal, err
		}
	}
	var lockedVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM idenqa.review_cases WHERE tenant_id=$1 AND id=$2 FOR SHARE`, scope.ID().String(), caseID.String()).Scan(&lockedVersion); err != nil {
		return value, principal, err
	}
	if lockedVersion != version {
		return value, principal, review.ErrConflict
	}
	return value, principal, nil
}
func (s *EvidenceStore) composition(tx pg.Transaction) (*evidencepostgres.Store, *authority.Service, error) {
	bound := boundTransaction{tx}
	assets, err := evidencepostgres.New(bound, s.wrapper, s.catalog)
	if err != nil {
		return nil, nil, err
	}
	authorities, err := authoritypostgres.NewWithClock(bound, s.wrapper, s.clock)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := verificationpostgres.NewSessionStore(bound, s.wrapper, s.catalog)
	if err != nil {
		return nil, nil, err
	}
	service, err := authority.NewService(authorities, authorities, authorities, sessions, s.ids, s.clock, 24*time.Hour)
	return assets, service, err
}

// IssueEvidence commits a case-bound display capability and retry record.
func (s *EvidenceStore) IssueEvidence(ctx context.Context, scope tenant.Scope, input review.EvidenceRequest) (review.EvidenceAccess, error) {
	var result review.EvidenceAccess
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, principal, err := s.eligible(ctx, scope, tx, input.Actor, input.CaseID, input.Version)
		if err != nil {
			return err
		}
		if input.Retry.TenantID() != scope.ID() || input.Retry.Principal().String() != input.Actor.ID || input.Retry.Operation() != "reviews.evidence.issue" {
			return review.ErrInvalid
		}
		q := sqlgen.New(tx)
		reservation, err := idempg.Reserve(ctx, q, input.Retry)
		if err != nil {
			return err
		}
		if replay, exists := reservation.Result(); exists {
			var wire struct {
				GrantID string `json:"grant_id"`
			}
			if json.Unmarshal(replay.Body(), &wire) != nil {
				return review.ErrConflict
			}
			grant, err := id.ParseGrant(wire.GrantID)
			if err != nil {
				return err
			}
			result, err = s.findAccess(ctx, scope, tx, grant)
			if err != nil {
				return err
			}
			if result.CaseID != input.CaseID || result.ReviewerID != principal.ID || result.CaseVersion != input.Version || result.EvidenceID != input.EvidenceID {
				return review.ErrForbidden
			}
			return nil
		}
		var configuration []byte
		if err := tx.QueryRow(ctx, `SELECT configuration FROM idenqa.review_case_settings WHERE tenant_id=$1 AND case_id=$2`, scope.ID().String(), value.ID.String()).Scan(&configuration); errors.Is(err, pgx.ErrNoRows) {
			return review.ErrForbidden
		} else if err != nil {
			return err
		}
		var settings review.PolicySettings
		if json.Unmarshal(configuration, &settings) != nil || settings.Validate() != nil {
			return review.ErrForbidden
		}
		assets, authorizer, err := s.composition(tx)
		if err != nil {
			return err
		}
		asset, err := assets.Find(ctx, scope, input.EvidenceID)
		if err != nil {
			return err
		}
		record := asset.Record()
		if record.VerificationID != value.VerificationID || !asset.CanRead() {
			return review.ErrForbidden
		}
		var selected *review.DisplayRule
		for i := range settings.Display {
			if settings.Display[i].Requirement == record.RequirementKey {
				selected = &settings.Display[i]
				break
			}
		}
		if selected == nil {
			return review.ErrForbidden
		}
		var recipient string
		if err := tx.QueryRow(ctx, `SELECT a.recipient_reference FROM idenqa.verification_sessions s JOIN idenqa.processing_authorities a ON a.tenant_id=s.tenant_id AND a.id=s.authority_id WHERE s.tenant_id=$1 AND s.id=$2`, scope.ID().String(), value.VerificationID.String()).Scan(&recipient); err != nil {
			return err
		}
		issuer, err := evidence.NewGrantIssuer(assets, authorizer, assets, s.ids, s.catalog, s.clock, 5*time.Minute)
		if err != nil {
			return err
		}
		grant, err := issuer.Issue(ctx, scope, evidence.GrantInput{EvidenceID: input.EvidenceID, CheckReference: "review." + strings.ToLower(value.ID.String()), Runner: evidence.Runner{Identity: principal.ID, WorkloadVersion: "review.display.v1"}, Purpose: evidence.Name(selected.Purpose), PermittedVariants: []string{evidence.VariantOriginal}, RecipientReference: recipient, OutputDestination: "review.display", MaximumUses: 1, TTL: 5 * time.Minute, Attribution: evidence.CommandAttribution{Principal: evidence.Actor{Type: "tenant.api_key", ID: input.Actor.ID}, TenantActor: evidence.Actor{Type: "tenant.operator", ID: principal.ID}, Reason: "review.evidence.display"}})
		if err != nil {
			return err
		}
		redemption, err := s.ids.NewRedemption()
		if err != nil {
			return err
		}
		redactions, err := json.Marshal(selected.Redactions)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_evidence_access(tenant_id,grant_id,redemption_id,case_id,case_version,evidence_id,reviewer_id,redactions,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, scope.ID().String(), grant.ID().String(), redemption.String(), value.ID.String(), value.Version, input.EvidenceID.String(), principal.ID, redactions, s.clock.Now().UTC())
		if err != nil {
			return err
		}
		result = review.EvidenceAccess{GrantID: grant.ID(), RedemptionID: redemption, CaseID: value.ID, CaseVersion: value.Version, EvidenceID: input.EvidenceID, ReviewerID: principal.ID, ExpiresAt: grant.Record().ExpiresAt, Redactions: selected.Redactions}
		body, err := json.Marshal(struct {
			GrantID string `json:"grant_id"`
		}{grant.ID().String()})
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(201, body)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, input.Retry, replay, s.clock.Now().UTC())
	})
	return result, err
}
func (s *EvidenceStore) findAccess(ctx context.Context, scope tenant.Scope, tx pg.Transaction, grant id.Grant) (review.EvidenceAccess, error) {
	result := review.EvidenceAccess{GrantID: grant}
	var redemption, caseID, evidenceID string
	var redactions []byte
	err := tx.QueryRow(ctx, `SELECT r.redemption_id,r.case_id,r.case_version,r.evidence_id,r.reviewer_id,r.redactions,g.expires_at FROM idenqa.review_evidence_access r JOIN idenqa.evidence_processing_grants g ON g.tenant_id=r.tenant_id AND g.id=r.grant_id WHERE r.tenant_id=$1 AND r.grant_id=$2`, scope.ID().String(), grant.String()).Scan(&redemption, &caseID, &result.CaseVersion, &evidenceID, &result.ReviewerID, &redactions, &result.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, review.ErrForbidden
	}
	if err != nil {
		return result, err
	}
	result.RedemptionID, err = id.ParseRedemption(redemption)
	if err != nil {
		return result, err
	}
	result.CaseID, err = id.ParseReviewCase(caseID)
	if err != nil {
		return result, err
	}
	result.EvidenceID, err = id.ParseEvidence(evidenceID)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(redactions, &result.Redactions)
	return result, err
}

// ReadEvidence rechecks current authority before audited evidence delivery.
func (s *EvidenceStore) ReadEvidence(ctx context.Context, scope tenant.Scope, actor review.Actor, grant id.Grant, receiver evidence.PlaintextReceiver) error {
	var readErr error
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		access, err := s.findAccess(ctx, scope, tx, grant)
		if err != nil {
			return err
		}
		_, principal, err := s.eligible(ctx, scope, tx, actor, access.CaseID, access.CaseVersion)
		if err != nil {
			return err
		}
		if principal.ID != access.ReviewerID {
			return review.ErrForbidden
		}
		configurable, ok := receiver.(interface{ Configure(review.EvidenceAccess) })
		if !ok {
			return review.ErrForbidden
		}
		configurable.Configure(access)
		assets, authorizer, err := s.composition(tx)
		if err != nil {
			return err
		}
		reader, err := evidence.NewReader(assets, assets, assets, assets, authorizer, s.opener, s.objects, s.clock, 5*time.Second)
		if err != nil {
			return err
		}
		readErr = reader.Read(ctx, scope, evidence.ReadInput{GrantID: grant, RedemptionID: access.RedemptionID, Runner: evidence.Runner{Identity: principal.ID, WorkloadVersion: "review.display.v1"}}, receiver)
		return nil
	})
	if err != nil {
		return err
	}
	return readErr
}

// ListEvidence lists bounded display-eligible artefact references.
func (s *EvidenceStore) ListEvidence(ctx context.Context, scope tenant.Scope, actor review.Actor, caseID id.ReviewCase, version int64) ([]review.EvidenceMetadata, error) {
	result := make([]review.EvidenceMetadata, 0)
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, _, err := s.eligible(ctx, scope, tx, actor, caseID, version)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT a.id,a.requirement_key,a.evidence_type,a.artefact,a.acquisition_method FROM idenqa.evidence_assets a JOIN idenqa.review_case_settings s ON s.tenant_id=a.tenant_id AND s.case_id=$2 WHERE a.tenant_id=$1 AND a.verification_id=$3 AND a.state='available' AND EXISTS(SELECT 1 FROM jsonb_array_elements(s.configuration->'display') r WHERE r->>'requirement'=a.requirement_key) ORDER BY a.id LIMIT 101`, scope.ID().String(), caseID.String(), value.VerificationID.String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item review.EvidenceMetadata
			if err := rows.Scan(&item.ID, &item.Requirement, &item.EvidenceType, &item.Artefact, &item.AcquisitionMethod); err != nil {
				return err
			}
			result = append(result, item)
		}
		if len(result) > 100 {
			return review.ErrConflict
		}
		return rows.Err()
	})
	return result, err
}
