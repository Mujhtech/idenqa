package realtime

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	presentedTicketPrefix = "idq_wst_v1_"
	ticketDigestDomain    = "idq-websocket-ticket\x00v1\x00"
	ticketSecretBytes     = 32
	ticketSecretText      = 43
	ulidTextLength        = 26
	presentedTicketLength = len(presentedTicketPrefix) + ulidTextLength + 1 +
		ulidTextLength + 1 + ticketSecretText
	redactedTicket = "[REDACTED]"
)

var (
	// ErrInvalidTicket deliberately covers malformed, unknown, mismatched,
	// expired, and already-redeemed connection tickets.
	ErrInvalidTicket = errors.New("realtime: invalid connection ticket")
	// ErrTicketUnavailable reports that current capture authority cannot issue
	// a connection ticket.
	ErrTicketUnavailable = errors.New("realtime: connection ticket unavailable")
)

// PresentedTicket is display-once WebSocket handshake credential material.
type PresentedTicket struct {
	tenant id.Tenant
	id     id.WebSocketTicket
	secret [ticketSecretBytes]byte
}

// ParsePresentedTicket strictly parses the v1 display-once credential.
func ParsePresentedTicket(encoded string) (PresentedTicket, error) {
	if len(encoded) != presentedTicketLength || !strings.HasPrefix(encoded, presentedTicketPrefix) {
		return PresentedTicket{}, ErrInvalidTicket
	}
	tenantEnd := len(presentedTicketPrefix) + ulidTextLength
	ticketStart := tenantEnd + 1
	ticketEnd := ticketStart + ulidTextLength
	secretStart := ticketEnd + 1
	if encoded[tenantEnd] != '_' || encoded[ticketEnd] != '_' {
		return PresentedTicket{}, ErrInvalidTicket
	}
	tenantID, err := id.ParseTenant("ten_" + encoded[len(presentedTicketPrefix):tenantEnd])
	if err != nil {
		return PresentedTicket{}, ErrInvalidTicket
	}
	ticketID, err := id.ParseWebSocketTicket("wst_" + encoded[ticketStart:ticketEnd])
	if err != nil {
		return PresentedTicket{}, ErrInvalidTicket
	}
	decoded, err := base64.RawURLEncoding.DecodeString(encoded[secretStart:])
	if err != nil || len(decoded) != ticketSecretBytes {
		return PresentedTicket{}, ErrInvalidTicket
	}
	var secret [ticketSecretBytes]byte
	copy(secret[:], decoded)

	return PresentedTicket{tenant: tenantID, id: ticketID, secret: secret}, nil
}

// TenantHint returns the untrusted tenant lookup hint embedded in the ticket.
func (ticket PresentedTicket) TenantHint() id.Tenant { return ticket.tenant }

// ID returns the non-secret ticket record identifier.
func (ticket PresentedTicket) ID() id.WebSocketTicket { return ticket.id }

// Reveal returns the complete credential at its authorised delivery boundary.
func (ticket PresentedTicket) Reveal() string {
	if ticket.IsZero() {
		return ""
	}

	return presentedTicketPrefix + ticket.tenant.String()[4:] + "_" + ticket.id.String()[4:] + "_" +
		base64.RawURLEncoding.EncodeToString(ticket.secret[:])
}

// IsZero reports whether no credential was generated.
func (ticket PresentedTicket) IsZero() bool { return ticket.tenant.IsZero() || ticket.id.IsZero() }

// String always redacts the credential.
func (PresentedTicket) String() string { return redactedTicket }

// GoString always redacts the credential in detailed formatting.
func (PresentedTicket) GoString() string { return redactedTicket }

// MarshalText prevents generic text encoders from exposing credential material.
func (PresentedTicket) MarshalText() ([]byte, error) { return []byte(redactedTicket), nil }

// TicketGenerator creates display-once ticket material from explicit entropy.
type TicketGenerator struct{ entropy io.Reader }

// NewTicketGenerator constructs a ticket generator.
func NewTicketGenerator(entropy io.Reader) (*TicketGenerator, error) {
	if entropy == nil {
		return nil, errors.New("realtime: ticket entropy is required")
	}

	return &TicketGenerator{entropy: entropy}, nil
}

// NewSystemTicketGenerator uses cryptographic operating-system entropy.
func NewSystemTicketGenerator() (*TicketGenerator, error) { return NewTicketGenerator(rand.Reader) }

// Generate creates a ticket for existing tenant and record identifiers.
func (generator *TicketGenerator) Generate(
	tenantID id.Tenant,
	ticketID id.WebSocketTicket,
) (PresentedTicket, error) {
	if generator == nil || generator.entropy == nil || tenantID.IsZero() || ticketID.IsZero() {
		return PresentedTicket{}, errors.New("realtime: ticket generator is not initialised")
	}
	presented := PresentedTicket{tenant: tenantID, id: ticketID}
	if _, err := io.ReadFull(generator.entropy, presented.secret[:]); err != nil {
		return PresentedTicket{}, fmt.Errorf("realtime: generate ticket secret: %w", err)
	}

	return presented, nil
}

// TicketDigest is redacting persistence-only verification material.
type TicketDigest struct{ value [sha256.Size]byte }

// ParseTicketDigest validates a stored v1 digest.
func ParseTicketDigest(value []byte) (TicketDigest, error) {
	if len(value) != sha256.Size {
		return TicketDigest{}, errors.New("realtime: ticket digest must contain 32 bytes")
	}
	var digest TicketDigest
	copy(digest.value[:], value)

	return digest, nil
}

// DigestTicket computes domain-separated persistence material.
func DigestTicket(ticket PresentedTicket) (TicketDigest, error) {
	if ticket.IsZero() {
		return TicketDigest{}, errors.New("realtime: presented ticket is required")
	}
	hash := sha256.New()
	if _, err := io.WriteString(hash, ticketDigestDomain); err != nil {
		return TicketDigest{}, fmt.Errorf("realtime: hash ticket domain: %w", err)
	}
	if _, err := io.WriteString(hash, ticket.tenant.String()); err != nil {
		return TicketDigest{}, fmt.Errorf("realtime: hash ticket tenant: %w", err)
	}
	if _, err := io.WriteString(hash, "\x00"+ticket.id.String()+"\x00"); err != nil {
		return TicketDigest{}, fmt.Errorf("realtime: hash ticket identity: %w", err)
	}
	if _, err := hash.Write(ticket.secret[:]); err != nil {
		return TicketDigest{}, fmt.Errorf("realtime: hash ticket secret: %w", err)
	}
	var digest TicketDigest
	copy(digest.value[:], hash.Sum(nil))

	return digest, nil
}

// Bytes returns a defensive persistence copy.
func (digest TicketDigest) Bytes() []byte { return append([]byte(nil), digest.value[:]...) }

// IsZero reports whether no digest was computed.
func (digest TicketDigest) IsZero() bool { return digest == TicketDigest{} }

// String always redacts verification material.
func (TicketDigest) String() string { return redactedTicket }

// GoString always redacts verification material in detailed formatting.
func (TicketDigest) GoString() string { return redactedTicket }

// ClientKind identifies the verified client-identity binding surface.
type ClientKind string

const (
	// ClientKindBrowser binds a ticket to one exact browser origin.
	ClientKindBrowser ClientKind = "browser"
	// ClientKindNative binds a ticket to one verified native application identity.
	ClientKindNative ClientKind = "native"
)

// ClientBinding is an immutable exact browser or native client identity.
type ClientBinding struct {
	kind     ClientKind
	identity string
}

// NewBrowserBinding validates an exact HTTP or HTTPS origin.
func NewBrowserBinding(origin string) (ClientBinding, error) {
	parsed, err := url.Parse(origin)
	if err != nil || len(origin) > 2048 || (parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.String() != origin {
		return ClientBinding{}, errors.New("realtime: browser binding must be an exact HTTP or HTTPS origin")
	}

	return ClientBinding{kind: ClientKindBrowser, identity: origin}, nil
}

// NewNativeBinding validates a bounded application identity. Its caller must
// obtain the value from an authenticated platform boundary, not user input.
func NewNativeBinding(identity string) (ClientBinding, error) {
	if len(identity) == 0 || len(identity) > 255 || strings.TrimSpace(identity) != identity {
		return ClientBinding{}, errors.New("realtime: native application identity is invalid")
	}
	for _, character := range identity {
		if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') &&
			(character < '0' || character > '9') && character != '.' && character != '_' && character != '-' {
			return ClientBinding{}, errors.New("realtime: native application identity is invalid")
		}
	}

	return ClientBinding{kind: ClientKindNative, identity: identity}, nil
}

// RestoreClientBinding validates a durable binding record.
func RestoreClientBinding(kind ClientKind, identity string) (ClientBinding, error) {
	switch kind {
	case ClientKindBrowser:
		return NewBrowserBinding(identity)
	case ClientKindNative:
		return NewNativeBinding(identity)
	default:
		return ClientBinding{}, errors.New("realtime: client binding kind is invalid")
	}
}

// Kind returns the client surface.
func (binding ClientBinding) Kind() ClientKind { return binding.kind }

// Identity returns the exact verified identity value.
func (binding ClientBinding) Identity() string { return binding.identity }

// IsZero reports whether no binding was constructed.
func (binding ClientBinding) IsZero() bool { return binding.kind == "" || binding.identity == "" }

// Ticket is an immutable durable connection-ticket record except for its
// one-way redemption transition.
type Ticket struct {
	id             id.WebSocketTicket
	tenantID       id.Tenant
	verificationID id.Verification
	captureTokenID id.CaptureToken
	digest         TicketDigest
	binding        ClientBinding
	region         string
	protocol       string
	issuedAt       time.Time
	expiresAt      time.Time
	redeemedAt     *time.Time
	connectionID   id.Connection
}

// NewTicket constructs an unredeemed durable ticket.
func NewTicket(
	identifier id.WebSocketTicket,
	tenantID id.Tenant,
	verificationID id.Verification,
	captureTokenID id.CaptureToken,
	digest TicketDigest,
	binding ClientBinding,
	region string,
	protocol string,
	issuedAt time.Time,
	expiresAt time.Time,
) (Ticket, error) {
	return RestoreTicket(identifier, tenantID, verificationID, captureTokenID, digest, binding, region,
		protocol, issuedAt, expiresAt, nil, id.Connection{})
}

// RestoreTicket validates durable ticket state.
func RestoreTicket(
	identifier id.WebSocketTicket,
	tenantID id.Tenant,
	verificationID id.Verification,
	captureTokenID id.CaptureToken,
	digest TicketDigest,
	binding ClientBinding,
	region string,
	protocol string,
	issuedAt time.Time,
	expiresAt time.Time,
	redeemedAt *time.Time,
	connectionID id.Connection,
) (Ticket, error) {
	issuedAt = issuedAt.UTC()
	expiresAt = expiresAt.UTC()
	if identifier.IsZero() || tenantID.IsZero() || verificationID.IsZero() || captureTokenID.IsZero() ||
		digest.IsZero() || binding.IsZero() || !validRegion(region) || protocol != SubprotocolV1 ||
		issuedAt.IsZero() || !expiresAt.After(issuedAt) || expiresAt.Sub(issuedAt) < MinimumTicketLifetime ||
		expiresAt.Sub(issuedAt) > MaximumTicketLifetime {
		return Ticket{}, errors.New("realtime: ticket identity, binding, or lifetime is invalid")
	}
	redemption := utcTimeCopy(redeemedAt)
	if (redemption == nil) != connectionID.IsZero() ||
		(redemption != nil && (redemption.Before(issuedAt) || !redemption.Before(expiresAt))) {
		return Ticket{}, errors.New("realtime: ticket redemption state is invalid")
	}

	return Ticket{
		id: identifier, tenantID: tenantID, verificationID: verificationID, captureTokenID: captureTokenID,
		digest: digest, binding: binding, region: region, protocol: protocol,
		issuedAt: issuedAt, expiresAt: expiresAt, redeemedAt: redemption, connectionID: connectionID,
	}, nil
}

// ID returns the non-secret ticket record identifier.
func (ticket Ticket) ID() id.WebSocketTicket { return ticket.id }

// TenantID returns the owning tenant.
func (ticket Ticket) TenantID() id.Tenant { return ticket.tenantID }

// VerificationID returns the bound verification session.
func (ticket Ticket) VerificationID() id.Verification { return ticket.verificationID }

// CaptureTokenID returns the bound capture credential record.
func (ticket Ticket) CaptureTokenID() id.CaptureToken { return ticket.captureTokenID }

// Digest returns the redacting persistence digest.
func (ticket Ticket) Digest() TicketDigest { return ticket.digest }

// Binding returns the exact client identity binding.
func (ticket Ticket) Binding() ClientBinding { return ticket.binding }

// Region returns the exact routing region.
func (ticket Ticket) Region() string { return ticket.region }

// Protocol returns the exact WebSocket subprotocol.
func (ticket Ticket) Protocol() string { return ticket.protocol }

// IssuedAt returns the UTC issuance time.
func (ticket Ticket) IssuedAt() time.Time { return ticket.issuedAt }

// ExpiresAt returns the exclusive UTC expiry time.
func (ticket Ticket) ExpiresAt() time.Time { return ticket.expiresAt }

// RedeemedAt returns a defensive copy of the redemption time.
func (ticket Ticket) RedeemedAt() *time.Time { return utcTimeCopy(ticket.redeemedAt) }

// ConnectionID returns the accepted connection identity, if redeemed.
func (ticket Ticket) ConnectionID() id.Connection { return ticket.connectionID }

func validRegion(region string) bool {
	if len(region) == 0 || len(region) > 63 || region[0] < 'a' || region[0] > 'z' {
		return false
	}
	for _, character := range region[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}

	return true
}

func utcTimeCopy(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()

	return &result
}
