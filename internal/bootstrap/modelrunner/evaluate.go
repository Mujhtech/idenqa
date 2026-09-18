package modelrunner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/config"
)

var errDataset = errors.New("model evaluation: invalid dataset, content or runtime")
var datasetToken = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}$`)

// Dataset describes approved local images and an explicitly fixed experimental threshold.
// Subject IDs must be pseudonymous; calibration and evaluation subjects cannot overlap.
type Dataset struct {
	Version            int      `json:"version"`
	Reference          string   `json:"reference"`
	ApprovalReference  string   `json:"approval_reference"`
	ThresholdReference string   `json:"threshold_reference"`
	RealScoreThreshold *float64 `json:"real_score_threshold"`
	Samples            []Sample `json:"samples"`
}

// Sample binds content and label metadata without accepting arbitrary URLs.
type Sample struct {
	ID               string `json:"id"`
	SubjectID        string `json:"subject_id,omitempty"`
	Split            string `json:"split"`
	Path             string `json:"path"`
	SHA256           string `json:"sha256"`
	Label            string `json:"label"`
	AttackType       string `json:"attack_type,omitempty"`
	DeviceClass      string `json:"device_class"`
	CaptureCondition string `json:"capture_condition"`
}

// Counts reports image-level errors and nonresponses separately. Null rates mean no denominator.
type Counts struct {
	Genuine       int      `json:"genuine"`
	Attacks       int      `json:"attacks"`
	GenuineScored int      `json:"genuine_scored"`
	AttacksScored int      `json:"attacks_scored"`
	FalseAccepts  int      `json:"false_accepts"`
	FalseRejects  int      `json:"false_rejects"`
	APCER         *float64 `json:"apcer_scored_only"`
	BPCER         *float64 `json:"bpcer_scored_only"`
	APNRR         *float64 `json:"attack_nonresponse_rate"`
	BPNRR         *float64 `json:"genuine_nonresponse_rate"`
}

// EvaluationReport is aggregate evaluation evidence, never model activation approval.
type EvaluationReport struct {
	Version               int                `json:"version"`
	DatasetReference      string             `json:"dataset_reference"`
	DatasetDigest         string             `json:"dataset_digest"`
	ApprovalReference     string             `json:"approval_reference"`
	ThresholdReference    string             `json:"threshold_reference"`
	RealScoreThreshold    float64            `json:"real_score_threshold"`
	Provenance            modelv1.Provenance `json:"provenance"`
	ConfigurationDigest   string             `json:"configuration_digest"`
	ProductionAccepted    bool               `json:"production_accepted"`
	Coverage              string             `json:"coverage"`
	EvaluationSubjects    int                `json:"evaluation_subjects"`
	UnknownSubjectSamples int                `json:"unknown_subject_samples"`
	SubjectSplitCheck     string             `json:"subject_split_check"`
	Total                 Counts             `json:"total"`
	Groups                []EvaluationGroup  `json:"groups"`
	Nonresponses          map[string]int     `json:"nonresponses"`
}

// EvaluationGroup stratifies results by declared device, capture condition and attack type.
type EvaluationGroup struct {
	DeviceClass      string `json:"device_class"`
	CaptureCondition string `json:"capture_condition"`
	AttackType       string `json:"attack_type"`
	Counts           Counts `json:"counts"`
}

func validateDataset(value Dataset) error {
	return validateDatasetMetadata(value, false)
}

func validateDatasetMetadata(value Dataset, allowUnpinned bool) error {
	if value.Version != 1 || !datasetToken.MatchString(value.Reference) || !datasetToken.MatchString(value.ApprovalReference) || !datasetToken.MatchString(value.ThresholdReference) || value.RealScoreThreshold == nil || math.IsNaN(*value.RealScoreThreshold) || math.IsInf(*value.RealScoreThreshold, 0) || *value.RealScoreThreshold < 0 || *value.RealScoreThreshold > 1 || len(value.Samples) == 0 || len(value.Samples) > 10000 {
		return errDataset
	}
	subjects, ids, contents := map[string]string{}, map[string]bool{}, map[string]bool{}
	evaluation := 0
	unknownSubjects, hasCalibration := false, false
	for _, s := range value.Samples {
		hash, err := hex.DecodeString(s.SHA256)
		if !datasetToken.MatchString(s.ID) || (s.SubjectID != "" && !datasetToken.MatchString(s.SubjectID)) || !datasetToken.MatchString(s.DeviceClass) || !datasetToken.MatchString(s.CaptureCondition) || !filepath.IsLocal(s.Path) || len(s.Path) > 512 || ((!allowUnpinned || s.SHA256 != "") && (err != nil || len(hash) != 32 || hex.EncodeToString(hash) != s.SHA256)) || ids[s.ID] || (s.SHA256 != "" && contents[s.SHA256]) || (s.Split != "calibration" && s.Split != "evaluation") || (s.Label != "genuine" && s.Label != "attack") || (s.Label == "genuine" && s.AttackType != "") || (s.Label == "attack" && !datasetToken.MatchString(s.AttackType)) {
			return errDataset
		}
		if s.SubjectID == "" {
			unknownSubjects = true
		}
		if s.Split == "calibration" {
			hasCalibration = true
		}
		if prior, ok := subjects[s.SubjectID]; s.SubjectID != "" && ok && prior != s.Split {
			return errDataset
		}
		subjects[s.SubjectID], ids[s.ID], contents[s.SHA256] = s.Split, true, true
		if s.Split == "evaluation" {
			evaluation++
		}
	}
	if evaluation == 0 || (unknownSubjects && hasCalibration) {
		return errDataset
	}
	return nil
}

// Evaluate reads only manifest-listed files through a confined filesystem root.
// It never calls Core, issues evidence grants, changes thresholds or activates a model.
func Evaluate(ctx context.Context, settings Settings, path string) (EvaluationReport, error) {
	var dataset Dataset
	if config.ReadClosedFile(path, &dataset, 4<<20) != nil || validateDataset(dataset) != nil {
		return EvaluationReport{}, errDataset
	}
	if settings.Model.FacePreparation == nil || !settings.Model.EvaluationOnly || settings.Model.Manifest.Validate() != nil || settings.Model.Width != 128 || settings.Model.Height != 128 || settings.Model.Manifest.Provenance.OutputSchemaDigest != onnx.OutputSchemaDigest() || settings.Model.Manifest.Provenance.ModelID != settings.Model.Registration.ModelID || settings.Model.Registration.Validate() != nil || settings.Model.Manifest.Provenance.PreprocessingDigest != onnx.FacePreprocessingDigest(*settings.Model.FacePreparation) || settings.Model.Registration.ConfigurationDigest != onnx.ConfigurationDigest(settings.Model) {
		return EvaluationReport{}, errDataset
	}
	engine, err := newEngine(ctx, settings)
	if err != nil {
		return EvaluationReport{}, errDataset
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return EvaluationReport{}, errDataset
	}
	defer func() { _ = root.Close() }()
	return evaluateDataset(ctx, dataset, settings.Model, root, engine)
}

func evaluateDataset(ctx context.Context, dataset Dataset, configuration onnx.Configuration, root *os.Root, engine onnx.ImagePredictor) (EvaluationReport, error) {
	raw, err := json.Marshal(dataset)
	if err != nil {
		return EvaluationReport{}, errDataset
	}
	hash := sha256.Sum256(raw)
	report := EvaluationReport{Version: 1, DatasetReference: dataset.Reference, DatasetDigest: "sha256:" + hex.EncodeToString(hash[:]), ApprovalReference: dataset.ApprovalReference, ThresholdReference: dataset.ThresholdReference, RealScoreThreshold: *dataset.RealScoreThreshold, Provenance: configuration.Manifest.Provenance, ConfigurationDigest: configuration.Registration.ConfigurationDigest, Groups: []EvaluationGroup{}, Nonresponses: map[string]int{}}
	subjects := map[string]bool{}
	groups := map[string]*EvaluationGroup{}
	for _, sample := range dataset.Samples {
		if ctx.Err() != nil {
			return EvaluationReport{}, ctx.Err()
		}
		raw, err := readDatasetImage(root, sample.Path)
		if err != nil {
			return EvaluationReport{}, err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != sample.SHA256 {
			clear(raw)
			return EvaluationReport{}, errDataset
		}
		if sample.Split != "evaluation" {
			clear(raw)
			continue
		}
		picture, err := onnx.DecodeImage(ctx, raw)
		clear(raw)
		if err != nil {
			return EvaluationReport{}, errDataset
		}
		result, err := engine.InferImage(ctx, picture)
		clear(picture.RGB)
		if err != nil {
			return EvaluationReport{}, errDataset
		}
		if (result.Score == nil) == (result.Reason == "") {
			return EvaluationReport{}, errDataset
		}
		if result.Score != nil && (math.IsNaN(*result.Score) || math.IsInf(*result.Score, 0) || *result.Score < 0 || *result.Score > 1) {
			return EvaluationReport{}, errDataset
		}
		if result.Reason != "" {
			switch result.Reason {
			case "face_not_found", "multiple_faces", "face_too_small", "face_at_edge":
				report.Nonresponses[result.Reason]++
			default:
				return EvaluationReport{}, errDataset
			}
		}
		if sample.SubjectID == "" {
			report.UnknownSubjectSamples++
		} else {
			subjects[sample.SubjectID] = true
		}
		key := sample.DeviceClass + "/" + sample.CaptureCondition + "/" + sample.AttackType
		group := groups[key]
		if group == nil {
			group = &EvaluationGroup{DeviceClass: sample.DeviceClass, CaptureCondition: sample.CaptureCondition, AttackType: sample.AttackType}
			groups[key] = group
		}
		report.Total.add(sample.Label, result.Score, report.RealScoreThreshold)
		group.Counts.add(sample.Label, result.Score, report.RealScoreThreshold)
	}
	report.EvaluationSubjects = len(subjects)
	report.SubjectSplitCheck = "checked_declared_ids"
	if report.UnknownSubjectSamples > 0 {
		report.SubjectSplitCheck = "unavailable_subject_ids"
	}
	report.Total.rates()
	report.Coverage = "both_classes_not_representativeness_approval"
	if report.Total.Genuine == 0 {
		report.Coverage = "attack_only"
	} else if report.Total.Attacks == 0 {
		report.Coverage = "genuine_only"
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := groups[key]
		group.Counts.rates()
		report.Groups = append(report.Groups, *group)
	}
	return report, nil
}
func (counts *Counts) add(label string, score *float64, threshold float64) {
	if label == "genuine" {
		counts.Genuine++
		if score != nil {
			counts.GenuineScored++
			if *score < threshold {
				counts.FalseRejects++
			}
		}
	} else {
		counts.Attacks++
		if score != nil {
			counts.AttacksScored++
			if *score >= threshold {
				counts.FalseAccepts++
			}
		}
	}
}
func fraction(numerator, denominator int) *float64 {
	if denominator == 0 {
		return nil
	}
	value := float64(numerator) / float64(denominator)
	return &value
}
func (counts *Counts) rates() {
	counts.APCER = fraction(counts.FalseAccepts, counts.AttacksScored)
	counts.BPCER = fraction(counts.FalseRejects, counts.GenuineScored)
	counts.APNRR = fraction(counts.Attacks-counts.AttacksScored, counts.Attacks)
	counts.BPNRR = fraction(counts.Genuine-counts.GenuineScored, counts.Genuine)
}
