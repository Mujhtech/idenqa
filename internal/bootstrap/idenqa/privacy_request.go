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

type privacyRequestOptions struct {
	reviewOptions
	requestType       string
	subjectID         string
	verificationID    string
	region            string
	purpose           string
	recordID          string
	decisionID        string
	name              string
	value             string
	kind              string
	normalization     string
	outcome           string
	reasonCode        string
	recipient         string
	dataClass         string
	legalBasis        string
	reference         string
	role              string
	dataClasses       string
	regions           string
	transferMechanism string
	processorName     string
	expiresAt         string
}

func newPrivacyRequestCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "request",
		Short: "Create and administer data-subject privacy requests through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newPrivacyRequestCreateCommand())
	root.AddCommand(newPrivacyRequestListCommand())
	root.AddCommand(newPrivacyRequestGetCommand())
	for _, operation := range []string{"approve", "deny", "withdraw", "execute"} {
		root.AddCommand(newPrivacyRequestTransitionCommand(operation))
	}
	return root
}

func newPrivacyRequestCreateCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "create",
		Short: "Create one tenant privacy request",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := privacyCreateBody(options)
			if err != nil {
				return cli.UsageError(err)
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodPost, path: "/v1/privacy-requests", body: body,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	addPrivacyCreateFlags(command, options)
	return command
}

func addPrivacyCreateFlags(command *cobra.Command, options *privacyRequestOptions) {
	command.Flags().StringVar(&options.requestType, "type", "", "request type: access, portability, correction, restriction, objection, or erasure")
	command.Flags().StringVar(&options.subjectID, "subject-id", "", "exact subject scope")
	command.Flags().StringVar(&options.verificationID, "verification-id", "", "linked verification session")
	command.Flags().StringVar(&options.region, "region", "", "pinned deployment region")
	command.Flags().StringVar(&options.purpose, "purpose", "", "purpose scope for restriction or objection")
	command.Flags().StringVar(&options.recordID, "record-id", "", "identity record target for a correction")
	command.Flags().StringVar(&options.decisionID, "decision-id", "", "decision target for a review correction")
	command.Flags().StringVar(&options.name, "name", "", "identity attribute name for a correction")
	command.Flags().StringVar(&options.value, "value", "", "corrected subject-attested value")
	command.Flags().StringVar(&options.kind, "kind", "", "identity record kind: observation, claim, or fact")
	command.Flags().StringVar(&options.normalization, "normalization", "", "normalization profile for the corrected value")
	command.Flags().StringVar(&options.expiresAt, "expires-at", "", "optional RFC 3339 expiry within the deployment maximum")
}

func privacyCreateBody(options *privacyRequestOptions) (map[string]any, error) {
	if options.requestType == "" || options.region == "" {
		return nil, errors.New("--type and --region are required")
	}
	body := map[string]any{"type": options.requestType, "region": options.region}
	for key, value := range map[string]string{
		"subject_id": options.subjectID, "verification_id": options.verificationID, "purpose": options.purpose,
		"record_id": options.recordID, "decision_id": options.decisionID, "name": options.name,
		"value": options.value, "kind": options.kind, "normalization": options.normalization,
	} {
		if value != "" {
			body[key] = value
		}
	}
	if options.expiresAt != "" {
		body["expires_at"] = options.expiresAt
	}
	return body, nil
}

func newPrivacyRequestListCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "list",
		Short: "List tenant privacy requests",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.cursor != "" {
				query.Set("cursor", options.cursor)
			}
			if options.requestType != "" {
				query.Set("type", options.requestType)
			}
			if options.subjectID != "" {
				query.Set("subject_id", options.subjectID)
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodGet, path: "/v1/privacy-requests", query: query,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
	command.Flags().StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	command.Flags().StringVar(&options.requestType, "type", "", "restrict the page to one request type")
	command.Flags().StringVar(&options.subjectID, "subject-id", "", "restrict the page to one exact subject")
	return command
}

func newPrivacyRequestGetCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "get <requestID>",
		Short: "Get one privacy request",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := id.ParsePrivacyRequest(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid privacy request id is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodGet, path: "/v1/privacy-requests/" + identifier.String(),
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newPrivacyRequestTransitionCommand(operation string) *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   operation + " <requestID>",
		Short: strings.ToUpper(operation[:1]) + operation[1:] + " one privacy request",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := id.ParsePrivacyRequest(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid privacy request id is required"))
			}
			if options.expectedVersion < 1 {
				return cli.UsageError(errors.New("expected-version must be positive"))
			}
			body := map[string]any{"expected_version": options.expectedVersion}
			if operation == "approve" || operation == "deny" {
				if options.reasonCode == "" {
					return cli.UsageError(errors.New("reason-code is required"))
				}
				body["reason_code"] = options.reasonCode
				if operation == "approve" && options.outcome != "" {
					body["outcome"] = options.outcome
				}
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodPost,
				path: "/v1/privacy-requests/" + identifier.String() + "/" + operation, body: body,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "optimistic expected version")
	if operation == "approve" || operation == "deny" {
		command.Flags().StringVar(&options.reasonCode, "reason-code", "", "bounded decision reason code")
	}
	if operation == "approve" {
		command.Flags().StringVar(&options.outcome, "outcome", "approved", "approved or partially_approved")
	}
	return command
}

func newPrivacyRestrictionCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "restriction",
		Short: "Administer processing restrictions through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newPrivacyRestrictionLiftCommand())
	return root
}

func newPrivacyRestrictionLiftCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "lift <restrictionID>",
		Short: "Lift one processing restriction",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := id.ParsePrivacyRestriction(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid restriction id is required"))
			}
			if options.expectedVersion < 1 || options.reasonCode == "" {
				return cli.UsageError(errors.New("expected-version and reason-code are required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodPost, path: "/v1/privacy-restrictions/" + identifier.String() + "/lift",
				body: map[string]any{"expected_version": options.expectedVersion, "reason_code": options.reasonCode},
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "optimistic expected version")
	command.Flags().StringVar(&options.reasonCode, "reason-code", "", "bounded lift reason code")
	return command
}

func newPrivacyDisclosureCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "disclosure",
		Short: "Record and list transfer or disclosure records through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newPrivacyDisclosureCreateCommand())
	root.AddCommand(newPrivacyDisclosureListCommand())
	return root
}

func newPrivacyDisclosureCreateCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "create",
		Short: "Record one transfer or disclosure",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			requestID, err := id.ParsePrivacyRequest(options.decisionID)
			if err != nil {
				return cli.UsageError(errors.New("--request-id must be a valid privacy request id"))
			}
			if options.recipient == "" || options.purpose == "" || options.dataClass == "" || options.legalBasis == "" || options.region == "" || options.reference == "" {
				return cli.UsageError(errors.New("--recipient, --purpose, --data-class, --legal-basis, --region, and --reference are required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodPost, path: "/v1/privacy-disclosures",
				body: map[string]any{
					"request_id": requestID.String(), "recipient": options.recipient, "purpose": options.purpose,
					"data_class": options.dataClass, "legal_basis": options.legalBasis, "region": options.region, "reference": options.reference,
				},
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.decisionID, "request-id", "", "exact privacy request")
	command.Flags().StringVar(&options.recipient, "recipient", "", "recipient reference")
	command.Flags().StringVar(&options.purpose, "purpose", "", "disclosure purpose")
	command.Flags().StringVar(&options.dataClass, "data-class", "", "subject_export, identity_successor, deletion_request, restriction, or objection")
	command.Flags().StringVar(&options.legalBasis, "legal-basis", "", "bounded legal basis reference")
	command.Flags().StringVar(&options.region, "region", "", "pinned deployment region")
	command.Flags().StringVar(&options.reference, "reference", "", "bounded digest reference")
	return command
}

func newPrivacyDisclosureListCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "list",
		Short: "List transfer and disclosure records",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.cursor != "" {
				query.Set("cursor", options.cursor)
			}
			if options.decisionID != "" {
				requestID, err := id.ParsePrivacyRequest(options.decisionID)
				if err != nil {
					return cli.UsageError(errors.New("--request-id must be a valid privacy request id"))
				}
				query.Set("request_id", requestID.String())
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodGet, path: "/v1/privacy-disclosures", query: query,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
	command.Flags().StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	command.Flags().StringVar(&options.decisionID, "request-id", "", "restrict the page to one privacy request")
	return command
}

func newPrivacyProcessorCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "processor",
		Short: "Administer the processor and subprocessor inventory through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newPrivacyProcessorPutCommand())
	root.AddCommand(newPrivacyProcessorListCommand())
	return root
}

func newPrivacyProcessorPutCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "put [processorID]",
		Short: "Create or update one processor inventory entry",
		Args:  cli.UsageArgs(cobra.MaximumNArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if options.processorName == "" || options.role == "" || options.purpose == "" || options.dataClasses == "" || options.regions == "" || options.transferMechanism == "" {
				return cli.UsageError(errors.New("--name, --role, --purpose, --data-classes, --regions, and --transfer-mechanism are required"))
			}
			if options.expectedVersion < 0 {
				return cli.UsageError(errors.New("expected-version must not be negative"))
			}
			body := map[string]any{
				"name": options.processorName, "role": options.role, "purpose": options.purpose,
				"data_classes": splitList(options.dataClasses), "regions": splitList(options.regions),
				"transfer_mechanism": options.transferMechanism, "expected_version": options.expectedVersion,
			}
			path := "/v1/privacy-processors"
			method := http.MethodPost
			if len(args) == 1 {
				identifier, err := id.ParseProcessor(args[0])
				if err != nil {
					return cli.UsageError(errors.New("a valid processor id is required"))
				}
				path += "/" + identifier.String()
				method = http.MethodPut
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "privacy", method: method, path: path, body: body})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.processorName, "name", "", "processor display name")
	command.Flags().StringVar(&options.role, "role", "", "processor, subprocessor, or recipient")
	command.Flags().StringVar(&options.purpose, "purpose", "", "processing purpose")
	command.Flags().StringVar(&options.dataClasses, "data-classes", "", "comma-separated data classes")
	command.Flags().StringVar(&options.regions, "regions", "", "comma-separated deployment regions")
	command.Flags().StringVar(&options.transferMechanism, "transfer-mechanism", "", "bounded transfer mechanism reference")
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "zero creates; a positive version updates")
	return command
}

func newPrivacyProcessorListCommand() *cobra.Command {
	options := &privacyRequestOptions{}
	command := &cobra.Command{
		Use:   "list",
		Short: "List the processor inventory",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.cursor != "" {
				query.Set("cursor", options.cursor)
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy", method: http.MethodGet, path: "/v1/privacy-processors", query: query,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
	command.Flags().StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	return command
}

func splitList(value string) []string {
	entries := strings.Split(value, ",")
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		trimmed := strings.TrimSpace(entry)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
