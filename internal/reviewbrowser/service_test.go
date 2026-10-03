package reviewbrowser_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/reviewbrowser"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	tenantValue = "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
	caseValue   = "rvc_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
	originValue = "https://console.example"
)

func TestServiceRedeemAndAuthenticate(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := reviewbrowser.NewVerifier(map[string]string{"cloud-1": base64.StdEncoding.EncodeToString(publicKey)}, binding(), time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryStore{}
	authorizer := &authorizer{}
	service, err := reviewbrowser.New(verifier, store, authorizer, zeroReader{}, func() time.Time { return now }, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	envelope := signedEnvelope(t, privateKey, claims(now))

	redemption, err := service.Redeem(t.Context(), envelope, originValue)
	if err != nil {
		t.Fatalf("Redeem() error = %v", err)
	}
	if authorizer.calls != 1 || !authorizer.sawSession || redemption.Token == "" || redemption.ExpiresAt != now.Add(5*time.Minute) {
		t.Fatalf("Redeem() = %#v, authorizer calls = %d", redemption, authorizer.calls)
	}
	session, err := service.Authenticate(t.Context(), redemption.Token, originValue)
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if session.Actor.ID != "usr_reviewer" || session.CaseID.String() != caseValue || session.Version != 7 {
		t.Fatalf("Authenticate() = %#v", session)
	}
	if _, err := service.Authenticate(t.Context(), redemption.Token, "https://other.example"); !errors.Is(err, reviewbrowser.ErrForbidden) {
		t.Fatalf("Authenticate(wrong origin) error = %v", err)
	}
}

func TestServiceRejectsBindingAndReplay(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, _ := ed25519.GenerateKey(nil)
	verifier, _ := reviewbrowser.NewVerifier(map[string]string{"cloud-1": base64.StdEncoding.EncodeToString(publicKey)}, binding(), time.Minute, func() time.Time { return now })
	store := &memoryStore{consumeErr: reviewbrowser.ErrReplay}
	service, _ := reviewbrowser.New(verifier, store, &authorizer{}, zeroReader{}, func() time.Time { return now }, time.Minute)
	envelope := signedEnvelope(t, privateKey, claims(now))

	if _, err := service.Redeem(t.Context(), envelope, "https://other.example"); !errors.Is(err, reviewbrowser.ErrForbidden) {
		t.Fatalf("Redeem(wrong origin) error = %v", err)
	}
	if _, err := service.Redeem(t.Context(), envelope, originValue); !errors.Is(err, reviewbrowser.ErrReplay) {
		t.Fatalf("Redeem(replay) error = %v", err)
	}

	tampered := envelope
	tampered.Claims.ExpectedVersion++
	if _, err := service.Redeem(t.Context(), tampered, originValue); !errors.Is(err, reviewbrowser.ErrForbidden) {
		t.Fatalf("Redeem(tampered) error = %v", err)
	}
}

func binding() reviewbrowser.Binding {
	return reviewbrowser.Binding{OrganisationID: "org_1", EnvironmentID: "env_1", DeploymentID: "dep_1", Region: "eu-west-1"}
}

func claims(now time.Time) reviewbrowser.Claims {
	return reviewbrowser.Claims{
		Version: reviewbrowser.Version, Audience: reviewbrowser.Audience, BootstrapID: "bootstrap_1",
		Scope:   reviewbrowser.Scope{OrganisationID: "org_1", TenantID: tenantValue, EnvironmentID: "env_1", DeploymentID: "dep_1", Region: "eu-west-1"},
		ActorID: "usr_reviewer", ReviewCaseID: caseValue, ExpectedVersion: 7, Purpose: reviewbrowser.Purpose,
		Origin: originValue, Nonce: "nonce_with_sufficient_entropy", IssuedAt: now.Add(-time.Second), ExpiresAt: now.Add(30 * time.Second),
	}
}

func signedEnvelope(t *testing.T, privateKey ed25519.PrivateKey, claims reviewbrowser.Claims) reviewbrowser.Envelope {
	t.Helper()
	encoded, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return reviewbrowser.Envelope{KeyID: "cloud-1", Algorithm: "Ed25519", Claims: claims, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, digest[:]))}
}

type zeroReader struct{}

func (zeroReader) Read(value []byte) (int, error) {
	clear(value)
	return len(value), nil
}

type authorizer struct {
	calls      int
	sawSession bool
}

func (a *authorizer) ListDelegated(ctx context.Context, _ tenant.Scope, _ review.Actor, _ id.ReviewCase, _ int64) ([]review.EvidenceMetadata, error) {
	a.calls++
	_, a.sawSession = reviewbrowser.SessionFromContext(ctx)
	return nil, nil
}

type memoryStore struct {
	session    reviewbrowser.Session
	tokenHash  [32]byte
	consumeErr error
}

func (store *memoryStore) Consume(_ context.Context, session reviewbrowser.Session, tokenHash, _, _ [32]byte) error {
	if store.consumeErr != nil {
		return store.consumeErr
	}
	store.session, store.tokenHash = session, tokenHash
	return nil
}

func (store *memoryStore) Authenticate(_ context.Context, scope tenant.Scope, tokenHash [32]byte, origin string, now time.Time) (reviewbrowser.Session, error) {
	if scope.ID() != store.session.Scope.ID() || tokenHash != store.tokenHash || origin != store.session.Origin || !store.session.ExpiresAt.After(now) {
		return reviewbrowser.Session{}, reviewbrowser.ErrForbidden
	}
	return store.session, nil
}
