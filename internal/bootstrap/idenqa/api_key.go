package idenqa

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/spf13/cobra"
)

type apiKeyOptions struct {
	envFile      string
	actor        string
	reason       string
	tenantID     string
	keyID        string
	label        string
	scopes       []string
	expiresAt    string
	noExpiry     bool
	overlap      time.Duration
	version      int64
	confirmation bool
}

func newAPIKeyCommand() *cobra.Command {
	options := &apiKeyOptions{}
	command := &cobra.Command{
		Use:   "api-key",
		Short: "Administer tenant API keys",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown api-key operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("api-key requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", config.DefaultEnvFile, "load local configuration from this dotenv file")
	command.PersistentFlags().StringVar(&options.actor, "actor", "", "operator assertion recorded in the audit trail")
	command.PersistentFlags().StringVar(&options.reason, "reason", "", "reason recorded in the audit trail")
	command.PersistentFlags().StringVar(&options.tenantID, "tenant", "", "owning tenant identifier")
	command.AddCommand(
		newAPIKeyOperationCommand("create", options),
		newAPIKeyOperationCommand("list", options),
		newAPIKeyOperationCommand("rotate", options),
		newAPIKeyOperationCommand("revoke", options),
	)

	return command
}

func newAPIKeyOperationCommand(operation string, options *apiKeyOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   operation,
		Short: apiKeyOperationDescription(operation),
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validateAPIKeyOptions(operation, options)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeAPIKeyOperation(command, operation, options)
		},
	}
	switch operation {
	case "create":
		command.Flags().StringVar(&options.label, "label", "", "operator-facing key label")
		command.Flags().StringArrayVar(&options.scopes, "scope", nil, "scope pattern; repeat for multiple patterns")
		addAPIKeyExpiryFlags(command, options)
	case "rotate":
		command.Flags().StringVar(&options.keyID, "id", "", "predecessor API-key identifier")
		command.Flags().DurationVar(&options.overlap, "overlap", 0, "bounded predecessor and successor overlap")
		command.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm the irreversible rotation schedule")
		addAPIKeyExpiryFlags(command, options)
	case "revoke":
		command.Flags().StringVar(&options.keyID, "id", "", "API-key identifier")
		command.Flags().Int64Var(&options.version, "version", 0, "expected lifecycle version")
		command.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm irreversible revocation")
	}

	return command
}

func addAPIKeyExpiryFlags(command *cobra.Command, options *apiKeyOptions) {
	command.Flags().StringVar(&options.expiresAt, "expires-at", "", "fixed expiry in RFC3339 format")
	command.Flags().BoolVar(&options.noExpiry, "no-expiry", false, "explicitly request a key without expiry")
}

func apiKeyOperationDescription(operation string) string {
	switch operation {
	case "create":
		return "Create and display a tenant API key once"
	case "list":
		return "List secret-free tenant API-key metadata"
	case "rotate":
		return "Rotate an API key with bounded overlap"
	case "revoke":
		return "Irreversibly revoke an API key"
	default:
		return "Operate on a tenant API key"
	}
}

func validateAPIKeyOptions(operation string, options *apiKeyOptions) error {
	if options.actor == "" || options.reason == "" {
		return cli.UsageError(errors.New("api-key operation requires --actor and --reason"))
	}
	if err := (access.AdminAction{Actor: options.actor, Reason: options.reason}).Validate(); err != nil {
		return cli.UsageError(errors.New("api-key operation has invalid --actor or --reason"))
	}
	if options.tenantID == "" {
		return cli.UsageError(errors.New("api-key operation requires --tenant"))
	}
	if _, err := id.ParseTenant(options.tenantID); err != nil {
		return cli.UsageError(errors.New("api-key tenant identifier is invalid"))
	}
	switch operation {
	case "create":
		if options.label == "" || len(options.scopes) == 0 {
			return cli.UsageError(errors.New("api-key create requires --label and at least one --scope"))
		}
		if err := validateExpiryChoice(options); err != nil {
			return err
		}
		if _, err := parseExpiryIntent(options); err != nil {
			return err
		}
		if _, err := parseAPIKeyPatterns(options.scopes); err != nil {
			return err
		}
	case "rotate":
		if options.keyID == "" || options.overlap <= 0 {
			return cli.UsageError(errors.New("api-key rotate requires --id and positive --overlap"))
		}
		if !options.confirmation {
			return cli.UsageError(errors.New("api-key rotate requires --confirm"))
		}
		if err := validateExpiryChoice(options); err != nil {
			return err
		}
		if _, err := id.ParseAPIKey(options.keyID); err != nil {
			return cli.UsageError(errors.New("API-key identifier is invalid"))
		}
		if _, err := parseExpiryIntent(options); err != nil {
			return err
		}
	case "revoke":
		if options.keyID == "" || options.version < 1 {
			return cli.UsageError(errors.New("api-key revoke requires --id and positive --version"))
		}
		if !options.confirmation {
			return cli.UsageError(errors.New("api-key revoke requires --confirm"))
		}
		if _, err := id.ParseAPIKey(options.keyID); err != nil {
			return cli.UsageError(errors.New("API-key identifier is invalid"))
		}
	}

	return nil
}

func validateExpiryChoice(options *apiKeyOptions) error {
	if (options.expiresAt == "") == !options.noExpiry {
		return cli.UsageError(errors.New("api-key operation requires exactly one of --expires-at or --no-expiry"))
	}

	return nil
}

func executeAPIKeyOperation(command *cobra.Command, operation string, options *apiKeyOptions) error {
	ctx := command.Context()
	tenantID, err := id.ParseTenant(options.tenantID)
	if err != nil {
		return cli.UsageError(errors.New("api-key tenant identifier is invalid"))
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return cli.UsageError(errors.New("api-key tenant identifier is invalid"))
	}
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return cli.RuntimeError("load API-key administration configuration", err)
	}
	if configuration.DatabaseAdminURL == "" {
		return cli.RuntimeError(
			"load API-key administration configuration",
			errors.New("administrative database url is required"),
		)
	}
	pool, err := postgres.Open(ctx, postgres.Config{
		URL: configuration.DatabaseAdminURL, MaxConnections: 2, MinConnections: 0,
		MaxConnectionAge: configuration.DatabaseMaxLifetime, MaxConnectionIdle: configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval, ConnectTimeout: configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return cli.RuntimeError("open API-key administration database", err)
	}
	defer pool.Close()
	store, err := accesspostgres.New(pool)
	if err != nil {
		return cli.RuntimeError("compose API-key administration", err)
	}
	action := access.AdminAction{Actor: options.actor, Reason: options.reason}

	switch operation {
	case "create", "rotate":
		return executeAPIKeyIssuance(command, operation, options, configuration, store, scope, action)
	case "list", "revoke":
		return executeAPIKeyLifecycle(command, operation, options, store, scope, action)
	default:
		return cli.UsageError(fmt.Errorf("unknown api-key operation %q", operation))
	}
}

func executeAPIKeyIssuance(
	command *cobra.Command,
	operation string,
	options *apiKeyOptions,
	configuration config.API,
	store access.AdminRepository,
	scope tenant.Scope,
	action access.AdminAction,
) error {
	administrator, err := composeAdministrativeIssuer(configuration, store)
	if err != nil {
		return cli.RuntimeError("compose API-key issuance", err)
	}
	expiry, err := parseExpiryIntent(options)
	if err != nil {
		return err
	}
	var issued access.IssuedKey
	switch operation {
	case "create":
		patterns, parseErr := parseAPIKeyPatterns(options.scopes)
		if parseErr != nil {
			return parseErr
		}
		issued, err = administrator.Issue(command.Context(), action, scope, access.IssueInput{
			Label: options.label, Patterns: patterns, Expiry: expiry,
		})
	case "rotate":
		keyID, parseErr := id.ParseAPIKey(options.keyID)
		if parseErr != nil {
			return cli.UsageError(errors.New("API-key identifier is invalid"))
		}
		issued, err = administrator.Rotate(command.Context(), action, scope, access.RotateInput{
			KeyID: keyID, Expiry: expiry, Overlap: options.overlap,
		})
	}
	if err != nil {
		return cli.RuntimeError("execute API-key issuance", err)
	}

	return writeIssuedAPIKey(command, issued)
}

func executeAPIKeyLifecycle(
	command *cobra.Command,
	operation string,
	options *apiKeyOptions,
	store access.AdminRepository,
	scope tenant.Scope,
	action access.AdminAction,
) error {
	administrator, err := access.NewAdministrator(store, clock.System{})
	if err != nil {
		return cli.RuntimeError("compose API-key administration", err)
	}
	switch operation {
	case "list":
		keys, err := administrator.List(command.Context(), action, scope)
		if err != nil {
			return cli.RuntimeError("list API keys", err)
		}
		for _, key := range keys {
			if err := writeAPIKey(command, key, clock.System{}.Now()); err != nil {
				return err
			}
		}
	case "revoke":
		keyID, parseErr := id.ParseAPIKey(options.keyID)
		if parseErr != nil {
			return cli.UsageError(errors.New("API-key identifier is invalid"))
		}
		key, err := administrator.Revoke(command.Context(), action, scope, keyID, options.version)
		if err != nil {
			return cli.RuntimeError("revoke API key", err)
		}
		return writeAPIKey(command, key, clock.System{}.Now())
	}

	return nil
}

func composeAdministrativeIssuer(
	configuration config.API,
	repository access.AdminRepository,
) (*access.AdministrativeIssuer, error) {
	configured := configuration.APIKeyPeppers.Values()
	pepperValues := make(map[access.PepperVersion][]byte, len(configured))
	for version, material := range configured {
		pepperValues[access.PepperVersion(version)] = material
	}
	peppers, err := access.NewPepperSet(
		access.PepperVersion(configuration.APIKeyActivePepperVersion),
		pepperValues,
	)
	if err != nil {
		return nil, fmt.Errorf("configure API-key peppers: %w", err)
	}
	var maximumLifetime *time.Duration
	if configuration.APIKeyMaximumLifetime > 0 {
		maximum := configuration.APIKeyMaximumLifetime
		maximumLifetime = &maximum
	}
	expiryPolicy, err := access.NewExpiryPolicy(configuration.APIKeyAllowNoExpiry, maximumLifetime)
	if err != nil {
		return nil, err
	}
	rotationPolicy, err := access.NewRotationPolicy(configuration.APIKeyMaximumOverlap)
	if err != nil {
		return nil, err
	}
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		return nil, errors.New("construct identifier generator")
	}
	secrets, err := access.NewSystemKeyGenerator()
	if err != nil {
		return nil, err
	}
	issuer, err := access.NewIssuer(repository, identifiers, secrets, peppers, clock.System{}, access.IssuerConfig{
		Registry: access.TenantRegistry(), ExpiryPolicy: expiryPolicy, RotationPolicy: rotationPolicy,
	})
	if err != nil {
		return nil, err
	}

	return access.NewAdministrativeIssuer(issuer, repository)
}

func parseExpiryIntent(options *apiKeyOptions) (access.ExpiryIntent, error) {
	if options.noExpiry {
		return access.WithoutExpiry(), nil
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, options.expiresAt)
	if err != nil {
		return access.ExpiryIntent{}, cli.UsageError(errors.New("API-key expiry must use RFC3339 format"))
	}

	return access.ExpiringAt(expiresAt), nil
}

func parseAPIKeyPatterns(values []string) ([]access.Pattern, error) {
	patterns := make([]access.Pattern, 0, len(values))
	for _, value := range values {
		pattern, err := access.ParsePattern(value)
		if err != nil {
			return nil, cli.UsageError(fmt.Errorf("API-key scope %q is invalid", value))
		}
		patterns = append(patterns, pattern)
	}

	return patterns, nil
}

func writeIssuedAPIKey(command *cobra.Command, issued access.IssuedKey) error {
	key := issued.Key()
	_, err := fmt.Fprintf(
		command.OutOrStdout(),
		"api_key id=%s tenant=%s label=%s state=%s version=%s scopes=%s expires_at=%s replaces=%s credential=%s\n",
		key.ID(), key.TenantID(), strconv.Quote(key.Label()), key.StateAt(clock.System{}.Now()),
		strconv.FormatInt(key.Version(), 10), formatPatterns(key.Grant().Patterns()), formatTime(key.ExpiresAt()),
		formatKeyID(key.ReplacesID()), issued.Credential().Reveal(),
	)
	if err != nil {
		return cli.RuntimeError("write issued API key", err)
	}

	return nil
}

func writeAPIKey(command *cobra.Command, key access.Key, now time.Time) error {
	_, err := fmt.Fprintf(
		command.OutOrStdout(),
		"api_key id=%s tenant=%s label=%s state=%s version=%s scopes=%s created_at=%s updated_at=%s expires_at=%s revoked_at=%s retired_at=%s replaces=%s\n",
		key.ID(), key.TenantID(), strconv.Quote(key.Label()), key.StateAt(now), strconv.FormatInt(key.Version(), 10),
		formatPatterns(key.Grant().Patterns()), key.CreatedAt().Format(time.RFC3339Nano),
		key.UpdatedAt().Format(time.RFC3339Nano), formatTime(key.ExpiresAt()), formatTime(key.RevokedAt()),
		formatTime(key.RetiredAt()), formatKeyID(key.ReplacesID()),
	)
	if err != nil {
		return cli.RuntimeError("write API-key result", err)
	}

	return nil
}

func formatPatterns(patterns []access.Pattern) string {
	values := make([]string, len(patterns))
	for index, pattern := range patterns {
		values[index] = string(pattern)
	}

	return strings.Join(values, ",")
}

func formatTime(value *time.Time) string {
	if value == nil {
		return "-"
	}

	return value.Format(time.RFC3339Nano)
}

func formatKeyID(identifier id.APIKey) string {
	if identifier.IsZero() {
		return "-"
	}

	return identifier.String()
}
