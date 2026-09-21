package idenqa

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

func newExperienceCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "experience",
		Short: "Inspect, validate, and operate portable capture experiences",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(
		newExperienceListCommand(),
		newExperienceGetCommand(),
		newExperienceValidateCommand(),
		newExperienceApproveCommand(),
		newExperiencePublishCommand(),
		newExperienceRevokeCommand(),
		newExperienceRollbackCommand(),
		newExperienceExportCommand(),
		newExperienceImportCommand(),
		newExperienceResolveCommand(),
	)
	return root
}

func newExperienceListCommand() *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   "list",
		Short: "List tenant capture experiences",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runReviewHTTP(command, options, reviewRequest{operation: "experience", method: http.MethodGet, path: "/v1/experiences"})
		},
	}
	addReviewAPICommonFlags(command, options)
	return command
}

func newExperienceGetCommand() *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   "get <experience-id>",
		Short: "Get one capture experience",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := validExperienceID(args[0])
			if err != nil {
				return err
			}
			return runReviewHTTP(command, options, reviewRequest{operation: "experience", method: http.MethodGet, path: "/v1/experiences/" + identifier})
		},
	}
	addReviewAPICommonFlags(command, options)
	return command
}

type experienceTransitionOptions struct {
	reviewOptions
	expectedVersion int64
	reason          string
}

type experienceRollbackOptions struct {
	experienceTransitionOptions
	targetVersion uint32
}

func newExperienceApproveCommand() *cobra.Command {
	options := &experienceTransitionOptions{}
	command := &cobra.Command{
		Use:   "approve <experience-id>",
		Short: "Approve the latest revision (publication requires this step)",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := validExperienceID(args[0])
			if err != nil {
				return err
			}
			if options.expectedVersion < 1 {
				return cli.UsageError(errors.New("--expected-version must be at least 1"))
			}
			body := map[string]any{"expected_version": options.expectedVersion}
			if options.reason != "" {
				body["reason"] = options.reason
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "experience", method: http.MethodPost, path: "/v1/experiences/" + identifier + "/approve", body: body,
			})
		},
	}
	addExperienceTransitionFlags(command, options)
	return command
}

func newExperiencePublishCommand() *cobra.Command {
	options := &experienceTransitionOptions{}
	command := &cobra.Command{
		Use:   "publish <experience-id>",
		Short: "Publish the approved revision and supersede the prior live revision",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := validExperienceID(args[0])
			if err != nil {
				return err
			}
			if options.expectedVersion < 1 {
				return cli.UsageError(errors.New("--expected-version must be at least 1"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "experience", method: http.MethodPost, path: "/v1/experiences/" + identifier + "/publish",
				body: map[string]any{"expected_version": options.expectedVersion},
			})
		},
	}
	addExperienceTransitionFlags(command, options)
	return command
}

func newExperienceRevokeCommand() *cobra.Command {
	options := &experienceTransitionOptions{}
	command := &cobra.Command{
		Use:   "revoke <experience-id>",
		Short: "Revoke the live revision (kill switch forces the safe default)",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := validExperienceID(args[0])
			if err != nil {
				return err
			}
			if options.expectedVersion < 1 || options.reason == "" {
				return cli.UsageError(errors.New("--expected-version and --reason are required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "experience", method: http.MethodPost, path: "/v1/experiences/" + identifier + "/revoke",
				body: map[string]any{"expected_version": options.expectedVersion, "reason": options.reason},
			})
		},
	}
	addExperienceTransitionFlags(command, options)
	return command
}

func newExperienceRollbackCommand() *cobra.Command {
	options := &experienceRollbackOptions{}
	command := &cobra.Command{
		Use:   "rollback <experience-id>",
		Short: "Republish a previously approved immutable revision",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := validExperienceID(args[0])
			if err != nil {
				return err
			}
			if options.expectedVersion < 1 || options.targetVersion < 1 {
				return cli.UsageError(errors.New("--expected-version and --target-version must be at least 1"))
			}
			body := map[string]any{"expected_version": options.expectedVersion, "target_version": options.targetVersion}
			if options.reason != "" {
				body["reason"] = options.reason
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "experience", method: http.MethodPost, path: "/v1/experiences/" + identifier + "/rollback", body: body,
			})
		},
	}
	addExperienceTransitionFlags(command, &options.experienceTransitionOptions)
	command.Flags().Uint32Var(&options.targetVersion, "target-version", 0, "previously approved revision version to republish")
	return command
}

func newExperienceExportCommand() *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   "export <experience-id>",
		Short: "Export the signed canonical manifest",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := validExperienceID(args[0])
			if err != nil {
				return err
			}
			return runReviewHTTP(command, options, reviewRequest{operation: "experience", method: http.MethodGet, path: "/v1/experiences/" + identifier + "/export"})
		},
	}
	addReviewAPICommonFlags(command, options)
	return command
}

type experienceImportOptions struct {
	reviewOptions
	file string
}

func newExperienceImportCommand() *cobra.Command {
	options := &experienceImportOptions{}
	command := &cobra.Command{
		Use:   "import",
		Short: "Import a signed manifest as a tenant draft",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			manifest, err := readBoundedJSON(options.file, 2*contract.MaxDocumentBytes)
			if err != nil {
				return cli.RuntimeError("experience import", err)
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "experience", method: http.MethodPost, path: "/v1/experiences/import", body: json.RawMessage(manifest),
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.file, "file", "", "signed manifest JSON file")
	return command
}

type experienceLocalOptions struct {
	file          string
	workflow      string
	country       string
	applicationID string
	origin        string
	sdkVersion    string
	locale        string
}

func newExperienceValidateCommand() *cobra.Command {
	options := &experienceLocalOptions{}
	command := &cobra.Command{
		Use:   "validate",
		Short: "Validate a document or signed manifest locally",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			document, manifest, err := readExperienceDocument(options.file)
			if err != nil {
				return cli.RuntimeError("experience validate", err)
			}
			digest, err := contract.DigestDocument(document)
			if err != nil {
				return cli.RuntimeError("experience validate", err)
			}
			report := map[string]any{
				"valid": true, "experience_id": document.ExperienceID, "version": document.Version,
				"schema_version": document.SchemaVersion, "digest": digest, "signed": manifest != nil,
			}
			if manifest != nil {
				report["key_id"] = manifest.KeyID
				report["digest_matches"] = manifest.Digest == digest
			}
			encoded, _ := json.Marshal(report)
			_, _ = fmt.Fprintln(command.OutOrStdout(), string(encoded))
			return nil
		},
	}
	command.Flags().StringVar(&options.file, "file", "", "document or signed manifest JSON file")
	return command
}

func newExperienceResolveCommand() *cobra.Command {
	options := &experienceLocalOptions{}
	command := &cobra.Command{
		Use:   "resolve",
		Short: "Resolve targeting against one local document or manifest",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			document, manifest, err := readExperienceDocument(options.file)
			if err != nil {
				return cli.RuntimeError("experience resolve", err)
			}
			candidate := experience.Published{
				ExperienceID: mustLocalExperienceID(document.ExperienceID), Version: document.Version,
				Manifest: contract.Manifest{Document: document},
			}
			if manifest != nil {
				candidate.Manifest = *manifest
			}
			match, matched, err := experience.Resolve([]experience.Published{candidate}, experience.ResolutionRequest{
				Workflow: options.workflow, Country: options.country, ApplicationID: options.applicationID,
				Origin: options.origin, SDKVersion: options.sdkVersion, Locale: options.locale,
			})
			if err != nil {
				return cli.RuntimeError("experience resolve", err)
			}
			report := map[string]any{"matched": matched, "fallback": !matched}
			if matched {
				score, _ := experience.MatchDocument(match.Manifest.Document, experience.ResolutionRequest{
					Workflow: options.workflow, Country: options.country, ApplicationID: options.applicationID,
					Origin: options.origin, SDKVersion: options.sdkVersion,
				})
				report["experience_id"] = match.ExperienceID.String()
				report["version"] = match.Version
				report["specificity"] = score
			}
			encoded, _ := json.Marshal(report)
			_, _ = fmt.Fprintln(command.OutOrStdout(), string(encoded))
			return nil
		},
	}
	command.Flags().StringVar(&options.file, "file", "", "document or signed manifest JSON file")
	command.Flags().StringVar(&options.workflow, "workflow", "", "workflow identifier")
	command.Flags().StringVar(&options.country, "country", "", "ISO 3166-1 alpha-2 country")
	command.Flags().StringVar(&options.applicationID, "application-id", "", "application identity")
	command.Flags().StringVar(&options.origin, "origin", "", "exact HTTPS origin")
	command.Flags().StringVar(&options.sdkVersion, "sdk-version", "", "SDK semantic version")
	command.Flags().StringVar(&options.locale, "locale", "", "requested BCP 47 locale")
	return command
}

func addExperienceTransitionFlags(command *cobra.Command, options *experienceTransitionOptions) {
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "required optimistic aggregate revision")
	command.Flags().StringVar(&options.reason, "reason", "", "bounded reason recorded in immutable history")
}

func validExperienceID(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) != 30 || !strings.HasPrefix(trimmed, "exp_") {
		return "", cli.UsageError(errors.New("a stable exp_ experience identifier is required"))
	}
	for index := 4; index < len(trimmed); index++ {
		character := trimmed[index]
		if (character < '0' || character > '9') && (character < 'A' || character > 'Z') {
			return "", cli.UsageError(errors.New("a stable exp_ experience identifier is required"))
		}
	}
	return trimmed, nil
}

func readBoundedJSON(path string, limit int) ([]byte, error) {
	if path == "" {
		return nil, errors.New("--file is required")
	}
	file, err := os.Open(path) //nolint:gosec // The operator explicitly selects the local experience file.
	if err != nil {
		return nil, errors.New("read experience file")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, int64(limit)+1))
	if err != nil || len(raw) == 0 || len(raw) > limit {
		return nil, errors.New("experience file is empty or too large")
	}
	return raw, nil
}

func readExperienceDocument(path string) (contract.Document, *contract.Manifest, error) {
	raw, err := readBoundedJSON(path, 2*contract.MaxDocumentBytes)
	if err != nil {
		return contract.Document{}, nil, err
	}
	if manifest, err := contract.ParseManifest(raw); err == nil {
		return manifest.Document, &manifest, nil
	}
	document, err := contract.ParseDocument(raw)
	if err != nil {
		return contract.Document{}, nil, errors.New("experience document or manifest is invalid")
	}
	return document, nil, nil
}

func mustLocalExperienceID(value string) id.Experience {
	parsed, _ := id.ParseExperience(value)
	return parsed
}
