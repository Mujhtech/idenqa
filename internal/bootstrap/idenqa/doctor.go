package idenqa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/spf13/cobra"
)

const (
	doctorStatusPass = "pass"
	doctorStatusWarn = "warn"
	doctorStatusFail = "fail"
	doctorStatusSkip = "skip"

	doctorAPITimeout = 10 * time.Second
)

type doctorOptions struct {
	envFile, apiURL, keyFile string
	jsonOutput               bool
}

// doctorDatabase is one live PostgreSQL connection used only by preflight checks.
type doctorDatabase interface {
	Ping(context.Context) error
	CheckHeadgate(context.Context, string) (taskheadgate.MigrationReport, error)
	Close()
}

// doctorBackend opens the operational dependencies a preflight inspects. It is
// an interface so command tests can prove ordering and failure reporting
// without a live PostgreSQL instance.
type doctorBackend interface {
	OpenDatabase(context.Context, config.API) (doctorDatabase, error)
	ApplicationMigration(context.Context, config.API) (postgres.MigrationReport, error)
}

type doctorBackendProduction struct{}

func (doctorBackendProduction) OpenDatabase(ctx context.Context, configuration config.API) (doctorDatabase, error) {
	pool, err := postgres.Open(ctx, postgres.Config{
		URL: configuration.DatabaseURL, Role: configuration.DatabaseRole,
		MaxConnections:      configuration.DatabaseMaxConnections,
		MinConnections:      configuration.DatabaseMinConnections,
		MaxConnectionAge:    configuration.DatabaseMaxLifetime,
		MaxConnectionIdle:   configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval,
		ConnectTimeout:      configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return nil, err
	}
	return doctorPool{pool: pool}, nil
}

func (doctorBackendProduction) ApplicationMigration(ctx context.Context, configuration config.API) (postgres.MigrationReport, error) {
	migrator, err := postgres.OpenMigrator(ctx, postgres.MigrationConfig{
		URL:              configuration.OperationalDatabaseURL(),
		ConnectTimeout:   configuration.DatabaseConnectTimeout,
		StatementTimeout: configuration.DatabaseMigrationTimeout,
	})
	if err != nil {
		return postgres.MigrationReport{}, err
	}
	report, preflightErr := migrator.Preflight(ctx)
	closeErr := migrator.Close()
	return report, errors.Join(preflightErr, closeErr)
}

type doctorPool struct{ pool *postgres.Pool }

func (database doctorPool) Ping(ctx context.Context) error { return database.pool.Ping(ctx) }

func (database doctorPool) CheckHeadgate(ctx context.Context, schema string) (taskheadgate.MigrationReport, error) {
	return taskheadgate.CheckPoolSchema(ctx, database.pool.Native(), schema)
}

func (database doctorPool) Close() { database.pool.Close() }

type doctorCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type doctorReport struct {
	Checks   []doctorCheck `json:"checks"`
	Warnings int           `json:"warnings"`
	Failures int           `json:"failures"`
	OK       bool          `json:"ok"`
}

func (report *doctorReport) add(name, status, detail string) {
	report.addCheck(doctorCheck{Name: name, Status: status, Detail: detail})
}

func (report *doctorReport) addCheck(check doctorCheck) {
	report.Checks = append(report.Checks, check)
	switch check.Status {
	case doctorStatusWarn:
		report.Warnings++
	case doctorStatusFail:
		report.Failures++
	}
	report.OK = report.Failures == 0
}

func newDoctorCommand() *cobra.Command {
	options := &doctorOptions{}
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Run clean-install preflight and end-to-end diagnostics",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return executeDoctor(command, options, doctorBackendProduction{})
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.envFile, "env-file", config.DefaultEnvFile, "load local configuration from this dotenv file")
	flags.StringVar(&options.apiURL, "api-url", "", "optionally check this Core API URL for reachability and credential validity")
	flags.StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
	flags.BoolVar(&options.jsonOutput, "json", false, "print the diagnostic report as one JSON document")
	return command
}

func executeDoctor(command *cobra.Command, options *doctorOptions, backend doctorBackend) error {
	report := doctorReport{Checks: []doctorCheck{}}
	output := command.OutOrStdout()
	ctx := command.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	var base *url.URL
	if options.apiURL != "" {
		parsed, err := parsePublicAPIURL(options.apiURL)
		if err != nil {
			return cli.UsageError(err)
		}
		base = parsed
	}
	configuration, configurationErr := config.LoadAPI(options.envFile)
	if configurationErr != nil {
		report.add("configuration", doctorStatusFail,
			"configuration could not be loaded or validated: "+configurationErr.Error())
		for _, name := range []string{"database", "migrations", "headgate", "object-storage", "key-provider", "runner"} {
			report.add(name, doctorStatusSkip, "not checked because configuration is invalid")
		}
		if base != nil {
			report.add("api-reachability", doctorStatusSkip, "not checked because configuration is invalid")
			report.add("api-credential", doctorStatusSkip, "not checked because configuration is invalid")
		}
		if writeErr := writeDoctorReport(output, report, options.jsonOutput); writeErr != nil {
			return writeErr
		}
		return cli.RuntimeError("doctor", errors.New("required preflight checks failed"))
	}
	report.add("configuration", doctorStatusPass,
		"database, object storage, key provider, and runner configuration were validated")

	doctorCheckDatabase(ctx, configuration, backend, &report)
	doctorCheckObjectStorage(configuration, &report)
	doctorCheckKeyProvider(configuration, &report)
	doctorCheckRunner(configuration, &report)

	if base != nil {
		doctorCheckAPI(ctx, base, options, &report)
	}
	if err := writeDoctorReport(output, report, options.jsonOutput); err != nil {
		return err
	}
	if report.Failures > 0 {
		return cli.RuntimeError("doctor", errors.New("required preflight checks failed"))
	}
	return nil
}

func doctorCheckDatabase(ctx context.Context, configuration config.API, backend doctorBackend, report *doctorReport) {
	database, err := backend.OpenDatabase(ctx, configuration)
	if err != nil {
		report.add("database", doctorStatusFail, "PostgreSQL connectivity failed")
		report.add("migrations", doctorStatusSkip, "not checked because PostgreSQL is unavailable")
		report.add("headgate", doctorStatusSkip, "not checked because PostgreSQL is unavailable")
		return
	}
	defer database.Close()
	if err := database.Ping(ctx); err != nil {
		report.add("database", doctorStatusFail, "PostgreSQL connectivity failed")
		report.add("migrations", doctorStatusSkip, "not checked because PostgreSQL is unavailable")
		report.add("headgate", doctorStatusSkip, "not checked because PostgreSQL is unavailable")
		return
	}
	report.add("database", doctorStatusPass, "PostgreSQL connectivity verified")

	report.addCheck(doctorMigrationStatus(ctx, configuration, backend))
	if report.Checks[len(report.Checks)-1].Status == doctorStatusFail {
		report.add("headgate", doctorStatusSkip, "not checked because the application schema is incompatible")
		return
	}
	report.addCheck(doctorHeadgateStatus(ctx, configuration, database))
}

func doctorMigrationStatus(ctx context.Context, configuration config.API, backend doctorBackend) doctorCheck {
	report, err := backend.ApplicationMigration(ctx, configuration)
	switch {
	case errors.Is(err, postgres.ErrMigrationDirty):
		return doctorCheck{Name: "migrations", Status: doctorStatusFail,
			Detail: fmt.Sprintf("schema migration is dirty current=%d latest=%d", report.Current, report.Latest)}
	case errors.Is(err, postgres.ErrMigrationNewer):
		return doctorCheck{Name: "migrations", Status: doctorStatusFail,
			Detail: fmt.Sprintf("database schema is newer than this binary current=%d latest=%d", report.Current, report.Latest)}
	case err != nil:
		return doctorCheck{Name: "migrations", Status: doctorStatusFail, Detail: "application migration preflight failed"}
	case report.Pending:
		return doctorCheck{Name: "migrations", Status: doctorStatusWarn,
			Detail: fmt.Sprintf("schema current=%d latest=%d; run idenqa migrate up", report.Current, report.Latest)}
	default:
		return doctorCheck{Name: "migrations", Status: doctorStatusPass,
			Detail: fmt.Sprintf("schema current=%d latest=%d", report.Current, report.Latest)}
	}
}

func doctorHeadgateStatus(ctx context.Context, configuration config.API, database doctorDatabase) doctorCheck {
	report, err := database.CheckHeadgate(ctx, configuration.HeadgateSchema)
	switch {
	case report.State == "empty":
		return doctorCheck{Name: "headgate", Status: doctorStatusWarn,
			Detail: "schema is not installed; run idenqa migrate headgate up"}
	case report.State == "unversioned":
		return doctorCheck{Name: "headgate", Status: doctorStatusFail,
			Detail: "schema is partially installed without migration history"}
	case report.Current > report.Latest:
		return doctorCheck{Name: "headgate", Status: doctorStatusFail,
			Detail: fmt.Sprintf("schema is newer than this binary current=%d latest=%d", report.Current, report.Latest)}
	case report.Pending:
		return doctorCheck{Name: "headgate", Status: doctorStatusWarn,
			Detail: fmt.Sprintf("schema state=%s current=%d latest=%d; run idenqa migrate headgate up", report.State, report.Current, report.Latest)}
	case err != nil:
		return doctorCheck{Name: "headgate", Status: doctorStatusFail, Detail: "headgate schema preflight failed"}
	default:
		return doctorCheck{Name: "headgate", Status: doctorStatusPass,
			Detail: fmt.Sprintf("schema state=%s current=%d latest=%d", report.State, report.Current, report.Latest)}
	}
}

func doctorCheckObjectStorage(configuration config.API, report *doctorReport) {
	if configuration.EvidenceLocalDirectory == "" {
		report.add("object-storage", doctorStatusWarn,
			"local evidence paths are not configured; a provider-backed distribution must supply object storage")
		return
	}
	store, err := local.Open(local.Config{
		Directory: configuration.EvidenceLocalDirectory, MaxObjectBytes: configuration.EvidenceUploadMaximumBytes,
	})
	if err != nil {
		report.add("object-storage", doctorStatusFail, "local evidence directory is not usable")
		return
	}
	if err := store.Close(); err != nil {
		report.add("object-storage", doctorStatusFail, "local evidence directory is not usable")
		return
	}
	report.add("object-storage", doctorStatusPass, "local evidence ciphertext store is usable")
}

func doctorCheckKeyProvider(configuration config.API, report *doctorReport) {
	if configuration.EvidenceLocalKeyringFile == "" {
		report.add("key-provider", doctorStatusWarn,
			"local key provider is not configured; a provider-backed distribution must supply a KMS adapter")
		return
	}
	keyring, err := localkms.Open(configuration.EvidenceLocalKeyringFile)
	if err != nil {
		report.add("key-provider", doctorStatusFail, "local keyring is invalid or not owner-only")
		return
	}
	if err := keyring.Close(); err != nil {
		report.add("key-provider", doctorStatusFail, "local keyring could not be released")
		return
	}
	report.add("key-provider", doctorStatusPass, "local owner-only keyring is usable")
}

func doctorCheckRunner(configuration config.API, report *doctorReport) {
	checked := false
	if configuration.ProviderRuntimeFile != "" {
		checked = true
		if _, err := config.LoadProviderRuntime(configuration.ProviderRuntimeFile); err != nil {
			report.add("runner", doctorStatusFail, "provider runtime configuration is invalid")
			return
		}
	}
	if configuration.ModelRuntimeFile != "" {
		checked = true
		if _, err := config.LoadModelRuntimes(configuration.ModelRuntimeFile); err != nil {
			report.add("runner", doctorStatusFail, "model runtime configuration is invalid")
			return
		}
	}
	if configuration.ReviewAuthorityFile != "" {
		checked = true
		if err := (config.ReviewAuthorityFile{Path: configuration.ReviewAuthorityFile}).Validate(); err != nil {
			report.add("runner", doctorStatusFail, "review authority configuration is invalid")
			return
		}
	}
	if !checked {
		report.add("runner", doctorStatusPass, "no mounted provider, model, or review-authority file is configured")
		return
	}
	report.add("runner", doctorStatusPass, "mounted runner configuration is valid")
}

func doctorCheckAPI(ctx context.Context, base *url.URL, options *doctorOptions, report *doctorReport) {
	client := &http.Client{
		Timeout:       doctorAPITimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	reachability := *base
	reachability.Path = strings.TrimRight(base.Path, "/") + "/readyz"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, reachability.String(), nil)
	if err != nil {
		report.add("api-reachability", doctorStatusFail, "API readiness request could not be constructed")
	} else {
		response, requestErr := client.Do(request)
		if requestErr != nil {
			report.add("api-reachability", doctorStatusFail, "API readiness probe failed")
		} else {
			status := response.StatusCode
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if status == http.StatusOK {
				report.add("api-reachability", doctorStatusPass, "API readiness probe succeeded")
			} else {
				report.add("api-reachability", doctorStatusFail,
					fmt.Sprintf("API readiness probe returned status %d", status))
			}
		}
	}

	credential, credentialErr := readPublicAPICredential(options.keyFile)
	if credentialErr != nil {
		report.add("api-credential", doctorStatusFail, "API credential could not be read")
		return
	}
	if credential == "" {
		report.add("api-credential", doctorStatusSkip, "no API credential was supplied")
		return
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/v1/tenant"
	request, err = http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		report.add("api-credential", doctorStatusFail, "API credential request could not be constructed")
		return
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	response, err := client.Do(request)
	if err != nil {
		report.add("api-credential", doctorStatusFail, "API credential check failed")
		return
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	switch response.StatusCode {
	case http.StatusOK:
		report.add("api-credential", doctorStatusPass, "API credential is accepted")
	case http.StatusUnauthorized:
		report.add("api-credential", doctorStatusFail, "API credential was rejected")
	case http.StatusForbidden:
		report.add("api-credential", doctorStatusWarn,
			"API credential is authenticated but lacks tenant:read")
	case http.StatusServiceUnavailable:
		report.add("api-credential", doctorStatusWarn,
			"API is not ready to validate the credential; retry after readiness")
	default:
		report.add("api-credential", doctorStatusFail,
			fmt.Sprintf("API credential check returned status %d", response.StatusCode))
	}
}

func writeDoctorReport(output io.Writer, report doctorReport, jsonOutput bool) error {
	if jsonOutput {
		encoded, err := json.Marshal(report)
		if err != nil {
			return cli.RuntimeError("doctor", errors.New("encode diagnostic report"))
		}
		if _, err := fmt.Fprintln(output, string(encoded)); err != nil {
			return cli.RuntimeError("doctor", errors.New("write diagnostic report"))
		}
		return nil
	}
	for _, check := range report.Checks {
		if _, err := fmt.Fprintf(output, "%-16s %-4s %s\n", check.Name, check.Status, check.Detail); err != nil {
			return cli.RuntimeError("doctor", errors.New("write diagnostic report"))
		}
	}
	state := "ok"
	if report.Failures > 0 {
		state = "failed"
	}
	if _, err := fmt.Fprintf(
		output, "doctor %s checks=%d warnings=%d failures=%d\n",
		state, len(report.Checks), report.Warnings, report.Failures,
	); err != nil {
		return cli.RuntimeError("doctor", errors.New("write diagnostic summary"))
	}
	return nil
}
