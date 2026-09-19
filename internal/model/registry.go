package model

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"regexp"
	"slices"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

// Registry errors deliberately conceal absent and cross-tenant records.
var (
	ErrRegistryInvalid  = errors.New("model: invalid registry command")
	ErrRegistryConflict = errors.New("model: registry version conflict")
	ErrRegistryNotFound = errors.New("model: registry record not found")
)

var registryName = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// Registration records declared provenance; declarations are not production approval.
type Registration struct {
	Manifest           modelv1.Manifest               `json:"manifest"`
	Configuration      modelv1.ConfigurationReference `json:"configuration"`
	Owner              string                         `json:"owner"`
	License            string                         `json:"license"`
	TrainingProvenance string                         `json:"training_provenance"`
	IntendedUse        string                         `json:"intended_use"`
	ProhibitedUse      string                         `json:"prohibited_use"`
	Regions            []string                       `json:"regions"`
	HardwareClass      string                         `json:"hardware_class"`
	EvaluationOnly     bool                           `json:"evaluation_only"`
}

// ThresholdSet pins score meaning to the complete immutable execution provenance.
// Values are evaluation operating points, never accepted verification assurance.
type ThresholdSet struct {
	Configuration          modelv1.ConfigurationReference `json:"configuration"`
	Provenance             modelv1.Provenance             `json:"provenance"`
	ScoreName              string                         `json:"score_name"`
	Minimum                float64                        `json:"minimum"`
	Maximum                float64                        `json:"maximum"`
	Cutoff                 float64                        `json:"cutoff"`
	HigherIsGenuine        bool                           `json:"higher_is_genuine"`
	EvaluationReportDigest string                         `json:"evaluation_report_digest"`
	EvaluationOnly         bool                           `json:"evaluation_only"`
}

// RegistryRevision is immutable and independently numbered for model and threshold.
type RegistryRevision struct {
	Kind         string        `json:"kind"`
	Revision     int64         `json:"revision"`
	Digest       string        `json:"digest"`
	Registration *Registration `json:"registration,omitempty"`
	Thresholds   *ThresholdSet `json:"thresholds,omitempty"`
	CreatedAt    time.Time     `json:"created_at"`
}

// Deployment selects one model/threshold pair. Only evaluation mode is supported.
type Deployment struct {
	ModelRevision     int64  `json:"model_revision"`
	ThresholdRevision int64  `json:"threshold_revision"`
	Region            string `json:"region"`
}

// RegistryState is the current optimistic-concurrency pointer, not mutable model meaning.
type RegistryState struct {
	Name              string      `json:"name"`
	Version           int64       `json:"version"`
	ModelRevision     int64       `json:"latest_model_revision"`
	ThresholdRevision int64       `json:"latest_threshold_revision"`
	Active            *Deployment `json:"active,omitempty"`
	UpdatedAt         time.Time   `json:"updated_at"`
}

// RegistryCommand is a closed action on a tenant-owned evaluation registry.
type RegistryCommand struct {
	Operation       string        `json:"operation"`
	Name            string        `json:"name"`
	ExpectedVersion int64         `json:"expected_version"`
	Registration    *Registration `json:"registration,omitempty"`
	Thresholds      *ThresholdSet `json:"thresholds,omitempty"`
	Deployment      *Deployment   `json:"deployment,omitempty"`
	Reason          string        `json:"reason"`
}

// RegistryReceipt preserves the exact result and original actor of a command.
type RegistryReceipt struct {
	State     RegistryState     `json:"state"`
	Revision  *RegistryRevision `json:"revision,omitempty"`
	ActorID   string            `json:"actor_id"`
	Operation string            `json:"operation"`
	Reason    string            `json:"reason"`
	Replayed  bool              `json:"replayed"`
}

func validDigest(value string) bool {
	if len(value) != 71 || value[:7] != "sha256:" {
		return false
	}
	decoded, err := hex.DecodeString(value[7:])
	return err == nil && len(decoded) == 32 && value == "sha256:"+hex.EncodeToString(decoded)
}
func boundedText(value string) bool { return len(value) > 0 && len(value) <= 512 }

// Validate rejects missing governance declarations and every production-mode request.
func (value Registration) Validate() error {
	if value.Manifest.Validate() != nil || value.Configuration.Validate() != nil || value.Manifest.Provenance.ModelID != value.Configuration.ModelID || !value.EvaluationOnly || !boundedText(value.Owner) || !boundedText(value.License) || !boundedText(value.TrainingProvenance) || !boundedText(value.IntendedUse) || !boundedText(value.ProhibitedUse) || !registryName.MatchString(value.HardwareClass) || len(value.Regions) == 0 || len(value.Regions) > 32 {
		return ErrRegistryInvalid
	}
	seen := map[string]bool{}
	for _, region := range value.Regions {
		if !registryName.MatchString(region) || seen[region] {
			return ErrRegistryInvalid
		}
		seen[region] = true
	}
	return nil
}

// Validate bounds numeric inputs and requires exact model, runtime and transform pins.
func (value ThresholdSet) Validate() error {
	p := value.Provenance
	if value.Configuration.Validate() != nil || value.Configuration.ModelID != p.ModelID || !value.EvaluationOnly || p.ModelID == "" || p.ModelVersion == "" || !modelv1.CurrentVersion.Accepts(p.Contract) || !validDigest(p.ModelDigest) || !validDigest(p.RuntimeDigest) || !validDigest(p.PreprocessingDigest) || !validDigest(p.OutputSchemaDigest) || !registryName.MatchString(value.ScoreName) || !validDigest(value.EvaluationReportDigest) {
		return ErrRegistryInvalid
	}
	for _, number := range []float64{value.Minimum, value.Maximum, value.Cutoff} {
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return ErrRegistryInvalid
		}
	}
	if value.Minimum >= value.Maximum || value.Cutoff < value.Minimum || value.Cutoff > value.Maximum {
		return ErrRegistryInvalid
	}
	return nil
}

// validateEnvelope rejects irrelevant fields so replay meaning cannot hide ignored inputs.
func (command RegistryCommand) validateEnvelope() error {
	if !registryName.MatchString(command.Name) || !registryName.MatchString(command.Reason) || command.ExpectedVersion < 0 || command.ExpectedVersion == math.MaxInt64 {
		return ErrRegistryInvalid
	}
	switch command.Operation {
	case "register":
		if command.Registration == nil || command.Thresholds != nil || command.Deployment != nil {
			return ErrRegistryInvalid
		}
	case "threshold":
		if command.Thresholds == nil || command.Registration != nil || command.Deployment != nil {
			return ErrRegistryInvalid
		}
	case "activate", "rollback":
		if command.Registration != nil || command.Thresholds != nil || command.Deployment == nil || command.Deployment.ModelRevision < 1 || command.Deployment.ThresholdRevision < 1 || !registryName.MatchString(command.Deployment.Region) {
			return ErrRegistryInvalid
		}
	case "retire":
		if command.Registration != nil || command.Thresholds != nil || command.Deployment != nil {
			return ErrRegistryInvalid
		}
	default:
		return ErrRegistryInvalid
	}
	return nil
}

// Validate rejects irrelevant fields so replay meaning cannot hide ignored inputs.
func (command RegistryCommand) Validate() error {
	if err := command.validateEnvelope(); err != nil {
		return err
	}
	switch command.Operation {
	case "register":
		return command.Registration.Validate()
	case "threshold":
		return command.Thresholds.Validate()
	}
	return nil
}

// RevisionDigest binds immutable metadata using deterministic JSON encoding.
func RevisionDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", ErrRegistryInvalid
	}
	digest := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// ValidateDeployment refuses cross-model thresholds or undeclared execution regions.
func ValidateDeployment(deployment Deployment, registration Registration, thresholds ThresholdSet) error {
	if registration.Validate() != nil || thresholds.Validate() != nil || registration.Manifest.Provenance != thresholds.Provenance || registration.Configuration != thresholds.Configuration || !slices.Contains(registration.Regions, deployment.Region) {
		return ErrRegistryInvalid
	}
	return nil
}

// RegistrySelection pins a deployment at worker composition and preparation.
// Changes require a new mounted binding; retirement immediately fences new attempts.
type RegistrySelection struct {
	Name              string `json:"name"`
	ModelRevision     int64  `json:"model_revision"`
	ThresholdRevision int64  `json:"threshold_revision"`
	ModelDigest       string `json:"model_digest"`
	ThresholdDigest   string `json:"threshold_digest"`
}

// Validate checks all selection identifiers before database access.
func (value RegistrySelection) Validate() error {
	if !registryName.MatchString(value.Name) || value.ModelRevision < 1 || value.ThresholdRevision < 1 || !validDigest(value.ModelDigest) || !validDigest(value.ThresholdDigest) {
		return ErrRegistryInvalid
	}
	return nil
}
