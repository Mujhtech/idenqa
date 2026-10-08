// Package managedtenant owns the fixed, retry-safe Core workflow used to
// provision one tenant and its Cloud-specific authority classes.
package managedtenant

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"

	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Contract versions identify fixed Core-local provisioning and synthetic operations.
const (
	ContractVersion          = "idenqa.core/managed-tenant-provision/v1"
	RenewContractVersion     = "idenqa.core/managed-tenant-renew/v1"
	SyntheticContractVersion = "idenqa.core/synthetic-journey/v1"
	rotationOverlap          = 10 * time.Minute
)

// ErrInvalid rejects malformed or unsupported local provisioning requests.
var ErrInvalid = errors.New("managed tenant provision request is invalid")

var consolePatterns = []string{
	"audit:export",
	"capture_profiles:read", "capture_profiles:write",
	"deletions:read",
	"evidence:read",
	"privacy_requests:approve", "privacy_requests:read", "privacy_requests:write",
	"reviews:admin", "reviews:read",
	"subjects:delete", "subjects:read", "subjects:write",
	"tenant:read",
	"verification_sessions:cancel", "verification_sessions:read",
	"webhooks:configure", "webhooks:read", "webhooks:replay",
}

var reviewerPatterns = []string{"reviews:*", "appeals:*"}

var syntheticPatterns = []string{
	"authorities:write", "capture_profiles:write", "decisions:read", "notices:write",
	"policies:activate", "policies:write", "verification_sessions:create", "verification_sessions:read",
}

type tenantProvisioner interface {
	Provision(context.Context, tenant.AdminAction, tenant.ProvisionCommand) (tenant.Tenant, bool, error)
}

type credentialIssuer interface {
	Issue(context.Context, access.BridgeCommand, access.AdminAction, tenant.Scope, access.IssueInput, access.CredentialSealer) (access.BridgeIssueResult, error)
	Rotate(context.Context, access.BridgeCommand, access.AdminAction, tenant.Scope, access.RotateInput, access.CredentialSealer) (access.BridgeIssueResult, error)
	Find(context.Context, tenant.Scope, id.APIKey) (access.Key, error)
}

// SealerFactory constructs purpose-bound encrypted delivery for the regional caller.
type SealerFactory func(string, string) (access.CredentialSealer, error)

// Request binds tenant provisioning to one idempotent local command.
type Request struct {
	CommandID         string `json:"commandId"`
	DeliveryPublicKey string `json:"deliveryPublicKey"`
}

// Credential returns issuance metadata and encrypted regional delivery material.
type Credential struct {
	KeyID     string                    `json:"keyId"`
	IssuedAt  time.Time                 `json:"issuedAt"`
	ExpiresAt time.Time                 `json:"expiresAt"`
	Envelope  access.CredentialEnvelope `json:"envelope"`
}

// Result binds separate Console and reviewer authority to the provisioned tenant.
type Result struct {
	TenantID string     `json:"tenantId"`
	Created  bool       `json:"created"`
	Console  Credential `json:"console"`
	Reviewer Credential `json:"reviewer"`
}

// SyntheticProvisionRequest selects an isolated deployment fixture tenant.
type SyntheticProvisionRequest struct {
	DeploymentID string `json:"deploymentId"`
}

// SyntheticProvisionResult identifies the isolated Core tenant.
type SyntheticProvisionResult struct {
	TenantID string `json:"tenantId"`
}

// SyntheticRunRequest supplies a fixed Core-owned readiness journey.
type SyntheticRunRequest struct {
	RunID           string          `json:"runId"`
	DeploymentID    string          `json:"deploymentId"`
	CoreEndpoint    string          `json:"coreEndpoint"`
	ProfileDocument json.RawMessage `json:"profileDocument"`
	PolicyDocument  json.RawMessage `json:"policyDocument"`
	FixtureVersion  string          `json:"fixtureVersion"`
	Region          string          `json:"region"`
	ExpectedOutcome string          `json:"expectedOutcome"`
	TimeoutSeconds  int             `json:"timeoutSeconds"`
}

// SyntheticRunResult reports an authoritative synthetic journey outcome.
type SyntheticRunResult struct {
	TenantID   string    `json:"tenantId"`
	Outcome    string    `json:"outcome"`
	ObservedAt time.Time `json:"observedAt"`
}

// SyntheticRunner consumes ephemeral local authority for the public Core journey.
type SyntheticRunner interface {
	Run(context.Context, SyntheticRunRequest, string) (string, error)
}

// RenewRequest pins both predecessor keys and the immutable renewal command.
type RenewRequest struct {
	CommandID         string `json:"commandId"`
	DeliveryPublicKey string `json:"deliveryPublicKey"`
	TenantID          string `json:"tenantId"`
	ConsoleKeyID      string `json:"consoleKeyId"`
	ReviewerKeyID     string `json:"reviewerKeyId"`
}

// Service owns local tenant provisioning and purpose-limited credential lifecycle.
type Service struct {
	tracer observability.Tracer

	tenants  tenantProvisioner
	keys     credentialIssuer
	sealers  SealerFactory
	clock    clock.Clock
	lifetime time.Duration
	runner   SyntheticRunner
}

// New constructs provisioning with the configured positive lifetime capped at 24 hours.
func New(tenants tenantProvisioner, keys credentialIssuer, sealers SealerFactory, source clock.Clock, lifetime time.Duration) (*Service, error) {
	if tenants == nil || keys == nil || sealers == nil || source == nil || lifetime <= 0 || lifetime > 24*time.Hour {
		return nil, ErrInvalid
	}
	return &Service{tenants: tenants, keys: keys, sealers: sealers, clock: source, lifetime: lifetime}, nil
}

// WithSyntheticRunner installs the owned synthetic execution boundary.
func (service *Service) WithSyntheticRunner(runner SyntheticRunner) (*Service, error) {
	if service == nil || runner == nil {
		return nil, ErrInvalid
	}
	service.runner = runner
	return service, nil
}

// ProvisionSynthetic idempotently provisions an isolated readiness tenant.
func (service *Service) ProvisionSynthetic(ctx context.Context, request SyntheticProvisionRequest) (spanResult0 SyntheticProvisionResult, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "managedtenant.Service.ProvisionSynthetic")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || !validSyntheticIdentifier(request.DeploymentID) {
		return SyntheticProvisionResult{}, ErrInvalid
	}
	commandID := "synthetic-tenant:" + request.DeploymentID
	provisioned, _, err := service.tenants.Provision(ctx,
		tenant.AdminAction{Actor: "idenqa-cloud", Reason: "managed deployment synthetic readiness"},
		tenant.ProvisionCommand{ID: commandID, RequestDigest: sha256.Sum256([]byte(SyntheticContractVersion + "\x00" + request.DeploymentID))},
	)
	if err != nil {
		return SyntheticProvisionResult{}, err
	}
	return SyntheticProvisionResult{TenantID: provisioned.ID().String()}, nil
}

// RunSynthetic executes a fixed readiness journey with ephemeral tenant authority.
func (service *Service) RunSynthetic(ctx context.Context, request SyntheticRunRequest) (spanResult0 SyntheticRunResult, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "managedtenant.Service.RunSynthetic")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || service.runner == nil || !validSyntheticIdentifier(request.DeploymentID) ||
		len(request.RunID) < 8 || len(request.RunID) > 128 || strings.TrimSpace(request.RunID) != request.RunID ||
		request.FixtureVersion != "builtin.synthetic.selfie.v1" || request.ExpectedOutcome != "verified" ||
		len(request.ProfileDocument) < 2 || len(request.ProfileDocument) > 262144 || len(request.PolicyDocument) < 2 || len(request.PolicyDocument) > 262144 ||
		request.TimeoutSeconds < 1 || request.TimeoutSeconds > 900 {
		return SyntheticRunResult{}, ErrInvalid
	}
	provisioned, err := service.ProvisionSynthetic(ctx, SyntheticProvisionRequest{DeploymentID: request.DeploymentID})
	if err != nil {
		return SyntheticRunResult{}, err
	}
	tenantID, err := id.ParseTenant(provisioned.TenantID)
	if err != nil {
		return SyntheticRunResult{}, err
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return SyntheticRunResult{}, err
	}
	patterns := make([]access.Pattern, 0, len(syntheticPatterns))
	for _, raw := range syntheticPatterns {
		pattern, parseErr := access.ParsePattern(raw)
		if parseErr != nil {
			return SyntheticRunResult{}, parseErr
		}
		patterns = append(patterns, pattern)
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return SyntheticRunResult{}, err
	}
	commandID := "synthetic-run:" + request.RunID + ":" + hex.EncodeToString(random)
	sealer := &memorySealer{}
	credentialLifetime := time.Duration(request.TimeoutSeconds)*time.Second + 5*time.Minute
	if credentialLifetime > service.lifetime {
		credentialLifetime = service.lifetime
	}
	issued, err := service.keys.Issue(ctx,
		access.BridgeCommand{ID: commandID, RequestDigest: sha256.Sum256([]byte(SyntheticContractVersion + "\x00" + commandID))},
		access.AdminAction{Actor: "idenqa-core", Reason: "bounded synthetic readiness journey"}, scope,
		access.IssueInput{Label: "Idenqa synthetic readiness", Patterns: patterns, Expiry: access.ExpiringAt(service.clock.Now().UTC().Add(credentialLifetime))}, sealer,
	)
	if err != nil || !issued.Created || sealer.credential == "" {
		return SyntheticRunResult{}, errors.New("issue ephemeral synthetic credential")
	}
	outcome, err := service.runner.Run(ctx, request, sealer.credential)
	if err != nil {
		return SyntheticRunResult{}, err
	}
	return SyntheticRunResult{TenantID: provisioned.TenantID, Outcome: outcome, ObservedAt: service.clock.Now().UTC()}, nil
}

type memorySealer struct{ credential string }

func (sealer *memorySealer) Seal(credential string) (access.CredentialEnvelope, error) {
	if sealer == nil || credential == "" {
		return access.CredentialEnvelope{}, ErrInvalid
	}
	key := make([]byte, 32)
	defer clear(key)
	if _, err := rand.Read(key); err != nil {
		return access.CredentialEnvelope{}, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return access.CredentialEnvelope{}, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return access.CredentialEnvelope{}, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return access.CredentialEnvelope{}, err
	}
	ciphertext := aead.Seal(nil, nonce, []byte(credential), []byte(SyntheticContractVersion))
	sealer.credential = credential
	return access.CredentialEnvelope{
		Algorithm:  "AES-256-GCM/core-local-ephemeral-v1",
		Nonce:      base64.RawURLEncoding.EncodeToString(nonce),
		Ciphertext: base64.RawURLEncoding.EncodeToString(ciphertext),
	}, nil
}

func validSyntheticIdentifier(value string) bool {
	if len(value) < 8 || len(value) > 96 || strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

// Provision creates a tenant and separate scoped Console/reviewer credentials.
func (service *Service) Provision(ctx context.Context, request Request) (spanResult0 Result, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "managedtenant.Service.Provision")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || len(request.CommandID) < 16 || len(request.CommandID) > 180 || strings.TrimSpace(request.CommandID) != request.CommandID || request.DeliveryPublicKey == "" {
		return Result{}, ErrInvalid
	}
	meaning := struct {
		Version           string   `json:"version"`
		CommandID         string   `json:"commandId"`
		DeliveryPublicKey string   `json:"deliveryPublicKey"`
		ConsolePatterns   []string `json:"consolePatterns"`
		ReviewerPatterns  []string `json:"reviewerPatterns"`
	}{ContractVersion, request.CommandID, request.DeliveryPublicKey, consolePatterns, reviewerPatterns}
	encoded, err := json.Marshal(meaning)
	if err != nil {
		return Result{}, fmt.Errorf("encode managed tenant command: %w", err)
	}
	provisioned, created, err := service.tenants.Provision(ctx,
		tenant.AdminAction{Actor: "idenqa-cloud", Reason: "managed shared onboarding"},
		tenant.ProvisionCommand{ID: request.CommandID, RequestDigest: sha256.Sum256(encoded)},
	)
	if err != nil {
		return Result{}, err
	}
	scope, err := tenant.NewScope(provisioned.ID())
	if err != nil {
		return Result{}, err
	}
	expiresAt := service.clock.Now().UTC().Add(service.lifetime)
	console, err := service.issue(ctx, scope, request, "console", meaning.ConsolePatterns, expiresAt)
	if err != nil {
		return Result{}, fmt.Errorf("issue managed tenant Console authority: %w", err)
	}
	reviewer, err := service.issue(ctx, scope, request, "reviewer", meaning.ReviewerPatterns, expiresAt)
	if err != nil {
		return Result{}, fmt.Errorf("issue managed tenant reviewer authority: %w", err)
	}
	return Result{TenantID: provisioned.ID().String(), Created: created, Console: console, Reviewer: reviewer}, nil
}

// Renew rotates both predecessor-bound authority classes without extending their expiry.
func (service *Service) Renew(ctx context.Context, request RenewRequest) (spanResult0 Result, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "managedtenant.Service.Renew")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || len(request.CommandID) < 16 || len(request.CommandID) > 180 || strings.TrimSpace(request.CommandID) != request.CommandID || request.DeliveryPublicKey == "" {
		return Result{}, ErrInvalid
	}
	tenantID, err := id.ParseTenant(request.TenantID)
	if err != nil {
		return Result{}, ErrInvalid
	}
	consoleKeyID, err := id.ParseAPIKey(request.ConsoleKeyID)
	if err != nil {
		return Result{}, ErrInvalid
	}
	reviewerKeyID, err := id.ParseAPIKey(request.ReviewerKeyID)
	if err != nil || consoleKeyID == reviewerKeyID {
		return Result{}, ErrInvalid
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return Result{}, ErrInvalid
	}
	expiresAt := service.clock.Now().UTC().Add(service.lifetime)
	console, err := service.rotate(ctx, scope, request, "console", consoleKeyID, consolePatterns, expiresAt)
	if err != nil {
		return Result{}, fmt.Errorf("renew managed tenant Console authority: %w", err)
	}
	reviewer, err := service.rotate(ctx, scope, request, "reviewer", reviewerKeyID, reviewerPatterns, expiresAt)
	if err != nil {
		return Result{}, fmt.Errorf("renew managed tenant reviewer authority: %w", err)
	}
	return Result{TenantID: tenantID.String(), Console: console, Reviewer: reviewer}, nil
}

func (service *Service) issue(ctx context.Context, scope tenant.Scope, request Request, purpose string, rawPatterns []string, expiresAt time.Time) (Credential, error) {
	patterns := make([]access.Pattern, 0, len(rawPatterns))
	for _, raw := range rawPatterns {
		pattern, err := access.ParsePattern(raw)
		if err != nil {
			return Credential{}, err
		}
		patterns = append(patterns, pattern)
	}
	commandID := request.CommandID + ":" + purpose
	digest := sha256.Sum256([]byte(commandID + "\x00" + strings.Join(rawPatterns, "\x00") + "\x00" + request.DeliveryPublicKey))
	sealer, err := service.sealers(request.DeliveryPublicKey, commandID)
	if err != nil {
		return Credential{}, err
	}
	result, err := service.keys.Issue(ctx,
		access.BridgeCommand{ID: commandID, RequestDigest: digest},
		access.AdminAction{Actor: "idenqa-cloud", Reason: "managed shared onboarding"}, scope,
		access.IssueInput{Label: "Idenqa Cloud " + purpose, Patterns: patterns, Expiry: access.ExpiringAt(expiresAt)},
		sealer,
	)
	if err != nil {
		return Credential{}, err
	}
	return credential(result)
}

func (service *Service) rotate(ctx context.Context, scope tenant.Scope, request RenewRequest, purpose string, keyID id.APIKey, rawPatterns []string, expiresAt time.Time) (Credential, error) {
	patterns := make([]access.Pattern, 0, len(rawPatterns))
	for _, raw := range rawPatterns {
		pattern, err := access.ParsePattern(raw)
		if err != nil {
			return Credential{}, err
		}
		patterns = append(patterns, pattern)
	}
	commandID := request.CommandID + ":" + purpose
	meaning := struct {
		Version           string   `json:"version"`
		CommandID         string   `json:"commandId"`
		DeliveryPublicKey string   `json:"deliveryPublicKey"`
		TenantID          string   `json:"tenantId"`
		PredecessorKeyID  string   `json:"predecessorKeyId"`
		Patterns          []string `json:"patterns"`
	}{RenewContractVersion, commandID, request.DeliveryPublicKey, scope.ID().String(), keyID.String(), rawPatterns}
	encoded, err := json.Marshal(meaning)
	if err != nil {
		return Credential{}, err
	}
	sealer, err := service.sealers(request.DeliveryPublicKey, commandID)
	if err != nil {
		return Credential{}, err
	}
	command := access.BridgeCommand{ID: commandID, RequestDigest: sha256.Sum256(encoded)}
	predecessor, err := service.keys.Find(ctx, scope, keyID)
	if err != nil {
		return Credential{}, err
	}
	var result access.BridgeIssueResult
	switch predecessor.StateAt(service.clock.Now().UTC()) {
	case access.KeyStateActive:
		result, err = service.keys.Rotate(ctx, command,
			access.AdminAction{Actor: "idenqa-cloud", Reason: "managed shared tenant authority renewal"}, scope,
			access.RotateInput{KeyID: keyID, Expiry: access.ExpiringAt(expiresAt), Overlap: min(rotationOverlap, service.lifetime/4), Patterns: patterns}, sealer,
		)
	case access.KeyStateExpired:
		// Expiry is not a compromise signal. The fixed local bridge may recover
		// unattended managed authority by issuing the same bounded grant under
		// the immutable renewal command. Revoked and retired keys remain terminal.
		result, err = service.keys.Issue(ctx, command,
			access.AdminAction{Actor: "idenqa-cloud", Reason: "recover expired managed shared tenant authority"}, scope,
			access.IssueInput{Label: "Idenqa Cloud " + purpose, Patterns: patterns, Expiry: access.ExpiringAt(expiresAt)}, sealer,
		)
	default:
		return Credential{}, access.ErrKeyTerminal
	}
	if err != nil {
		return Credential{}, err
	}
	return credential(result)
}

func credential(result access.BridgeIssueResult) (Credential, error) {
	expiresAt := result.Key.ExpiresAt()
	if result.Key.ID().IsZero() || expiresAt == nil || !expiresAt.After(result.Key.CreatedAt()) || result.Envelope.Algorithm == "" || result.Envelope.Ciphertext == "" {
		return Credential{}, errors.New("managed tenant credential result is incomplete")
	}
	// PostgreSQL persists timestamps at microsecond precision. Match that
	// precision on the first reply so regional immutable secret replay has the
	// same metadata after a database-backed command observation.
	return Credential{KeyID: result.Key.ID().String(), IssuedAt: result.Key.CreatedAt().UTC().Truncate(time.Microsecond), ExpiresAt: expiresAt.UTC().Truncate(time.Microsecond), Envelope: result.Envelope}, nil
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (service *Service) WithTracer(tracer observability.Tracer) *Service {
	if service != nil {
		service.tracer = tracer
	}
	return service
}

func (service *Service) operationTracer() observability.Tracer {
	if service == nil {
		return nil
	}
	return service.tracer
}
