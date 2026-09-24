package model

// First-party evaluation identifiers. Third-party adapters may declare other
// valid names; these constants define only the capabilities owned by Idenqa.
const (
	EvaluationPassivePAD     = "idenqa.check.passive_pad" //nolint:gosec // Static contract identifier, not a credential.
	EvaluationFaceMatch      = "idenqa.check.face_match_1to1"
	EvaluationSelfieAnalysis = "idenqa.check.selfie_analysis"
)

// First-party normalized model signals.
const (
	SignalPassivePAD        = "idenqa.signal.passive_pad" //nolint:gosec // Static contract identifier, not a credential.
	SignalFaceMatch         = "idenqa.signal.face_match_1to1"
	SignalFaceCount         = "idenqa.signal.face_count"
	SignalFacePosition      = "idenqa.signal.face_position"
	SignalHeadPose          = "idenqa.signal.head_pose"
	SignalSelfieQuality     = "idenqa.signal.selfie_image_quality"
	SignalTemporalIntegrity = "idenqa.signal.temporal_integrity"
)

// SelfieAnalysisSignals is the exact ordered output vocabulary for the
// evaluation-only first-party selfie analysis capability.
func SelfieAnalysisSignals(temporal bool) []string {
	result := []string{SignalFaceCount, SignalFacePosition, SignalHeadPose, SignalSelfieQuality}
	if temporal {
		result = append(result, SignalTemporalIntegrity)
	}
	return result
}
