package config_test

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestLoadWorkerConfiguration(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	t.Setenv("IDENQA_HEADGATE_INSTALLATION_ID", "idenqa-test")
	t.Setenv("IDENQA_WORKER_EVIDENCE_CONCURRENCY", "12")
	t.Setenv("IDENQA_WORKER_LEASE_DURATION", "45s")

	configuration, err := config.LoadWorker("")
	if err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
	if configuration.HeadgateSchema != "headgate" ||
		configuration.HeadgateInstallationID != "idenqa-test" ||
		configuration.EvidenceWorkers != 12 || configuration.LeaseDuration != 45*time.Second ||
		configuration.RetryBase != time.Second || configuration.RetryCap != time.Hour ||
		configuration.VerificationRateLimit != 120 ||
		configuration.MaintenanceTenantConcurrency != 1 ||
		configuration.ReconciliationSweepInterval != time.Minute ||
		configuration.ReconciliationBatchSize != 100 ||
		configuration.ReconciliationItemLease != 2*time.Minute ||
		configuration.ProgressPollInterval != time.Second {
		t.Fatalf("LoadWorker() = %+v", configuration)
	}
}

func TestLoadWorkerRequiresDeploymentIdentity(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	if _, err := config.LoadWorker(""); err == nil {
		t.Fatal("LoadWorker() error = nil")
	}
}
