// Package acceptance validates content-free production-acceptance records.
package acceptance

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strings"
	"time"
)

const (
	// ProviderSchemaVersion identifies the exact provider/model record contract.
	ProviderSchemaVersion = "idenqa.acceptance.provider.v1"
	// MaximumRecordBytes bounds an operator-controlled record.
	MaximumRecordBytes = 1 << 20
	// ReceiptSchemaVersion identifies scope-bound interoperability receipts.
	ReceiptSchemaVersion = "idenqa.acceptance.receipt.v2"
)

var (
	// ErrInvalid means a record is malformed, unsafe, or internally inconsistent.
	ErrInvalid = errors.New("acceptance: invalid provider record")
	// ErrIncomplete means a record claims acceptance without complete evidence.
	ErrIncomplete = errors.New("acceptance: incomplete provider acceptance")
)

var requiredChecks = map[string][]string{
	"provider": {
		"PRV-01-authentication", "PRV-02-representative-samples", "PRV-03-outcome-mapping",
		"PRV-04-failure-paths", "PRV-05-operational-review", "PRV-06-regional-journey",
		"PRV-07-content-exclusion",
	},
	"model": {
		"MOD-01-provenance-rights", "MOD-02-evaluation-set", "MOD-03-thresholds-metrics",
		"MOD-04-attack-quality-coverage", "MOD-05-fairness", "MOD-06-runtime",
		"MOD-07-lifecycle", "MOD-08-assurance-mapping", "MOD-09-independent-review",
	},
}

var requiredApprovals = []string{"operations", "privacy_legal", "product", "provider_model", "security"}

// ProviderRecord is one exact provider or model acceptance decision. It stores
// only opaque references and aggregate measurements, never evaluation content.
type ProviderRecord struct {
	SchemaVersion string        `json:"schema_version"`
	RecordID      string        `json:"record_id"`
	GateSet       string        `json:"gate_set"`
	Decision      string        `json:"decision"`
	Tuple         ProviderTuple `json:"tuple"`
	Run           Run           `json:"run"`
	Build         Build         `json:"build"`
	Evidence      Evidence      `json:"evidence"`
	Checks        []Check       `json:"checks"`
	Risks         []Risk        `json:"risks"`
	Approvals     []Approval    `json:"approvals"`
}

// ProviderTuple identifies the exact scope to which the decision applies.
type ProviderTuple struct {
	AdapterModel        string `json:"adapter_model"`
	ArtifactDigest      string `json:"artifact_digest"`
	Capability          string `json:"capability"`
	Country             string `json:"country"`
	EvidenceClass       string `json:"evidence_class"`
	AcquisitionMethod   string `json:"acquisition_method"`
	Region              string `json:"region"`
	RuntimeClass        string `json:"runtime_class"`
	ConfigurationDigest string `json:"configuration_digest"`
}

// Receipt binds a validation result to its exact tuple and credential custody.
// Downstream consumers need not import Core implementation packages.
type Receipt struct {
	SchemaVersion       string `json:"schema_version"`
	RecordSchema        string `json:"record_schema"`
	RecordID            string `json:"record_id"`
	RecordKind          string `json:"record_kind"`
	Decision            string `json:"decision"`
	Passed              bool   `json:"passed"`
	Digest              string `json:"digest"`
	TupleDigest         string `json:"tuple_digest"`
	CredentialOwnership string `json:"credential_ownership"`
}

// Receipt returns scope metadata derived from the validated source record.
func (record ProviderRecord) Receipt() (Receipt, error) {
	digestValue, err := record.Digest()
	if err != nil {
		return Receipt{}, err
	}
	encoded, err := json.Marshal(record.Tuple)
	if err != nil {
		return Receipt{}, fmt.Errorf("encode acceptance tuple: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return Receipt{
		SchemaVersion: ReceiptSchemaVersion, RecordSchema: record.SchemaVersion,
		RecordID: record.RecordID, RecordKind: record.GateSet, Decision: record.Decision,
		Passed: record.Passed(), Digest: digestValue,
		TupleDigest:         "sha256:" + hex.EncodeToString(sum[:]),
		CredentialOwnership: record.Evidence.CredentialOwnership,
	}, nil
}

// Run binds the decision to one accountable, time-bounded execution.
type Run struct {
	RunID        string    `json:"run_id"`
	OperatorRef  string    `json:"operator_ref"`
	StartedAt    time.Time `json:"started_at"`
	EndedAt      time.Time `json:"ended_at"`
	RevalidateAt time.Time `json:"revalidate_at"`
}

// Build binds evidence to immutable source and signed release material.
type Build struct {
	RepositoryCommit string `json:"repository_commit"`
	BuildDigest      string `json:"build_digest"`
	SignatureRef     string `json:"signature_ref"`
	SBOMRef          string `json:"sbom_ref"`
	ContractVersion  string `json:"contract_version"`
}

// Evidence contains content-free references and aggregate measurements.
type Evidence struct {
	CredentialOwnership string   `json:"credential_ownership"`
	AccountEnvironment  string   `json:"account_environment"`
	ProcessingRegion    string   `json:"processing_region"`
	DatasetRef          string   `json:"dataset_ref"`
	RightsBasisRef      string   `json:"rights_basis_ref"`
	ConsentBasisRef     string   `json:"consent_basis_ref"`
	HardwareBoundsRef   string   `json:"hardware_bounds_ref"`
	SampleExclusionsRef string   `json:"sample_exclusions_ref"`
	ThresholdsRef       string   `json:"thresholds_ref"`
	RawReportRef        string   `json:"raw_report_ref"`
	FailuresRef         string   `json:"failures_ref"`
	IncidentsRef        string   `json:"incidents_ref"`
	Metrics             []Metric `json:"metrics"`
}

// Metric is one aggregate, unit-labelled measurement.
type Metric struct {
	Name        string  `json:"name"`
	Value       float64 `json:"value"`
	Unit        string  `json:"unit"`
	Population  uint64  `json:"population"`
	EvidenceRef string  `json:"evidence_ref"`
}

// Check records one mandatory protocol gate.
type Check struct {
	ID           string   `json:"id"`
	Result       string   `json:"result"`
	EvidenceRefs []string `json:"evidence_refs"`
	ExceptionRef string   `json:"exception_ref,omitempty"`
}

// Risk records a referenced residual risk without copying sensitive detail.
type Risk struct {
	Severity       string    `json:"severity"`
	RiskRef        string    `json:"risk_ref"`
	ControlRef     string    `json:"control_ref"`
	OwnerRef       string    `json:"owner_ref"`
	RemediationRef string    `json:"remediation_ref"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// Approval is one accountable decision over the exact record.
type Approval struct {
	Role        string    `json:"role"`
	ActorRef    string    `json:"actor_ref"`
	Decision    string    `json:"decision"`
	At          time.Time `json:"at"`
	EvidenceRef string    `json:"evidence_ref"`
}

// DecodeProvider strictly decodes and validates one bounded JSON record.
func DecodeProvider(encoded []byte) (ProviderRecord, error) {
	if len(encoded) == 0 || len(encoded) > MaximumRecordBytes {
		return ProviderRecord{}, ErrInvalid
	}
	var record ProviderRecord
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&record); err != nil {
		return ProviderRecord{}, errors.Join(ErrInvalid, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return ProviderRecord{}, ErrInvalid
	}
	if err := record.Validate(); err != nil {
		return ProviderRecord{}, err
	}
	return record, nil
}

// Validate rejects unsafe structure and acceptance claims that are incomplete.
func (record ProviderRecord) Validate() error {
	checks, ok := requiredChecks[record.GateSet]
	if record.SchemaVersion != ProviderSchemaVersion || !ok || !token(record.RecordID, 96) ||
		!oneOf(record.Decision, "open", "accepted", "accepted_with_expiring_restrictions", "rejected") ||
		!record.Tuple.valid() || !record.Run.valid(record.Decision) || !record.Build.valid(record.Decision) ||
		!record.Evidence.valid(record.Decision) || !validChecks(record.Checks, checks) ||
		!validRisks(record.Risks) || !validApprovals(record.Approvals) {
		return ErrInvalid
	}
	if !record.accepted() {
		return nil
	}
	if !record.complete(checks) {
		return ErrIncomplete
	}
	return nil
}

// Passed reports whether the record is a complete accepted decision.
func (record ProviderRecord) Passed() bool {
	checks, ok := requiredChecks[record.GateSet]
	return ok && record.accepted() && record.complete(checks)
}

// Digest returns a stable digest over normalized semantic JSON.
func (record ProviderRecord) Digest() (string, error) {
	if err := record.Validate(); err != nil {
		return "", err
	}
	normalized := record
	normalized.Checks = slices.Clone(record.Checks)
	for index := range normalized.Checks {
		normalized.Checks[index].EvidenceRefs = slices.Clone(normalized.Checks[index].EvidenceRefs)
		slices.Sort(normalized.Checks[index].EvidenceRefs)
	}
	slices.SortFunc(normalized.Checks, func(left, right Check) int { return strings.Compare(left.ID, right.ID) })
	normalized.Risks = slices.Clone(record.Risks)
	slices.SortFunc(normalized.Risks, func(left, right Risk) int { return strings.Compare(left.RiskRef, right.RiskRef) })
	normalized.Approvals = slices.Clone(record.Approvals)
	slices.SortFunc(normalized.Approvals, func(left, right Approval) int { return strings.Compare(left.Role, right.Role) })
	normalized.Evidence.Metrics = slices.Clone(record.Evidence.Metrics)
	slices.SortFunc(normalized.Evidence.Metrics, func(left, right Metric) int { return strings.Compare(left.Name, right.Name) })
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", fmt.Errorf("encode canonical provider record: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func (record ProviderRecord) accepted() bool {
	return record.Decision == "accepted" || record.Decision == "accepted_with_expiring_restrictions"
}

func (record ProviderRecord) complete(required []string) bool {
	if !record.Run.complete() || !record.Build.complete() || !record.Evidence.complete() || len(record.Checks) != len(required) {
		return false
	}
	for _, check := range record.Checks {
		if check.Result != "pass" || len(check.EvidenceRefs) == 0 {
			return false
		}
	}
	if len(record.Approvals) != len(requiredApprovals) {
		return false
	}
	for _, approval := range record.Approvals {
		if approval.Decision != "approve" || approval.At.IsZero() || !reference(approval.ActorRef) || !reference(approval.EvidenceRef) {
			return false
		}
		if approval.At.Before(record.Run.EndedAt) {
			return false
		}
	}
	for _, risk := range record.Risks {
		if risk.Severity == "critical" || (risk.Severity == "high" && record.Decision != "accepted_with_expiring_restrictions") {
			return false
		}
		if !risk.ExpiresAt.After(record.Run.EndedAt) {
			return false
		}
	}
	return record.Decision != "accepted_with_expiring_restrictions" || len(record.Risks) > 0
}

func (tuple ProviderTuple) valid() bool {
	return token(tuple.AdapterModel, 128) && digest(tuple.ArtifactDigest) && token(tuple.Capability, 128) &&
		token(tuple.Country, 32) && token(tuple.EvidenceClass, 128) && token(tuple.AcquisitionMethod, 128) &&
		token(tuple.Region, 64) && token(tuple.RuntimeClass, 128) && digest(tuple.ConfigurationDigest)
}

func (run Run) valid(decision string) bool {
	if !token(run.RunID, 96) || !optionalReference(run.OperatorRef) || !orderedTimes(run.StartedAt, run.EndedAt) {
		return false
	}
	if !run.RevalidateAt.IsZero() && (!utc(run.RevalidateAt) || (!run.EndedAt.IsZero() && !run.RevalidateAt.After(run.EndedAt))) {
		return false
	}
	return decision == "open" || run.complete()
}

func (run Run) complete() bool {
	return reference(run.OperatorRef) && utc(run.StartedAt) && utc(run.EndedAt) && run.EndedAt.After(run.StartedAt) && utc(run.RevalidateAt)
}

func (build Build) valid(decision string) bool {
	if !optionalHexCommit(build.RepositoryCommit) || !optionalDigest(build.BuildDigest) || !optionalReference(build.SignatureRef) ||
		!optionalReference(build.SBOMRef) || !optionalToken(build.ContractVersion, 64) {
		return false
	}
	return decision == "open" || build.complete()
}

func (build Build) complete() bool {
	return hexCommit(build.RepositoryCommit) && digest(build.BuildDigest) && reference(build.SignatureRef) &&
		reference(build.SBOMRef) && token(build.ContractVersion, 64)
}

func (evidence Evidence) valid(decision string) bool {
	if (evidence.CredentialOwnership != "" && !oneOf(evidence.CredentialOwnership, "tenant_owned", "operator_managed", "provider_owned", "not_applicable")) ||
		(evidence.AccountEnvironment != "" && !oneOf(evidence.AccountEnvironment, "production", "sandbox", "evaluation")) ||
		!optionalToken(evidence.ProcessingRegion, 64) || !optionalReference(evidence.DatasetRef) ||
		!optionalReference(evidence.RightsBasisRef) || !optionalReference(evidence.ConsentBasisRef) ||
		!optionalReference(evidence.HardwareBoundsRef) || !optionalReference(evidence.SampleExclusionsRef) ||
		!optionalReference(evidence.ThresholdsRef) || !optionalReference(evidence.RawReportRef) ||
		!optionalReference(evidence.FailuresRef) || !optionalReference(evidence.IncidentsRef) || len(evidence.Metrics) > 256 {
		return false
	}
	seen := make(map[string]struct{}, len(evidence.Metrics))
	for _, metric := range evidence.Metrics {
		if !token(metric.Name, 96) || !token(metric.Unit, 32) || metric.Population == 0 ||
			math.IsNaN(metric.Value) || math.IsInf(metric.Value, 0) || !reference(metric.EvidenceRef) {
			return false
		}
		if _, exists := seen[metric.Name]; exists {
			return false
		}
		seen[metric.Name] = struct{}{}
	}
	return decision == "open" || evidence.complete()
}

func (evidence Evidence) complete() bool {
	return oneOf(evidence.CredentialOwnership, "tenant_owned", "operator_managed", "provider_owned", "not_applicable") && evidence.AccountEnvironment == "production" &&
		token(evidence.ProcessingRegion, 64) && reference(evidence.DatasetRef) && reference(evidence.RightsBasisRef) &&
		reference(evidence.ConsentBasisRef) && reference(evidence.HardwareBoundsRef) && reference(evidence.SampleExclusionsRef) &&
		reference(evidence.ThresholdsRef) && reference(evidence.RawReportRef) && reference(evidence.FailuresRef) &&
		reference(evidence.IncidentsRef) && len(evidence.Metrics) > 0
}

func validChecks(checks []Check, required []string) bool {
	if len(checks) > len(required) {
		return false
	}
	allowed := make(map[string]struct{}, len(required))
	for _, id := range required {
		allowed[id] = struct{}{}
	}
	seen := make(map[string]struct{}, len(checks))
	for _, check := range checks {
		if _, exists := allowed[check.ID]; !exists || !oneOf(check.Result, "pass", "fail", "blocked", "not_applicable") || len(check.EvidenceRefs) > 32 {
			return false
		}
		if _, exists := seen[check.ID]; exists {
			return false
		}
		seen[check.ID] = struct{}{}
		for _, evidenceRef := range check.EvidenceRefs {
			if !reference(evidenceRef) {
				return false
			}
		}
		if (check.Result == "not_applicable") != (check.ExceptionRef != "") || !optionalReference(check.ExceptionRef) {
			return false
		}
	}
	return true
}

func validRisks(risks []Risk) bool {
	if len(risks) > 128 {
		return false
	}
	seen := make(map[string]struct{}, len(risks))
	for _, risk := range risks {
		if !oneOf(risk.Severity, "low", "medium", "high", "critical") || !reference(risk.RiskRef) ||
			!reference(risk.ControlRef) || !reference(risk.OwnerRef) || !reference(risk.RemediationRef) || !utc(risk.ExpiresAt) {
			return false
		}
		if _, exists := seen[risk.RiskRef]; exists {
			return false
		}
		seen[risk.RiskRef] = struct{}{}
	}
	return true
}

func validApprovals(approvals []Approval) bool {
	if len(approvals) > len(requiredApprovals) {
		return false
	}
	allowed := make(map[string]struct{}, len(requiredApprovals))
	for _, role := range requiredApprovals {
		allowed[role] = struct{}{}
	}
	seen := make(map[string]struct{}, len(approvals))
	for _, approval := range approvals {
		if _, exists := allowed[approval.Role]; !exists || !oneOf(approval.Decision, "approve", "reject", "pending") ||
			!optionalReference(approval.ActorRef) || !optionalReference(approval.EvidenceRef) || (!approval.At.IsZero() && !utc(approval.At)) {
			return false
		}
		if _, exists := seen[approval.Role]; exists {
			return false
		}
		seen[approval.Role] = struct{}{}
	}
	return true
}

func reference(value string) bool {
	if len(value) < 9 || len(value) > 256 || !strings.HasPrefix(value, "ref:") {
		return false
	}
	parts := strings.Split(value, ":")
	if len(parts) < 3 {
		return false
	}
	for _, part := range parts[1:] {
		if !token(part, 96) {
			return false
		}
	}
	return true
}

func digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func hexCommit(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func token(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z':
		case character >= 'A' && character <= 'Z':
		case character >= '0' && character <= '9':
		case strings.ContainsRune("._/-", character):
		default:
			return false
		}
	}
	return true
}

func orderedTimes(started, ended time.Time) bool {
	return (started.IsZero() || utc(started)) && (ended.IsZero() || utc(ended)) &&
		(started.IsZero() || ended.IsZero() || ended.After(started))
}

func utc(value time.Time) bool                     { return !value.IsZero() && value.Location() == time.UTC }
func oneOf(value string, allowed ...string) bool   { return slices.Contains(allowed, value) }
func optionalReference(value string) bool          { return value == "" || reference(value) }
func optionalDigest(value string) bool             { return value == "" || digest(value) }
func optionalHexCommit(value string) bool          { return value == "" || hexCommit(value) }
func optionalToken(value string, maximum int) bool { return value == "" || token(value, maximum) }
