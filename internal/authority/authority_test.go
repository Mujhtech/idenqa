package authority_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestNotice_ContentAddressedAndImmutable(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, false)

	record := authority.NoticeRecord{
		ID: fixture.notice.ID(), TenantID: fixture.notice.TenantID(),
		Key: fixture.notice.Key(), Locale: fixture.notice.Locale(),
		Controller: fixture.notice.Controller(), Recipient: fixture.notice.Recipient(),
		Copy: fixture.notice.Copy(), EffectiveAt: fixture.notice.EffectiveAt(),
		CreatedAt: fixture.notice.CreatedAt(), CreatedBy: fixture.notice.CreatedBy(),
		Digest: fixture.notice.Digest(),
	}
	restored, err := authority.RestoreNotice(record)
	if err != nil {
		t.Fatalf("RestoreNotice() error = %v", err)
	}
	if restored.Digest() != fixture.notice.Digest() {
		t.Fatalf("RestoreNotice().Digest() = %q, want %q", restored.Digest(), fixture.notice.Digest())
	}

	record.Copy.Purpose = "Changed meaning"
	if _, err := authority.RestoreNotice(record); err == nil {
		t.Fatal("RestoreNotice() accepted content under a stale digest")
	}
}

func TestEvaluate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		consentRequired bool
		change          func(*fixture)
		want            error
	}{
		{name: "active acknowledged authority", want: nil},
		{name: "active consent authority", consentRequired: true, want: nil},
		{name: "missing authority", change: func(f *fixture) {
			f.authority = authority.Authority{}
		}, want: authority.ErrProcessingNotPermitted},
		{name: "missing response", change: func(f *fixture) { f.response = nil }, want: authority.ErrSubjectResponseRequired},
		{name: "acknowledgement is not consent", consentRequired: true, change: func(f *fixture) {
			response := f.newResponse(t, authority.ResponseAcknowledge)
			f.response = &response
		}, want: authority.ErrSubjectResponseRequired},
		{name: "refusal blocks processing", change: func(f *fixture) {
			response := f.newResponse(t, authority.ResponseRefuse)
			f.response = &response
		}, want: authority.ErrProcessingNotPermitted},
		{name: "expired authority", change: func(f *fixture) {
			f.now = f.authority.Record().ExpiresAt
		}, want: authority.ErrProcessingNotPermitted},
		{name: "wrong purpose", change: func(f *fixture) {
			f.request.Purpose = "idenqa.purpose.account_recovery"
		}, want: authority.ErrProcessingNotPermitted},
		{name: "wrong evidence type", change: func(f *fixture) {
			f.request.EvidenceType = "idenqa.evidence.identity_document"
		}, want: authority.ErrProcessingNotPermitted},
		{name: "wrong recipient", change: func(f *fixture) {
			f.request.RecipientReference = "tenant.recipient.other"
		}, want: authority.ErrProcessingNotPermitted},
		{name: "wrong region", change: func(f *fixture) {
			f.request.Region = "idenqa.region.other"
		}, want: authority.ErrProcessingNotPermitted},
		{name: "wrong notice", change: func(f *fixture) {
			replacement, err := authority.NewNotice(authority.NoticeRecord{
				ID: mustNotice(t, "ntc_01ARZ3NDEKTSV4RRFFQ69G5FB3"), TenantID: f.notice.TenantID(),
				Key: f.notice.Key(), Locale: f.notice.Locale(), Controller: f.notice.Controller(),
				Recipient: f.notice.Recipient(), Copy: f.notice.Copy(), EffectiveAt: f.notice.EffectiveAt(),
				CreatedAt: f.notice.CreatedAt(), CreatedBy: f.notice.CreatedBy(),
			})
			if err != nil {
				t.Fatalf("NewNotice(replacement) error = %v", err)
			}
			f.notice = replacement
		}, want: authority.ErrProcessingNotPermitted},
		{name: "restricted authority", change: func(f *fixture) {
			if err := f.authority.Restrict(f.now); err != nil {
				t.Fatalf("Restrict() error = %v", err)
			}
		}, want: authority.ErrProcessingNotPermitted},
		{name: "withdrawn authority", change: func(f *fixture) {
			if err := f.authority.Withdraw(f.now); err != nil {
				t.Fatalf("Withdraw() error = %v", err)
			}
		}, want: authority.ErrProcessingNotPermitted},
		{name: "superseded authority", change: func(f *fixture) {
			if err := f.authority.Supersede(f.now); err != nil {
				t.Fatalf("Supersede() error = %v", err)
			}
		}, want: authority.ErrProcessingNotPermitted},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newFixture(t, test.consentRequired)
			if test.change != nil {
				test.change(&fixture)
			}
			err := authority.Evaluate(fixture.authority, fixture.notice, fixture.response, fixture.request, fixture.now)
			if !errors.Is(err, test.want) {
				t.Fatalf("Evaluate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestAuthority_TransitionIsIrreversible(t *testing.T) {
	t.Parallel()
	fixture := newFixture(t, false)
	if err := fixture.authority.Withdraw(fixture.now); err != nil {
		t.Fatalf("Withdraw() error = %v", err)
	}
	if err := fixture.authority.Restrict(fixture.now.Add(time.Second)); !errors.Is(err, authority.ErrConflict) {
		t.Fatalf("Restrict() after withdrawal error = %v, want conflict", err)
	}
	record := fixture.authority.Record()
	if record.State != authority.StateWithdrawn || record.Version != 2 ||
		record.WithdrawnAt == nil || !record.WithdrawnAt.Equal(fixture.now) {
		t.Fatalf("withdrawn record = %#v", record)
	}
}

type fixture struct {
	now       time.Time
	notice    authority.Notice
	authority authority.Authority
	response  *authority.Response
	request   authority.GrantRequest
	tokenID   id.CaptureToken
}

func newFixture(t *testing.T, consentRequired bool) fixture {
	t.Helper()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	tenantID := mustTenant(t, "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	verificationID := mustVerification(t, "ver_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	subjectID := mustSubject(t, "sub_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	noticeID := mustNotice(t, "ntc_01ARZ3NDEKTSV4RRFFQ69G5FAY")
	authorityID := mustAuthority(t, "aut_01ARZ3NDEKTSV4RRFFQ69G5FAZ")
	keyID := mustKey(t, "key_01ARZ3NDEKTSV4RRFFQ69G5FB0")
	tokenID := mustToken(t, "ctk_01ARZ3NDEKTSV4RRFFQ69G5FB1")

	notice, err := authority.NewNotice(authority.NoticeRecord{
		ID: noticeID, TenantID: tenantID, Key: "tenant.notice.identity_verification",
		Locale: "en", Controller: "Example Controller", Recipient: "Example Recipient",
		Copy: authority.NoticeCopy{
			Title: "Identity verification", Summary: "We need to verify your identity.",
			Purpose:      "Your evidence is used only for identity verification.",
			Consequences: "You may refuse and collection will not continue.",
		},
		EffectiveAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour), CreatedBy: keyID,
	})
	if err != nil {
		t.Fatalf("NewNotice() error = %v", err)
	}
	declaration, err := authority.New(authority.Record{
		ID: authorityID, TenantID: tenantID, SubjectID: subjectID,
		VerificationID: verificationID, NoticeID: noticeID,
		Category:     "tenant.authority.customer_declared",
		Purpose:      "idenqa.purpose.identity_verification",
		Jurisdiction: "tenant.jurisdiction.synthetic",
		PolicyPack:   "tenant.policy.synthetic_v1", IsConsentRequired: consentRequired,
		RequirementPurposes:  []string{"idenqa.purpose.identity_verification"},
		EvidenceTypes:        []string{"idenqa.evidence.selfie_image"},
		RecipientReference:   "tenant.recipient.primary",
		RecipientDisplayName: "Example Recipient",
		Regions:              []string{"idenqa.region.synthetic"},
		RetentionReference:   "tenant.retention.synthetic_v1",
		ValidFrom:            now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour),
		CreatedAt: now.Add(-time.Minute), CreatedBy: keyID,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response, err := authority.NewResponse(authority.ResponseRecord{
		ID:       mustAcknowledgement(t, "ack_01ARZ3NDEKTSV4RRFFQ69G5FB2"),
		TenantID: tenantID, AuthorityID: authorityID, NoticeID: noticeID,
		SubjectID: subjectID, VerificationID: verificationID, CaptureTokenID: tokenID,
		Action: authority.ResponseAcknowledge, Locale: "en", RecordedAt: now,
	})
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	if consentRequired {
		response = newFixtureResponse(t, response.Record(), authority.ResponseConsent)
	}
	return fixture{
		now: now, notice: notice, authority: declaration, response: &response, tokenID: tokenID,
		request: authority.GrantRequest{
			TenantID: tenantID, VerificationID: verificationID, SubjectID: subjectID,
			Purpose:            "idenqa.purpose.identity_verification",
			EvidenceType:       "idenqa.evidence.selfie_image",
			RecipientReference: "tenant.recipient.primary", Region: "idenqa.region.synthetic",
		},
	}
}

func (fixture fixture) newResponse(t *testing.T, action authority.ResponseAction) authority.Response {
	t.Helper()
	return newFixtureResponse(t, fixture.response.Record(), action)
}

func newFixtureResponse(t *testing.T, record authority.ResponseRecord, action authority.ResponseAction) authority.Response {
	t.Helper()
	record.Action = action
	response, err := authority.NewResponse(record)
	if err != nil {
		t.Fatalf("NewResponse() error = %v", err)
	}
	return response
}

func mustTenant(t *testing.T, value string) id.Tenant {
	t.Helper()
	parsed, err := id.ParseTenant(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustVerification(t *testing.T, value string) id.Verification {
	t.Helper()
	parsed, err := id.ParseVerification(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustSubject(t *testing.T, value string) id.Subject {
	t.Helper()
	parsed, err := id.ParseSubject(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustNotice(t *testing.T, value string) id.Notice {
	t.Helper()
	parsed, err := id.ParseNotice(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustAuthority(t *testing.T, value string) id.Authority {
	t.Helper()
	parsed, err := id.ParseAuthority(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustKey(t *testing.T, value string) id.APIKey {
	t.Helper()
	parsed, err := id.ParseAPIKey(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustToken(t *testing.T, value string) id.CaptureToken {
	t.Helper()
	parsed, err := id.ParseCaptureToken(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
func mustAcknowledgement(t *testing.T, value string) id.Acknowledgement {
	t.Helper()
	parsed, err := id.ParseAcknowledgement(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
