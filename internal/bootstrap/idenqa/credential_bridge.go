package idenqa

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/managedtenant"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/transport/credentialbridge"
	"github.com/Mujhtech/idenqa/internal/transport/localreadinessbridge"
	"github.com/Mujhtech/idenqa/internal/transport/managedtenantbridge"
	"github.com/Mujhtech/idenqa/internal/transport/usagebridge"
	usagepostgres "github.com/Mujhtech/idenqa/internal/usage/postgres"
	"github.com/spf13/cobra"
)

const credentialBridgeShutdownTimeout = 10 * time.Second
const credentialBridgeWriteTimeout = 16 * time.Minute

type credentialBridgeOptions struct {
	envFile    string
	socketPath string
}

func newCredentialBridgeCommand() *cobra.Command {
	options := &credentialBridgeOptions{}
	command := &cobra.Command{
		Use:   "credential-bridge",
		Short: "Run the local deployment-agent credential boundary",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown credential-bridge operation %q", args[0]))
			}

			return cli.UsageError(errors.New("credential-bridge requires an operation"))
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("credential-bridge requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(
		&options.envFile,
		"env-file",
		config.DefaultEnvFile,
		"load local configuration from this dotenv file",
	)
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Serve the fixed credential protocol on a Unix socket",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if err := validateCredentialBridgeSocketPath(options.socketPath); err != nil {
				return cli.UsageError(err)
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return serveCredentialBridge(command, options)
		},
	}
	serve.Flags().StringVar(&options.socketPath, "socket", "", "absolute path to the protected Unix socket")
	_ = serve.MarkFlagRequired("socket")
	command.AddCommand(serve)

	return command
}

func serveCredentialBridge(command *cobra.Command, options *credentialBridgeOptions) error {
	ctx := command.Context()
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return cli.RuntimeError("load credential bridge configuration", err)
	}
	pool, err := postgres.Open(ctx, postgres.Config{
		URL: configuration.OperationalDatabaseURL(), MaxConnections: 2, MinConnections: 0,
		MaxConnectionAge: configuration.DatabaseMaxLifetime, MaxConnectionIdle: configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval, ConnectTimeout: configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return cli.RuntimeError("open credential bridge database", err)
	}
	defer pool.Close()
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		return cli.RuntimeError("check credential bridge database schema", err)
	}
	store, err := accesspostgres.New(pool)
	if err != nil {
		return cli.RuntimeError("compose credential bridge store", err)
	}
	issuer, err := composeIssuer(configuration, store)
	if err != nil {
		return cli.RuntimeError("compose credential bridge issuer", err)
	}
	service, err := access.NewCredentialBridge(issuer, store)
	if err != nil {
		return cli.RuntimeError("compose credential bridge service", err)
	}
	handler, err := credentialbridge.NewHandler(service)
	if err != nil {
		return cli.RuntimeError("compose credential bridge transport", err)
	}
	tenantStore, err := tenantpostgres.New(pool)
	if err != nil {
		return cli.RuntimeError("compose managed tenant store", err)
	}
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		return cli.RuntimeError("compose managed tenant identifiers", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, identifiers, clock.System{})
	if err != nil {
		return cli.RuntimeError("compose managed tenant admin", err)
	}
	lifetime := 24 * time.Hour
	if configuration.APIKeyMaximumLifetime > 0 && configuration.APIKeyMaximumLifetime < lifetime {
		lifetime = configuration.APIKeyMaximumLifetime
	}
	managedService, err := managedtenant.New(tenantAdmin, service, credentialbridge.NewSealer, clock.System{}, lifetime)
	if err != nil {
		return cli.RuntimeError("compose managed tenant service", err)
	}
	managedService, err = managedService.WithSyntheticRunner(managedSyntheticRunner{})
	if err != nil {
		return cli.RuntimeError("compose synthetic journey service", err)
	}
	managedHandler, err := managedtenantbridge.NewHandler(managedService)
	if err != nil {
		return cli.RuntimeError("compose managed tenant transport", err)
	}
	readinessHandler, err := localreadinessbridge.NewHandler(localReadinessService{configuration: configuration, database: pool})
	if err != nil {
		return cli.RuntimeError("compose local readiness transport", err)
	}
	usageStore, err := usagepostgres.New(pool)
	if err != nil {
		return cli.RuntimeError("compose regional usage receipt store", err)
	}
	usageHandler, err := usagebridge.NewHandler(usageStore, time.Now)
	if err != nil {
		return cli.RuntimeError("compose regional usage receipt bridge", err)
	}
	mux := http.NewServeMux()
	mux.Handle("/local/v1/usage/receipts/", usageHandler)
	mux.Handle("/local/v1/credential-commands/", handler)
	mux.Handle("/local/v1/managed-tenants/provision", managedHandler)
	mux.Handle("/local/v1/managed-tenants/renew", managedHandler)
	mux.Handle("/local/v1/synthetic-tenants/provision", managedHandler)
	mux.Handle("/local/v1/synthetic-journeys/run", managedHandler)
	mux.Handle("/local/v1/readiness", readinessHandler)
	listener, cleanup, err := listenCredentialBridge(ctx, options.socketPath)
	if err != nil {
		return cli.RuntimeError("listen on credential bridge socket", err)
	}
	defer cleanup()
	server := &http.Server{
		Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second,
		WriteTimeout: credentialBridgeWriteTimeout, IdleTimeout: 30 * time.Second,
		ErrorLog: log.New(command.ErrOrStderr(), "credential-bridge: ", log.LstdFlags),
	}
	if _, err := fmt.Fprintf(command.ErrOrStderr(), "credential bridge listening socket=%s\n", options.socketPath); err != nil {
		return cli.RuntimeError("write credential bridge status", err)
	}

	serveResult := make(chan error, 1)
	go func() { serveResult <- server.Serve(listener) }()
	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return cli.RuntimeError("serve credential bridge", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), credentialBridgeShutdownTimeout)
		defer cancel()
		shutdownErr := server.Shutdown(shutdownContext)
		serveErr := <-serveResult
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		if err := errors.Join(shutdownErr, serveErr); err != nil {
			return cli.RuntimeError("shutdown credential bridge", err)
		}

		return nil
	}
}

func validateCredentialBridgeSocketPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || filepath.Base(path) == "." {
		return errors.New("credential bridge socket must be a clean absolute path")
	}
	parent, err := os.Stat(filepath.Dir(path))
	if err != nil {
		return errors.New("credential bridge socket parent directory must already exist")
	}
	if !parent.IsDir() || parent.Mode().Perm()&0o022 != 0 {
		return errors.New("credential bridge socket parent directory must not be writable by group or others")
	}
	if _, err := os.Lstat(path); err == nil {
		return errors.New("credential bridge socket path already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return errors.New("credential bridge socket path cannot be inspected")
	}

	return nil
}

func listenCredentialBridge(ctx context.Context, path string) (net.Listener, func(), error) {
	if err := validateCredentialBridgeSocketPath(path); err != nil {
		return nil, nil, err
	}
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(ctx, "unix", path)
	if err != nil {
		return nil, nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)

		return nil, nil, fmt.Errorf("protect credential bridge socket: %w", err)
	}
	owned, err := os.Lstat(path)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(path)

		return nil, nil, fmt.Errorf("inspect credential bridge socket: %w", err)
	}
	cleanup := func() {
		_ = listener.Close()
		current, err := os.Lstat(path)
		if err == nil && os.SameFile(owned, current) {
			_ = os.Remove(path)
		}
	}

	return listener, cleanup, nil
}
