package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

var (
	// ErrCallbackUnavailable includes unknown, expired, and inactive callback
	// references. It deliberately does not distinguish them.
	ErrCallbackUnavailable = errors.New("provider: callback unavailable")
	// ErrCallbackInvalid identifies a malformed callback envelope.
	ErrCallbackInvalid = errors.New("provider: callback envelope invalid")
	// ErrCallbackRejected identifies an adapter-authenticated rejection.
	ErrCallbackRejected = errors.New("provider: callback rejected")
	// ErrCallbackConflict identifies the same provider replay identity with
	// conflicting accepted content.
	ErrCallbackConflict = errors.New("provider: callback conflicts with accepted receipt")
)

// CallbackTarget is the durable attempt context resolved from one opaque
// callback reference. It contains no provider payload material.
type CallbackTarget struct {
	Scope     tenant.Scope
	Request   providerv1.Request
	CheckID   id.Check
	AttemptID id.Attempt
	Deadline  time.Time
}

// CallbackResolver resolves one opaque callback reference to its exact
// persisted request and current authority. Unknown, expired, or inactive
// references return ErrCallbackUnavailable.
type CallbackResolver interface {
	ResolveCallback(context.Context, string) (CallbackTarget, error)
}

// CallbackReceipt reports how one normalized progress value was recorded.
type CallbackReceipt struct {
	Duplicate bool
	Terminal  bool
}

// CallbackReceiptStore records normalized progress keyed by provider replay
// identity. Identical replay is an exact duplicate; conflicting terminal
// content for one replay identity returns ErrCallbackConflict.
type CallbackReceiptStore interface {
	SaveCallbackProgress(context.Context, CallbackTarget, providerv1.Progress) (CallbackReceipt, error)
}

// CallbackWakeup is the optional best-effort wake-up for an accepted terminal
// callback. When absent, the bounded asynchronous polling cadence remains the
// fallback.
type CallbackWakeup interface {
	Wake(context.Context, CallbackTarget) error
}

// CallbackStatus is the closed public callback disposition.
type CallbackStatus string

// Supported callback dispositions.
const (
	CallbackPending   CallbackStatus = "pending"
	CallbackAccepted  CallbackStatus = "accepted"
	CallbackDuplicate CallbackStatus = "duplicate"
)

// CallbackOutcome is the safe result of one accepted callback.
type CallbackOutcome struct {
	Status CallbackStatus
}

// CallbackService verifies and durably records provider callbacks. Signature
// verification stays inside the isolated adapter reached through the runner;
// this service owns only reference resolution, receipt deduplication, and the
// bounded wake-up.
type CallbackService struct {
	resolver CallbackResolver
	verifier providerv1.CallbackVerifier
	receipts CallbackReceiptStore
	wakeup   CallbackWakeup
	now      func() time.Time
}

// NewCallbackService constructs the provider callback ingress service.
func NewCallbackService(
	resolver CallbackResolver,
	verifier providerv1.CallbackVerifier,
	receipts CallbackReceiptStore,
	wakeup CallbackWakeup,
	now func() time.Time,
) (*CallbackService, error) {
	if resolver == nil || verifier == nil || receipts == nil || now == nil {
		return nil, ErrCallbackUnavailable
	}
	return &CallbackService{resolver: resolver, verifier: verifier, receipts: receipts, wakeup: wakeup, now: now}, nil
}

// Handle authenticates one bounded callback and records its normalized
// progress. Raw callback material is never persisted or returned.
func (service *CallbackService) Handle(ctx context.Context, token string, envelope providerv1.CallbackEnvelope) (CallbackOutcome, error) {
	if service == nil || envelope.Validate() != nil {
		return CallbackOutcome{}, ErrCallbackInvalid
	}
	reference, err := id.ParseProviderCallback(token)
	if err != nil {
		return CallbackOutcome{}, ErrCallbackUnavailable
	}
	target, err := service.resolver.ResolveCallback(ctx, reference.String())
	if err != nil {
		return CallbackOutcome{}, err
	}
	now := service.now().UTC()
	if target.Request.CallbackReference != reference.String() || target.Scope.ID().IsZero() ||
		target.Request.TenantID != target.Scope.ID().String() || target.CheckID.IsZero() || target.AttemptID.IsZero() ||
		!now.Before(target.Deadline) {
		return CallbackOutcome{}, ErrCallbackUnavailable
	}
	progress, err := service.verifier.VerifyCallback(ctx, target.Request, envelope)
	if err != nil {
		if _, rejected := providerv1.AsCallbackRejection(err); rejected {
			return CallbackOutcome{}, ErrCallbackRejected
		}
		return CallbackOutcome{}, err
	}
	if progress.ValidateForRequest(target.Request) != nil {
		return CallbackOutcome{}, ErrCallbackRejected
	}
	receipt, err := service.receipts.SaveCallbackProgress(ctx, target, progress)
	if err != nil {
		return CallbackOutcome{}, err
	}
	outcome := CallbackOutcome{Status: CallbackAccepted}
	switch {
	case receipt.Duplicate:
		outcome.Status = CallbackDuplicate
	case !receipt.Terminal:
		outcome.Status = CallbackPending
	case service.wakeup != nil:
		// A wake-up failure is benign: the durable receipt is already visible to
		// the existing bounded polling cadence.
		_ = service.wakeup.Wake(ctx, target)
	}
	return outcome, nil
}

// CallbackTokenDigest derives the indexed lookup digest for one callback
// reference. An absent reference produces an empty digest.
func CallbackTokenDigest(reference string) (string, error) {
	if reference == "" {
		return "", nil
	}
	if _, err := id.ParseProviderCallback(reference); err != nil {
		return "", ErrCallbackUnavailable
	}
	sum := sha256.Sum256([]byte(reference))
	return hex.EncodeToString(sum[:]), nil
}

// CallbackConfigurationDigest binds the provider and configuration revision
// that authenticated one callback.
func CallbackConfigurationDigest(request providerv1.Request) (string, error) {
	encoded, err := json.Marshal(struct {
		ProviderID        string             `json:"provider_id"`
		SchemaDigest      string             `json:"schema_digest"`
		CredentialVersion string             `json:"credential_version"`
		AdapterID         string             `json:"adapter_id"`
		AdapterVersion    string             `json:"adapter_version"`
		PackageDigest     string             `json:"package_digest"`
		Contract          providerv1.Version `json:"contract"`
	}{
		ProviderID:        request.ProviderID,
		SchemaDigest:      request.Configuration.SchemaDigest,
		CredentialVersion: request.Configuration.CredentialVersion,
		AdapterID:         request.Adapter.AdapterID,
		AdapterVersion:    request.Adapter.AdapterVersion,
		PackageDigest:     request.Adapter.PackageDigest,
		Contract:          request.Adapter.Contract,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

// CallbackProgressDigest canonicalizes one normalized progress value for
// exact replay comparison.
func CallbackProgressDigest(progress providerv1.Progress) (string, error) {
	encoded, err := json.Marshal(progress)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}
