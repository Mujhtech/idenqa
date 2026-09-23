// Package config owns deployment settings for the S3-backed distribution.
package config

import (
	"errors"
	"time"

	s3objects "github.com/Mujhtech/idenqa/adapters/objectstore/s3"
	coreconfig "github.com/Mujhtech/idenqa/internal/config"
)

const ciphertextExpansionAllowance = 2

// ObjectStoreConfiguration contains the S3 settings shared by the API and
// worker composition roots. AWS credentials and custom certificate authorities
// remain in the AWS SDK's standard external configuration chain.
type ObjectStoreConfiguration struct {
	Bucket         string        `envconfig:"S3_BUCKET"`
	Prefix         string        `envconfig:"S3_PREFIX"`
	S3Region       string        `envconfig:"S3_REGION"`
	Endpoint       string        `envconfig:"S3_ENDPOINT"`
	ForcePathStyle bool          `envconfig:"S3_FORCE_PATH_STYLE" default:"false"`
	AllowHTTP      bool          `envconfig:"S3_ALLOW_HTTP" default:"false"`
	CleanupTimeout time.Duration `envconfig:"S3_CLEANUP_TIMEOUT" default:"30s"`
}

// Configuration embeds the complete core API schema and the distribution's
// object-store settings.
type Configuration struct {
	coreconfig.API
	ObjectStoreConfiguration
}

// WorkerConfiguration embeds the complete core worker schema and the same
// object-store settings used by the API distribution.
type WorkerConfiguration struct {
	coreconfig.Worker
	ObjectStoreConfiguration
}

// Load processes the core and distribution fields as one strict IDENQA_*
// environment schema, then validates both layers before resources are opened.
func Load(envFile string) (Configuration, error) {
	var configuration Configuration
	if err := coreconfig.LoadAPIInto(envFile, &configuration, &configuration.API); err != nil {
		return Configuration{}, err
	}
	if configuration.EvidenceLocalDirectory != "" {
		return Configuration{}, errors.New("validate S3 distribution configuration: local evidence directory is not supported")
	}
	if configuration.KMSProvider == "local" && configuration.EvidenceLocalKeyringFile == "" {
		return Configuration{}, errors.New("validate S3 distribution configuration: local keyring file is required")
	}
	if configuration.KMSProvider == "aws" && configuration.EvidenceLocalKeyringFile != "" {
		return Configuration{}, errors.New("validate S3 distribution configuration: AWS KMS provider must not configure a local keyring")
	}
	if err := validateObjectStore(configuration.ObjectStoreConfiguration, configuration.EvidenceUploadConfiguration); err != nil {
		return Configuration{}, errors.New("validate S3 distribution configuration: object store settings are invalid")
	}

	return configuration, nil
}

// LoadWorker processes the core worker and distribution fields as one strict
// IDENQA_* environment schema, then validates the S3 deletion composition.
func LoadWorker(envFile string) (WorkerConfiguration, error) {
	var configuration WorkerConfiguration
	if err := coreconfig.LoadWorkerInto(envFile, &configuration, &configuration.Worker); err != nil {
		return WorkerConfiguration{}, err
	}
	if configuration.EvidenceLocalDirectory != "" {
		return WorkerConfiguration{}, errors.New("validate S3 worker distribution configuration: local evidence directory is not supported")
	}
	if err := validateObjectStore(configuration.ObjectStoreConfiguration, configuration.EvidenceUploadConfiguration); err != nil {
		return WorkerConfiguration{}, errors.New("validate S3 worker distribution configuration: object store settings are invalid")
	}

	return configuration, nil
}

// ObjectStoreConfig maps validated deployment settings to the provider adapter
// without exposing its types to core configuration or application packages.
func (configuration Configuration) ObjectStoreConfig(maximumPlaintextBytes int64) s3objects.Config {
	return configuration.ObjectStoreConfiguration.objectStoreConfig(maximumPlaintextBytes)
}

// ObjectStoreConfig maps worker settings to the provider adapter.
func (configuration WorkerConfiguration) ObjectStoreConfig(maximumPlaintextBytes int64) s3objects.Config {
	return configuration.ObjectStoreConfiguration.objectStoreConfig(maximumPlaintextBytes)
}

func (configuration ObjectStoreConfiguration) objectStoreConfig(maximumPlaintextBytes int64) s3objects.Config {
	return s3objects.Config{
		Bucket:         configuration.Bucket,
		Prefix:         configuration.Prefix,
		Region:         configuration.S3Region,
		Endpoint:       configuration.Endpoint,
		ForcePathStyle: configuration.ForcePathStyle,
		AllowHTTP:      configuration.AllowHTTP,
		MaxObjectBytes: maximumPlaintextBytes * ciphertextExpansionAllowance,
		CleanupTimeout: configuration.CleanupTimeout,
	}
}

func validateObjectStore(configuration ObjectStoreConfiguration, upload coreconfig.EvidenceUploadConfiguration) error {
	policy, err := upload.EvidenceUploadPolicy()
	if err != nil {
		return err
	}

	return s3objects.Validate(configuration.objectStoreConfig(policy.MaximumBytes()))
}
