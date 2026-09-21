package experience

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
)

func TestExportAndImportCreateDraft(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	ctx := context.Background()

	created := mustPublish(t, environment, mustCreateDraft(t, environment))
	manifest, err := environment.service.Export(ctx, environment.authority, created.ID)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	raw, err := contract.EncodeManifest(manifest)
	if err != nil {
		t.Fatalf("EncodeManifest() error = %v", err)
	}

	// Import into a second tenant creates a fresh draft with the same stable id.
	other := newTestEnvironment(t)
	imported, err := other.service.Import(ctx, other.authority, raw)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if imported.ID.String() != created.ID.String() || imported.State != StateDraft || imported.LatestVersion != 1 {
		t.Fatalf("imported = %+v", imported)
	}
	if imported.ID.String() == "" {
		t.Fatal("imported aggregate lost its stable id")
	}

	// Importing again advances the draft revision instead of duplicating.
	again, err := other.service.Import(ctx, other.authority, raw)
	if err != nil {
		t.Fatalf("Import() again error = %v", err)
	}
	if again.LatestVersion != 2 || again.State != StateDraft {
		t.Fatalf("second import = %+v", again)
	}
}

func TestImportRejectsTamperedAndUnknownDocuments(t *testing.T) {
	t.Parallel()
	environment := newTestEnvironment(t)
	ctx := context.Background()
	created := mustPublish(t, environment, mustCreateDraft(t, environment))
	manifest, err := environment.service.Export(ctx, environment.authority, created.ID)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	other := newTestEnvironment(t)

	tampered := manifest
	tampered.Document.Name = "Hijacked"
	raw, _ := contract.EncodeManifest(tampered)
	if _, err := other.service.Import(ctx, other.authority, raw); !errors.Is(err, ErrSignature) {
		t.Fatalf("Import() tampered error = %v, want ErrSignature", err)
	}

	wrongKey := manifest
	wrongKey.KeyID = "expkey_unknown_1"
	raw, _ = contract.EncodeManifest(wrongKey)
	if _, err := other.service.Import(ctx, other.authority, raw); !errors.Is(err, ErrSignature) {
		t.Fatalf("Import() wrong key error = %v, want ErrSignature", err)
	}

	document := SafeDefaultDocument()
	document.MandatoryCopyVersion = "mc-1999-01-01"
	seed := make([]byte, 32)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	signed, err := contract.SignDocument(document, "expkey_fake_1", ed25519.NewKeyFromSeed(seed))
	if err != nil {
		t.Fatalf("SignDocument() error = %v", err)
	}
	raw, _ = contract.EncodeManifest(signed)
	if _, err := other.service.Import(ctx, other.authority, raw); !errors.Is(err, ErrUnknownMandatoryCopy) {
		t.Fatalf("Import() unknown mandatory error = %v, want ErrUnknownMandatoryCopy", err)
	}
}
