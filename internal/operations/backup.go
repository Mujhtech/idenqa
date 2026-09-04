// Package operations owns open-source recovery and diagnostic contracts.
package operations

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"time"
)

const (
	// BackupSchemaVersion is the exact initial portable manifest version.
	BackupSchemaVersion = 1
	// MaximumManifestBytes bounds operator-controlled input.
	MaximumManifestBytes = 1 << 20
)

var (
	// ErrInvalid means backup evidence is malformed or incomplete.
	ErrInvalid = errors.New("operations: invalid backup manifest")
	// ErrIntegrity means a restored artifact differs from its manifest.
	ErrIntegrity = errors.New("operations: backup integrity failure")
)

// Artifact is one required, path-local backup component.
type Artifact struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// BackupManifest closes over a region-pinned recoverable core snapshot.
type BackupManifest struct {
	SchemaVersion  uint16     `json:"schema_version"`
	Region         string     `json:"region"`
	DatabaseSchema uint       `json:"database_schema"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      time.Time  `json:"expires_at"`
	Artifacts      []Artifact `json:"artifacts"`
}

// Validate rejects traversal, missing recovery components, and retention drift.
func (manifest BackupManifest) Validate() error {
	if manifest.SchemaVersion != BackupSchemaVersion || !safeToken(manifest.Region, 63) || manifest.DatabaseSchema == 0 ||
		manifest.CreatedAt.IsZero() || manifest.CreatedAt.Location() != time.UTC || manifest.ExpiresAt.Location() != time.UTC ||
		manifest.ExpiresAt.Sub(manifest.CreatedAt) != 35*24*time.Hour || len(manifest.Artifacts) < 4 || len(manifest.Artifacts) > 10_000 {
		return ErrInvalid
	}
	required := map[string]bool{"postgres": false, "evidence": false, "keyring": false, "audit": false}
	seen := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		if !safeToken(artifact.Kind, 64) || !validLocalPath(artifact.Path) || len(artifact.SHA256) != 64 || artifact.Bytes < 0 {
			return ErrInvalid
		}
		if _, err := hex.DecodeString(artifact.SHA256); err != nil {
			return ErrInvalid
		}
		if _, exists := seen[artifact.Path]; exists {
			return ErrInvalid
		}
		seen[artifact.Path] = struct{}{}
		if _, exists := required[artifact.Kind]; exists {
			required[artifact.Kind] = true
		}
	}
	for _, present := range required {
		if !present {
			return ErrInvalid
		}
	}
	return nil
}

// Report contains no backup content or operator paths.
type Report struct {
	Region         string    `json:"region"`
	DatabaseSchema uint      `json:"database_schema"`
	Artifacts      int       `json:"artifacts"`
	Bytes          int64     `json:"bytes"`
	VerifiedAt     time.Time `json:"verified_at"`
}

// ReconciliationReport is a payload-free restore/readiness snapshot.
type ReconciliationReport struct {
	PendingOutbox           int64     `json:"pending_outbox"`
	EvidenceObligations     int64     `json:"evidence_obligations"`
	VerificationObligations int64     `json:"verification_obligations"`
	DeletionWorkflows       int64     `json:"deletion_workflows"`
	ExpiredLeases           int64     `json:"expired_leases"`
	TombstoneGaps           int64     `json:"tombstone_gaps"`
	AuditHeadGaps           int64     `json:"audit_head_gaps"`
	CheckedAt               time.Time `json:"checked_at"`
}

// Ready reports whether structural restore reconciliation found no safety gaps.
// Pending durable work is observable but does not itself indicate corruption.
func (report ReconciliationReport) Ready() bool {
	return report.ExpiredLeases == 0 && report.TombstoneGaps == 0 && report.AuditHeadGaps == 0
}

// VerifyDirectory validates exact restored files beneath root without
// following an operator path outside that directory.
func VerifyDirectory(ctx context.Context, root string, manifest BackupManifest, now time.Time) (report Report, resultErr error) {
	if ctx == nil || root == "" || now.IsZero() || now.Location() != time.UTC || manifest.Validate() != nil {
		return Report{}, ErrInvalid
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return Report{}, fmt.Errorf("open backup root: %w", err)
	}
	defer func() {
		if closeErr := directory.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("close backup root: %w", closeErr))
		}
	}()
	artifacts := append([]Artifact(nil), manifest.Artifacts...)
	slices.SortFunc(artifacts, func(left, right Artifact) int { return compare(left.Path, right.Path) })
	var total int64
	for _, artifact := range artifacts {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		file, err := directory.Open(artifact.Path)
		if err != nil {
			return Report{}, errors.Join(ErrIntegrity, err)
		}
		digest := sha256.New()
		read, copyErr := io.Copy(digest, io.LimitReader(file, artifact.Bytes+1))
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil || read != artifact.Bytes || hex.EncodeToString(digest.Sum(nil)) != artifact.SHA256 {
			return Report{}, ErrIntegrity
		}
		total += read
	}
	return Report{Region: manifest.Region, DatabaseSchema: manifest.DatabaseSchema, Artifacts: len(artifacts), Bytes: total, VerifiedAt: now}, nil
}

// DecodeManifest strictly decodes one bounded canonical JSON manifest.
func DecodeManifest(encoded []byte) (BackupManifest, error) {
	if len(encoded) == 0 || len(encoded) > MaximumManifestBytes {
		return BackupManifest{}, ErrInvalid
	}
	var manifest BackupManifest
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return BackupManifest{}, ErrInvalid
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return BackupManifest{}, ErrInvalid
	}
	return manifest, manifest.Validate()
}

func validLocalPath(value string) bool {
	return value != "" && value[0] != '/' && value != "." && value != ".." && !containsTraversal(value)
}
func containsTraversal(value string) bool {
	start := 0
	for index := 0; index <= len(value); index++ {
		if index == len(value) || value[index] == '/' {
			part := value[start:index]
			if part == "" || part == "." || part == ".." {
				return true
			}
			start = index + 1
		}
	}
	return false
}
func safeToken(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}
func compare(left, right string) int {
	if left < right {
		return -1
	}
	if left > right {
		return 1
	}
	return 0
}
