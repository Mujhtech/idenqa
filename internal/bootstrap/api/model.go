package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	tinkcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto/tink"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/go-chi/chi/v5"
)

const modelEvidenceLimit = 10 << 20

type modelEvidenceRoutes struct {
	pool       database
	plan       *model.Plan
	credential [32]byte
	objects    evidence.ObjectReader
	opener     evidence.ContentOpener
	catalog    evidence.Catalog
	ids        *id.Generator
}
type modelBoundTransaction struct{ tx pg.Transaction }

func (bound modelBoundTransaction) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	return work(ctx, bound.tx)
}

func newSingleModelEvidenceRoutes(settings config.ModelRuntime, pool database, infrastructure EvidenceInfrastructure, catalog evidence.Catalog) (*modelEvidenceRoutes, error) {
	plan, err := model.NewPlan(settings.Binding, settings.Manifest)
	if err != nil {
		return nil, err
	}
	credential, err := config.ReadCredentialFile(settings.GatewayCredentialFile)
	if err != nil {
		return nil, err
	}
	if _, err := runner.NewCredentialSet(credential); err != nil {
		return nil, err
	}
	objects, ok := infrastructure.objects.(evidence.ObjectReader)
	if !ok || infrastructure.keys == nil {
		return nil, errors.New("model gateway requires controlled evidence infrastructure")
	}
	purpose, err := kms.NewPurpose("idenqa.evidence.content")
	if err != nil {
		return nil, err
	}
	opener, err := tinkcrypto.NewStreaming(infrastructure.keys, infrastructure.keys, purpose)
	if err != nil {
		return nil, err
	}
	ids, err := id.NewSystemGenerator()
	if err != nil {
		return nil, err
	}
	return &modelEvidenceRoutes{pool, plan, sha256.Sum256([]byte("Bearer " + credential)), objects, opener, catalog, ids}, nil
}
func (routes *modelEvidenceRoutes) RegisterInternal(router chi.Router) {
	router.Post("/model-evidence", routes.read)
}
func (routes *modelEvidenceRoutes) read(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	given := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(given[:], routes.credential[:]) != 1 {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	var input struct {
		AttemptID    string `json:"attempt_id"`
		GrantID      string `json:"grant_id"`
		RedemptionID string `json:"redemption_id"`
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 4097))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	tenantID, err := id.ParseTenant(routes.plan.Binding.TenantID)
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var receiver *modelBuffer
	var readErr error
	err = routes.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, tenantID.String()); err != nil {
			return err
		}
		var encoded []byte
		if err := tx.QueryRow(ctx, `SELECT requests.request_body FROM idenqa.model_requests requests JOIN idenqa.model_dispatches dispatch ON dispatch.tenant_id=requests.tenant_id AND dispatch.attempt_id=requests.attempt_id WHERE requests.tenant_id=$1 AND requests.attempt_id=$2 AND dispatch.result_body IS NULL`, tenantID.String(), input.AttemptID).Scan(&encoded); err != nil {
			return evidence.ErrReadDenied
		}
		var request modelv1.Request
		if json.Unmarshal(encoded, &request) != nil || request.Validate() != nil || request.Configuration != routes.plan.Binding.Configuration || request.TenantID != tenantID.String() || request.Evaluation != routes.plan.Capability.Evaluation {
			return evidence.ErrReadDenied
		}
		matched := false
		for _, reference := range request.Evidence {
			if reference.GrantID == input.GrantID && reference.RedemptionID == input.RedemptionID {
				matched = true
			}
		}
		if !matched {
			return evidence.ErrReadDenied
		}
		verificationID, err := id.ParseVerification(request.VerificationID)
		if err != nil {
			return err
		}
		// Hold the session lock until authenticated plaintext and the grant audit commit.
		var state string
		if err := tx.QueryRow(ctx, `SELECT state FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID.String(), request.VerificationID).Scan(&state); err != nil {
			return evidence.ErrReadDenied
		}
		if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, verificationID, time.Now().UTC(), clock.System{}, verification.SessionStateProcessing); err != nil {
			return err
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_attempts AS attempts JOIN idenqa.verification_checks AS checks ON checks.tenant_id=attempts.tenant_id AND checks.id=attempts.check_id WHERE attempts.tenant_id=$1 AND attempts.id=$2 AND attempts.verification_id=$3 AND attempts.state='running' AND checks.state='running' AND attempts.deadline > $4)`, tenantID.String(), request.AttemptID, request.VerificationID, time.Now().UTC()).Scan(&active); err != nil || !active {
			return evidence.ErrReadDenied
		}
		bound := modelBoundTransaction{tx}
		store, err := evidencepostgres.New(bound, routes.catalog)
		if err != nil {
			return err
		}
		authorities, err := authoritypostgres.New(bound)
		if err != nil {
			return err
		}
		sessions, err := verificationpostgres.NewSessionStore(bound, routes.catalog)
		if err != nil {
			return err
		}
		authorizer, err := authority.NewService(authorities, authorities, authorities, sessions, routes.ids, clock.System{}, 24*time.Hour)
		if err != nil {
			return err
		}
		reader, err := evidence.NewReader(store, store, store, store, authorizer, routes.opener, routes.objects, clock.System{}, 5*time.Second)
		if err != nil {
			return err
		}
		grantID, err := id.ParseGrant(input.GrantID)
		if err != nil {
			return evidence.ErrReadDenied
		}
		redemptionID, err := id.ParseRedemption(input.RedemptionID)
		if err != nil {
			return evidence.ErrReadDenied
		}
		runner := evidence.Runner{Identity: routes.plan.Manifest.Provenance.ModelID, WorkloadVersion: routes.plan.Manifest.Provenance.ModelVersion}
		receiver = &modelBuffer{binding: evidence.ReceiverBinding{Runner: runner, CheckReference: request.Evaluation, OutputDestination: routes.plan.OutputDestination()}}
		readErr = reader.Read(ctx, scope, evidence.ReadInput{GrantID: grantID, RedemptionID: redemptionID, Runner: runner}, receiver)
		return nil
	})
	if receiver != nil {
		defer clear(receiver.Bytes())
	}
	if err != nil || readErr != nil || receiver == nil || receiver.Len() == 0 {
		http.Error(w, "evidence unavailable", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", receiver.mediaType)
	w.WriteHeader(200)
	_, _ = w.Write(receiver.Bytes())
}

type modelBuffer struct {
	bytes.Buffer
	binding   evidence.ReceiverBinding
	mediaType string
}

func (buffer *modelBuffer) Binding() evidence.ReceiverBinding { return buffer.binding }
func (buffer *modelBuffer) Write(value []byte) (int, error) {
	if buffer.Len()+len(value) > modelEvidenceLimit {
		return 0, evidence.ErrReadDenied
	}
	return buffer.Buffer.Write(value)
}
func (buffer *modelBuffer) Receive(ctx context.Context, _ id.Redemption, mediaType string, produce func(io.Writer) error, afterCommit func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := produce(buffer); err != nil {
		return err
	}
	buffer.mediaType = mediaType
	return afterCommit()
}

// One HTTP endpoint dispatches only by a distinct workload credential. The selected
// route still validates its saved request binding before any evidence is released.
type modelEvidenceGroup struct{ routes []*modelEvidenceRoutes }

func newModelEvidenceRoutes(configuration config.API, pool database, infrastructure EvidenceInfrastructure, catalog evidence.Catalog) (*modelEvidenceGroup, error) {
	settings, err := config.LoadModelRuntimes(configuration.ModelRuntimeFile)
	if err != nil {
		return nil, err
	}
	group := &modelEvidenceGroup{}
	seen := map[[32]byte]bool{}
	for _, setting := range settings {
		route, err := newSingleModelEvidenceRoutes(setting, pool, infrastructure, catalog)
		if err != nil {
			return nil, err
		}
		if seen[route.credential] {
			return nil, errors.New("model routes require distinct gateway credentials")
		}
		seen[route.credential] = true
		group.routes = append(group.routes, route)
	}
	return group, nil
}
func (group *modelEvidenceGroup) RegisterInternal(router chi.Router) {
	router.Post("/model-evidence", group.read)
}
func (group *modelEvidenceGroup) read(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	given := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	var selected *modelEvidenceRoutes
	for _, route := range group.routes {
		if subtle.ConstantTimeCompare(given[:], route.credential[:]) == 1 {
			selected = route
		}
	}
	if len(r.Header.Values("Authorization")) != 1 || selected == nil {
		http.Error(w, "unauthorised", http.StatusUnauthorized)
		return
	}
	selected.read(w, r)
}
