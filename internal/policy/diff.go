package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Revision-diff limits intentionally bound both the change count and the
// encoded output independently of any single revision's size.
const (
	MaximumRevisionDiffChanges = 256
	MaximumRevisionDiffBytes   = 512 * 1024
)

// RevisionDiffKind is one closed structural change vocabulary.
type RevisionDiffKind string

// Revision-diff kinds form the complete structural-change vocabulary.
const (
	RevisionDiffAdded   RevisionDiffKind = "added"
	RevisionDiffRemoved RevisionDiffKind = "removed"
	RevisionDiffChanged RevisionDiffKind = "changed"
)

// RevisionDiffRevision identifies one side of a revision diff without source.
type RevisionDiffRevision struct {
	Revision uint32 `json:"revision"`
	Digest   string `json:"digest"`
}

// RevisionDiffChange is one bounded canonical JSON-pointer change. Old and New
// carry canonical fragments of the compared documents and never secrets.
type RevisionDiffChange struct {
	Path string           `json:"path"`
	Kind RevisionDiffKind `json:"kind"`
	Old  json.RawMessage  `json:"old,omitempty"`
	New  json.RawMessage  `json:"new,omitempty"`
}

// RevisionDiff is the deterministic structural difference between two stored
// revisions of the same policy. Identity fields are reported on From and To
// and are never repeated as changes.
type RevisionDiff struct {
	SchemaMajor uint16               `json:"schema_major"`
	SchemaMinor uint16               `json:"schema_minor"`
	PolicyID    string               `json:"policy_id"`
	From        RevisionDiffRevision `json:"from"`
	To          RevisionDiffRevision `json:"to"`
	Identical   bool                 `json:"identical"`
	Truncated   bool                 `json:"truncated"`
	ChangeCount int                  `json:"change_count"`
	Changes     []RevisionDiffChange `json:"changes"`
	Digest      string               `json:"digest"`
}

type revisionDiffPayload struct {
	SchemaMajor uint16               `json:"schema_major"`
	SchemaMinor uint16               `json:"schema_minor"`
	PolicyID    string               `json:"policy_id"`
	From        RevisionDiffRevision `json:"from"`
	To          RevisionDiffRevision `json:"to"`
	Identical   bool                 `json:"identical"`
	Truncated   bool                 `json:"truncated"`
	ChangeCount int                  `json:"change_count"`
	Changes     []RevisionDiffChange `json:"changes"`
}

// DiffRevisions computes a bounded canonical structural diff between two
// stored revisions of one policy. It reads only already-validated canonical
// meaning and never loads expressions into an engine.
func DiffRevisions(from, to Revision) (RevisionDiff, error) {
	if from.reference.ID.IsZero() || to.reference.ID.IsZero() {
		return RevisionDiff{}, fmt.Errorf("%w: revision diff identity", ErrInvalid)
	}
	if from.reference.ID != to.reference.ID {
		return RevisionDiff{}, fmt.Errorf("%w: revision diff policy", ErrConflict)
	}
	fromValue, err := diffDocument(from.canonical)
	if err != nil {
		return RevisionDiff{}, fmt.Errorf("%w: diff source revision", ErrRevisionConflict)
	}
	toValue, err := diffDocument(to.canonical)
	if err != nil {
		return RevisionDiff{}, fmt.Errorf("%w: diff target revision", ErrRevisionConflict)
	}
	for _, identity := range []string{"schema_major", "schema_minor", "policy_id", "revision"} {
		delete(fromValue, identity)
		delete(toValue, identity)
	}

	changes := make([]RevisionDiffChange, 0)
	count := 0
	if err := collectRevisionDiff("", fromValue, toValue, &changes, &count); err != nil {
		return RevisionDiff{}, fmt.Errorf("%w: revision diff", ErrRevisionConflict)
	}
	truncated := count > MaximumRevisionDiffChanges
	if truncated {
		changes = changes[:MaximumRevisionDiffChanges]
	}
	payload := revisionDiffPayload{
		SchemaMajor: from.reference.SchemaMajor, SchemaMinor: from.reference.SchemaMinor,
		PolicyID:  from.reference.ID.String(),
		From:      RevisionDiffRevision{Revision: from.reference.Revision, Digest: from.reference.Digest},
		To:        RevisionDiffRevision{Revision: to.reference.Revision, Digest: to.reference.Digest},
		Identical: count == 0, Truncated: truncated, ChangeCount: count,
		Changes: changes,
	}
	encoded, err := json.Marshal(payload)
	if err != nil || len(encoded) > MaximumRevisionDiffBytes {
		return RevisionDiff{}, fmt.Errorf("%w: revision diff encoding", ErrRevisionConflict)
	}
	sum := sha256.Sum256(encoded)
	return RevisionDiff{
		SchemaMajor: payload.SchemaMajor, SchemaMinor: payload.SchemaMinor,
		PolicyID: payload.PolicyID, From: payload.From, To: payload.To,
		Identical: payload.Identical, Truncated: payload.Truncated,
		ChangeCount: payload.ChangeCount, Changes: payload.Changes,
		Digest: hex.EncodeToString(sum[:]),
	}, nil
}

func diffDocument(canonical []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil || value == nil {
		return nil, ErrInvalid
	}
	return value, nil
}

func collectRevisionDiff(
	path string,
	from, to any,
	changes *[]RevisionDiffChange,
	count *int,
) error {
	if reflect.DeepEqual(from, to) {
		return nil
	}
	fromObject, fromOK := from.(map[string]any)
	toObject, toOK := to.(map[string]any)
	if fromOK || toOK {
		if !fromOK || !toOK {
			return appendRevisionDiffChange(changes, count, path, RevisionDiffChanged, from, to)
		}
		keys := make([]string, 0, len(fromObject)+len(toObject))
		for key := range fromObject {
			keys = append(keys, key)
		}
		for key := range toObject {
			if _, exists := fromObject[key]; !exists {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			fromValue, fromExists := fromObject[key]
			toValue, toExists := toObject[key]
			childPath := path + "/" + escapeRevisionDiffPointer(key)
			switch {
			case !toExists:
				if err := appendRevisionDiffChange(changes, count, childPath, RevisionDiffRemoved, fromValue, nil); err != nil {
					return err
				}
			case !fromExists:
				if err := appendRevisionDiffChange(changes, count, childPath, RevisionDiffAdded, nil, toValue); err != nil {
					return err
				}
			default:
				if err := collectRevisionDiff(childPath, fromValue, toValue, changes, count); err != nil {
					return err
				}
			}
		}
		return nil
	}
	fromList, fromOK := from.([]any)
	toList, toOK := to.([]any)
	if !fromOK || !toOK {
		return appendRevisionDiffChange(changes, count, path, RevisionDiffChanged, from, to)
	}
	for index := range max(len(fromList), len(toList)) {
		childPath := path + "/" + strconv.Itoa(index)
		switch {
		case index >= len(toList):
			if err := appendRevisionDiffChange(changes, count, childPath, RevisionDiffRemoved, fromList[index], nil); err != nil {
				return err
			}
		case index >= len(fromList):
			if err := appendRevisionDiffChange(changes, count, childPath, RevisionDiffAdded, nil, toList[index]); err != nil {
				return err
			}
		default:
			if err := collectRevisionDiff(childPath, fromList[index], toList[index], changes, count); err != nil {
				return err
			}
		}
	}
	return nil
}

func appendRevisionDiffChange(
	changes *[]RevisionDiffChange,
	count *int,
	path string,
	kind RevisionDiffKind,
	from, to any,
) error {
	*count++
	if len(*changes) >= MaximumRevisionDiffChanges {
		return nil
	}
	change := RevisionDiffChange{Path: path, Kind: kind}
	switch kind {
	case RevisionDiffAdded:
		encoded, err := json.Marshal(to)
		if err != nil {
			return err
		}
		change.New = encoded
	case RevisionDiffRemoved:
		encoded, err := json.Marshal(from)
		if err != nil {
			return err
		}
		change.Old = encoded
	default:
		oldEncoded, err := json.Marshal(from)
		if err != nil {
			return err
		}
		newEncoded, err := json.Marshal(to)
		if err != nil {
			return err
		}
		change.Old, change.New = oldEncoded, newEncoded
	}
	*changes = append(*changes, change)
	return nil
}

func escapeRevisionDiffPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
