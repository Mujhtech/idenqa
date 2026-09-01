// Package audit owns append-only consequential history and verification.
package audit

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// SchemaVersion is the exact portable audit-export format version.
const SchemaVersion = 1

// MaximumExportBytes bounds offline verification memory.
const MaximumExportBytes = 16 << 20

var (
	// ErrInvalid means the export does not satisfy the closed contract.
	ErrInvalid = errors.New("audit: invalid data")
	// ErrChain means cryptographic or sequence verification failed.
	ErrChain      = errors.New("audit: chain verification failed")
	tokenPattern  = regexp.MustCompile(`^[a-z][a-z0-9_.:-]{0,127}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Record is one tenant-sequenced reference-only audit event.
type Record struct {
	Sequence     uint64    `json:"sequence"`
	EventID      string    `json:"event_id"`
	EventType    string    `json:"event_type"`
	AggregateID  string    `json:"aggregate_id"`
	ActorID      string    `json:"actor_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	EventDigest  string    `json:"event_digest"`
	PreviousHash string    `json:"previous_hash"`
	Hash         string    `json:"hash"`
}

// Append constructs exactly the next record in a tenant chain.
func Append(previous *Record, eventID, eventType, aggregateID, actorID, eventDigest string, occurredAt time.Time) (Record, error) {
	sequence, previousHash := uint64(1), zeroHash()
	if previous != nil {
		sequence, previousHash = previous.Sequence+1, previous.Hash
	}
	record := Record{Sequence: sequence, EventID: eventID, EventType: eventType, AggregateID: aggregateID, ActorID: actorID, OccurredAt: occurredAt, EventDigest: eventDigest, PreviousHash: previousHash}
	if err := validateRecordFields(record); err != nil {
		return Record{}, err
	}
	record.Hash = recordHash(record)
	return record, nil
}

// Checkpoint signs one exact tenant chain head.
type Checkpoint struct {
	TenantID        string    `json:"tenant_id"`
	ThroughSequence uint64    `json:"through_sequence"`
	ChainHash       string    `json:"chain_hash"`
	KeyID           string    `json:"key_id"`
	CreatedAt       time.Time `json:"created_at"`
	Signature       string    `json:"signature"`
}

// SignCheckpoint signs the exact tenant sequence and chain head.
func SignCheckpoint(tenantID, keyID string, head Record, createdAt time.Time, privateKey ed25519.PrivateKey) (Checkpoint, error) {
	checkpoint := Checkpoint{TenantID: tenantID, ThroughSequence: head.Sequence, ChainHash: head.Hash, KeyID: keyID, CreatedAt: createdAt}
	if len(privateKey) != ed25519.PrivateKeySize || !validCheckpoint(checkpoint, false) {
		return Checkpoint{}, ErrInvalid
	}
	checkpoint.Signature = hex.EncodeToString(ed25519.Sign(privateKey, checkpointMessage(checkpoint)))
	return checkpoint, nil
}

// Export is the portable, closed audit verification format.
type Export struct {
	SchemaVersion uint16       `json:"schema_version"`
	TenantID      string       `json:"tenant_id"`
	Records       []Record     `json:"records"`
	Checkpoints   []Checkpoint `json:"checkpoints"`
}

// Report contains no event payloads.
type Report struct {
	TenantID     string `json:"tenant_id"`
	Records      int    `json:"records"`
	Checkpoints  int    `json:"checkpoints"`
	LastSequence uint64 `json:"last_sequence"`
	ChainHash    string `json:"chain_hash"`
}

// Verify detects gaps, reorder, mutation, wrong keys, and invalid checkpoints.
func Verify(export Export, keys map[string]ed25519.PublicKey) (Report, error) {
	if export.SchemaVersion != SchemaVersion || export.TenantID == "" || len(export.Records) == 0 || len(export.Checkpoints) == 0 {
		return Report{}, ErrInvalid
	}
	previousHash := zeroHash()
	for index, record := range export.Records {
		if record.Sequence != uint64(index+1) || record.PreviousHash != previousHash || validateRecordFields(record) != nil || record.Hash != recordHash(record) {
			return Report{}, fmt.Errorf("%w at sequence %d", ErrChain, record.Sequence)
		}
		previousHash = record.Hash
	}
	for _, checkpoint := range export.Checkpoints {
		if checkpoint.TenantID != export.TenantID || !validCheckpoint(checkpoint, true) || checkpoint.ThroughSequence > uint64(len(export.Records)) || export.Records[checkpoint.ThroughSequence-1].Hash != checkpoint.ChainHash {
			return Report{}, ErrChain
		}
		publicKey := keys[checkpoint.KeyID]
		signature, err := hex.DecodeString(checkpoint.Signature)
		if err != nil || len(publicKey) != ed25519.PublicKeySize || !ed25519.Verify(publicKey, checkpointMessage(checkpoint), signature) {
			return Report{}, ErrChain
		}
	}
	for index := 1; index < len(export.Checkpoints); index++ {
		if export.Checkpoints[index].ThroughSequence <= export.Checkpoints[index-1].ThroughSequence ||
			export.Checkpoints[index].CreatedAt.Before(export.Checkpoints[index-1].CreatedAt) {
			return Report{}, ErrChain
		}
	}
	head := export.Records[len(export.Records)-1]
	return Report{TenantID: export.TenantID, Records: len(export.Records), Checkpoints: len(export.Checkpoints), LastSequence: head.Sequence, ChainHash: head.Hash}, nil
}

func validateRecordFields(record Record) error {
	if record.Sequence == 0 || !tokenPattern.MatchString(record.EventType) || record.EventID == "" || record.AggregateID == "" || record.ActorID == "" || record.OccurredAt.IsZero() || record.OccurredAt.Location() != time.UTC || !digestPattern.MatchString(record.EventDigest) || !digestPattern.MatchString(record.PreviousHash) {
		return ErrInvalid
	}
	return nil
}

func recordHash(record Record) string {
	encoded, _ := json.Marshal(struct {
		Schema       uint16    `json:"schema"`
		Sequence     uint64    `json:"sequence"`
		EventID      string    `json:"event_id"`
		EventType    string    `json:"event_type"`
		AggregateID  string    `json:"aggregate_id"`
		ActorID      string    `json:"actor_id"`
		OccurredAt   time.Time `json:"occurred_at"`
		EventDigest  string    `json:"event_digest"`
		PreviousHash string    `json:"previous_hash"`
	}{SchemaVersion, record.Sequence, record.EventID, record.EventType, record.AggregateID, record.ActorID, record.OccurredAt, record.EventDigest, record.PreviousHash})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func checkpointMessage(value Checkpoint) []byte {
	encoded, _ := json.Marshal(struct {
		Schema          uint16    `json:"schema"`
		TenantID        string    `json:"tenant_id"`
		ThroughSequence uint64    `json:"through_sequence"`
		ChainHash       string    `json:"chain_hash"`
		KeyID           string    `json:"key_id"`
		CreatedAt       time.Time `json:"created_at"`
	}{SchemaVersion, value.TenantID, value.ThroughSequence, value.ChainHash, value.KeyID, value.CreatedAt})
	return encoded
}
func validCheckpoint(value Checkpoint, signature bool) bool {
	return value.TenantID != "" && value.ThroughSequence > 0 && digestPattern.MatchString(value.ChainHash) && tokenPattern.MatchString(value.KeyID) && !value.CreatedAt.IsZero() && value.CreatedAt.Location() == time.UTC && (!signature || len(value.Signature) == ed25519.SignatureSize*2)
}
func zeroHash() string {
	return string(make([]byte, 0)) + "0000000000000000000000000000000000000000000000000000000000000000"
}
