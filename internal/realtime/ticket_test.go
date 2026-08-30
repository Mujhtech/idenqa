package realtime_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestTicketGeneratorRoundTripAndRedaction(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	generator, err := realtime.NewTicketGenerator(bytes.NewReader(bytes.Repeat([]byte{9}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	presented, err := generator.Generate(fixture.tenantID, fixture.ticketID)
	if err != nil {
		t.Fatal(err)
	}
	encoded := presented.Reveal()
	parsed, err := realtime.ParsePresentedTicket(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.TenantHint() != fixture.tenantID || parsed.ID() != fixture.ticketID || parsed.Reveal() != encoded {
		t.Fatalf("ParsePresentedTicket() = %#v", parsed)
	}
	marshaled, err := presented.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	if presented.String() != "[REDACTED]" || fmt.Sprintf("%#v", presented) != "[REDACTED]" ||
		string(marshaled) != "[REDACTED]" || strings.Contains(fmt.Sprint(presented), encoded) {
		t.Fatal("presented ticket formatting exposed credential material")
	}
	digest, err := realtime.DigestTicket(presented)
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := realtime.ParseTicketDigest(digest.Bytes())
	if err != nil || reparsed.Bytes() == nil || reparsed.String() != "[REDACTED]" {
		t.Fatalf("ParseTicketDigest() = %v, %v", reparsed, err)
	}
}

func TestParsePresentedTicketRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	generator, err := realtime.NewTicketGenerator(bytes.NewReader(bytes.Repeat([]byte{5}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	presented, err := generator.Generate(fixture.tenantID, fixture.ticketID)
	if err != nil {
		t.Fatal(err)
	}
	encoded := presented.Reveal()
	tests := []string{"", "idq_wst_v2_" + encoded[len("idq_wst_v1_"):], encoded[:len(encoded)-1], encoded + "x"}
	for _, value := range tests {
		if _, err := realtime.ParsePresentedTicket(value); !errors.Is(err, realtime.ErrInvalidTicket) {
			t.Fatalf("ParsePresentedTicket(%q) error = %v", value, err)
		}
	}
}

func TestClientBindingValidation(t *testing.T) {
	t.Parallel()

	browser, err := realtime.NewBrowserBinding("https://capture.example.com:8443")
	if err != nil || browser.Kind() != realtime.ClientKindBrowser {
		t.Fatalf("NewBrowserBinding() = %#v, %v", browser, err)
	}
	native, err := realtime.NewNativeBinding("TEAM123.com.example.capture")
	if err != nil || native.Kind() != realtime.ClientKindNative {
		t.Fatalf("NewNativeBinding() = %#v, %v", native, err)
	}
	for _, invalid := range []string{"https://user@example.com", "https://example.com/", "https://example.com/path", "wss://example.com"} {
		if _, err := realtime.NewBrowserBinding(invalid); err == nil {
			t.Fatalf("NewBrowserBinding(%q) error = nil", invalid)
		}
	}
	for _, invalid := range []string{"", " com.example", "com/example", "com example"} {
		if _, err := realtime.NewNativeBinding(invalid); err == nil {
			t.Fatalf("NewNativeBinding(%q) error = nil", invalid)
		}
	}
}

func TestNewTicketValidatesLifetimeAndRedemption(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	ticket := fixture.newTicket(t, fixture.now, fixture.now.Add(30*time.Second))
	if ticket.RedeemedAt() != nil || !ticket.ConnectionID().IsZero() {
		t.Fatal("new ticket is already redeemed")
	}
	if _, err := realtime.NewTicket(
		fixture.ticketID, fixture.tenantID, fixture.verificationID, fixture.captureTokenID,
		fixture.digest, fixture.binding, "lagos-1", realtime.SubprotocolV1,
		fixture.now, fixture.now.Add(9*time.Second),
	); err == nil {
		t.Fatal("NewTicket(short lifetime) error = nil")
	}
	connectionID, err := fixture.identifiers.NewConnection()
	if err != nil {
		t.Fatal(err)
	}
	tooLate := ticket.ExpiresAt()
	if _, err := realtime.RestoreTicket(
		ticket.ID(), ticket.TenantID(), ticket.VerificationID(), ticket.CaptureTokenID(), ticket.Digest(),
		ticket.Binding(), ticket.Region(), ticket.Protocol(), ticket.IssuedAt(), ticket.ExpiresAt(),
		&tooLate, connectionID,
	); err == nil {
		t.Fatal("RestoreTicket(expiry redemption) error = nil")
	}
}

type ticketRepository struct {
	created    realtime.Ticket
	redemption realtime.Redemption
	createErr  error
	redeemErr  error
}

func (repository *ticketRepository) Create(
	_ context.Context,
	_ tenant.Scope,
	ticket realtime.Ticket,
) error {
	repository.created = ticket

	return repository.createErr
}

func (repository *ticketRepository) Redeem(
	_ context.Context,
	redemption realtime.Redemption,
) (realtime.Ticket, error) {
	repository.redemption = redemption
	if repository.redeemErr != nil {
		return realtime.Ticket{}, repository.redeemErr
	}
	redeemedAt := redemption.RedeemedAt

	return realtime.RestoreTicket(
		repository.created.ID(), repository.created.TenantID(), repository.created.VerificationID(),
		repository.created.CaptureTokenID(), repository.created.Digest(), repository.created.Binding(),
		repository.created.Region(), repository.created.Protocol(), repository.created.IssuedAt(),
		repository.created.ExpiresAt(), &redeemedAt, redemption.ConnectionID,
	)
}

func TestServiceIssuesAndRedeemsBoundTicket(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	repository := &ticketRepository{}
	service, err := realtime.NewService(repository, fixture.identifiers, fixture.generator,
		fixedClock{now: fixture.now}, realtime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.Issue(t.Context(), realtime.IssueInput{
		Scope: fixture.scope, VerificationID: fixture.verificationID, CaptureTokenID: fixture.captureTokenID,
		Binding: fixture.binding, Region: "lagos-1", SessionExpiresAt: fixture.now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Credential.IsZero() || issued.Ticket.ExpiresAt() != fixture.now.Add(30*time.Second) ||
		repository.created.ID() != issued.Ticket.ID() {
		t.Fatalf("Issue() = %#v", issued)
	}
	redeemed, err := service.Redeem(t.Context(), issued.Credential.Reveal(), fixture.binding, "lagos-1", realtime.SubprotocolV1)
	if err != nil {
		t.Fatal(err)
	}
	if redeemed.ConnectionID().IsZero() || redeemed.RedeemedAt() == nil ||
		repository.redemption.Digest.IsZero() || repository.redemption.TicketID != issued.Ticket.ID() {
		t.Fatalf("Redeem() = %#v", redeemed)
	}
}

func TestServiceFailsClosed(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	repository := &ticketRepository{createErr: realtime.ErrTicketUnavailable}
	service, err := realtime.NewService(repository, fixture.identifiers, fixture.generator,
		fixedClock{now: fixture.now}, realtime.DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	input := realtime.IssueInput{
		Scope: fixture.scope, VerificationID: fixture.verificationID, CaptureTokenID: fixture.captureTokenID,
		Binding: fixture.binding, Region: "lagos-1", SessionExpiresAt: fixture.now.Add(time.Hour),
	}
	if _, err := service.Issue(t.Context(), input); !errors.Is(err, realtime.ErrTicketUnavailable) {
		t.Fatalf("Issue(unavailable) error = %v", err)
	}
	input.SessionExpiresAt = fixture.now.Add(20 * time.Second)
	if _, err := service.Issue(t.Context(), input); !errors.Is(err, realtime.ErrTicketUnavailable) {
		t.Fatalf("Issue(short session) error = %v", err)
	}
	if _, err := service.Redeem(t.Context(), "secret", fixture.binding, "lagos-1", realtime.SubprotocolV1); !errors.Is(err, realtime.ErrInvalidTicket) {
		t.Fatalf("Redeem(malformed) error = %v", err)
	}
}

type ticketFixture struct {
	now            time.Time
	identifiers    *id.Generator
	generator      *realtime.TicketGenerator
	tenantID       id.Tenant
	verificationID id.Verification
	captureTokenID id.CaptureToken
	ticketID       id.WebSocketTicket
	scope          tenant.Scope
	binding        realtime.ClientBinding
	digest         realtime.TicketDigest
}

func newTicketFixture(t *testing.T) ticketFixture {
	t.Helper()

	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	identifiers, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{3}, 1024)))
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	verificationID, err := identifiers.NewVerification()
	if err != nil {
		t.Fatal(err)
	}
	captureTokenID, err := identifiers.NewCaptureToken()
	if err != nil {
		t.Fatal(err)
	}
	ticketID, err := identifiers.NewWebSocketTicket()
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := realtime.NewBrowserBinding("https://capture.example.com")
	if err != nil {
		t.Fatal(err)
	}
	generator, err := realtime.NewTicketGenerator(bytes.NewReader(bytes.Repeat([]byte{7}, 1024)))
	if err != nil {
		t.Fatal(err)
	}
	presented, err := generator.Generate(tenantID, ticketID)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := realtime.DigestTicket(presented)
	if err != nil {
		t.Fatal(err)
	}

	return ticketFixture{
		now: now, identifiers: identifiers, generator: generator, tenantID: tenantID,
		verificationID: verificationID, captureTokenID: captureTokenID, ticketID: ticketID,
		scope: scope, binding: binding, digest: digest,
	}
}

func (fixture ticketFixture) newTicket(t *testing.T, issuedAt, expiresAt time.Time) realtime.Ticket {
	t.Helper()

	ticket, err := realtime.NewTicket(
		fixture.ticketID, fixture.tenantID, fixture.verificationID, fixture.captureTokenID,
		fixture.digest, fixture.binding, "lagos-1", realtime.SubprotocolV1, issuedAt, expiresAt,
	)
	if err != nil {
		t.Fatal(err)
	}

	return ticket
}
