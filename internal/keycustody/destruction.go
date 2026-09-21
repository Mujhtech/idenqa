package keycustody

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

const (
	// VerificationIDPrefix is the public identifier prefix for destruction
	// verification receipts.
	VerificationIDPrefix id.Prefix = "kdv"
	// ScheduleIDPrefix is the public identifier prefix for destruction
	// scheduling receipts.
	ScheduleIDPrefix id.Prefix = "kds"
	// DestructionApprovalWindow bounds how long one verified receipt may
	// authorise a provider-side destruction schedule.
	DestructionApprovalWindow = time.Hour
)

var (
	// ErrDestructionBlocked identifies a verified reference scan that still
	// found ciphertext or ledger references for the target material.
	ErrDestructionBlocked = errors.New("key custody: destruction is blocked by live references")
	// ErrDestructionUnverified identifies scheduling without a verified,
	// unexpired receipt.
	ErrDestructionUnverified = errors.New("key custody: destruction requires an unexpired verified receipt")
)

// DestructionTarget is one exact provider wrapping identity proposed for
// destruction. Empty or malformed identities never reach a provider.
type DestructionTarget struct {
	Provider  string `json:"provider"`
	Reference string `json:"reference"`
	Version   string `json:"version"`
	Algorithm string `json:"algorithm"`
}

// Validate rejects malformed destruction targets.
func (target DestructionTarget) Validate() error {
	record := kms.WrappedKeyRecord{
		Provider: target.Provider, Reference: target.Reference,
		Version: target.Version, Algorithm: target.Algorithm,
	}
	probe := append([]byte(target.Provider+target.Reference+target.Version+target.Algorithm), 'x')
	if _, err := kms.NewWrappedKey(kms.WrappedKeyRecord{
		Provider: record.Provider, Reference: record.Reference, Version: record.Version,
		Algorithm: record.Algorithm, Ciphertext: probe,
	}); err != nil {
		return ErrInvalid
	}

	return nil
}

// Valid reports whether the target is well formed.
func (target DestructionTarget) Valid() bool { return target.Validate() == nil }

// ReferenceClass identifies one durable artifact class that can reference a
// wrapping identity.
type ReferenceClass string

// Reference classes cover the custody catalog, identity ledger, webhook and
// evidence wrapping tables, and every other durable KMS wrapping user.
const (
	ReferenceEvidenceAsset      ReferenceClass = "evidence.assets"
	ReferenceWebhookEvent       ReferenceClass = "webhook.events"
	ReferenceWebhookDelivery    ReferenceClass = "webhook.deliveries"
	ReferenceWebhookSecret      ReferenceClass = "webhook.secrets"
	ReferenceHMACKey            ReferenceClass = "keycustody.hmac_keys"
	ReferenceIdentityLookupKey  ReferenceClass = "identity.keys"
	ReferenceIdentitySubjectKey ReferenceClass = "identity.subjects"
	ReferenceFraudKey           ReferenceClass = "fraud.keys"
	ReferenceIdentityToken      ReferenceClass = "identity.identifier_tokens"
)

// ReferenceClasses returns every scanned class in stable order.
func ReferenceClasses() []ReferenceClass {
	return []ReferenceClass{
		ReferenceEvidenceAsset,
		ReferenceWebhookEvent,
		ReferenceWebhookDelivery,
		ReferenceWebhookSecret,
		ReferenceHMACKey,
		ReferenceIdentityLookupKey,
		ReferenceIdentitySubjectKey,
		ReferenceFraudKey,
		ReferenceIdentityToken,
	}
}

// VerificationReceipt is one immutable, explicitly authorised destruction
// verification. It is produced from a single consistent reference snapshot.
type VerificationReceipt struct {
	ID         string
	Target     DestructionTarget
	State      string
	Counts     map[ReferenceClass]int64
	Total      int64
	Verifier   string
	Reason     string
	Digest     string
	VerifiedAt time.Time
}

// Verified reports whether the receipt proves zero remaining references.
func (receipt VerificationReceipt) Verified() bool {
	return receipt.State == "verified" && receipt.Total == 0
}

// DestructionSchedule records the provider-side scheduling decision.
type DestructionSchedule struct {
	ID                 string
	VerificationID     string
	Target             DestructionTarget
	Mode               string
	ProviderDeletionAt *time.Time
	Actor              string
	Reason             string
	CreatedAt          time.Time
}

// ReferenceScanner counts, in one consistent snapshot, every durable
// reference that still targets the exact wrapping material. Implementations
// are conservative: any unreadable class fails closed.
type ReferenceScanner interface {
	Scan(context.Context, DestructionTarget) (map[ReferenceClass]int64, error)
}

// DestructionScheduler schedules provider-side key deletion. It is optional:
// a nil scheduler means the installation records destruction for the operator
// to perform out of band.
type DestructionScheduler interface {
	ScheduleDestruction(ctx context.Context, reference, version string, pendingWindow time.Duration) (time.Time, error)
}

// DestructionRepository persists immutable verification and scheduling
// receipts.
type DestructionRepository interface {
	RecordVerification(context.Context, VerificationReceipt) error
	Verification(context.Context, string) (VerificationReceipt, error)
	RecordSchedule(context.Context, DestructionSchedule) error
}

// DestructionService verifies reference freedom before any provider-side
// destruction is scheduled.
type DestructionService struct {
	scanner     ReferenceScanner
	repository  DestructionRepository
	scheduler   DestructionScheduler
	identifiers *id.Generator
	now         func() time.Time
}

// NewDestructionService composes verified destruction with an optional
// provider scheduler.
func NewDestructionService(
	scanner ReferenceScanner,
	repository DestructionRepository,
	scheduler DestructionScheduler,
	identifiers *id.Generator,
	now func() time.Time,
) (*DestructionService, error) {
	if scanner == nil || repository == nil || identifiers == nil || now == nil {
		return nil, ErrInvalid
	}

	return &DestructionService{scanner: scanner, repository: repository, scheduler: scheduler, identifiers: identifiers, now: now}, nil
}

// Verify scans every reference class and records an immutable receipt. It
// fails closed with ErrDestructionBlocked while any reference remains; the
// blocked receipt is still recorded so the refusal is auditable.
func (service *DestructionService) Verify(ctx context.Context, actor id.APIKey, target DestructionTarget, reason string) (VerificationReceipt, error) {
	if ctx == nil || actor.IsZero() || !target.Valid() {
		return VerificationReceipt{}, ErrInvalid
	}
	if err := validateReason(reason); err != nil {
		return VerificationReceipt{}, err
	}
	at := service.now().UTC().Truncate(time.Microsecond)
	counts, err := service.scanner.Scan(ctx, target)
	if err != nil {
		return VerificationReceipt{}, ErrUnavailable
	}
	receipt := VerificationReceipt{
		Target: target, Counts: make(map[ReferenceClass]int64, len(ReferenceClasses())),
		Verifier: actor.String(), Reason: reason, VerifiedAt: at,
	}
	for _, class := range ReferenceClasses() {
		count := counts[class]
		if count < 0 {
			return VerificationReceipt{}, ErrUnavailable
		}
		receipt.Counts[class] = count
		receipt.Total += count
	}
	receipt.State = "verified"
	if receipt.Total > 0 {
		receipt.State = "blocked"
	}
	identifier, err := service.identifiers.New(VerificationIDPrefix)
	if err != nil {
		return VerificationReceipt{}, ErrUnavailable
	}
	receipt.ID = identifier.String()
	digest, err := receiptDigest(receipt)
	if err != nil {
		return VerificationReceipt{}, ErrUnavailable
	}
	receipt.Digest = digest
	if err := service.repository.RecordVerification(ctx, receipt); err != nil {
		return VerificationReceipt{}, ErrUnavailable
	}
	if receipt.Total > 0 {
		return receipt, ErrDestructionBlocked
	}

	return receipt, nil
}

// Authorize checks a receipt from the Authorized method. It returns the
// receipt and ErrDestructionUnverified when the receipt is blocked, unknown,
// or stale.
func (service *DestructionService) Authorize(ctx context.Context, receiptID string) (VerificationReceipt, error) {
	if service == nil || ctx == nil || receiptID == "" {
		return VerificationReceipt{}, ErrInvalid
	}
	receipt, err := service.repository.Verification(ctx, receiptID)
	if err != nil {
		return VerificationReceipt{}, ErrDestructionUnverified
	}
	if !receipt.Verified() || service.now().UTC().Sub(receipt.VerifiedAt) > DestructionApprovalWindow {
		return VerificationReceipt{}, ErrDestructionUnverified
	}

	return receipt, nil
}

// ReceiptDirect returns one recorded receipt, including blocked refusals, to
// an already-authenticated operator. HTTP reads use it so a refusal can always
// be inspected.
func (service *DestructionService) ReceiptDirect(ctx context.Context, identifier string) (VerificationReceipt, error) {
	if service == nil || ctx == nil || identifier == "" {
		return VerificationReceipt{}, ErrInvalid
	}

	return service.repository.Verification(ctx, identifier)
}

// Schedule records, and when a provider scheduler is configured also
// schedules, destruction for one exact verified receipt. recordOnly forces
// the recorded mode even when a scheduler exists.
func (service *DestructionService) Schedule(ctx context.Context, actor id.APIKey, receiptID string, pendingWindow time.Duration, recordOnly bool, reason string) (DestructionSchedule, error) {
	if ctx == nil || actor.IsZero() {
		return DestructionSchedule{}, ErrInvalid
	}
	if err := validateReason(reason); err != nil {
		return DestructionSchedule{}, err
	}
	if pendingWindow < time.Hour || pendingWindow > 30*24*time.Hour {
		return DestructionSchedule{}, ErrInvalid
	}
	receipt, err := service.Authorize(ctx, receiptID)
	if err != nil {
		return DestructionSchedule{}, err
	}
	schedule := DestructionSchedule{
		VerificationID: receipt.ID, Target: receipt.Target, Mode: "recorded",
		Actor: actor.String(), Reason: reason, CreatedAt: service.now().UTC().Truncate(time.Microsecond),
	}
	if service.scheduler != nil && !recordOnly {
		deletionAt, err := service.scheduler.ScheduleDestruction(ctx, receipt.Target.Reference, receipt.Target.Version, pendingWindow)
		if err != nil || deletionAt.IsZero() {
			return DestructionSchedule{}, ErrUnavailable
		}
		schedule.Mode = "scheduled"
		at := deletionAt.UTC()
		schedule.ProviderDeletionAt = &at
	}
	identifier, err := service.identifiers.New(ScheduleIDPrefix)
	if err != nil {
		return DestructionSchedule{}, ErrUnavailable
	}
	schedule.ID = identifier.String()
	if err := service.repository.RecordSchedule(ctx, schedule); err != nil {
		return DestructionSchedule{}, ErrUnavailable
	}

	return schedule, nil
}

// ExecuteAuthorized enforces kms:write before running one destruction action.
func (service *DestructionService) ExecuteAuthorized(ctx context.Context, auth access.Context, command string, target DestructionTarget, receiptID string, pendingWindow time.Duration, recordOnly bool, reason string) (any, error) {
	if auth.TenantScope().ID().IsZero() {
		return nil, ErrInvalid
	}
	if err := auth.Require(access.PermissionKMSWrite); err != nil {
		return nil, err
	}
	actor := auth.Principal().KeyID()
	switch command {
	case "verify":
		return service.Verify(ctx, actor, target, reason)
	case "schedule":
		return service.Schedule(ctx, actor, receiptID, pendingWindow, recordOnly, reason)
	default:
		return nil, ErrInvalid
	}
}

func receiptDigest(receipt VerificationReceipt) (string, error) {
	body, err := json.Marshal(struct {
		Provider, Reference, Version, Algorithm string
		State                                   string
		Counts                                  map[ReferenceClass]int64
		Total                                   int64
		Verifier, Reason                        string
		VerifiedAt                              time.Time
	}{
		receipt.Target.Provider, receipt.Target.Reference, receipt.Target.Version, receipt.Target.Algorithm,
		receipt.State, receipt.Counts, receipt.Total, receipt.Verifier, receipt.Reason, receipt.VerifiedAt,
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:]), nil
}

func validateReason(reason string) error {
	if len(reason) < 8 || len(reason) > 500 {
		return ErrInvalid
	}
	for index := 0; index < len(reason); index++ {
		if reason[index] < 0x20 || reason[index] == 0x7f {
			return ErrInvalid
		}
	}

	return nil
}
