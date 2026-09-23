package idenqa

import (
	"errors"
	"net/http"
	"strings"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

type consentOptions struct {
	reviewOptions
	reason string
}

func newConsentCommand() *cobra.Command {
	root := &cobra.Command{Use: "consent", Short: "Inspect and withdraw immutable consent receipts", Args: cli.UsageArgs(cobra.NoArgs)}
	for _, operation := range []string{"get", "revoke"} {
		op := operation
		options := &consentOptions{}
		command := &cobra.Command{Use: op + " <consentID>", Short: op + " a consent receipt", Args: cli.UsageArgs(cobra.ExactArgs(1)), RunE: func(command *cobra.Command, args []string) error {
			identifier, err := id.ParseAcknowledgement(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid consent receipt id is required"))
			}
			request := reviewRequest{operation: "consent", method: http.MethodGet, path: "/v1/consent-receipts/" + identifier.String()}
			if op == "revoke" {
				if strings.TrimSpace(options.reason) == "" {
					return cli.UsageError(errors.New("reason is required"))
				}
				request.method, request.path, request.body, request.idempotency = http.MethodPost, request.path+"/revoke", map[string]string{"reason": options.reason}, true
			}
			return runReviewHTTP(command, &options.reviewOptions, request)
		}}
		addReviewAPICommonFlags(command, &options.reviewOptions)
		if op == "revoke" {
			command.Flags().StringVar(&options.reason, "reason", "", "bounded audit reason")
			command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key")
		}
		root.AddCommand(command)
	}
	return root
}
