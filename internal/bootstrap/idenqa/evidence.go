package idenqa

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

type evidenceOptions struct {
	reviewOptions
	limit, maximumUses, ttlSeconds                                                                          int
	reason, checkReference, runnerIdentity, workloadVersion, purpose, recipientReference, outputDestination string
	variants                                                                                                []string
}

func newEvidenceCommand() *cobra.Command {
	root := &cobra.Command{Use: "evidence", Short: "Inspect evidence metadata and administer processing grants", Args: cli.UsageArgs(cobra.NoArgs)}
	root.AddCommand(newEvidenceGetCommand(), newEvidenceHistoryCommand(), newEvidenceGrantCommand())
	return root
}

func newEvidenceGetCommand() *cobra.Command {
	options := &evidenceOptions{}
	command := &cobra.Command{Use: "get <evidenceID>", Short: "Get safe evidence metadata", Args: cli.UsageArgs(cobra.ExactArgs(1)), RunE: func(command *cobra.Command, args []string) error {
		identifier, err := id.ParseEvidence(args[0])
		if err != nil {
			return cli.UsageError(errors.New("a valid evidence id is required"))
		}
		return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "evidence", method: http.MethodGet, path: "/v1/evidence/" + identifier.String()})
	}}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newEvidenceHistoryCommand() *cobra.Command {
	options := &evidenceOptions{limit: 50}
	command := &cobra.Command{Use: "history <evidenceID>", Short: "List safe evidence lifecycle events", Args: cli.UsageArgs(cobra.ExactArgs(1)), RunE: func(command *cobra.Command, args []string) error {
		identifier, err := id.ParseEvidence(args[0])
		if err != nil {
			return cli.UsageError(errors.New("a valid evidence id is required"))
		}
		if options.limit < 1 || options.limit > 100 {
			return cli.UsageError(errors.New("limit must be from 1 to 100"))
		}
		return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "evidence", method: http.MethodGet, path: "/v1/evidence/" + identifier.String() + "/lifecycle", query: url.Values{"limit": {strconv.Itoa(options.limit)}}})
	}}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().IntVar(&options.limit, "limit", 50, "event limit from 1 to 100")
	return command
}

func newEvidenceGrantCommand() *cobra.Command {
	root := &cobra.Command{Use: "grant", Short: "Inspect and revoke evidence-processing grants", Args: cli.UsageArgs(cobra.NoArgs)}
	for _, operation := range []string{"create", "get", "revoke"} {
		op := operation
		options := &evidenceOptions{}
		command := &cobra.Command{Use: op + " <id>", Short: op + " an evidence-processing grant", Args: cli.UsageArgs(cobra.ExactArgs(1)), RunE: func(command *cobra.Command, args []string) error {
			if op == "create" {
				evidenceID, err := id.ParseEvidence(args[0])
				if err != nil {
					return cli.UsageError(errors.New("a valid evidence id is required"))
				}
				if options.maximumUses < 1 || options.maximumUses > 32 || options.ttlSeconds < 1 || options.ttlSeconds > 3600 || strings.TrimSpace(options.reason) == "" {
					return cli.UsageError(errors.New("maximum-uses, ttl-seconds, and reason are invalid"))
				}
				body := map[string]any{"check_reference": options.checkReference, "runner_identity": options.runnerIdentity, "workload_version": options.workloadVersion, "purpose": options.purpose, "permitted_variants": options.variants, "recipient_reference": options.recipientReference, "output_destination": options.outputDestination, "maximum_uses": options.maximumUses, "ttl_seconds": options.ttlSeconds, "reason": options.reason}
				return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "evidence grant", method: http.MethodPost, path: "/v1/evidence/" + evidenceID.String() + "/access-grants", body: body, idempotency: true})
			}
			identifier, err := id.ParseGrant(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid grant id is required"))
			}
			request := reviewRequest{operation: "evidence grant", method: http.MethodGet, path: "/v1/evidence-access-grants/" + identifier.String()}
			if op == "revoke" {
				if strings.TrimSpace(options.reason) == "" {
					return cli.UsageError(errors.New("reason is required"))
				}
				request.method, request.path, request.body = http.MethodPost, request.path+"/revoke", map[string]string{"reason": options.reason}
				request.idempotency = true
			}
			return runReviewHTTP(command, &options.reviewOptions, request)
		}}
		addReviewAPICommonFlags(command, &options.reviewOptions)
		switch op {
		case "create":
			command.Flags().StringVar(&options.checkReference, "check-reference", "", "policy-approved check reference")
			command.Flags().StringVar(&options.runnerIdentity, "runner-identity", "", "authenticated runner identity")
			command.Flags().StringVar(&options.workloadVersion, "workload-version", "", "runner workload version")
			command.Flags().StringVar(&options.purpose, "purpose", "", "registered processing purpose")
			command.Flags().StringSliceVar(&options.variants, "variant", []string{"evidence.variant.original"}, "permitted evidence variant")
			command.Flags().StringVar(&options.recipientReference, "recipient-reference", "", "authority-bound recipient reference")
			command.Flags().StringVar(&options.outputDestination, "output-destination", "", "bounded output destination")
			command.Flags().IntVar(&options.maximumUses, "maximum-uses", 1, "maximum grant uses from 1 to 32")
			command.Flags().IntVar(&options.ttlSeconds, "ttl-seconds", 300, "grant lifetime from 1 to 3600 seconds")
			command.Flags().StringVar(&options.reason, "reason", "", "bounded audit reason")
			command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key")
		case "revoke":
			command.Flags().StringVar(&options.reason, "reason", "", "bounded audit reason")
			command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key")
		}
		root.AddCommand(command)
	}
	return root
}
