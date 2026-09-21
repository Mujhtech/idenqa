// Package keycustody owns tenant-scoped HMAC key lifecycles for predictable
// identifiers. Key material is generated internally and wrapped by the owned
// KMS port; only immutable key versions are published, and every state
// transition is version-checked and audited.
package keycustody

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// KeySize is the fixed 256-bit HMAC key length.
	KeySize = 32
	// MaximumDomains bounds one tenant's versioned identifier domains.
	MaximumDomains = 64
	// MaximumVersions bounds one domain's retained versions.
	MaximumVersions = 64
	// KeyIDPrefix is the public identifier prefix for HMAC keys.
	KeyIDPrefix id.Prefix = "hmk"
)

var (
	// ErrInvalid identifies malformed domains, commands, or attribution.
	ErrInvalid = errors.New("key custody: invalid command or key state")
	// ErrNotFound identifies a domain or key version that does not exist.
	ErrNotFound = errors.New("key custody: key not found")
	// ErrConflict identifies an expected-version mismatch.
	ErrConflict = errors.New("key custody: version conflict")
	// ErrReferenced identifies a retire attempt on a version that is still the
	// active resolution target or is still referenced by durable identifiers.
	ErrReferenced = errors.New("key custody: key version is still referenced")
	// ErrNoActiveKey identifies new-token issuance without an active version.
	ErrNoActiveKey = errors.New("key custody: domain has no active key version")
	// ErrUnavailable identifies KMS or persistence availability failures.
	ErrUnavailable = errors.New("key custody: key custody unavailable")
)

// State is one immutable lifecycle state for a key version.
type State string

const (
	// StateActive means the version may issue and verify identifiers.
	StateActive State = "active"
	// StateDisabled means the version issues and verifies nothing; it may be
	// retired once it is no longer referenced.
	StateDisabled State = "disabled"
	// StateRetired means the version is permanently unusable.
	StateRetired State = "retired"
)

// Domain is a validated, stable, tenant-scoped identifier namespace. The
// domain string is preserved in every derived identifier so rotating keys
// never merges two identifier meanings.
type Domain struct{ value string }

// ParseDomain validates a namespaced domain such as identity.identifier.v1.
func ParseDomain(value string) (Domain, error) {
	if _, err := kms.NewPurpose(value); err != nil {
		return Domain{}, ErrInvalid
	}

	return Domain{value: value}, nil
}

// String returns the canonical domain name.
func (domain Domain) String() string { return domain.value }

// IsZero reports whether the domain has not been initialised.
func (domain Domain) IsZero() bool { return domain.value == "" }

// Version is one immutable key version.
type Version struct {
	ID        string     `json:"id"`
	Domain    string     `json:"domain"`
	Version   int64      `json:"version"`
	State     State      `json:"state"`
	CreatedAt time.Time  `json:"created_at"`
	RetiredAt *time.Time `json:"retired_at,omitempty"`
}

// Command is a validated, authorized, idempotent lifecycle mutation.
type Command struct {
	Operation       string
	Domain          string
	ExpectedVersion int64
	Reason          string
	ActorKeyID      string
	Retry           idempotency.Request
	At              time.Time
}

// Result exposes safe lifecycle metadata without key material.
type Result struct {
	Domain        string    `json:"domain"`
	ActiveVersion int64     `json:"active_version"`
	Generation    int64     `json:"generation"`
	Versions      []Version `json:"versions,omitempty"`
	Enabled       bool      `json:"enabled"`
}

// Repository owns transactional lifecycle mutations, safe reads, and wrapped
// key access. It never returns wrapped material through Result.
type Repository interface {
	Execute(context.Context, tenant.Scope, Command) (Result, error)
	Read(context.Context, tenant.Scope, string) (Result, error)
	// Version returns the immutable record for one exact key version.
	Version(context.Context, tenant.Scope, string, int64) (Version, error)
	// Versions returns every retained version for one domain.
	Versions(context.Context, tenant.Scope, string) ([]Version, error)
	// Material releases one unwrapped HMAC key for the exact version. The
	// caller must clear the returned slice.
	Material(context.Context, tenant.Scope, string, int64) ([]byte, error)
}

// References reports whether durable identifiers still reference a key
// version. Implementations are conservative: an unknown domain or an
// unreadable ledger reports referenced so a retire never removes a key that
// live identifiers may still resolve through.
type References interface {
	ReferencedKeyVersion(context.Context, tenant.Scope, string, int64) (bool, error)
}

// Service authorizes and validates lifecycle operations at the application
// boundary.
type Service struct {
	repository Repository
	identifier *id.Generator
	now        func() time.Time
}

// NewService constructs authorized lifecycle operations with an explicit
// identifier generator and clock.
func NewService(repository Repository, identifier *id.Generator, now func() time.Time) (*Service, error) {
	if repository == nil || identifier == nil || now == nil {
		return nil, ErrInvalid
	}

	return &Service{repository: repository, identifier: identifier, now: now}, nil
}

// Execute authorizes and fingerprints one lifecycle command.
func (service *Service) Execute(ctx context.Context, auth access.Context, key string, command Command) (Result, error) {
	if ctx == nil || auth.TenantScope().ID().IsZero() {
		return Result{}, ErrInvalid
	}
	if err := auth.Require(access.PermissionKMSWrite); err != nil {
		return Result{}, err
	}

	return service.ExecuteDirect(ctx, auth.TenantScope(), auth.Principal().KeyID(), key, command)
}

// ExecuteDirect validates and fingerprints one lifecycle command for an
// already-authenticated operator actor. The operator CLI uses it directly;
// HTTP callers use Execute so the permission is always checked.
func (service *Service) ExecuteDirect(ctx context.Context, scope tenant.Scope, actor id.APIKey, key string, command Command) (Result, error) {
	if ctx == nil || scope.ID().IsZero() || actor.IsZero() {
		return Result{}, ErrInvalid
	}
	if err := validateCommand(command); err != nil {
		return Result{}, err
	}
	command.ActorKeyID = actor.String()
	command.At = service.now().UTC().Truncate(time.Microsecond)
	canonical, err := json.Marshal(struct {
		Operation       string
		Domain          string
		ExpectedVersion int64
		Reason          string
		Key             string
	}{command.Operation, command.Domain, command.ExpectedVersion, command.Reason, key})
	if err != nil {
		return Result{}, ErrInvalid
	}
	command.Retry, err = idempotency.NewRequest(scope.ID(), actor, "keycustody."+command.Operation, key, canonical, command.At, 30*24*time.Hour)
	if err != nil {
		return Result{}, ErrInvalid
	}

	return service.repository.Execute(ctx, scope, command)
}

// Read authorizes a safe domain read.
func (service *Service) Read(ctx context.Context, auth access.Context, domain string) (Result, error) {
	if err := auth.Require(access.PermissionKMSRead); err != nil {
		return Result{}, err
	}
	return service.repository.Read(ctx, auth.TenantScope(), domain)
}

// GenerateKey returns validated key identifier and material for persistence.
// The caller owns the material and must clear it.
func (service *Service) GenerateKey() (string, []byte, error) {
	if service == nil || service.identifier == nil {
		return "", nil, ErrInvalid
	}
	identifier, err := service.identifier.New(KeyIDPrefix)
	if err != nil {
		return "", nil, ErrUnavailable
	}
	material := make([]byte, KeySize)
	if _, err := rand.Read(material); err != nil {
		clear(material)
		return "", nil, ErrUnavailable
	}

	return identifier.String(), material, nil
}

func validateCommand(command Command) error {
	switch command.Operation {
	case "create":
		if command.ExpectedVersion != 0 {
			return ErrInvalid
		}
	case "rotate":
		if command.ExpectedVersion < 1 {
			return ErrInvalid
		}
	case "disable", "retire":
		if command.ExpectedVersion < 1 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if command.Reason == "" || len(command.Reason) > 512 {
		return ErrInvalid
	}
	if _, err := ParseDomain(command.Domain); err != nil {
		return err
	}

	return nil
}

// Token derives one deterministic identifier token for the exact domain,
// version, and ordered parts. Domain separation is part of the MAC input, so a
// token created for one domain can never verify in another.
func Token(material []byte, domain string, parts ...string) (string, error) {
	if len(material) != KeySize || domain == "" || len(parts) == 0 {
		return "", ErrInvalid
	}
	canonical, err := json.Marshal(append([]string{domain}, parts...))
	if err != nil {
		return "", ErrUnavailable
	}
	defer clear(canonical)
	mac := hmac.New(sha256.New, material)
	if _, err := mac.Write(canonical); err != nil {
		return "", ErrUnavailable
	}

	return hex.EncodeToString(mac.Sum(nil)), nil
}

// EqualToken compares two tokens in constant time.
func EqualToken(first, second string) bool {
	if first == "" || second == "" || len(first) != len(second) {
		return false
	}

	return subtle.ConstantTimeCompare([]byte(first), []byte(second)) == 1
}
