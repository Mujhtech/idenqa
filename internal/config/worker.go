package config

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Worker is the configuration owned by the open-source background worker.
type Worker struct {
	ReviewRoutingFile string `envconfig:"REVIEW_ROUTING_FILE"`
	API
	// SyntheticProcessing enables the v1 fixture plan only on synthetic-data installations.
	SyntheticProcessing           bool          `envconfig:"WORKER_SYNTHETIC_PROCESSING" default:"false"`
	WorkerID                      string        `envconfig:"WORKER_ID"`
	VerificationWorkers           int           `envconfig:"WORKER_VERIFICATION_CONCURRENCY" default:"8"`
	EvidenceWorkers               int           `envconfig:"WORKER_EVIDENCE_CONCURRENCY" default:"4"`
	DeliveryWorkers               int           `envconfig:"WORKER_DELIVERY_CONCURRENCY" default:"8"`
	MaintenanceWorkers            int           `envconfig:"WORKER_MAINTENANCE_CONCURRENCY" default:"2"`
	VerificationRateLimit         int64         `envconfig:"WORKER_VERIFICATION_RATE_LIMIT" default:"120"`
	VerificationRatePeriod        time.Duration `envconfig:"WORKER_VERIFICATION_RATE_PERIOD" default:"1m"`
	VerificationRateBurst         int64         `envconfig:"WORKER_VERIFICATION_RATE_BURST" default:"20"`
	VerificationTenantConcurrency uint64        `envconfig:"WORKER_VERIFICATION_TENANT_CONCURRENCY" default:"4"`
	EvidenceRateLimit             int64         `envconfig:"WORKER_EVIDENCE_RATE_LIMIT" default:"60"`
	EvidenceRatePeriod            time.Duration `envconfig:"WORKER_EVIDENCE_RATE_PERIOD" default:"1m"`
	EvidenceRateBurst             int64         `envconfig:"WORKER_EVIDENCE_RATE_BURST" default:"10"`
	EvidenceTenantConcurrency     uint64        `envconfig:"WORKER_EVIDENCE_TENANT_CONCURRENCY" default:"2"`
	DeliveryRateLimit             int64         `envconfig:"WORKER_DELIVERY_RATE_LIMIT" default:"300"`
	DeliveryRatePeriod            time.Duration `envconfig:"WORKER_DELIVERY_RATE_PERIOD" default:"1m"`
	DeliveryRateBurst             int64         `envconfig:"WORKER_DELIVERY_RATE_BURST" default:"50"`
	DeliveryTenantConcurrency     uint64        `envconfig:"WORKER_DELIVERY_TENANT_CONCURRENCY" default:"8"`
	MaintenanceRateLimit          int64         `envconfig:"WORKER_MAINTENANCE_RATE_LIMIT" default:"30"`
	MaintenanceRatePeriod         time.Duration `envconfig:"WORKER_MAINTENANCE_RATE_PERIOD" default:"1m"`
	MaintenanceRateBurst          int64         `envconfig:"WORKER_MAINTENANCE_RATE_BURST" default:"5"`
	MaintenanceTenantConcurrency  uint64        `envconfig:"WORKER_MAINTENANCE_TENANT_CONCURRENCY" default:"1"`
	LeaseDuration                 time.Duration `envconfig:"WORKER_LEASE_DURATION" default:"30s"`
	ShutdownTimeout               time.Duration `envconfig:"WORKER_SHUTDOWN_TIMEOUT" default:"25s"`
	EmptyPollFloor                time.Duration `envconfig:"WORKER_EMPTY_POLL_FLOOR" default:"50ms"`
	EmptyPollCeiling              time.Duration `envconfig:"WORKER_EMPTY_POLL_CEILING" default:"2s"`
	CrashLimit                    int32         `envconfig:"WORKER_CRASH_LIMIT" default:"3"`
	RetryBase                     time.Duration `envconfig:"WORKER_RETRY_BASE" default:"1s"`
	RetryCap                      time.Duration `envconfig:"WORKER_RETRY_CAP" default:"1h"`
	MemoryLimitBytes              uint64        `envconfig:"WORKER_MEMORY_LIMIT_BYTES" default:"0"`
	MemoryCheckPeriod             time.Duration `envconfig:"WORKER_MEMORY_CHECK_INTERVAL" default:"30s"`
	ReconciliationSweepInterval   time.Duration `envconfig:"WORKER_RECONCILIATION_SWEEP_INTERVAL" default:"1m"`
	ReconciliationBatchSize       int           `envconfig:"WORKER_RECONCILIATION_BATCH_SIZE" default:"100"`
	ReconciliationItemLease       time.Duration `envconfig:"WORKER_RECONCILIATION_ITEM_LEASE" default:"2m"`
	ProgressPollInterval          time.Duration `envconfig:"WORKER_PROGRESS_POLL_INTERVAL" default:"1s"`
}

// LoadWorker loads the optional dotenv file and validates IDENQA_* worker settings.
func LoadWorker(envFile string) (Worker, error) {
	var configuration Worker
	if err := loadAPIInto(envFile, &configuration, &configuration.API, false); err != nil {
		return Worker{}, err
	}
	if err := configuration.validateWorker(); err != nil {
		return Worker{}, fmt.Errorf("validate worker configuration: %w", err)
	}
	return configuration, nil
}

// LoadWorkerInto loads and validates the core worker fields embedded in a
// larger deployment-owned configuration. Provider distributions use this seam
// to add settings without introducing provider dependencies into the root
// worker module.
func LoadWorkerInto(envFile string, target any, configuration *Worker) error {
	if target == nil || configuration == nil {
		return errors.New("worker configuration target is required")
	}
	if err := loadAPIInto(envFile, target, &configuration.API, true); err != nil {
		return err
	}
	if err := configuration.validateWorker(); err != nil {
		return fmt.Errorf("validate worker configuration: %w", err)
	}

	return nil
}

func (configuration Worker) validateWorker() error {
	if configuration.DatabaseMaxConnections < 2 {
		return errors.New("worker database pool requires one query connection plus one notification connection")
	}
	if !validDeploymentRegion(configuration.HeadgateInstallationID) ||
		!validDeploymentRegion(configuration.HeadgateSchema) {
		return errors.New("headgate installation ID and schema must be lowercase deployment identifiers")
	}
	if configuration.WorkerID != "" &&
		(strings.TrimSpace(configuration.WorkerID) != configuration.WorkerID ||
			!validDeploymentRegion(configuration.WorkerID)) {
		return errors.New("worker ID must be a lowercase deployment identifier")
	}
	workers := []int{
		configuration.VerificationWorkers, configuration.EvidenceWorkers,
		configuration.DeliveryWorkers, configuration.MaintenanceWorkers,
	}
	for _, count := range workers {
		if count < 1 || count > 1024 {
			return errors.New("worker queue concurrency must be between 1 and 1024")
		}
	}
	rateLimits := []int64{
		configuration.VerificationRateLimit, configuration.EvidenceRateLimit,
		configuration.DeliveryRateLimit, configuration.MaintenanceRateLimit,
	}
	ratePeriods := []time.Duration{
		configuration.VerificationRatePeriod, configuration.EvidenceRatePeriod,
		configuration.DeliveryRatePeriod, configuration.MaintenanceRatePeriod,
	}
	rateBursts := []int64{
		configuration.VerificationRateBurst, configuration.EvidenceRateBurst,
		configuration.DeliveryRateBurst, configuration.MaintenanceRateBurst,
	}
	tenantConcurrency := []uint64{
		configuration.VerificationTenantConcurrency, configuration.EvidenceTenantConcurrency,
		configuration.DeliveryTenantConcurrency, configuration.MaintenanceTenantConcurrency,
	}
	for index := range rateLimits {
		if rateLimits[index] < 1 || rateLimits[index] > 1_000_000_000 ||
			ratePeriods[index] < time.Millisecond || ratePeriods[index] > 24*time.Hour ||
			rateBursts[index] < 1 || rateBursts[index] > 1_000_000_000 ||
			tenantConcurrency[index] < 1 || tenantConcurrency[index] > 100_000 {
			return errors.New("worker queue admission policy is invalid")
		}
	}
	if configuration.LeaseDuration < 3*time.Second || configuration.ShutdownTimeout <= 0 ||
		configuration.EmptyPollFloor < time.Millisecond ||
		configuration.EmptyPollCeiling < configuration.EmptyPollFloor ||
		configuration.CrashLimit == 0 || configuration.CrashLimit > 100 ||
		configuration.RetryBase < time.Millisecond || configuration.RetryCap < configuration.RetryBase ||
		configuration.MemoryCheckPeriod <= 0 ||
		configuration.ReconciliationSweepInterval < time.Second ||
		configuration.ReconciliationSweepInterval > time.Hour ||
		configuration.ReconciliationBatchSize < 1 || configuration.ReconciliationBatchSize > 100 ||
		configuration.ReconciliationItemLease < time.Second ||
		configuration.ReconciliationItemLease > 10*time.Minute ||
		configuration.ProgressPollInterval < 100*time.Millisecond ||
		configuration.ProgressPollInterval > time.Minute {
		return errors.New("worker lifecycle settings are invalid")
	}
	return nil
}
