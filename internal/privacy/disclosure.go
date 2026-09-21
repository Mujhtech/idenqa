package privacy

import (
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// DisclosureClass is the closed vocabulary of transferred data classes.
type DisclosureClass string

// The selected disclosure data classes.
const (
	DisclosureSubjectExport     DisclosureClass = "subject_export"
	DisclosureIdentitySuccessor DisclosureClass = "identity_successor"
	DisclosureDeletionRequest   DisclosureClass = "deletion_request"
	DisclosureRestriction       DisclosureClass = "restriction"
	DisclosureObjection         DisclosureClass = "objection"
)

// Valid reports whether the disclosure class is in the selected vocabulary.
func (value DisclosureClass) Valid() bool {
	switch value {
	case DisclosureSubjectExport, DisclosureIdentitySuccessor, DisclosureDeletionRequest, DisclosureRestriction, DisclosureObjection:
		return true
	default:
		return false
	}
}

// Disclosure is one immutable transfer or disclosure record. Reference is a
// bounded non-reversible digest of the transferred material; raw bytes and
// recipient secrets never enter this record.
type Disclosure struct {
	ID          id.PrivacyDisclosure
	RequestID   id.PrivacyRequest
	Recipient   string
	Purpose     string
	DataClass   DisclosureClass
	LegalBasis  string
	Region      string
	Reference   string
	DisclosedAt time.Time
	Version     int64
}

// NewDisclosure creates one immutable disclosure record.
func NewDisclosure(identifier id.PrivacyDisclosure, requestID id.PrivacyRequest, recipient, purpose string, class DisclosureClass, legalBasis, region, reference string, disclosedAt time.Time) (Disclosure, error) {
	disclosure := Disclosure{
		ID: identifier, RequestID: requestID, Recipient: recipient, Purpose: purpose,
		DataClass: class, LegalBasis: legalBasis, Region: region, Reference: reference,
		DisclosedAt: disclosedAt, Version: 1,
	}
	if disclosure.Validate() != nil {
		return Disclosure{}, ErrInvalid
	}
	return disclosure, nil
}

// Validate checks immutable disclosure meaning.
func (disclosure Disclosure) Validate() error {
	if disclosure.ID.IsZero() || disclosure.RequestID.IsZero() || !token(disclosure.Recipient, 200) ||
		!token(disclosure.Purpose, 128) || !disclosure.DataClass.Valid() || !token(disclosure.LegalBasis, 200) ||
		!validRegion(disclosure.Region) || !token(disclosure.Reference, 128) || disclosure.Version != 1 ||
		disclosure.DisclosedAt.IsZero() || disclosure.DisclosedAt.Location() != time.UTC {
		return ErrInvalid
	}
	return nil
}
