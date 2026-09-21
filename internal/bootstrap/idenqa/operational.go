package idenqa

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/config"
	identitypostgres "github.com/Mujhtech/idenqa/internal/identity/postgres"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	kmskeys "github.com/Mujhtech/idenqa/internal/platform/kms/keys"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
)

// operationalDependencies is the shared database, key-provider, and
// identifier composition used by administrative CLI commands.
type operationalDependencies struct {
	pool        *postgres.Pool
	identifiers *id.Generator
	keys        kmskeys.Keys
	references  keycustody.References
}

// openOperationalDependencies loads the operator environment, opens the
// bounded database pool, and verifies the required schema version.
func openOperationalDependencies(ctx context.Context, envFile string) (*operationalDependencies, error) {
	configuration, err := config.LoadAPI(envFile)
	if err != nil {
		return nil, err
	}
	if configuration.DatabaseURL == "" {
		return nil, errors.New("runtime database url is required")
	}
	pool, err := postgres.Open(ctx, postgres.Config{
		URL: configuration.DatabaseURL, Role: configuration.DatabaseRole,
		MaxConnections: 2, MinConnections: 0,
		MaxConnectionAge:    configuration.DatabaseMaxLifetime,
		MaxConnectionIdle:   configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval,
		ConnectTimeout:      configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return nil, err
	}
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		pool.Close()

		return nil, err
	}
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		pool.Close()

		return nil, err
	}
	keyOptions := kmskeys.Options{
		Provider:             configuration.KMSProvider,
		LocalKeyringFile:     configuration.EvidenceLocalKeyringFile,
		AWSKeyID:             configuration.KMSAWSKeyID,
		AWSRegion:            configuration.KMSAWSRegion,
		AWSMaxPlaintextBytes: configuration.KMSAWSMaxPlaintextBytes,
	}
	if !kmskeys.Enabled(keyOptions) {
		pool.Close()

		return nil, errors.New("administrative key operation requires a configured KMS provider")
	}
	keyProvider, err := kmskeys.Open(ctx, keyOptions)
	if err != nil {
		pool.Close()

		return nil, err
	}
	identityStore, err := identitypostgres.New(pool, keyProvider, keyProvider, identifiers, nil)
	if err != nil {
		pool.Close()

		return nil, err
	}

	return &operationalDependencies{pool: pool, identifiers: identifiers, keys: keyProvider, references: identityStore}, nil
}

// Close releases the command-owned key provider and database pool.
func (dependencies *operationalDependencies) Close() {
	if dependencies != nil && dependencies.pool != nil {
		if dependencies.keys != nil {
			_ = dependencies.keys.Close()
		}
		dependencies.pool.Close()
	}
}
