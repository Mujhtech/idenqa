package modelrunner

import (
	"strings"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/model"
)

// CheckThresholdRevision binds an exported registry revision to the local evaluation inputs.
// It never replaces a dataset's declared operating point or modifies an existing revision.
func CheckThresholdRevision(settings Settings, datasetPath, revisionPath string) error {
	var revision model.RegistryRevision
	if err := config.ReadClosedFile(revisionPath, &revision, 64<<10); err != nil {
		return err
	}
	if revision.Kind != "threshold" || revision.Revision < 1 || revision.Thresholds == nil || revision.Registration != nil || revision.Thresholds.Validate() != nil {
		return errDataset
	}
	digest, err := model.RevisionDigest(revision.Thresholds)
	if err != nil || digest != revision.Digest {
		return errDataset
	}
	value := revision.Thresholds
	if value.Provenance != settings.Model.Manifest.Provenance || value.Configuration != settings.Model.Registration || !value.HigherIsGenuine || value.ScoreName != "real_score" || value.Minimum != 0 || value.Maximum != 1 {
		return errDataset
	}
	var dataset Dataset
	if config.ReadClosedFile(datasetPath, &dataset, 4<<20) != nil || validateDataset(dataset) != nil || dataset.RealScoreThreshold == nil || *dataset.RealScoreThreshold != value.Cutoff || dataset.ThresholdReference != "threshold-sha256-"+strings.TrimPrefix(digest, "sha256:") {
		return errDataset
	}
	return nil
}
