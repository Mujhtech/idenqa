package idenqa

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	keycustodypostgres "github.com/Mujhtech/idenqa/internal/keycustody/postgres"
	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	keyrewrappostgres "github.com/Mujhtech/idenqa/internal/keyrewrap/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

// keyOperations is the administrative composition shared by the fleet rewrap,
// verified destruction, and recovery ceremony commands.
type keyOperations struct {
	dependencies *operationalDependencies
	rewrap       *keyrewrap.Service
	destruction  *keycustody.DestructionService
	recovery     *keycustody.RecoveryService
}

// openKeyOperations loads the operator environment and composes every key
// operation over the configured provider.
func openKeyOperations(ctx context.Context, envFile string) (*keyOperations, error) {
	dependencies, err := openOperationalDependencies(ctx, envFile)
	if err != nil {
		return nil, err
	}
	rewrapRepository, err := keyrewrappostgres.New(dependencies.pool, dependencies.keys, dependencies.keys, time.Now)
	if err != nil {
		return nil, err
	}
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		return nil, err
	}
	evidenceStore, err := evidencepostgres.New(dependencies.pool, dependencies.keys, catalog)
	if err != nil {
		return nil, err
	}
	evidenceRewrapper, err := evidence.NewRewrapper(evidenceStore, evidenceStore, dependencies.keys, dependencies.keys)
	if err != nil {
		return nil, err
	}
	adapters, err := keyRewrapAdapters(rewrapRepository, evidenceRewrapper, dependencies.identifiers)
	if err != nil {
		return nil, err
	}
	rewrap, err := keyrewrap.NewService(rewrapRepository, rewrapRepository, adapters, dependencies.keys, nil, time.Now)
	if err != nil {
		return nil, err
	}
	var scheduler keycustody.DestructionScheduler
	if provider, ok := dependencies.keys.(keycustody.DestructionScheduler); ok {
		scheduler = provider
	}
	custodyStore, err := keycustodypostgres.New(dependencies.pool, dependencies.keys, dependencies.keys, dependencies.references, custodyGenerator(dependencies.identifiers))
	if err != nil {
		return nil, err
	}
	destruction, err := keycustody.NewDestructionService(custodyStore, custodyStore, scheduler, dependencies.identifiers, time.Now)
	if err != nil {
		return nil, err
	}
	recovery, err := keycustody.NewRecoveryService(custodyStore, rewrap, rewrap, dependencies.identifiers, time.Now)
	if err != nil {
		return nil, err
	}

	return &keyOperations{dependencies: dependencies, rewrap: rewrap, destruction: destruction, recovery: recovery}, nil
}

// Close releases the command-owned provider and pool.
func (operations *keyOperations) Close() {
	if operations != nil && operations.dependencies != nil {
		operations.dependencies.Close()
	}
}

func keyRewrapAdapters(repository *keyrewrappostgres.Store, evidenceRewrapper *evidence.Rewrapper, identifiers *id.Generator) ([]keyrewrap.Adapter, error) {
	workerIdentifier, err := identifiers.NewTask()
	if err != nil {
		return nil, err
	}
	workerID := strings.ToLower(workerIdentifier.String())
	evidenceAdapter, err := keyrewrappostgres.NewEvidenceAdapter(repository, evidenceRewrapper, workerID, time.Now)
	if err != nil {
		return nil, err
	}
	webhookEventAdapter, err := keyrewrappostgres.NewWebhookEventAdapter(repository)
	if err != nil {
		return nil, err
	}
	webhookDeliveryAdapter, err := keyrewrappostgres.NewWebhookDeliveryAdapter(repository)
	if err != nil {
		return nil, err
	}
	webhookSecretAdapter, err := keyrewrappostgres.NewWebhookSecretAdapter(repository)
	if err != nil {
		return nil, err
	}
	hmacAdapter, err := keyrewrappostgres.NewHMACKeyAdapter(repository)
	if err != nil {
		return nil, err
	}
	identityLookupAdapter, err := keyrewrappostgres.NewIdentityLookupAdapter(repository)
	if err != nil {
		return nil, err
	}
	identitySubjectAdapter, err := keyrewrappostgres.NewIdentitySubjectAdapter(repository)
	if err != nil {
		return nil, err
	}
	fraudAdapter, err := keyrewrappostgres.NewFraudKeyAdapter(repository)
	if err != nil {
		return nil, err
	}

	return []keyrewrap.Adapter{
		evidenceAdapter, webhookEventAdapter, webhookDeliveryAdapter, webhookSecretAdapter,
		hmacAdapter, identityLookupAdapter, identitySubjectAdapter, fraudAdapter,
	}, nil
}

func custodyGenerator(identifiers *id.Generator) func() (string, []byte, error) {
	return func() (string, []byte, error) {
		identifier, err := identifiers.New(keycustody.KeyIDPrefix)
		if err != nil {
			return "", nil, err
		}
		material := make([]byte, keycustody.KeySize)
		if _, err := rand.Read(material); err != nil {
			clear(material)
			return "", nil, err
		}

		return identifier.String(), material, nil
	}
}

type kmsRewrapOptions struct {
	envFile      string
	class        string
	batch        int
	confirmation bool
}

func newKMSRewrapCommand() *cobra.Command {
	options := &kmsRewrapOptions{}
	command := &cobra.Command{
		Use:   "rewrap",
		Short: "Inspect or run bounded fleet key-material rewraps",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown rewrap operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("rewrap requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", ".env", "load local configuration from this dotenv file")
	status := &cobra.Command{
		Use:   "status",
		Short: "Show durable rewrap progress per artifact class",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if options.class != "" {
				if _, err := keyrewrap.ParseClass(options.class); err != nil {
					return cli.UsageError(errors.New("rewrap status requires a valid --class"))
				}
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeKMSRewrapStatus(command, options)
		},
	}
	status.Flags().StringVar(&options.class, "class", "", "limit output to one artifact class")
	run := &cobra.Command{
		Use:   "run",
		Short: "Run one bounded rewrap sweep pass now",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if options.class != "" {
				if _, err := keyrewrap.ParseClass(options.class); err != nil {
					return cli.UsageError(errors.New("rewrap run requires a valid --class"))
				}
			}
			if options.batch < keyrewrap.MinimumBatch || options.batch > keyrewrap.MaximumBatch {
				return cli.UsageError(fmt.Errorf("rewrap run requires --batch between %d and %d", keyrewrap.MinimumBatch, keyrewrap.MaximumBatch))
			}
			if !options.confirmation {
				return cli.UsageError(errors.New("rewrap run requires --confirm"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeKMSRewrapRun(command, options)
		},
	}
	run.Flags().StringVar(&options.class, "class", "", "limit the pass to one artifact class")
	run.Flags().IntVar(&options.batch, "batch", keyrewrap.DefaultBatch, "maximum objects per class in this pass")
	run.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this bounded rewrap pass")
	command.AddCommand(status, run)

	return command
}

func executeKMSRewrapStatus(command *cobra.Command, options *kmsRewrapOptions) error {
	ctx := command.Context()
	operations, err := openKeyOperations(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open key operations", err)
	}
	defer operations.Close()
	states, err := operations.rewrap.Statuses(ctx)
	if err != nil {
		return cli.RuntimeError("read key rewrap state", err)
	}
	for _, state := range states {
		if options.class != "" && state.Class.String() != options.class {
			continue
		}
		if _, err := fmt.Fprintf(command.OutOrStdout(),
			"key_rewrap_state class=%s generation=%d status=%s epoch=%s cursor_tenant=%s cursor_object=%s processed=%d rewrapped=%d skipped=%d failed=%d last_error=%q\n",
			state.Class.String(), state.Generation, state.Status, state.Epoch.Key,
			state.Cursor.Tenant.String(), state.Cursor.Object,
			state.Processed, state.Rewrapped, state.Skipped, state.Failed, state.LastError); err != nil {
			return cli.RuntimeError("write key rewrap state", err)
		}
	}

	return nil
}

func executeKMSRewrapRun(command *cobra.Command, options *kmsRewrapOptions) error {
	ctx := command.Context()
	operations, err := openKeyOperations(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open key operations", err)
	}
	defer operations.Close()
	results := make([]keyrewrap.Result, 0, 1)
	if options.class == "" {
		results, err = operations.rewrap.SweepAll(ctx, options.batch)
	} else {
		class, parseErr := keyrewrap.ParseClass(options.class)
		if parseErr != nil {
			return cli.UsageError(errors.New("rewrap run requires a valid --class"))
		}
		var result keyrewrap.Result
		result, err = operations.rewrap.SweepClass(ctx, class, options.batch)
		results = append(results, result)
	}
	for _, result := range results {
		if _, writeErr := fmt.Fprintf(command.OutOrStdout(),
			"key_rewrap_batch class=%s generation=%d status=%s rewrapped=%d skipped=%d failed=%d epoch_changed=%t\n",
			result.Class.String(), result.Generation, result.Status, result.Rewrapped, result.Skipped,
			result.Failed, result.EpochChanged); writeErr != nil {
			return cli.RuntimeError("write key rewrap batch", writeErr)
		}
	}
	if err != nil {
		return cli.RuntimeError("run key rewrap sweep", err)
	}

	return nil
}

type kmsDestroyOptions struct {
	envFile      string
	actorKey     string
	provider     string
	reference    string
	version      string
	algorithm    string
	verification string
	window       time.Duration
	recordOnly   bool
	reason       string
	confirmation bool
}

func newKMSDestroyCommand() *cobra.Command {
	options := &kmsDestroyOptions{}
	command := &cobra.Command{
		Use:   "destroy",
		Short: "Verify reference freedom and record provider key destruction",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown destroy operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("destroy requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", ".env", "load local configuration from this dotenv file")
	command.PersistentFlags().StringVar(&options.actorKey, "actor-key", "", "authenticated operator API-key identifier")
	command.PersistentFlags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable receipt")
	command.PersistentFlags().BoolVar(&options.confirmation, "confirm", false, "confirm this destruction operation")
	verify := &cobra.Command{
		Use:   "verify",
		Short: "Prove no durable reference remains for one wrapping identity",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validateDestroyTarget(options, true)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeKMSDestroyVerify(command, options)
		},
	}
	verify.Flags().StringVar(&options.provider, "provider", "", "wrapping provider name")
	verify.Flags().StringVar(&options.reference, "reference", "", "wrapping key reference")
	verify.Flags().StringVar(&options.version, "version", "", "wrapping material version")
	verify.Flags().StringVar(&options.algorithm, "algorithm", "", "wrapping algorithm")
	schedule := &cobra.Command{
		Use:   "schedule",
		Short: "Schedule provider deletion for one verified receipt",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if options.verification == "" {
				return cli.UsageError(errors.New("destroy schedule requires --verification"))
			}
			if options.actorKey == "" {
				return cli.UsageError(errors.New("destroy schedule requires --actor-key"))
			}
			if len(strings.TrimSpace(options.reason)) < 8 {
				return cli.UsageError(errors.New("destroy schedule requires a --reason of at least eight characters"))
			}
			if !options.confirmation {
				return cli.UsageError(errors.New("destroy schedule requires --confirm"))
			}
			if options.window < time.Hour || options.window > 30*24*time.Hour {
				return cli.UsageError(errors.New("destroy schedule requires --window between 1h and 720h"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeKMSDestroySchedule(command, options)
		},
	}
	schedule.Flags().StringVar(&options.verification, "verification", "", "verified destruction receipt identifier")
	schedule.Flags().DurationVar(&options.window, "window", 7*24*time.Hour, "provider pending-deletion window")
	schedule.Flags().BoolVar(&options.recordOnly, "record-only", false, "record the decision without calling the provider")
	command.AddCommand(verify, schedule)

	return command
}

func validateDestroyTarget(options *kmsDestroyOptions, requireConfirm bool) error {
	if options.provider == "" || options.reference == "" || options.version == "" || options.algorithm == "" {
		return cli.UsageError(errors.New("destroy verify requires --provider, --reference, --version, and --algorithm"))
	}
	if options.actorKey == "" {
		return cli.UsageError(errors.New("destroy verify requires --actor-key"))
	}
	if len(strings.TrimSpace(options.reason)) < 8 {
		return cli.UsageError(errors.New("destroy verify requires a --reason of at least eight characters"))
	}
	if requireConfirm && !options.confirmation {
		return cli.UsageError(errors.New("destroy verify requires --confirm"))
	}
	if err := (keycustody.DestructionTarget{
		Provider: options.provider, Reference: options.reference,
		Version: options.version, Algorithm: options.algorithm,
	}).Validate(); err != nil {
		return cli.UsageError(errors.New("destroy target identity is invalid"))
	}

	return nil
}

func executeKMSDestroyVerify(command *cobra.Command, options *kmsDestroyOptions) error {
	ctx := command.Context()
	operations, err := openKeyOperations(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open key operations", err)
	}
	defer operations.Close()
	actor, _ := id.ParseAPIKey(options.actorKey)
	receipt, err := operations.destruction.Verify(ctx, actor, keycustody.DestructionTarget{
		Provider: options.provider, Reference: options.reference,
		Version: options.version, Algorithm: options.algorithm,
	}, options.reason)
	if receipt.ID != "" {
		if _, writeErr := fmt.Fprintf(command.OutOrStdout(),
			"key_destruction_verification id=%s state=%s total=%d digest=%s\n",
			receipt.ID, receipt.State, receipt.Total, receipt.Digest); writeErr != nil {
			return cli.RuntimeError("write destruction verification", writeErr)
		}
		for _, class := range keycustody.ReferenceClasses() {
			if _, writeErr := fmt.Fprintf(command.OutOrStdout(),
				"key_destruction_reference receipt=%s class=%s count=%d\n",
				receipt.ID, class, receipt.Counts[class]); writeErr != nil {
				return cli.RuntimeError("write destruction reference count", writeErr)
			}
		}
	}
	if errors.Is(err, keycustody.ErrDestructionBlocked) {
		return cli.RuntimeError("destruction blocked by live references", err)
	}
	if err != nil {
		return cli.RuntimeError("verify key destruction", err)
	}

	return nil
}

func executeKMSDestroySchedule(command *cobra.Command, options *kmsDestroyOptions) error {
	ctx := command.Context()
	operations, err := openKeyOperations(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open key operations", err)
	}
	defer operations.Close()
	actor, _ := id.ParseAPIKey(options.actorKey)
	schedule, err := operations.destruction.Schedule(ctx, actor, options.verification, options.window, options.recordOnly, options.reason)
	if err != nil {
		return cli.RuntimeError("schedule key destruction", err)
	}
	deletionAt := ""
	if schedule.ProviderDeletionAt != nil {
		deletionAt = schedule.ProviderDeletionAt.Format(time.RFC3339)
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(),
		"key_destruction_schedule id=%s verification=%s mode=%s provider_deletion_at=%s\n",
		schedule.ID, schedule.VerificationID, schedule.Mode, deletionAt); err != nil {
		return cli.RuntimeError("write destruction schedule", err)
	}

	return nil
}

type kmsRecoveryOptions struct {
	envFile         string
	actorKey        string
	identifier      string
	kind            string
	class           string
	tenantID        string
	targetProvider  string
	targetReference string
	targetVersion   string
	targetAlgorithm string
	expectedVersion int64
	reason          string
	confirmation    bool
}

func newKMSRecoveryCommand() *cobra.Command {
	options := &kmsRecoveryOptions{}
	command := &cobra.Command{
		Use:   "recovery",
		Short: "Operate dual-control key recovery ceremonies",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown recovery operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("recovery requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", ".env", "load local configuration from this dotenv file")
	command.PersistentFlags().StringVar(&options.actorKey, "actor-key", "", "authenticated operator API-key identifier")
	command.PersistentFlags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable ceremony")
	command.PersistentFlags().BoolVar(&options.confirmation, "confirm", false, "confirm this ceremony transition")
	start := &cobra.Command{
		Use:   "start",
		Short: "Start a dual-control recovery ceremony",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if options.actorKey == "" {
				return cli.UsageError(errors.New("recovery start requires --actor-key"))
			}
			if options.kind == "" || options.class == "" {
				return cli.UsageError(errors.New("recovery start requires --kind and --class"))
			}
			if len(strings.TrimSpace(options.reason)) < 8 {
				return cli.UsageError(errors.New("recovery start requires a --reason of at least eight characters"))
			}
			if !options.confirmation {
				return cli.UsageError(errors.New("recovery start requires --confirm"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeKMSRecovery(command, options, "start")
		},
	}
	start.Flags().StringVar(&options.kind, "kind", "", "ceremony kind: rewrap or migrate_epoch")
	start.Flags().StringVar(&options.class, "class", "", "artifact class the ceremony governs")
	start.Flags().StringVar(&options.tenantID, "tenant", "", "owning tenant for a rewrap ceremony")
	start.Flags().StringVar(&options.targetProvider, "target-provider", "", "target wrapping provider")
	start.Flags().StringVar(&options.targetReference, "target-reference", "", "target wrapping reference")
	start.Flags().StringVar(&options.targetVersion, "target-version", "", "target wrapping material version")
	start.Flags().StringVar(&options.targetAlgorithm, "target-algorithm", "", "target wrapping algorithm")
	for _, operation := range []struct {
		name  string
		short string
	}{
		{name: "approve", short: "Approve a started ceremony as a distinct principal"},
		{name: "complete", short: "Complete an approved ceremony and apply its operation"},
		{name: "abort", short: "Abort a started or approved ceremony without applying anything"},
	} {
		operation := operation
		entry := &cobra.Command{
			Use:   operation.name,
			Short: operation.short,
			Args:  cli.UsageArgs(cobra.NoArgs),
			PreRunE: func(_ *cobra.Command, _ []string) error {
				if options.identifier == "" {
					return cli.UsageError(fmt.Errorf("recovery %s requires --id", operation.name))
				}
				if options.actorKey == "" {
					return cli.UsageError(fmt.Errorf("recovery %s requires --actor-key", operation.name))
				}
				if options.expectedVersion < 1 {
					return cli.UsageError(fmt.Errorf("recovery %s requires a positive --expected-version", operation.name))
				}
				if len(strings.TrimSpace(options.reason)) < 8 {
					return cli.UsageError(fmt.Errorf("recovery %s requires a --reason of at least eight characters", operation.name))
				}
				if !options.confirmation {
					return cli.UsageError(fmt.Errorf("recovery %s requires --confirm", operation.name))
				}

				return nil
			},
			RunE: func(command *cobra.Command, _ []string) error {
				return executeKMSRecovery(command, options, operation.name)
			},
		}
		entry.Flags().StringVar(&options.identifier, "id", "", "ceremony identifier")
		entry.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "expected ceremony version")
		command.AddCommand(entry)
	}
	show := &cobra.Command{
		Use:   "show",
		Short: "Show one ceremony",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if options.identifier == "" {
				return cli.UsageError(errors.New("recovery show requires --id"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeKMSRecoveryShow(command, options)
		},
	}
	show.Flags().StringVar(&options.identifier, "id", "", "ceremony identifier")
	command.AddCommand(show)

	return command
}

func executeKMSRecovery(command *cobra.Command, options *kmsRecoveryOptions, operation string) error {
	ctx := command.Context()
	operations, err := openKeyOperations(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open key operations", err)
	}
	defer operations.Close()
	actor, _ := id.ParseAPIKey(options.actorKey)
	result, err := operations.recovery.ExecuteDirect(ctx, actor, keycustody.RecoveryCommand{
		Operation: operation, Identifier: options.identifier, Kind: options.kind, Class: options.class,
		TenantID: options.tenantID,
		Target: keycustody.DestructionTarget{
			Provider: options.targetProvider, Reference: options.targetReference,
			Version: options.targetVersion, Algorithm: options.targetAlgorithm,
		},
		ExpectedVersion: options.expectedVersion, Reason: options.reason,
	})
	if err != nil {
		return cli.RuntimeError("apply recovery ceremony", err)
	}
	if err := writeRecoveryCeremony(command, result.Ceremony); err != nil {
		return err
	}

	return nil
}

func executeKMSRecoveryShow(command *cobra.Command, options *kmsRecoveryOptions) error {
	ctx := command.Context()
	operations, err := openKeyOperations(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open key operations", err)
	}
	defer operations.Close()
	result, err := operations.recovery.ReadDirect(ctx, options.identifier)
	if err != nil {
		return cli.RuntimeError("read recovery ceremony", err)
	}

	return writeRecoveryCeremony(command, result.Ceremony)
}

func writeRecoveryCeremony(command *cobra.Command, ceremony keycustody.RecoveryCeremony) error {
	if _, err := fmt.Fprintf(command.OutOrStdout(),
		"key_recovery_ceremony id=%s kind=%s class=%s state=%s version=%d started_by=%s approved_by=%s completed_by=%s aborted_by=%s\n",
		ceremony.ID, ceremony.Kind, ceremony.Class, ceremony.State, ceremony.Version,
		ceremony.StartedBy, ceremony.ApprovedBy, ceremony.CompletedBy, ceremony.AbortedBy); err != nil {
		return cli.RuntimeError("write recovery ceremony", err)
	}

	return nil
}
