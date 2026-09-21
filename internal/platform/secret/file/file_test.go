package file

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

func TestResolverReadsOwnerOnlyFile(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	path := filepath.Join(directory, "credential")
	if err := os.WriteFile(path, []byte("idq_wrk_v1_secret"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	reference, err := secret.ParseReference("secret://file" + path)
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	value, err := New().Resolve(context.Background(), reference)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	text, err := value.Text()
	if err != nil || text != "idq_wrk_v1_secret" {
		t.Fatalf("Text() = %q, %v", text, err)
	}
	if value.Version() == "" {
		t.Fatal("Resolve() returned an empty content version")
	}
}

func TestResolverFailsClosed(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	writable := filepath.Join(directory, "writable")
	if err := os.WriteFile(writable, []byte("value"), 0o644); err != nil { //nolint:gosec // deliberately group-readable to prove the resolver rejects it.
		t.Fatalf("WriteFile() error = %v", err)
	}
	empty := filepath.Join(directory, "empty")
	if err := os.WriteFile(empty, []byte{}, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missing, _ := secret.ParseReference("secret://file" + filepath.Join(directory, "missing"))
	versioned, _ := secret.ParseReference("secret://file" + writable + "?version=1")
	other, _ := secret.ParseReference("secret://aws/prod/runner")
	writableReference, _ := secret.ParseReference("secret://file" + writable)
	emptyReference, _ := secret.ParseReference("secret://file" + empty)

	tests := []struct {
		name      string
		reference secret.Reference
		wantErr   error
	}{
		{name: "missing", reference: missing, wantErr: secret.ErrNotFound},
		{name: "group readable", reference: writableReference, wantErr: secret.ErrInvalid},
		{name: "empty", reference: emptyReference, wantErr: secret.ErrInvalid},
		{name: "version selector", reference: versioned, wantErr: secret.ErrInvalid},
		{name: "wrong provider", reference: other, wantErr: secret.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := New().Resolve(context.Background(), test.reference); !errors.Is(err, test.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
