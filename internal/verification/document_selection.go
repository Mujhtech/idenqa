package verification

import (
	"errors"
	"maps"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/evidence"
)

// DocumentOption is a profile-pinned document branch, not evidence assurance.
type DocumentOption struct {
	ID        string          `json:"id"`
	Label     string          `json:"label"`
	Artefacts []evidence.Name `json:"artefacts"`
}

func cloneDocumentOptions(options []DocumentOption) []DocumentOption {
	result := slices.Clone(options)
	for i := range result {
		result[i].Artefacts = slices.Clone(result[i].Artefacts)
	}
	return result
}

func validateDocumentOptions(requirement Requirement) error {
	if requirement.DocumentOptions == nil {
		return nil
	}
	if requirement.EvidenceType != evidence.EvidenceDocumentImage || len(requirement.DocumentOptions) < 1 || len(requirement.DocumentOptions) > 16 {
		return errors.New("capture document options require 1..16 document image branches")
	}
	seen := map[string]bool{}
	union := map[evidence.Name]bool{}
	for _, option := range requirement.DocumentOptions {
		if !validRequirementKey(option.ID) || seen[option.ID] || strings.TrimSpace(option.Label) != option.Label || option.Label == "" || !utf8.ValidString(option.Label) || utf8.RuneCountInString(option.Label) > 80 || strings.ContainsFunc(option.Label, unicode.IsControl) {
			return errors.New("capture document option identity or label is invalid")
		}
		seen[option.ID] = true
		if len(option.Artefacts) < 1 || len(option.Artefacts) > 2 || !slices.Contains(option.Artefacts, evidence.ArtefactDocumentFront) {
			return errors.New("capture document option requires document front and optional back")
		}
		local := map[evidence.Name]bool{}
		for _, artefact := range option.Artefacts {
			if local[artefact] || (artefact != evidence.ArtefactDocumentFront && artefact != evidence.ArtefactDocumentBack) {
				return errors.New("capture document option artefact is invalid")
			}
			local[artefact], union[artefact] = true, true
		}
	}
	if len(union) != len(requirement.Artefacts) {
		return errors.New("capture document option union differs from requirement artefacts")
	}
	for _, artefact := range requirement.Artefacts {
		if !union[artefact] {
			return errors.New("capture document option union differs from requirement artefacts")
		}
	}
	return nil
}

// EffectiveArtefacts resolves the only artefacts authorised by a durable choice.
// An unselected document requirement has no authorised artefacts.
func EffectiveArtefacts(requirement Requirement, selections map[string]string) []evidence.Name {
	if len(requirement.DocumentOptions) == 0 {
		return slices.Clone(requirement.Artefacts)
	}
	for _, option := range requirement.DocumentOptions {
		if option.ID == selections[requirement.Key] {
			return slices.Clone(option.Artefacts)
		}
	}
	return nil
}

// DocumentSelections returns a defensive copy of mutable session choices.
func (session Session) DocumentSelections() map[string]string {
	return maps.Clone(session.documentSelections)
}

// WithCaptureCompletion restores the durable capture-completion marker without
// changing lifecycle or immutable requirements. Completion closes selection.
func (session Session) WithCaptureCompletion(at time.Time) (Session, error) {
	if at.IsZero() || at.Before(session.createdAt) {
		return Session{}, ErrSessionConflict
	}
	at = at.UTC()
	session.captureCompletedAt = &at
	return session, nil
}

// WithDocumentSelections restores choices separately from the immutable snapshot.
func (session Session) WithDocumentSelections(selections map[string]string) (Session, error) {
	for key, value := range selections {
		found := false
		for _, requirement := range session.requirements.Requirements {
			if requirement.Key != key {
				continue
			}
			for _, option := range requirement.DocumentOptions {
				if option.ID == value {
					found = true
				}
			}
		}
		if !found {
			return Session{}, ErrSessionConflict
		}
	}
	session.documentSelections = maps.Clone(selections)
	return session, nil
}

// SelectDocument activates a pinned branch before any upload intent exists.
func (session Session) SelectDocument(key, documentType string, expectedVersion int64, hasUploadIntent bool, now time.Time) (Session, error) {
	if session.version != expectedVersion || !session.AcceptsCaptureAt(now) || session.captureCompletedAt != nil || now.Before(session.updatedAt) {
		return Session{}, ErrSessionConflict
	}
	if hasUploadIntent && session.documentSelections[key] != documentType {
		return Session{}, ErrSessionConflict
	}
	selections := session.DocumentSelections()
	if selections == nil {
		selections = map[string]string{}
	}
	selections[key] = documentType
	updated, err := session.WithDocumentSelections(selections)
	if err != nil {
		return Session{}, err
	}
	updated.version++
	updated.updatedAt = now.UTC()
	return updated, nil
}
