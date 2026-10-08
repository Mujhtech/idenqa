package reviewbrowser

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"

	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Authorizer confirms the delegated actor can access the requested evidence.
type Authorizer interface {
	ListDelegated(context.Context, tenant.Scope, review.Actor, id.ReviewCase, int64) ([]review.EvidenceMetadata, error)
}

// Store consumes bootstraps and authenticates persisted browser sessions.
type Store interface {
	Consume(context.Context, Session, [32]byte, [32]byte, [32]byte) error
	Authenticate(context.Context, tenant.Scope, [32]byte, string, time.Time) (Session, error)
}

// Session is the verified, tenant-scoped identity bound to browser requests.
type Session struct {
	BootstrapID string
	Scope       tenant.Scope
	Actor       review.Actor
	CaseID      id.ReviewCase
	Version     int64
	Region      string
	Origin      string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

// Redemption contains the session token returned after a valid bootstrap.
type Redemption struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// Service redeems signed bootstraps and authenticates browser evidence sessions.
type Service struct {
	tracer observability.Tracer

	verifier   *Verifier
	store      Store
	authorizer Authorizer
	entropy    io.Reader
	now        func() time.Time
	sessionTTL time.Duration
}

// New constructs a review-browser service with explicit time and entropy sources.
func New(verifier *Verifier, store Store, authorizer Authorizer, entropy io.Reader, now func() time.Time, sessionTTL time.Duration) (*Service, error) {
	if verifier == nil || store == nil || authorizer == nil || entropy == nil || now == nil || sessionTTL <= 0 || sessionTTL > 15*time.Minute {
		return nil, ErrInvalid
	}
	return &Service{verifier: verifier, store: store, authorizer: authorizer, entropy: entropy, now: now, sessionTTL: sessionTTL}, nil
}

// NewSystem constructs a review-browser service using system time and entropy.
func NewSystem(verifier *Verifier, store Store, authorizer Authorizer, sessionTTL time.Duration) (*Service, error) {
	return New(verifier, store, authorizer, rand.Reader, time.Now, sessionTTL)
}

// Redeem verifies and consumes a bootstrap, returning a new browser session token.
func (service *Service) Redeem(ctx context.Context, envelope Envelope, origin string) (spanResult0 Redemption, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "reviewbrowser.Service.Redeem")
	defer observability.EndSpan(completeSpan, &spanErr)

	claims, err := service.verifier.Verify(envelope)
	if err != nil || origin == "" || origin != claims.Origin {
		return Redemption{}, ErrForbidden
	}
	scope, caseID, err := parseScopeAndCase(claims)
	if err != nil {
		return Redemption{}, err
	}
	actor := review.Actor{ID: claims.ActorID}
	now := service.now().UTC().Truncate(time.Microsecond)
	session := Session{
		BootstrapID: claims.BootstrapID, Scope: scope, Actor: actor, CaseID: caseID,
		Version: claims.ExpectedVersion, Region: claims.Scope.Region, Origin: origin,
		CreatedAt: now, ExpiresAt: now.Add(service.sessionTTL),
	}
	if _, err := service.authorizer.ListDelegated(WithSession(ctx, session), scope, actor, caseID, claims.ExpectedVersion); err != nil {
		return Redemption{}, err
	}
	secret := make([]byte, 32)
	if _, err := io.ReadFull(service.entropy, secret); err != nil {
		return Redemption{}, fmt.Errorf("generate review browser session: %w", err)
	}
	defer clear(secret)
	token := "rvs1." + claims.Scope.TenantID + "." + base64.RawURLEncoding.EncodeToString(secret)
	tokenHash := sha256.Sum256([]byte(token))
	encodedClaims, err := json.Marshal(claims)
	if err != nil {
		return Redemption{}, ErrInvalid
	}
	claimsHash := sha256.Sum256(encodedClaims)
	nonceHash := sha256.Sum256([]byte(claims.Nonce))
	if err := service.store.Consume(ctx, session, tokenHash, nonceHash, claimsHash); err != nil {
		return Redemption{}, err
	}
	return Redemption{Token: token, ExpiresAt: session.ExpiresAt}, nil
}

type sessionContextKey struct{}

// WithSession attaches a verified browser session identity to one request.
// It carries identity and exact scope, never a service dependency or secret.
func WithSession(ctx context.Context, session Session) context.Context {
	return context.WithValue(ctx, sessionContextKey{}, session)
}

// SessionFromContext returns the verified browser identity for reauthorisation.
func SessionFromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionContextKey{}).(Session)
	return session, ok
}

// Authenticate resolves a browser token to its live tenant-scoped session.
func (service *Service) Authenticate(ctx context.Context, token, origin string) (spanResult0 Session, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "reviewbrowser.Service.Authenticate")
	defer observability.EndSpan(completeSpan, &spanErr)

	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != "rvs1" || !validOrigin(origin) {
		return Session{}, ErrForbidden
	}
	tenantID, err := id.ParseTenant(parts[1])
	if err != nil {
		return Session{}, ErrForbidden
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != 32 {
		return Session{}, ErrForbidden
	}
	clear(secret)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return Session{}, ErrForbidden
	}
	hash := sha256.Sum256([]byte(token))
	return service.store.Authenticate(ctx, scope, hash, origin, service.now().UTC())
}

func parseScopeAndCase(claims Claims) (tenant.Scope, id.ReviewCase, error) {
	tenantID, err := id.ParseTenant(claims.Scope.TenantID)
	if err != nil {
		return tenant.Scope{}, id.ReviewCase{}, ErrInvalid
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return tenant.Scope{}, id.ReviewCase{}, ErrInvalid
	}
	caseID, err := id.ParseReviewCase(claims.ReviewCaseID)
	if err != nil {
		return tenant.Scope{}, id.ReviewCase{}, ErrInvalid
	}
	return scope, caseID, nil
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
