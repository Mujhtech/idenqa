package evidence

import (
	"context"
	"errors"
	"testing"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestObjectReconciliationFencesClaimsAndTerminalDisposition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	obligation := reconciliationFixture(t, now)
	if _, err := obligation.ClaimForRecovery(now.Add(time.Minute), time.Minute); !errors.Is(err, ErrReconciliationConflict) {
		t.Fatalf("ClaimForRecovery(before available) error = %v", err)
	}
	claimed, err := obligation.ClaimForRecovery(now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("ClaimForRecovery() error = %v", err)
	}
	if claimed.Record().State != ReconciliationClaimed || claimed.Record().Claim != 1 ||
		claimed.Record().Version != 2 {
		t.Fatalf("claimed record = %+v", claimed.Record())
	}
	resolved, err := claimed.Resolve(ReconciliationDeleted, now.Add(2*time.Minute+time.Second))
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if resolved.Record().State != ReconciliationDeleted || resolved.Record().ResolvedAt == nil ||
		resolved.Record().Version != 3 {
		t.Fatalf("resolved record = %+v", resolved.Record())
	}
}

func TestNewObjectReconciliationForObjectRecordsFailedStagingOutput(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	uploadID, _ := id.ParseUpload("upl_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	evidenceID, _ := id.ParseEvidence("evd_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	lease := now.Add(2 * time.Minute)
	object, err := objectstore.NewObject(objectstore.ObjectRecord{
		Key: "tenants/ten/evidence/evd/content/1", Version: "version-1", Size: 42,
		Checksum: string(platformcrypto.Sum([]byte("ciphertext"))),
	})
	if err != nil {
		t.Fatalf("NewObject() error = %v", err)
	}
	obligation, err := NewObjectReconciliationForObject(Upload{record: UploadRecord{
		ID: uploadID, TenantID: tenantID, EvidenceID: evidenceID,
		State: UploadStateUploading, Version: 2, Attempt: 1,
		LeaseExpiresAt: &lease,
	}}, object, now)
	if err != nil {
		t.Fatalf("NewObjectReconciliationForObject() error = %v", err)
	}
	record := obligation.Record()
	if record.UploadID != uploadID || record.EvidenceID != evidenceID ||
		record.Object != object.Record() || record.State != ReconciliationPending ||
		!record.AvailableAt.Equal(lease) {
		t.Fatalf("orphan obligation = %+v", record)
	}
	lateAt := lease.Add(time.Second)
	late, err := NewObjectReconciliationForObject(Upload{record: UploadRecord{
		ID: uploadID, TenantID: tenantID, EvidenceID: evidenceID,
		State: UploadStateUploading, Version: 2, Attempt: 1,
		LeaseExpiresAt: &lease,
	}}, object, lateAt)
	if err != nil {
		t.Fatalf("NewObjectReconciliationForObject(late) error = %v", err)
	}
	if !late.Record().AvailableAt.Equal(lateAt) {
		t.Fatalf("late AvailableAt = %v, want %v", late.Record().AvailableAt, lateAt)
	}
}

func TestReconciliationServiceUsesAuthoritativeAcceptanceBeforeDisposition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		accepted   bool
		want       ReconciliationState
		wantDelete int
	}{
		{name: "accepted exact object is retained", accepted: true, want: ReconciliationRetained},
		{name: "unaccepted absent evidence is deleted", want: ReconciliationDeleted, wantDelete: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
			pending := reconciliationFixture(t, now)
			claimed, err := pending.ClaimForRecovery(now.Add(2*time.Minute), time.Minute)
			if err != nil {
				t.Fatalf("ClaimForRecovery() error = %v", err)
			}
			record := claimed.Record()
			uploadState := UploadStateUploading
			assetErr := ErrNotFound
			asset := Asset{}
			if test.accepted {
				uploadState = UploadStateAccepted
				assetErr = nil
				asset = Asset{
					record:  Record{ID: record.EvidenceID},
					content: Content{object: claimed.Object()},
				}
			}
			queue := &reconciliationQueueStub{claimed: claimed}
			objects := &reconciliationObjectStub{}
			service, err := NewReconciliationService(
				queue,
				reconciliationUploadStub{upload: Upload{record: UploadRecord{
					ID: record.UploadID, TenantID: record.TenantID, EvidenceID: record.EvidenceID,
					State: uploadState, Attempt: record.UploadAttempt,
				}}},
				reconciliationAssetStub{asset: asset, err: assetErr}, objects,
				reconciliationClock{now: now.Add(3 * time.Minute)}, time.Minute, time.Minute, time.Minute,
			)
			if err != nil {
				t.Fatalf("NewReconciliationService() error = %v", err)
			}
			scope, err := tenant.NewScope(record.TenantID)
			if err != nil {
				t.Fatalf("NewScope() error = %v", err)
			}
			got, err := service.ReconcileNext(context.Background(), scope)
			if err != nil {
				t.Fatalf("ReconcileNext() error = %v", err)
			}
			if got != test.want || queue.completed != test.want || objects.deletes != test.wantDelete {
				t.Fatalf("state=%q completed=%q deletes=%d", got, queue.completed, objects.deletes)
			}
		})
	}
}

func TestReconciliationServiceRetriesInconsistentAcceptedStateWithoutDeleting(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 29, 10, 0, 0, 0, time.UTC)
	pending := reconciliationFixture(t, now)
	claimed, err := pending.ClaimForRecovery(now.Add(2*time.Minute), time.Minute)
	if err != nil {
		t.Fatalf("ClaimForRecovery() error = %v", err)
	}
	record := claimed.Record()
	queue := &reconciliationQueueStub{claimed: claimed}
	objects := &reconciliationObjectStub{}
	service, err := NewReconciliationService(
		queue,
		reconciliationUploadStub{upload: Upload{record: UploadRecord{
			ID: record.UploadID, TenantID: record.TenantID, EvidenceID: record.EvidenceID,
			State: UploadStateAccepted, Attempt: record.UploadAttempt,
		}}},
		reconciliationAssetStub{err: ErrNotFound}, objects,
		reconciliationClock{now: now.Add(3 * time.Minute)}, time.Minute, time.Minute, time.Minute,
	)
	if err != nil {
		t.Fatalf("NewReconciliationService() error = %v", err)
	}
	scope, _ := tenant.NewScope(record.TenantID)
	if _, err := service.ReconcileNext(context.Background(), scope); err == nil {
		t.Fatal("ReconcileNext(inconsistent accepted state) error = nil")
	}
	if queue.retries != 1 || objects.deletes != 0 || queue.completed != "" {
		t.Fatalf("retries=%d deletes=%d completed=%q", queue.retries, objects.deletes, queue.completed)
	}
}

func reconciliationFixture(t *testing.T, now time.Time) ObjectReconciliation {
	t.Helper()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	uploadID, _ := id.ParseUpload("upl_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	evidenceID, _ := id.ParseEvidence("evd_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	obligation, err := RestoreObjectReconciliation(ReconciliationRecord{
		TenantID: tenantID, UploadID: uploadID, EvidenceID: evidenceID, UploadAttempt: 1,
		Object: objectstore.ObjectRecord{
			Key: "tenants/ten/evidence/evd/content/1", Version: "version-1", Size: 42,
			Checksum: string(platformcrypto.Sum([]byte("ciphertext"))),
		},
		State: ReconciliationPending, Version: 1, AvailableAt: now.Add(2 * time.Minute),
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("RestoreObjectReconciliation() error = %v", err)
	}
	return obligation
}

type reconciliationQueueStub struct {
	claimed   ObjectReconciliation
	completed ReconciliationState
	retries   int
}

func (stub *reconciliationQueueStub) ClaimObjectReconciliation(context.Context, tenant.Scope, time.Time, time.Duration) (ObjectReconciliation, error) {
	return stub.claimed, nil
}

func (stub *reconciliationQueueStub) RetryObjectReconciliation(context.Context, tenant.Scope, ObjectReconciliation, time.Time, time.Duration) error {
	stub.retries++
	return nil
}

func (stub *reconciliationQueueStub) CompleteObjectReconciliation(_ context.Context, _ tenant.Scope, _ ObjectReconciliation, state ReconciliationState, _ time.Time) error {
	stub.completed = state
	return nil
}

type reconciliationUploadStub struct{ upload Upload }

func (stub reconciliationUploadStub) FindUpload(context.Context, tenant.Scope, id.Upload) (Upload, error) {
	return stub.upload, nil
}

type reconciliationAssetStub struct {
	asset Asset
	err   error
}

func (stub reconciliationAssetStub) Find(context.Context, tenant.Scope, id.Evidence) (Asset, error) {
	return stub.asset, stub.err
}

type reconciliationObjectStub struct{ deletes int }

func (stub *reconciliationObjectStub) Delete(context.Context, objectstore.Object) error {
	stub.deletes++
	return nil
}

type reconciliationClock struct{ now time.Time }

func (source reconciliationClock) Now() time.Time { return source.now }
