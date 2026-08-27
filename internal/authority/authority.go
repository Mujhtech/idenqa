// Package authority owns declared processing authority, immutable notices,
// subject responses, and evidence-processing permission checks.
package authority

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	maxCodeLength        = 200
	maxDisplayLength     = 200
	maxCopyLength        = 8000
	maxLocaleLength      = 35
	maxScopedValues      = 64
	authorityCodePattern = "^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)+$"
)

var (
	codeExpression = regexp.MustCompile(authorityCodePattern)

	// ErrNotFound deliberately covers absent and cross-tenant authority resources.
	ErrNotFound = errors.New("authority: resource not found")
	// ErrConflict identifies an invalid authority declaration or lifecycle transition.
	ErrConflict = errors.New("authority: lifecycle conflict")
	// ErrVersionConflict identifies an optimistic-concurrency precondition mismatch.
	ErrVersionConflict = errors.New("authority: version precondition failed")
	// ErrProcessingNotPermitted means current authority cannot permit the requested operation.
	ErrProcessingNotPermitted = errors.New("authority: processing is not permitted")
	// ErrSubjectResponseRequired means the required subject interaction has not been recorded.
	ErrSubjectResponseRequired = errors.New("authority: subject response is required")
)

// NoticeCopy is the structured mandatory subject-facing notice content.
type NoticeCopy struct {
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	Purpose      string `json:"purpose"`
	Consequences string `json:"consequences"`
}

// NoticeRecord contains the complete immutable notice-version representation.
type NoticeRecord struct {
	ID          id.Notice
	TenantID    id.Tenant
	Key         string
	Locale      string
	Controller  string
	Recipient   string
	Copy        NoticeCopy
	EffectiveAt time.Time
	CreatedAt   time.Time
	CreatedBy   id.APIKey
	Digest      string
}

// Notice is one immutable, content-addressed subject-facing notice version.
type Notice struct{ record NoticeRecord }

// NewNotice validates and content-addresses an immutable notice version.
func NewNotice(record NoticeRecord) (Notice, error) {
	record.EffectiveAt = record.EffectiveAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	record.Digest = ""
	if record.ID.IsZero() || record.TenantID.IsZero() || record.CreatedBy.IsZero() ||
		record.EffectiveAt.IsZero() || record.CreatedAt.IsZero() {
		return Notice{}, errors.New("authority: notice identity, actor, and times are required")
	}
	if err := validateCode("notice key", record.Key); err != nil {
		return Notice{}, err
	}
	if err := validateLocale(record.Locale); err != nil {
		return Notice{}, err
	}
	if err := validateDisplay("controller", record.Controller); err != nil {
		return Notice{}, err
	}
	if err := validateDisplay("recipient", record.Recipient); err != nil {
		return Notice{}, err
	}
	if err := validateCopy(record.Copy); err != nil {
		return Notice{}, err
	}
	digest, err := noticeDigest(record)
	if err != nil {
		return Notice{}, err
	}
	record.Digest = digest

	return Notice{record: record}, nil
}

// RestoreNotice validates an immutable notice loaded from durable state.
func RestoreNotice(record NoticeRecord) (Notice, error) {
	want := record.Digest
	notice, err := NewNotice(record)
	if err != nil {
		return Notice{}, err
	}
	if want == "" || notice.record.Digest != want {
		return Notice{}, errors.New("authority: notice digest does not match its content")
	}

	return notice, nil
}

// ID returns the immutable notice-version identifier.
func (notice Notice) ID() id.Notice { return notice.record.ID }

// TenantID returns the owning tenant.
func (notice Notice) TenantID() id.Tenant { return notice.record.TenantID }

// Key returns the stable semantic notice key.
func (notice Notice) Key() string { return notice.record.Key }

// Locale returns the canonical notice locale.
func (notice Notice) Locale() string { return notice.record.Locale }

// Controller returns the controller display identity snapshot.
func (notice Notice) Controller() string { return notice.record.Controller }

// Recipient returns the recipient display identity snapshot.
func (notice Notice) Recipient() string { return notice.record.Recipient }

// Copy returns the immutable mandatory copy.
func (notice Notice) Copy() NoticeCopy { return notice.record.Copy }

// EffectiveAt returns when this notice version becomes displayable.
func (notice Notice) EffectiveAt() time.Time { return notice.record.EffectiveAt }

// CreatedAt returns the creation time.
func (notice Notice) CreatedAt() time.Time { return notice.record.CreatedAt }

// CreatedBy returns the tenant principal that created the version.
func (notice Notice) CreatedBy() id.APIKey { return notice.record.CreatedBy }

// Digest returns the canonical SHA-256 content digest.
func (notice Notice) Digest() string { return notice.record.Digest }

// State is the current processing-authority lifecycle state.
type State string

const (
	// StateActive permits evaluation subject to scope, time, and response checks.
	StateActive State = "active"
	// StateRestricted blocks future processing while preserving history.
	StateRestricted State = "restricted"
	// StateWithdrawn blocks future processing after withdrawal.
	StateWithdrawn State = "withdrawn"
	// StateSuperseded blocks this authority because another declaration is required.
	StateSuperseded State = "superseded"
)

// Record is the complete durable processing-authority representation.
type Record struct {
	ID                   id.Authority
	TenantID             id.Tenant
	SubjectID            id.Subject
	VerificationID       id.Verification
	NoticeID             id.Notice
	Category             string
	Purpose              string
	Jurisdiction         string
	PolicyPack           string
	IsConsentRequired    bool
	RequirementPurposes  []string
	EvidenceTypes        []string
	RecipientReference   string
	RecipientDisplayName string
	Regions              []string
	RetentionReference   string
	State                State
	Version              int64
	ValidFrom            time.Time
	ExpiresAt            time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
	CreatedBy            id.APIKey
	RestrictedAt         *time.Time
	WithdrawnAt          *time.Time
	SupersededAt         *time.Time
}

// Authority is one tenant declaration bound to one verification-local subject.
type Authority struct{ record Record }

// New validates a new active processing-authority declaration.
func New(record Record) (Authority, error) {
	record.State = StateActive
	record.Version = 1
	record.RestrictedAt = nil
	record.WithdrawnAt = nil
	record.SupersededAt = nil
	record.ValidFrom = record.ValidFrom.UTC()
	record.ExpiresAt = record.ExpiresAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.CreatedAt
	return restore(record)
}

// Restore validates processing authority loaded from durable state.
func Restore(record Record) (Authority, error) { return restore(record) }

func restore(record Record) (Authority, error) {
	record.ValidFrom = record.ValidFrom.UTC()
	record.ExpiresAt = record.ExpiresAt.UTC()
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	if err := validateCodeSet("requirement purposes", record.RequirementPurposes); err != nil {
		return Authority{}, err
	}
	if err := validateCodeSet("evidence types", record.EvidenceTypes); err != nil {
		return Authority{}, err
	}
	if err := validateCodeSet("regions", record.Regions); err != nil {
		return Authority{}, err
	}
	record.RequirementPurposes = canonicalSet(record.RequirementPurposes)
	record.EvidenceTypes = canonicalSet(record.EvidenceTypes)
	record.Regions = canonicalSet(record.Regions)
	if record.ID.IsZero() || record.TenantID.IsZero() || record.SubjectID.IsZero() ||
		record.VerificationID.IsZero() || record.NoticeID.IsZero() || record.CreatedBy.IsZero() {
		return Authority{}, errors.New("authority: declaration identity and actor are required")
	}
	for name, value := range map[string]string{
		"authority category": record.Category, "purpose": record.Purpose,
		"jurisdiction": record.Jurisdiction, "policy pack": record.PolicyPack,
		"recipient reference": record.RecipientReference,
		"retention reference": record.RetentionReference,
	} {
		if err := validateCode(name, value); err != nil {
			return Authority{}, err
		}
	}
	if err := validateDisplay("recipient display name", record.RecipientDisplayName); err != nil {
		return Authority{}, err
	}
	if !slices.Contains(record.RequirementPurposes, record.Purpose) {
		return Authority{}, errors.New("authority: declared purpose is absent from requirement purposes")
	}
	if record.Version < 1 || record.ValidFrom.IsZero() || !record.ExpiresAt.After(record.ValidFrom) ||
		record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return Authority{}, errors.New("authority: declaration lifecycle is invalid")
	}
	if err := validateStateTimes(record); err != nil {
		return Authority{}, err
	}
	return Authority{record: record}, nil
}

// ID returns the processing-authority identifier.
func (authority Authority) ID() id.Authority { return authority.record.ID }

// TenantID returns the owning tenant.
func (authority Authority) TenantID() id.Tenant { return authority.record.TenantID }

// SubjectID returns the verification-local subject.
func (authority Authority) SubjectID() id.Subject { return authority.record.SubjectID }

// VerificationID returns the bound verification.
func (authority Authority) VerificationID() id.Verification { return authority.record.VerificationID }

// NoticeID returns the exact required notice version.
func (authority Authority) NoticeID() id.Notice { return authority.record.NoticeID }

// Record returns a defensive durable representation.
func (authority Authority) Record() Record {
	record := authority.record
	record.RequirementPurposes = slices.Clone(record.RequirementPurposes)
	record.EvidenceTypes = slices.Clone(record.EvidenceTypes)
	record.Regions = slices.Clone(record.Regions)
	record.RestrictedAt = timeCopy(record.RestrictedAt)
	record.WithdrawnAt = timeCopy(record.WithdrawnAt)
	record.SupersededAt = timeCopy(record.SupersededAt)
	return record
}

// Restrict irreversibly blocks future processing under this authority.
func (authority *Authority) Restrict(now time.Time) error {
	return authority.transition(StateRestricted, now)
}

// Withdraw irreversibly records withdrawal and blocks dependent processing.
func (authority *Authority) Withdraw(now time.Time) error {
	return authority.transition(StateWithdrawn, now)
}

// Supersede irreversibly blocks this authority in favour of a new declaration.
func (authority *Authority) Supersede(now time.Time) error {
	return authority.transition(StateSuperseded, now)
}

func (authority *Authority) transition(state State, now time.Time) error {
	if authority == nil || authority.record.ID.IsZero() || authority.record.State != StateActive {
		return ErrConflict
	}
	now = now.UTC()
	if now.Before(authority.record.UpdatedAt) {
		return ErrConflict
	}
	authority.record.State = state
	authority.record.Version++
	authority.record.UpdatedAt = now
	switch state {
	case StateRestricted:
		authority.record.RestrictedAt = &now
	case StateWithdrawn:
		authority.record.WithdrawnAt = &now
	case StateSuperseded:
		authority.record.SupersededAt = &now
	default:
		return ErrConflict
	}
	return nil
}

// ResponseAction is one explicit subject interaction.
type ResponseAction string

const (
	// ResponseAcknowledge records notice acknowledgement without consent.
	ResponseAcknowledge ResponseAction = "acknowledge"
	// ResponseConsent records explicit consent.
	ResponseConsent ResponseAction = "consent"
	// ResponseRefuse records explicit refusal.
	ResponseRefuse ResponseAction = "refuse"
)

// ResponseRecord contains one append-only subject response.
type ResponseRecord struct {
	ID                        id.Acknowledgement
	TenantID                  id.Tenant
	AuthorityID               id.Authority
	NoticeID                  id.Notice
	SubjectID                 id.Subject
	VerificationID            id.Verification
	CaptureTokenID            id.CaptureToken
	Action                    ResponseAction
	Locale                    string
	RenderedExperienceVersion string
	RecordedAt                time.Time
}

// Response is an immutable subject interaction receipt.
type Response struct{ record ResponseRecord }

// NewResponse validates an append-only subject response.
func NewResponse(record ResponseRecord) (Response, error) {
	record.RecordedAt = record.RecordedAt.UTC()
	if record.ID.IsZero() || record.TenantID.IsZero() || record.AuthorityID.IsZero() ||
		record.NoticeID.IsZero() || record.SubjectID.IsZero() ||
		record.VerificationID.IsZero() || record.CaptureTokenID.IsZero() ||
		record.RecordedAt.IsZero() {
		return Response{}, errors.New("authority: subject response identity and time are required")
	}
	if record.Action != ResponseAcknowledge && record.Action != ResponseConsent &&
		record.Action != ResponseRefuse {
		return Response{}, errors.New("authority: subject response action is invalid")
	}
	if err := validateLocale(record.Locale); err != nil {
		return Response{}, err
	}
	if record.RenderedExperienceVersion != "" {
		if err := validateCode("rendered experience version", record.RenderedExperienceVersion); err != nil {
			return Response{}, err
		}
	}
	return Response{record: record}, nil
}

// Record returns the immutable subject-response representation.
func (response Response) Record() ResponseRecord { return response.record }

// GrantRequest is the exact evidence operation being authorised.
type GrantRequest struct {
	TenantID           id.Tenant
	VerificationID     id.Verification
	SubjectID          id.Subject
	Purpose            string
	EvidenceType       string
	RecipientReference string
	Region             string
}

// Evaluate permits a synthetic evidence-processing grant only when every
// current authority, scope, notice, and subject-response condition is met.
func Evaluate(
	authority Authority,
	notice Notice,
	response *Response,
	request GrantRequest,
	now time.Time,
) error {
	record := authority.record
	now = now.UTC()
	if record.State != StateActive || now.Before(record.ValidFrom) || !now.Before(record.ExpiresAt) {
		return ErrProcessingNotPermitted
	}
	if notice.ID().String() != record.NoticeID.String() ||
		notice.TenantID().String() != record.TenantID.String() ||
		now.Before(notice.EffectiveAt()) {
		return ErrProcessingNotPermitted
	}
	if request.TenantID.String() != record.TenantID.String() ||
		request.VerificationID.String() != record.VerificationID.String() ||
		request.SubjectID.String() != record.SubjectID.String() ||
		request.RecipientReference != record.RecipientReference ||
		!slices.Contains(record.RequirementPurposes, request.Purpose) ||
		!slices.Contains(record.EvidenceTypes, request.EvidenceType) ||
		!slices.Contains(record.Regions, request.Region) {
		return ErrProcessingNotPermitted
	}
	if response == nil {
		return ErrSubjectResponseRequired
	}
	receipt := response.record
	if receipt.TenantID.String() != record.TenantID.String() ||
		receipt.AuthorityID.String() != record.ID.String() ||
		receipt.NoticeID.String() != record.NoticeID.String() ||
		receipt.SubjectID.String() != record.SubjectID.String() ||
		receipt.VerificationID.String() != record.VerificationID.String() ||
		receipt.Action == ResponseRefuse {
		return ErrProcessingNotPermitted
	}
	if record.IsConsentRequired && receipt.Action != ResponseConsent {
		return ErrSubjectResponseRequired
	}
	if !record.IsConsentRequired &&
		receipt.Action != ResponseAcknowledge && receipt.Action != ResponseConsent {
		return ErrSubjectResponseRequired
	}
	return nil
}

func noticeDigest(record NoticeRecord) (string, error) {
	encoded, err := json.Marshal(struct {
		Key         string     `json:"key"`
		Locale      string     `json:"locale"`
		Controller  string     `json:"controller"`
		Recipient   string     `json:"recipient"`
		Copy        NoticeCopy `json:"copy"`
		EffectiveAt string     `json:"effective_at"`
	}{
		Key: record.Key, Locale: record.Locale, Controller: record.Controller,
		Recipient: record.Recipient, Copy: record.Copy,
		EffectiveAt: record.EffectiveAt.Format(time.RFC3339Nano),
	})
	if err != nil {
		return "", fmt.Errorf("authority: encode canonical notice: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func validateCode(name, value string) error {
	if len(value) == 0 || len(value) > maxCodeLength || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value || !codeExpression.MatchString(value) {
		return fmt.Errorf("authority: %s is invalid", name)
	}
	return nil
}

func validateCodeSet(name string, values []string) error {
	if len(values) == 0 || len(values) > maxScopedValues {
		return fmt.Errorf("authority: %s size is invalid", name)
	}
	for _, value := range values {
		if err := validateCode(name, value); err != nil {
			return err
		}
	}
	if len(slices.Compact(slices.Clone(values))) != len(values) {
		return fmt.Errorf("authority: %s contains duplicates", name)
	}
	return nil
}

func canonicalSet(values []string) []string {
	values = slices.Clone(values)
	slices.Sort(values)
	return slices.Compact(values)
}

func validateLocale(value string) error {
	if len(value) < 2 || len(value) > maxLocaleLength || strings.TrimSpace(value) != value {
		return errors.New("authority: locale is invalid")
	}
	for _, character := range value {
		valid := (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-'
		if !valid {
			return errors.New("authority: locale is invalid")
		}
	}
	return nil
}

func validateDisplay(name, value string) error {
	if value == "" || len(value) > maxDisplayLength || !utf8.ValidString(value) ||
		strings.TrimSpace(value) != value || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("authority: %s is invalid", name)
	}
	return nil
}

func validateCopy(noticeCopy NoticeCopy) error {
	for name, value := range map[string]string{
		"title": noticeCopy.Title, "summary": noticeCopy.Summary,
		"purpose copy": noticeCopy.Purpose, "consequence copy": noticeCopy.Consequences,
	} {
		if value == "" || len(value) > maxCopyLength || !utf8.ValidString(value) ||
			strings.TrimSpace(value) != value {
			return fmt.Errorf("authority: %s is invalid", name)
		}
	}
	return nil
}

func validateStateTimes(record Record) error {
	count := 0
	for state, value := range map[State]*time.Time{
		StateRestricted: record.RestrictedAt,
		StateWithdrawn:  record.WithdrawnAt,
		StateSuperseded: record.SupersededAt,
	} {
		if value != nil {
			count++
			utc := value.UTC()
			if utc.Before(record.CreatedAt) || !utc.Equal(record.UpdatedAt) || state != record.State {
				return errors.New("authority: transition time is invalid")
			}
		}
	}
	if (record.State == StateActive && count != 0) || (record.State != StateActive && count != 1) {
		return errors.New("authority: lifecycle state and transition time disagree")
	}
	return nil
}

func timeCopy(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
