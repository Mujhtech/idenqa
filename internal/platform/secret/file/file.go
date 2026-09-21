// Package file resolves secret://file references from operator-mounted
// owner-only files. It is the unchanged open-source default and preserves the
// existing mounted-credential security posture.
package file

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

const maximumFileSize = secret.MaxPayloadBytes

// Resolver reads mounted secret files.
type Resolver struct{}

// New constructs the mounted-file resolver.
func New() *Resolver { return &Resolver{} }

// Resolve reads the exact absolute path named by a secret://file reference.
func (resolver *Resolver) Resolve(ctx context.Context, reference secret.Reference) (secret.Value, error) {
	if resolver == nil || ctx == nil || reference.IsZero() {
		return secret.Value{}, secret.ErrInvalid
	}
	if reference.Provider() != "file" || reference.Version() != "" {
		return secret.Value{}, secret.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return secret.Value{}, err
	}
	path := filepath.Clean(reference.Path())
	if !filepath.IsAbs(path) {
		return secret.Value{}, secret.ErrInvalid
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return secret.Value{}, secret.ErrNotFound
	}
	if err != nil {
		return secret.Value{}, secret.ErrUnavailable
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 ||
		info.Size() <= 0 || info.Size() > maximumFileSize {
		return secret.Value{}, secret.ErrInvalid
	}
	file, err := os.Open(path) // #nosec G304 -- operator-mounted reference path, never request input.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return secret.Value{}, secret.ErrNotFound
		}
		if errors.Is(err, os.ErrPermission) {
			return secret.Value{}, secret.ErrDenied
		}
		return secret.Value{}, secret.ErrUnavailable
	}
	defer func() { _ = file.Close() }()

	raw, err := io.ReadAll(io.LimitReader(file, maximumFileSize+1))
	if err != nil || len(raw) == 0 || len(raw) > maximumFileSize {
		clear(raw)
		return secret.Value{}, secret.ErrInvalid
	}
	digest := sha256.Sum256(raw)
	value, err := secret.NewValue(reference, "sha256:"+hex.EncodeToString(digest[:]), raw)
	clear(raw)
	if err != nil {
		return secret.Value{}, secret.ErrInvalid
	}

	return value, nil
}
