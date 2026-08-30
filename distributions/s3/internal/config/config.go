// Package config owns deployment settings for the S3-backed API distribution.
package config

import (
	"errors"
	"time"

	s3objects "github.com/Mujhtech/idenqa/adapters/objectstore/s3"
	coreconfig "github.com/Mujhtech/idenqa/internal/config"
)

const ciphertextExpansionAllowance = 2

// Configuration embeds the complete core API schema and adds only settings
// owned by this distribution. AWS credentials and custom certificate
// authorities remain in the AWS SDK's standard external configuration chain.
type Configuration struct {
	coreconfig.API
	Bucket         string        `envconfig:"S3_BUCKET"`
	Prefix         string        `envconfig:"S3_PREFIX"`
	Region         string        `envconfig:"S3_REGION"`
	Endpoint       string        `envconfig:"S3_ENDPOINT"`
	ForcePathStyle bool          `envconfig:"S3_FORCE_PATH_STYLE" default:"false"`
	AllowHTTP      bool          `envconfig:"S3_ALLOW_HTTP" default:"false"`
	CleanupTimeout time.Duration `envconfig:"S3_CLEANUP_TIMEOUT" default:"30s"`
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
	if configuration.EvidenceLocalKeyringFile == "" {
		return Configuration{}, errors.New("validate S3 distribution configuration: local keyring file is required")
	}
	policy, err := configuration.EvidenceUploadPolicy()
	if err != nil {
		return Configuration{}, err
	}
	if err := s3objects.Validate(configuration.ObjectStoreConfig(policy.MaximumBytes())); err != nil {
		return Configuration{}, errors.New("validate S3 distribution configuration: object store settings are invalid")
	}

	return configuration, nil
}

// ObjectStoreConfig maps validated deployment settings to the provider adapter
// without exposing its types to core configuration or application packages.
func (configuration Configuration) ObjectStoreConfig(maximumPlaintextBytes int64) s3objects.Config {
	return s3objects.Config{
		Bucket:         configuration.Bucket,
		Prefix:         configuration.Prefix,
		Region:         configuration.Region,
		Endpoint:       configuration.Endpoint,
		ForcePathStyle: configuration.ForcePathStyle,
		AllowHTTP:      configuration.AllowHTTP,
		MaxObjectBytes: maximumPlaintextBytes * ciphertextExpansionAllowance,
		CleanupTimeout: configuration.CleanupTimeout,
	}
}
