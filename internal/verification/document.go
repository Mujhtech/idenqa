package verification

import (
	"slices"
	"sort"
	"strings"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/document/consistency"
	"github.com/Mujhtech/idenqa/internal/document/fields"
	"github.com/Mujhtech/idenqa/internal/pack"
)

// Document knowledge signal names contributed by deterministic Core
// post-processing. They are normalised signal names like any runner signal:
// the existing policy fact projection maps each one to a bounded fact, and no
// raw document text, MRZ line, barcode payload, or field value is carried.
const (
	// SignalDocumentMRZ reports machine-readable zone structural and checksum validity.
	SignalDocumentMRZ = "idenqa.signal.document_mrz"
	// SignalDocumentExpiry reports the derived expiry state.
	SignalDocumentExpiry = "idenqa.signal.document_expiry"
	// SignalDocumentConsistency reports cross-source field agreement.
	SignalDocumentConsistency = "idenqa.signal.document_consistency"
	// SignalDocumentSides reports front/back correspondence.
	SignalDocumentSides = "idenqa.signal.document_sides"
	// SignalDocumentClassification reports definitive or provisional classification.
	SignalDocumentClassification = "idenqa.signal.document_classification"
)

// Reason codes introduced by verification-owned document post-processing.
const (
	reasonDocumentExpired            = "document_expired"
	reasonDocumentExpiringSoon       = "document_expiring_soon"
	reasonDocumentExpiryUnavailable  = "document_expiry_unavailable"
	reasonDocumentConsistencyNoData  = "document_consistency_insufficient_data"
	reasonDocumentConsistencySingle  = "document_consistency_single_source"
	reasonDocumentSidesSingle        = "document_sides_single"
	reasonDocumentSidesIncomparable  = "document_sides_incomparable"
	reasonDocumentClassificationProv = "document_classification_provisional"
	reasonDocumentClassificationConf = "document_classification_conflict"
	reasonDocumentMRZInvalid         = "mrz_unparsable"
	reasonDocumentSupportUnsupported = "document_support_unsupported"
)

// ResultOption configures optional deterministic post-processing of one
// runner result. Options are additive and never change provider signal meaning.
type ResultOption func(*resultOptions)

type resultOptions struct {
	analyses []document.Analysis
	support  DocumentSupportResolver
}

// WithDocumentAnalyses attaches bounded deterministic document analyses to
// provider or model result normalisation. Each document side may appear at
// most once. Invalid analyses fail closed before any signal is derived.
func WithDocumentAnalyses(analyses ...document.Analysis) ResultOption {
	return func(options *resultOptions) {
		options.analyses = append(options.analyses, analyses...)
	}
}

// ConsumeProviderDocument converts one transient provider document observation
// into bounded Core signals and clears it from the result. The returned value
// is the only form that may be persisted, fingerprinted, audited, or logged;
// the observation itself must not survive this call. Malformed or unbounded
// observations fail closed instead of contributing meaning.
func ConsumeProviderDocument(result providerv1.Result) (providerv1.Result, error) {
	if result.Document == nil {
		return result, nil
	}
	if result.Outcome != providerv1.ResultOutcomeCompleted || result.Document.Validate() != nil {
		return providerv1.Result{}, ErrInvalidCheck
	}
	analyses, err := documentAnalysesFromObservation(*result.Document, result.CompletedAt)
	if err != nil {
		return providerv1.Result{}, err
	}
	derived, err := documentSignals(resultOptions{analyses: analyses}, result.CompletedAt)
	if err != nil {
		return providerv1.Result{}, err
	}
	signals, err := mergeResultSignals(result.Signals, derived)
	if err != nil {
		return providerv1.Result{}, err
	}
	result.Document = nil
	result.Signals = signals
	return result, nil
}

// documentAnalysesFromObservation builds the WithDocumentAnalyses input for
// one provider observation. Provider extraction describes the primary document
// side; multi-side correspondence remains the capture plan's responsibility.
func documentAnalysesFromObservation(observation providerv1.DocumentObservation, reference time.Time) ([]document.Analysis, error) {
	barcodes := make([]string, 0, 1)
	if observation.BarcodePayload != "" {
		barcodes = append(barcodes, observation.BarcodePayload)
	}
	raw := make([]fields.RawField, len(observation.Fields))
	for index, field := range observation.Fields {
		raw[index] = fields.RawField{Name: field.Name, Value: field.Value}
	}
	analysis, err := fields.Analyse(fields.Input{
		Side:           document.SideFront,
		ProviderFields: raw,
		MRZLines:       observation.MRZLines,
		Barcodes:       barcodes,
		Reference:      reference,
	})
	if err != nil {
		return nil, ErrInvalidCheck
	}
	return []document.Analysis{analysis}, nil
}

func mergeResultSignals(signals []providerv1.Signal, derived []Signal) ([]providerv1.Signal, error) {
	merged := make([]providerv1.Signal, len(signals), len(signals)+len(derived))
	existing := make(map[string]struct{}, len(signals))
	for index, signal := range signals {
		merged[index] = signal
		existing[signal.Name] = struct{}{}
	}
	for _, signal := range derived {
		if _, exists := existing[signal.Name]; exists {
			continue
		}
		if len(merged) >= maximumSignals {
			return nil, ErrInvalidCheck
		}
		merged = append(merged, providerv1.Signal{
			Name: signal.Name, Outcome: providerv1.SignalOutcome(signal.Outcome), ReasonCodes: slices.Clone(signal.ReasonCodes),
		})
		existing[signal.Name] = struct{}{}
	}
	return merged, nil
}

func resolveResultOptions(options []ResultOption) (resultOptions, error) {
	var resolved resultOptions
	for _, option := range options {
		if option != nil {
			option(&resolved)
		}
	}
	if len(resolved.analyses) > 2 {
		return resultOptions{}, ErrInvalidCheck
	}
	return resolved, nil
}

func documentSignals(options resultOptions, evaluatedAt time.Time) ([]Signal, error) {
	if len(options.analyses) == 0 {
		return nil, nil
	}
	var front, back *document.Analysis
	for index := range options.analyses {
		analysis := &options.analyses[index]
		if !analysis.Valid() {
			return nil, ErrInvalidCheck
		}
		switch analysis.Side {
		case document.SideFront:
			if front != nil {
				return nil, ErrInvalidCheck
			}
			front = analysis
		case document.SideBack:
			if back != nil {
				return nil, ErrInvalidCheck
			}
			back = analysis
		default:
			return nil, ErrInvalidCheck
		}
	}
	comparison, err := consistency.Compare(front, back, evaluatedAt)
	if err != nil {
		return nil, ErrInvalidCheck
	}
	signals := make([]Signal, 0, 5)
	if signal := mrzSignal(front, back); signal != nil {
		signals = append(signals, *signal)
	}
	signals = append(signals,
		expirySignal(comparison),
		consistencySignal(front, back, comparison),
		sidesSignal(front, back, comparison),
		classificationSignal(front, back, options.support),
	)
	return signals, nil
}

func mrzSignal(front, back *document.Analysis) *Signal {
	codes := make(map[string]struct{})
	present, hard, inconclusive := false, false, false
	for _, analysis := range []*document.Analysis{front, back} {
		if analysis == nil || analysis.MRZ == nil {
			continue
		}
		present = true
		if analysis.MRZ.Valid {
			continue
		}
		if len(analysis.MRZ.Issues) == 0 {
			inconclusive = true
			codes[reasonDocumentMRZInvalid] = struct{}{}
			continue
		}
		for _, issue := range analysis.MRZ.Issues {
			codes[issue.Code] = struct{}{}
			switch issue.Code {
			case "mrz_check_digit_invalid", "mrz_composite_check_invalid":
				hard = true
			default:
				inconclusive = true
			}
		}
	}
	if !present {
		return nil
	}
	outcome := SignalSatisfied
	switch {
	case hard:
		outcome = SignalNotSatisfied
	case inconclusive:
		outcome = SignalInconclusive
	}
	return &Signal{Name: SignalDocumentMRZ, Outcome: outcome, ReasonCodes: boundedReasonCodes(codes)}
}

func expirySignal(comparison consistency.Result) Signal {
	switch comparison.Expiry {
	case consistency.ExpiryExpired:
		return Signal{Name: SignalDocumentExpiry, Outcome: SignalNotSatisfied, ReasonCodes: []string{reasonDocumentExpired}}
	case consistency.ExpiryExpiringSoon:
		return Signal{Name: SignalDocumentExpiry, Outcome: SignalSatisfied, ReasonCodes: []string{reasonDocumentExpiringSoon}}
	case consistency.ExpiryValid:
		return Signal{Name: SignalDocumentExpiry, Outcome: SignalSatisfied, ReasonCodes: []string{}}
	default:
		return Signal{Name: SignalDocumentExpiry, Outcome: SignalInconclusive, ReasonCodes: []string{reasonDocumentExpiryUnavailable}}
	}
}

func consistencySignal(front, back *document.Analysis, comparison consistency.Result) Signal {
	codes := make(map[string]struct{})
	for _, finding := range comparison.Findings {
		if finding.Kind == consistency.FindingFieldConflict || finding.Kind == consistency.FindingSideMismatch {
			codes[finding.Code] = struct{}{}
		}
	}
	if len(codes) > 0 {
		return Signal{Name: SignalDocumentConsistency, Outcome: SignalNotSatisfied, ReasonCodes: boundedReasonCodes(codes)}
	}
	sources := make(map[document.Source]struct{})
	fields := 0
	for _, analysis := range []*document.Analysis{front, back} {
		if analysis == nil {
			continue
		}
		for _, field := range analysis.Fields {
			fields++
			for _, source := range field.Sources {
				sources[source] = struct{}{}
			}
		}
	}
	switch {
	case fields == 0:
		return Signal{Name: SignalDocumentConsistency, Outcome: SignalInconclusive, ReasonCodes: []string{reasonDocumentConsistencyNoData}}
	case len(sources) < 2:
		return Signal{Name: SignalDocumentConsistency, Outcome: SignalInconclusive, ReasonCodes: []string{reasonDocumentConsistencySingle}}
	default:
		return Signal{Name: SignalDocumentConsistency, Outcome: SignalSatisfied, ReasonCodes: []string{}}
	}
}

func sidesSignal(front, back *document.Analysis, comparison consistency.Result) Signal {
	codes := make(map[string]struct{})
	missing := false
	for _, finding := range comparison.Findings {
		switch finding.Kind {
		case consistency.FindingSideMismatch:
			codes[finding.Code] = struct{}{}
		case consistency.FindingSideMissing:
			missing = true
			codes[finding.Code] = struct{}{}
		}
	}
	if front != nil && back != nil {
		if len(codes) > 0 {
			return Signal{Name: SignalDocumentSides, Outcome: SignalNotSatisfied, ReasonCodes: boundedReasonCodes(codes)}
		}
		if commonFieldCount(front, back) == 0 && !classificationComparable(front, back) {
			return Signal{Name: SignalDocumentSides, Outcome: SignalInconclusive, ReasonCodes: []string{reasonDocumentSidesIncomparable}}
		}
		return Signal{Name: SignalDocumentSides, Outcome: SignalSatisfied, ReasonCodes: []string{}}
	}
	if missing {
		return Signal{Name: SignalDocumentSides, Outcome: SignalNotSatisfied, ReasonCodes: boundedReasonCodes(codes)}
	}
	if len(planSides(front, back)) == 0 {
		return Signal{Name: SignalDocumentSides, Outcome: SignalInconclusive, ReasonCodes: []string{reasonDocumentSidesSingle}}
	}
	return Signal{Name: SignalDocumentSides, Outcome: SignalSatisfied, ReasonCodes: []string{}}
}

func planSides(front, back *document.Analysis) []document.Side {
	sides := make([]document.Side, 0, document.MaximumPlanSides)
	for _, analysis := range []*document.Analysis{front, back} {
		if analysis == nil {
			continue
		}
		for _, side := range analysis.PlanSides {
			exists := false
			for _, existing := range sides {
				if existing == side {
					exists = true
					break
				}
			}
			if !exists {
				sides = append(sides, side)
			}
		}
	}
	return sides
}

func classificationSignal(front, back *document.Analysis, support DocumentSupportResolver) Signal {
	classifications := make([]document.Classification, 0, 2)
	for _, analysis := range []*document.Analysis{front, back} {
		if analysis != nil {
			classifications = append(classifications, analysis.Classification)
		}
	}
	allDefinitive := len(classifications) > 0
	typeValue, stateValue := "", ""
	typeConflict, stateConflict := false, false
	reasons := make(map[string]struct{})
	for _, classification := range classifications {
		if classification.Status != document.ClassificationDefinitive {
			allDefinitive = false
			for _, reason := range classification.Reasons {
				reasons[reason] = struct{}{}
			}
			continue
		}
		if typeValue != "" && classification.DocumentType != typeValue {
			typeConflict = true
		}
		if stateValue != "" && classification.IssuingState != "" && classification.IssuingState != stateValue {
			stateConflict = true
		}
		if typeValue == "" {
			typeValue = classification.DocumentType
		}
		if stateValue == "" {
			stateValue = classification.IssuingState
		}
	}
	if typeConflict || stateConflict {
		return Signal{Name: SignalDocumentClassification, Outcome: SignalInconclusive, ReasonCodes: []string{reasonDocumentClassificationConf}}
	}
	if !allDefinitive {
		reasons[reasonDocumentClassificationProv] = struct{}{}
		return Signal{Name: SignalDocumentClassification, Outcome: SignalInconclusive, ReasonCodes: boundedReasonCodes(reasons)}
	}
	if support != nil {
		if projection, ok := support.Support(stateValue, typeValue); ok && projection.SupportLevel == pack.SupportUnsupported {
			reasons[reasonDocumentSupportUnsupported] = struct{}{}
			return Signal{Name: SignalDocumentClassification, Outcome: SignalInconclusive, ReasonCodes: boundedReasonCodes(reasons)}
		}
	}
	codes := []string{"document_type_" + typeValue}
	if stateValue != "" {
		codes = append(codes, "document_country_"+strings.ToLower(stateValue))
	}
	return Signal{Name: SignalDocumentClassification, Outcome: SignalSatisfied, ReasonCodes: codes}
}

func commonFieldCount(front, back *document.Analysis) int {
	count := 0
	for _, field := range front.Fields {
		if _, exists := back.Field(field.Name); exists {
			count++
		}
	}
	return count
}

func classificationComparable(front, back *document.Analysis) bool {
	left, right := front.Classification, back.Classification
	return left.Status == document.ClassificationDefinitive &&
		right.Status == document.ClassificationDefinitive &&
		left.DocumentType == right.DocumentType &&
		(left.IssuingState == "" || right.IssuingState == "" || left.IssuingState == right.IssuingState)
}

func boundedReasonCodes(codes map[string]struct{}) []string {
	result := make([]string, 0, len(codes))
	for code := range codes {
		result = append(result, code)
	}
	sort.Strings(result)
	if len(result) > maximumReasonCodes {
		result = result[:maximumReasonCodes]
	}
	return result
}

func mergeDocumentSignals(signals []Signal, derived []Signal) []Signal {
	if len(derived) == 0 {
		return signals
	}
	existing := make(map[string]struct{}, len(signals))
	for _, signal := range signals {
		existing[signal.Name] = struct{}{}
	}
	for _, signal := range derived {
		if _, exists := existing[signal.Name]; exists {
			continue
		}
		signals = append(signals, signal)
		existing[signal.Name] = struct{}{}
	}
	return signals
}
