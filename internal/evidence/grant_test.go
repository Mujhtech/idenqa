package evidence_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestGrantClaimBindsRunnerAssetTimeAndMaximumUses(t *testing.T) {
	t.Parallel()

	grant, asset, now := validGrant(t)
	claimed, err := grant.Claim(grant.Record().Runner, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	if claimed.Uses() != 1 || !claimed.Binds(asset) {
		t.Fatalf("claimed uses = %d, binds = %t", claimed.Uses(), claimed.Binds(asset))
	}

	tests := []struct {
		name   string
		grant  evidence.Grant
		runner evidence.Runner
		now    time.Time
	}{
		{name: "wrong runner", grant: grant, runner: evidence.Runner{Identity: "runner.other", WorkloadVersion: "workload.v1"}, now: now.Add(time.Minute)},
		{name: "expired", grant: grant, runner: grant.Record().Runner, now: now.Add(time.Hour)},
		{name: "exhausted", grant: claimed, runner: grant.Record().Runner, now: now.Add(2 * time.Minute)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.grant.Claim(test.runner, test.now); !errors.Is(err, evidence.ErrGrantDenied) {
				t.Fatalf("Claim() error = %v, want ErrGrantDenied", err)
			}
		})
	}
}

func TestGrantRevocationIsIrreversible(t *testing.T) {
	t.Parallel()

	grant, _, now := validGrant(t)
	revoked, err := grant.Revoke(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if _, err := revoked.Claim(grant.Record().Runner, now.Add(2*time.Minute)); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("Claim(revoked) error = %v, want ErrGrantDenied", err)
	}
	if _, err := revoked.Revoke(now.Add(3 * time.Minute)); !errors.Is(err, evidence.ErrGrantConflict) {
		t.Fatalf("Revoke(revoked) error = %v, want ErrGrantConflict", err)
	}
}

func TestGrantRejectsLifetimeBeyondOwnedMaximum(t *testing.T) {
	t.Parallel()

	grant, _, _ := validGrant(t)
	record := grant.Record()
	record.ExpiresAt = record.CreatedAt.Add(time.Hour + time.Nanosecond)
	if _, err := evidence.NewGrant(record); err == nil {
		t.Fatal("NewGrant(lifetime beyond maximum) error = nil")
	}
}

func TestCommandAttributionRequiresBothActorsAndReason(t *testing.T) {
	t.Parallel()

	valid := evidence.CommandAttribution{
		Principal:   evidence.Actor{Type: "internal.service", ID: "workflow.core"},
		TenantActor: evidence.Actor{Type: "tenant.service", ID: "tenant.api"},
		Reason:      "issue evidence processing grant",
	}
	if !valid.Valid() {
		t.Fatal("valid command attribution was rejected")
	}
	tests := []evidence.CommandAttribution{
		{},
		{Principal: valid.Principal, TenantActor: valid.TenantActor},
		{Principal: evidence.Actor{Type: "internal.service", ID: "bad actor"}, TenantActor: valid.TenantActor, Reason: valid.Reason},
		{Principal: valid.Principal, TenantActor: evidence.Actor{Type: "tenant", ID: "tenant.api"}, Reason: valid.Reason},
		{Principal: valid.Principal, TenantActor: valid.TenantActor, Reason: "bad\nreason"},
	}
	for index, attribution := range tests {
		if attribution.Valid() {
			t.Fatalf("invalid command attribution %d was accepted", index)
		}
	}
}

func TestCompletedRedemptionRemainsReplayableAfterGrantExpiry(t *testing.T) {
	t.Parallel()

	grant, _, now := validGrant(t)
	claimed, err := grant.Claim(grant.Record().Runner, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Claim() error = %v", err)
	}
	redemptionID, err := id.ParseRedemption("rdm_01ARZ3NDEKTSV4RRFFQ69G5FB4")
	if err != nil {
		t.Fatalf("ParseRedemption() error = %v", err)
	}
	pending, err := evidence.NewRedemption(redemptionID, claimed)
	if err != nil {
		t.Fatalf("NewRedemption() error = %v", err)
	}
	if pending.ValidFor(grant.ID(), grant.Record().Runner, now.Add(2*time.Hour)) {
		t.Fatal("pending redemption remained valid after grant expiry")
	}
	completed, err := pending.Complete(evidence.GrantOutcomeSucceeded)
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if !completed.ValidFor(grant.ID(), grant.Record().Runner, now.Add(2*time.Hour)) {
		t.Fatal("completed redemption was not replayable after grant expiry")
	}
	if _, err := completed.Complete(evidence.GrantOutcomeFailed); !errors.Is(err, evidence.ErrGrantConflict) {
		t.Fatalf("Complete(conflicting outcome) error = %v, want ErrGrantConflict", err)
	}
}

func validGrant(t *testing.T) (evidence.Grant, evidence.Asset, time.Time) {
	t.Helper()
	assetRecord, registry := validAssetRecord(t)
	asset, err := evidence.NewAvailable(assetRecord, registry)
	if err != nil {
		t.Fatalf("NewAvailable() error = %v", err)
	}
	now := assetRecord.CreatedAt.Add(time.Hour)
	grantID, err := id.ParseGrant("grt_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	if err != nil {
		t.Fatalf("ParseGrant() error = %v", err)
	}
	authorityID, err := id.ParseAuthority("aut_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	if err != nil {
		t.Fatalf("ParseAuthority() error = %v", err)
	}
	responseID, err := id.ParseAcknowledgement("ack_01ARZ3NDEKTSV4RRFFQ69G5FAY")
	if err != nil {
		t.Fatalf("ParseAcknowledgement() error = %v", err)
	}
	grant, err := evidence.NewGrant(evidence.GrantRecord{
		ID: grantID, TenantID: assetRecord.TenantID, SubjectID: assetRecord.SubjectID,
		VerificationID: assetRecord.VerificationID, EvidenceID: assetRecord.ID,
		RequirementKey: assetRecord.RequirementKey,
		AuthorityID:    authorityID, ResponseID: responseID,
		CheckReference: "check.selfie.match", Runner: evidence.Runner{
			Identity: "runner.synthetic", WorkloadVersion: "workload.v1",
		},
		Purpose: evidence.PurposeIdentityVerification, Operation: evidence.OperationPlaintextRead,
		PermittedVariants: []string{evidence.VariantOriginal}, Region: assetRecord.Region,
		RecipientReference: "tenant.recipient.primary", OutputDestination: "workflow.result.normalized",
		PolicyReference: "tenant.policy.synthetic_v1", MaximumUses: 1,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("NewGrant() error = %v", err)
	}
	return grant, asset, now
}
