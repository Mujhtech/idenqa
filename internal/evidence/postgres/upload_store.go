package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/outbox"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/realtime"
	realtimepostgres "github.com/Mujhtech/idenqa/internal/realtime/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

const uploadExpiredReason = "evidence.upload.expired"

const evidenceReadyEventSchemaVersion int32 = 1

var (
	_ evidence.UploadCreator         = (*Store)(nil)
	_ evidence.UploadFinder          = (*Store)(nil)
	_ evidence.UploadAttemptClaimer  = (*Store)(nil)
	_ evidence.UploadAttemptReleaser = (*Store)(nil)
	_ evidence.UploadAttemptRejecter = (*Store)(nil)
	_ evidence.UploadAccepter        = (*Store)(nil)
)

// CreateUpload atomically reserves the initiation key, inserts one immutable
// intent and its audit, and stores a safe replay reference.
func (store *Store) CreateUpload(
	ctx context.Context,
	scope tenant.Scope,
	mutation evidence.UploadCreateMutation,
) (evidence.Upload, error) {
	record := mutation.Upload.Record()
	request := mutation.Idempotency
	if scope.ID().IsZero() || record.TenantID != scope.ID() ||
		request.TenantID() != scope.ID() ||
		request.Principal().String() != record.CaptureTokenID.String() ||
		request.Operation() != evidence.OperationCreateUpload ||
		!request.CreatedAt().Equal(record.CreatedAt) ||
		record.State != evidence.UploadStateIssued || record.Version != 1 || record.Attempt != 0 {
		return evidence.Upload{}, evidence.ErrUploadConflict
	}
	if _, err := store.catalog.Resolve(record.Registry); err != nil {
		return evidence.Upload{}, fmt.Errorf("create upload registry: %w", err)
	}

	created := mutation.Upload
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		reservation, err := idempotencypostgres.Reserve(ctx, queries, request)
		if err != nil {
			return err
		}
		if replay, exists := reservation.Result(); exists {
			created, err = store.restoreUploadReplay(ctx, queries, scope.ID(), replay)

			return err
		}

		if err := store.lockUploadSession(ctx, queries, scope, record.VerificationID, record.CaptureTokenID); err != nil {
			return err
		}
		parameters, err := createUploadParameters(record)
		if err != nil {
			return err
		}
		if err := queries.CreateEvidenceUploadIntent(ctx, parameters); err != nil {
			return fmt.Errorf("insert evidence upload intent: %w", err)
		}
		if err := insertUploadAudit(ctx, queries, record, record.CaptureTokenID, "create", ""); err != nil {
			return err
		}
		encoded, err := json.Marshal(uploadReplay{UploadID: record.ID.String()})
		if err != nil {
			return fmt.Errorf("encode upload idempotency result: %w", err)
		}
		result, err := idempotency.NewResult(201, encoded)
		if err != nil {
			return err
		}

		return idempotencypostgres.Complete(ctx, queries, request, result, record.CreatedAt)
	})

	return created, err
}

// FindUpload retrieves an upload intent only within its explicit tenant scope.
func (store *Store) FindUpload(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.Upload,
) (evidence.Upload, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return evidence.Upload{}, evidence.ErrUploadNotFound
	}

	var upload evidence.Upload
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set upload tenant scope: %w", err)
			}
			row, err := queries.FindEvidenceUploadIntent(ctx, sqlgen.FindEvidenceUploadIntentParams{
				TenantID: scope.ID().String(), ID: identifier.String(),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return evidence.ErrUploadNotFound
			}
			if err != nil {
				return fmt.Errorf("find evidence upload intent: %w", err)
			}
			upload, err = store.restoreUpload(row)

			return err
		},
	)

	return upload, err
}

// ListAcceptedUploads returns a bounded capture-token and verification-scoped
// completion projection in deterministic acceptance order.
func (store *Store) ListAcceptedUploads(
	ctx context.Context,
	scope tenant.Scope,
	captureTokenID id.CaptureToken,
	verificationID id.Verification,
	limit int32,
) ([]evidence.Upload, error) {
	if scope.ID().IsZero() || captureTokenID.IsZero() || verificationID.IsZero() || limit < 1 {
		return nil, evidence.ErrUploadNotFound
	}

	var uploads []evidence.Upload
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set capture progress tenant scope: %w", err)
			}
			rows, err := queries.ListAcceptedEvidenceUploadIntents(
				ctx,
				sqlgen.ListAcceptedEvidenceUploadIntentsParams{
					TenantID: scope.ID().String(), CaptureTokenID: captureTokenID.String(),
					VerificationID: verificationID.String(), Limit: limit,
				},
			)
			if err != nil {
				return fmt.Errorf("list accepted evidence upload intents: %w", err)
			}
			uploads = make([]evidence.Upload, len(rows))
			for index, row := range rows {
				upload, err := store.restoreUpload(row)
				if err != nil {
					return err
				}
				uploads[index] = upload
			}

			return nil
		},
	)

	return uploads, err
}

// ClaimUploadAttempt locks the durable intent and fences one new whole-body attempt.
func (store *Store) ClaimUploadAttempt(
	ctx context.Context,
	scope tenant.Scope,
	principal id.CaptureToken,
	identifier id.Upload,
	expectedVersion int64,
	now time.Time,
) (evidence.Upload, error) {
	return store.transitionUpload(ctx, scope, principal, identifier, true, func(upload evidence.Upload) (evidence.Upload, string, string, error) {
		transitioned, err := upload.ClaimAttempt(expectedVersion, now)
		return transitioned, "claim", "", err
	})
}

// FailUploadAttempt releases the exact current attempt for a complete-body retry.
func (store *Store) FailUploadAttempt(
	ctx context.Context,
	scope tenant.Scope,
	principal id.CaptureToken,
	identifier id.Upload,
	expectedVersion int64,
	attempt uint32,
	now time.Time,
) (evidence.Upload, error) {
	return store.transitionUpload(ctx, scope, principal, identifier, false, func(upload evidence.Upload) (evidence.Upload, string, string, error) {
		transitioned, err := upload.FailAttempt(expectedVersion, attempt, now)
		if err != nil {
			return evidence.Upload{}, "", "", err
		}
		if transitioned.State() == evidence.UploadStateExpired {
			return transitioned, "expire", uploadExpiredReason, nil
		}

		return transitioned, "retry", "", nil
	})
}

// RejectUploadAttempt terminally records a safe validation or policy reason.
func (store *Store) RejectUploadAttempt(
	ctx context.Context,
	scope tenant.Scope,
	principal id.CaptureToken,
	identifier id.Upload,
	expectedVersion int64,
	attempt uint32,
	reason string,
	now time.Time,
) (evidence.Upload, error) {
	return store.transitionUpload(ctx, scope, principal, identifier, false, func(upload evidence.Upload) (evidence.Upload, string, string, error) {
		transitioned, err := upload.Reject(expectedVersion, attempt, reason, now)
		return transitioned, "reject", reason, err
	})
}

// AcceptUpload makes protected evidence available and accepts its exact upload
// in the same transaction as both audits and the evidence-ready outbox intent.
func (store *Store) AcceptUpload(
	ctx context.Context,
	scope tenant.Scope,
	mutation evidence.UploadAcceptance,
) (evidence.Upload, error) {
	record := mutation.Asset.Record()
	if scope.ID().IsZero() || mutation.UploadID.IsZero() || mutation.CaptureTokenID.IsZero() ||
		mutation.EventID.IsZero() || mutation.ExpectedVersion < 1 || mutation.Attempt < 1 ||
		mutation.Attempt > math.MaxInt32 ||
		mutation.PlaintextBytes < 1 || mutation.OccurredAt.IsZero() || record.TenantID != scope.ID() ||
		record.State != evidence.StateAvailable || record.Version != 1 {
		return evidence.Upload{}, evidence.ErrUploadConflict
	}
	if _, err := store.catalog.Resolve(record.Registry); err != nil {
		return evidence.Upload{}, fmt.Errorf("accept upload registry: %w", err)
	}
	event, err := evidenceReadyEvent(mutation)
	if err != nil {
		return evidence.Upload{}, err
	}

	var accepted evidence.Upload
	err = store.writeTx(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction, queries *sqlgen.Queries) error {
		// Lock the parent before any upload row or progress read. At READ COMMITTED,
		// the subsequent statements observe the preceding acceptance's commit.
		if _, err := queries.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{
			TenantID: scope.ID().String(), ID: record.VerificationID.String(),
		}); errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrUploadNotFound
		} else if err != nil {
			return fmt.Errorf("lock accepting verification session: %w", err)
		}
		row, err := queries.LockEvidenceUploadIntent(ctx, sqlgen.LockEvidenceUploadIntentParams{
			TenantID: scope.ID().String(), ID: mutation.UploadID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrUploadNotFound
		}
		if err != nil {
			return fmt.Errorf("lock accepting evidence upload: %w", err)
		}
		current, err := store.restoreUpload(row)
		if err != nil {
			return err
		}
		if current.Record().CaptureTokenID != mutation.CaptureTokenID ||
			current.Record().VerificationID != record.VerificationID {
			return evidence.ErrUploadNotFound
		}
		if err := authoritypostgres.ValidateUploadAcceptanceWithin(
			ctx, queries, scope, current.Record(), mutation.OccurredAt, store.clock,
		); err != nil {
			return err
		}
		accepted, err = current.Accept(
			mutation.ExpectedVersion,
			mutation.Attempt,
			mutation.Asset,
			mutation.PlaintextBytes,
			mutation.OccurredAt,
		)
		if err != nil {
			return err
		}
		parameters, err := createParameters(record)
		if err != nil {
			return err
		}
		if err := queries.CreateEvidenceAsset(ctx, parameters); err != nil {
			return fmt.Errorf("insert accepted evidence asset: %w", err)
		}
		if err := queries.InsertEvidenceAssetAudit(ctx, sqlgen.InsertEvidenceAssetAuditParams{
			TenantID: record.TenantID.String(), EvidenceID: record.ID.String(),
			AggregateVersion: record.Version, Action: "create", OccurredAt: timestamp(record.CreatedAt),
		}); err != nil {
			return fmt.Errorf("insert accepted evidence audit: %w", err)
		}
		object := mutation.Asset.Content().Object().Record()
		reconciliation, err := queries.RetainEvidenceObjectReconciliationForAcceptance(
			ctx,
			sqlgen.RetainEvidenceObjectReconciliationForAcceptanceParams{
				TenantID: scope.ID().String(), UploadID: mutation.UploadID.String(),
				UploadAttempt: databaseInt32(mutation.Attempt), EvidenceID: record.ID.String(),
				ObjectKey: object.Key, ObjectVersion: object.Version, ObjectSize: object.Size,
				ObjectChecksum: object.Checksum, UpdatedAt: timestamp(mutation.OccurredAt),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrReconciliationConflict
		}
		if err != nil {
			return fmt.Errorf("retain accepted evidence reconciliation: %w", err)
		}
		if err := queries.InsertEvidenceObjectReconciliationAudit(
			ctx,
			sqlgen.InsertEvidenceObjectReconciliationAuditParams{
				TenantID: scope.ID().String(), UploadID: mutation.UploadID.String(),
				UploadAttempt: databaseInt32(mutation.Attempt), AggregateVersion: reconciliation.Version,
				Claim: reconciliation.Claim, Action: "retain", OccurredAt: timestamp(mutation.OccurredAt),
			},
		); err != nil {
			return fmt.Errorf("insert retained evidence reconciliation audit: %w", err)
		}
		transition, err := transitionUploadParameters(accepted.Record(), current.Version())
		if err != nil {
			return err
		}
		persisted, err := queries.TransitionEvidenceUploadIntent(ctx, transition)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrUploadVersionConflict
		}
		if err != nil {
			return fmt.Errorf("accept evidence upload intent: %w", err)
		}
		accepted, err = store.restoreUpload(persisted)
		if err != nil {
			return err
		}
		if err := insertUploadAudit(
			ctx, queries, accepted.Record(), mutation.CaptureTokenID, "accept", "",
		); err != nil {
			return err
		}
		if err := queries.InsertOutboxEvent(ctx, sqlgen.InsertOutboxEventParams{
			ID: event.ID.String(), TenantID: scope.ID().String(),
			AggregateType: event.AggregateType, AggregateID: event.AggregateID,
			AggregateVersion: event.AggregateVersion, EventType: event.EventType,
			SchemaVersion: evidenceReadyEventSchemaVersion, Payload: event.Payload,
			OccurredAt: timestamp(event.OccurredAt), CreatedAt: timestamp(mutation.OccurredAt),
		}); err != nil {
			return fmt.Errorf("insert evidence-ready event: %w", err)
		}
		seed := "evidence.ready:" + record.ID.String()
		assurances := make([]string, len(record.Assurances))
		for index, assurance := range record.Assurances {
			assurances[index] = string(assurance)
		}
		if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, scope.ID().String(), record.Region, webhookv1.EvidenceReady, seed, mutation.OccurredAt, map[string]any{
			"evidence_id":        record.ID.String(),
			"verification_id":    record.VerificationID.String(),
			"evidence_type":      string(record.EvidenceType),
			"artefact":           string(record.Artefact),
			"acquisition_method": string(record.AcquisitionMethod),
			"assurances":         assurances,
			"evidence": map[string]any{
				"id":                 record.ID.String(),
				"type":               "evidence",
				"verification_id":    record.VerificationID.String(),
				"evidence_type":      string(record.EvidenceType),
				"artefact":           string(record.Artefact),
				"acquisition_method": string(record.AcquisitionMethod),
				"assurances":         assurances,
			},
		}); err != nil {
			return err
		}
		publication, err := queries.LoadCaptureProgressPublication(
			ctx,
			sqlgen.LoadCaptureProgressPublicationParams{
				TenantID: scope.ID().String(), VerificationID: record.VerificationID.String(),
				CaptureTokenID: mutation.CaptureTokenID.String(),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrUploadNotFound
		}
		if err != nil {
			return fmt.Errorf("load capture progress publication: %w", err)
		}
		profile, err := verification.ParseProfileFromCatalog(publication.Requirements, store.catalog)
		if err != nil {
			return fmt.Errorf("parse capture progress requirements: %w", err)
		}
		totalSteps, completedSteps, err := captureProfileProgress(profile, publication.AcceptedBindings)
		if err != nil || completedSteps < 1 || completedSteps > totalSteps {
			return errors.New("evidence postgres: capture progress count is invalid")
		}
		if completedSteps == totalSteps {
			if _, err := queries.CompleteVerificationCapture(
				ctx,
				sqlgen.CompleteVerificationCaptureParams{
					CompletedAt: timestamp(mutation.OccurredAt),
					TenantID:    scope.ID().String(), VerificationID: record.VerificationID.String(),
				},
			); err != nil {
				return fmt.Errorf("complete verification capture: %w", err)
			}
		}
		progressIntent, err := realtime.NewEventIntent(
			mutation.EventID,
			scope.ID(),
			record.VerificationID,
			id.Command{},
			id.Message{},
			id.Message{},
			mutation.OccurredAt,
			publication.CaptureTokenExpiresAt.Time.UTC(),
			realtime.CaptureProgress{
				CompletedSteps: completedSteps, TotalSteps: totalSteps,
			},
		)
		if err != nil {
			return fmt.Errorf("build capture progress event: %w", err)
		}
		if _, err := realtimepostgres.AppendEventWithinTransaction(ctx, queries, progressIntent); err != nil {
			return fmt.Errorf("append capture progress event: %w", err)
		}

		return nil
	})
	if errors.Is(err, platformpostgres.ErrCommitOutcomeUnknown) {
		return evidence.Upload{}, errors.Join(evidence.ErrUploadAcceptanceOutcomeUnknown, err)
	}

	return accepted, err
}

type acceptedCaptureBinding struct {
	RequirementKey    string `json:"requirement_key"`
	Artefact          string `json:"artefact"`
	AcquisitionMethod string `json:"acquisition_method"`
}

func captureProfileProgress(profile verification.Profile, encoded string) (uint32, uint32, error) {
	steps := 0
	requirements := make(map[string]verification.Requirement, len(profile.Requirements))
	for _, requirement := range profile.Requirements {
		requirements[requirement.Key] = requirement
		methods := 1
		if requirement.Acquisition.Strategy == verification.StrategyAllOf {
			methods = len(requirement.Acquisition.Methods)
		}
		steps += len(requirement.Artefacts) * methods
		if uint64(steps) > math.MaxUint32 {
			return 0, 0, errors.New("evidence postgres: capture profile has too many steps")
		}
	}
	if steps < 1 {
		return 0, 0, errors.New("evidence postgres: capture profile has no steps")
	}
	var bindings []acceptedCaptureBinding
	if err := json.Unmarshal([]byte(encoded), &bindings); err != nil {
		return 0, 0, fmt.Errorf("decode accepted capture bindings: %w", err)
	}
	completed := make(map[string]struct{}, len(bindings))
	for _, binding := range bindings {
		requirement, exists := requirements[binding.RequirementKey]
		if !exists || !containsEvidenceName(requirement.Artefacts, binding.Artefact) {
			return 0, 0, errors.New("evidence postgres: accepted capture binding is unknown")
		}
		key := binding.RequirementKey + "\x00" + binding.Artefact
		if requirement.Acquisition.Strategy == verification.StrategyAllOf {
			key += "\x00" + binding.AcquisitionMethod
		}
		completed[key] = struct{}{}
	}
	if len(completed) > steps {
		return 0, 0, errors.New("evidence postgres: completed capture progress exceeds profile")
	}
	// #nosec G115 -- both conversions are bounded above immediately before use.
	return uint32(steps), uint32(len(completed)), nil
}

func containsEvidenceName(values []evidence.Name, candidate string) bool {
	for _, value := range values {
		if string(value) == candidate {
			return true
		}
	}
	return false
}

func evidenceReadyEvent(mutation evidence.UploadAcceptance) (outbox.Intent, error) {
	record := mutation.Asset.Record()
	assurances := make([]string, len(record.Assurances))
	for index, assurance := range record.Assurances {
		assurances[index] = string(assurance)
	}
	event, err := outbox.NewIntent(
		mutation.EventID,
		"evidence",
		record.ID.String(),
		record.Version,
		evidence.EventEvidenceReady,
		uint32(evidenceReadyEventSchemaVersion),
		map[string]any{
			"evidence_id": record.ID.String(), "verification_id": record.VerificationID.String(),
			"requirement_key": record.RequirementKey, "evidence_type": string(record.EvidenceType),
			"artefact": string(record.Artefact), "acquisition_method": string(record.AcquisitionMethod),
			"assurances": assurances,
		},
		mutation.OccurredAt,
	)
	if err != nil {
		return outbox.Intent{}, fmt.Errorf("build evidence-ready event: %w", err)
	}

	return event, nil
}

type uploadTransition func(evidence.Upload) (evidence.Upload, string, string, error)

func (store *Store) transitionUpload(
	ctx context.Context,
	scope tenant.Scope,
	principal id.CaptureToken,
	identifier id.Upload,
	starting bool,
	transition uploadTransition,
) (evidence.Upload, error) {
	if scope.ID().IsZero() || principal.IsZero() || identifier.IsZero() || transition == nil {
		return evidence.Upload{}, evidence.ErrUploadNotFound
	}

	var result evidence.Upload
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		if starting {
			reference, err := queries.FindEvidenceUploadIntent(ctx, sqlgen.FindEvidenceUploadIntentParams{TenantID: scope.ID().String(), ID: identifier.String()})
			if errors.Is(err, pgx.ErrNoRows) {
				return evidence.ErrUploadNotFound
			}
			if err != nil {
				return err
			}
			if reference.CaptureTokenID != principal.String() {
				return evidence.ErrUploadNotFound
			}
			verificationID, err := id.ParseVerification(reference.VerificationID)
			if err != nil {
				return err
			}
			if err := store.lockUploadSession(ctx, queries, scope, verificationID, principal); err != nil {
				return err
			}
		}
		row, err := queries.LockEvidenceUploadIntent(ctx, sqlgen.LockEvidenceUploadIntentParams{
			TenantID: scope.ID().String(), ID: identifier.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrUploadNotFound
		}
		if err != nil {
			return fmt.Errorf("lock evidence upload intent: %w", err)
		}
		current, err := store.restoreUpload(row)
		if err != nil {
			return err
		}
		if current.Record().CaptureTokenID != principal {
			return evidence.ErrUploadNotFound
		}
		var action, reason string
		result, action, reason, err = transition(current)
		if err != nil {
			return err
		}
		parameters, err := transitionUploadParameters(result.Record(), current.Version())
		if err != nil {
			return err
		}
		persisted, err := queries.TransitionEvidenceUploadIntent(ctx, parameters)
		if errors.Is(err, pgx.ErrNoRows) {
			return evidence.ErrUploadVersionConflict
		}
		if err != nil {
			return fmt.Errorf("transition evidence upload intent: %w", err)
		}
		result, err = store.restoreUpload(persisted)
		if err != nil {
			return err
		}

		return insertUploadAudit(ctx, queries, result.Record(), principal, action, reason)
	})

	return result, err
}

func createUploadParameters(record evidence.UploadRecord) (sqlgen.CreateEvidenceUploadIntentParams, error) {
	if record.ProfileRevision > math.MaxInt32 || record.Registry.SchemaVersion > math.MaxInt32 ||
		record.Registry.Revision > math.MaxInt32 || record.Attempt > math.MaxInt32 ||
		record.AttemptTimeout%time.Millisecond != 0 {
		return sqlgen.CreateEvidenceUploadIntentParams{}, errors.New("evidence postgres: upload value exceeds database range")
	}
	assurances := make([]string, len(record.Assurances))
	for index, assurance := range record.Assurances {
		assurances[index] = string(assurance)
	}

	return sqlgen.CreateEvidenceUploadIntentParams{
		ID: record.ID.String(), TenantID: record.TenantID.String(),
		CaptureTokenID: record.CaptureTokenID.String(), SubjectID: record.SubjectID.String(),
		VerificationID: record.VerificationID.String(), EvidenceID: record.EvidenceID.String(),
		AuthorityID: record.AuthorityID.String(), ResponseID: record.ResponseID.String(),
		ProfileID: record.ProfileID.String(), ProfileRevision: int32(record.ProfileRevision),
		ProfileDigest: record.ProfileDigest, RegistrySchemaVersion: int32(record.Registry.SchemaVersion),
		RegistryRevision: int32(record.Registry.Revision), RegistryDigest: record.Registry.Digest,
		RequirementKey: record.RequirementKey, Purpose: string(record.Purpose),
		EvidenceType: string(record.EvidenceType), Artefact: string(record.Artefact),
		AcquisitionMethod: string(record.AcquisitionMethod), Assurances: assurances,
		FallbackCondition: nullableString(record.FallbackCondition),
		EncryptionPurpose: record.EncryptionPurpose,
		AllowedMediaTypes: append([]string(nil), record.AllowedMediaTypes...),
		MaximumBytes:      record.MaximumBytes, ExpectedBytes: record.ExpectedBytes,
		ExpectedDigest: record.ExpectedDigest, MediaType: record.MediaType,
		Region: record.Region, RetentionClass: record.RetentionClass,
		State: string(record.State), Version: record.Version, Attempt: int32(record.Attempt),
		AttemptTimeoutMilliseconds: record.AttemptTimeout.Milliseconds(),
		LeaseExpiresAt:             nullableTimestamp(record.LeaseExpiresAt), CreatedAt: timestamp(record.CreatedAt),
		UpdatedAt: timestamp(record.UpdatedAt), ExpiresAt: timestamp(record.ExpiresAt),
		AcceptedAt: nullableTimestamp(record.AcceptedAt), RejectionReason: nullableString(record.RejectionReason),
	}, nil
}

func transitionUploadParameters(
	record evidence.UploadRecord,
	expectedVersion int64,
) (sqlgen.TransitionEvidenceUploadIntentParams, error) {
	if record.Attempt > math.MaxInt32 {
		return sqlgen.TransitionEvidenceUploadIntentParams{},
			errors.New("evidence postgres: upload attempt exceeds database range")
	}

	return sqlgen.TransitionEvidenceUploadIntentParams{
		TenantID: record.TenantID.String(), ID: record.ID.String(), Version: expectedVersion,
		State: string(record.State), Version_2: record.Version, Attempt: int32(record.Attempt),
		LeaseExpiresAt: nullableTimestamp(record.LeaseExpiresAt), UpdatedAt: timestamp(record.UpdatedAt),
		AcceptedAt: nullableTimestamp(record.AcceptedAt), RejectionReason: nullableString(record.RejectionReason),
	}, nil
}

func insertUploadAudit(
	ctx context.Context,
	queries *sqlgen.Queries,
	record evidence.UploadRecord,
	principal id.CaptureToken,
	action string,
	reason string,
) error {
	if record.Attempt > math.MaxInt32 {
		return errors.New("evidence postgres: upload attempt exceeds database range")
	}
	if err := queries.InsertEvidenceUploadIntentAudit(ctx, sqlgen.InsertEvidenceUploadIntentAuditParams{
		TenantID: record.TenantID.String(), UploadID: record.ID.String(),
		AggregateVersion: record.Version, Attempt: int32(record.Attempt), Action: action,
		PrincipalID: principal.String(), Reason: nullableString(reason), OccurredAt: timestamp(record.UpdatedAt),
	}); err != nil {
		return fmt.Errorf("insert evidence upload audit: %w", err)
	}

	return nil
}

func (store *Store) restoreUploadReplay(
	ctx context.Context,
	queries *sqlgen.Queries,
	tenantID id.Tenant,
	replay idempotency.Result,
) (evidence.Upload, error) {
	var reference uploadReplay
	if err := json.Unmarshal(replay.Body(), &reference); err != nil {
		return evidence.Upload{}, fmt.Errorf("decode upload idempotency result: %w", err)
	}
	uploadID, err := id.ParseUpload(reference.UploadID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("parse replay upload id: %w", err)
	}
	row, err := queries.FindEvidenceUploadIntent(ctx, sqlgen.FindEvidenceUploadIntentParams{
		TenantID: tenantID.String(), ID: uploadID.String(),
	})
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("find replayed evidence upload intent: %w", err)
	}

	return store.restoreUpload(row)
}

func (store *Store) restoreUpload(row sqlgen.IdenqaEvidenceUploadIntent) (evidence.Upload, error) {
	if row.ProfileRevision <= 0 || row.RegistrySchemaVersion <= 0 || row.RegistryRevision <= 0 ||
		row.Attempt < 0 || row.AttemptTimeoutMilliseconds <= 0 {
		return evidence.Upload{}, errors.New("evidence postgres: invalid durable upload version")
	}
	uploadID, err := id.ParseUpload(row.ID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload tenant: %w", err)
	}
	captureTokenID, err := id.ParseCaptureToken(row.CaptureTokenID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload capture token: %w", err)
	}
	subjectID, err := id.ParseSubject(row.SubjectID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload subject: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload verification: %w", err)
	}
	evidenceID, err := id.ParseEvidence(row.EvidenceID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload evidence: %w", err)
	}
	authorityID, err := id.ParseAuthority(row.AuthorityID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload authority: %w", err)
	}
	responseID, err := id.ParseAcknowledgement(row.ResponseID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload response: %w", err)
	}
	profileID, err := id.ParseProfile(row.ProfileID)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload profile: %w", err)
	}
	reference := evidence.Reference{
		SchemaVersion: uint32(row.RegistrySchemaVersion), Revision: uint32(row.RegistryRevision),
		Digest: row.RegistryDigest,
	}
	registry, err := store.catalog.Resolve(reference)
	if err != nil {
		return evidence.Upload{}, fmt.Errorf("restore upload registry: %w", err)
	}
	assurances := make([]evidence.Name, len(row.Assurances))
	for index, assurance := range row.Assurances {
		assurances[index] = evidence.Name(assurance)
	}

	return evidence.RestoreUpload(evidence.UploadRecord{
		ID: uploadID, TenantID: tenantID, CaptureTokenID: captureTokenID,
		SubjectID: subjectID, VerificationID: verificationID, EvidenceID: evidenceID,
		AuthorityID: authorityID, ResponseID: responseID, ProfileID: profileID,
		ProfileRevision: uint32(row.ProfileRevision), ProfileDigest: row.ProfileDigest,
		Registry: reference, RequirementKey: row.RequirementKey, Purpose: evidence.Name(row.Purpose),
		EvidenceType: evidence.Name(row.EvidenceType), Artefact: evidence.Name(row.Artefact),
		AcquisitionMethod: evidence.Name(row.AcquisitionMethod),
		FallbackCondition: valueOrEmpty(row.FallbackCondition), Assurances: assurances,
		EncryptionPurpose: row.EncryptionPurpose,
		AllowedMediaTypes: append([]string(nil), row.AllowedMediaTypes...),
		MaximumBytes:      row.MaximumBytes, ExpectedBytes: row.ExpectedBytes,
		ExpectedDigest: row.ExpectedDigest, MediaType: row.MediaType,
		Region: row.Region, RetentionClass: row.RetentionClass,
		State: evidence.UploadState(row.State), Version: row.Version, Attempt: uint32(row.Attempt),
		AttemptTimeout: time.Duration(row.AttemptTimeoutMilliseconds) * time.Millisecond,
		LeaseExpiresAt: timestampPointer(row.LeaseExpiresAt), CreatedAt: row.CreatedAt.Time,
		UpdatedAt: row.UpdatedAt.Time, ExpiresAt: row.ExpiresAt.Time,
		AcceptedAt: timestampPointer(row.AcceptedAt), RejectionReason: valueOrEmpty(row.RejectionReason),
	}, registry)
}

type uploadReplay struct {
	UploadID string `json:"upload_id"`
}

// lockUploadSession orders parent, token, then upload locks and observes live
// deadlines after waiting. Cleanup transitions intentionally do not use this gate.
func (store *Store) lockUploadSession(ctx context.Context, queries *sqlgen.Queries, scope tenant.Scope, verificationID id.Verification, principal id.CaptureToken) error {
	session, err := queries.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{TenantID: scope.ID().String(), ID: verificationID.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence.ErrUploadNotFound
	}
	if err != nil {
		return fmt.Errorf("lock upload session: %w", err)
	}
	token, err := queries.LockCaptureTokenForUpload(ctx, sqlgen.LockCaptureTokenForUploadParams{TenantID: scope.ID().String(), ID: principal.String(), VerificationID: verificationID.String()})
	if errors.Is(err, pgx.ErrNoRows) {
		return evidence.ErrUploadNotFound
	}
	if err != nil {
		return fmt.Errorf("lock upload credential: %w", err)
	}
	now := store.clock.Now().UTC()
	if session.State != string(verification.SessionStateCollecting) || !session.ExpiresAt.Valid || !now.Before(session.ExpiresAt.Time) || token.RevokedAt.Valid || !token.ExpiresAt.Valid || !now.Before(token.ExpiresAt.Time) {
		return evidence.ErrUploadConflict
	}
	return nil
}

// AllowsRecoveredUpload requires an immutable retained binding and an unrevoked replacement credential.
func (store *Store) AllowsRecoveredUpload(ctx context.Context, scope tenant.Scope, token id.CaptureToken, upload id.Upload) (bool, error) {
	var allowed bool
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.capture_recovery_uploads r JOIN idenqa.capture_tokens t ON t.tenant_id=r.tenant_id AND t.id=r.new_token_id JOIN idenqa.evidence_upload_intents u ON u.tenant_id=r.tenant_id AND u.id=r.upload_id AND u.verification_id=t.verification_id WHERE r.tenant_id=$1 AND r.new_token_id=$2 AND r.upload_id=$3 AND r.disposition='retained' AND t.revoked_at IS NULL AND u.state='accepted')`, scope.ID().String(), token.String(), upload.String()).Scan(&allowed)
	})
	return allowed, err
}
