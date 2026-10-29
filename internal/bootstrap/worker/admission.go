package worker

import (
	"errors"

	admissionpostgres "github.com/Mujhtech/idenqa/internal/admission/postgres"
	"github.com/Mujhtech/idenqa/internal/config"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
)

func configuredOperationalAdmission(configuration config.API, pool *pg.Pool) (*admissionpostgres.Store, error) {
	policy, err := config.LoadAdmissionPolicy(configuration.AdmissionRuntimeFile)
	if err != nil || policy == nil {
		return nil, err
	}
	if policy.Region() != configuration.Region {
		return nil, errors.New("admission region differs from worker region")
	}
	return admissionpostgres.New(pool, policy)
}
