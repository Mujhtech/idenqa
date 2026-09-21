package privacy

import (
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// ProcessorRole is the closed processor-inventory role vocabulary.
type ProcessorRole string

// The selected processor roles.
const (
	ProcessorRoleProcessor    ProcessorRole = "processor"
	ProcessorRoleSubprocessor ProcessorRole = "subprocessor"
	ProcessorRoleRecipient    ProcessorRole = "recipient"
)

// Valid reports whether the role is in the selected vocabulary.
func (value ProcessorRole) Valid() bool {
	return value == ProcessorRoleProcessor || value == ProcessorRoleSubprocessor || value == ProcessorRoleRecipient
}

// Processor is one versioned processor or subprocessor inventory entry.
type Processor struct {
	ID                id.Processor
	Name              string
	Role              ProcessorRole
	Purpose           string
	DataClasses       []DataClass
	Regions           []string
	TransferMechanism string
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

// NewProcessor creates version one of one inventory entry.
func NewProcessor(identifier id.Processor, name string, role ProcessorRole, purpose string, dataClasses []DataClass, regions []string, transferMechanism string, at time.Time) (Processor, error) {
	processor := Processor{
		ID: identifier, Name: name, Role: role, Purpose: purpose,
		DataClasses: append([]DataClass(nil), dataClasses...), Regions: append([]string(nil), regions...),
		TransferMechanism: transferMechanism, Version: 1, CreatedAt: at, UpdatedAt: at,
	}
	if processor.Validate() != nil {
		return Processor{}, ErrInvalid
	}
	return processor, nil
}

// Validate checks bounded inventory meaning and the closed vocabularies.
func (processor Processor) Validate() error {
	if processor.ID.IsZero() || !displayText(processor.Name, 200) || !processor.Role.Valid() ||
		!displayText(processor.Purpose, 128) || !token(processor.TransferMechanism, 128) || processor.Version < 1 ||
		processor.CreatedAt.IsZero() || processor.CreatedAt.Location() != time.UTC ||
		processor.UpdatedAt.Before(processor.CreatedAt) || processor.UpdatedAt.Location() != time.UTC {
		return ErrInvalid
	}
	if len(processor.DataClasses) == 0 || len(processor.DataClasses) > 8 || len(processor.Regions) == 0 || len(processor.Regions) > 16 {
		return ErrInvalid
	}
	seenClasses := make(map[DataClass]struct{}, len(processor.DataClasses))
	for _, class := range processor.DataClasses {
		if !validDataClass(class) {
			return ErrInvalid
		}
		if _, duplicate := seenClasses[class]; duplicate {
			return ErrInvalid
		}
		seenClasses[class] = struct{}{}
	}
	seenRegions := make(map[string]struct{}, len(processor.Regions))
	for _, region := range processor.Regions {
		if !validRegion(region) {
			return ErrInvalid
		}
		if _, duplicate := seenRegions[region]; duplicate {
			return ErrInvalid
		}
		seenRegions[region] = struct{}{}
	}
	return nil
}

// Update creates the next immutable revision with expected-version semantics.
func (processor Processor) Update(expectedVersion int64, name string, role ProcessorRole, purpose string, dataClasses []DataClass, regions []string, transferMechanism string, at time.Time) (Processor, error) {
	if processor.Validate() != nil || expectedVersion != processor.Version || at.IsZero() || at.Location() != time.UTC || at.Before(processor.UpdatedAt) {
		return Processor{}, ErrConflict
	}
	next := processor
	next.Name, next.Role, next.Purpose = name, role, purpose
	next.DataClasses, next.Regions = append([]DataClass(nil), dataClasses...), append([]string(nil), regions...)
	next.TransferMechanism, next.UpdatedAt, next.Version = transferMechanism, at, processor.Version+1
	if next.Validate() != nil {
		return Processor{}, ErrInvalid
	}
	return next, nil
}

func validDataClass(value DataClass) bool {
	return slices.Contains([]DataClass{
		DataClassRawEvidence, DataClassDerivedEvidence, DataClassWebhookPayload,
		DataClassWorkflowMetadata, DataClassAuditRecord, DataClassDeletionProof, DataClassBackup,
	}, value)
}
