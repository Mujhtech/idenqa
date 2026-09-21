package idenqa

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/spf13/cobra"
)

var packCountryCode = regexp.MustCompile(`^[A-Za-z]{2}$|^[A-Za-z]{3}$`)

type packOptions struct {
	reviewOptions
}

func newPackCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "pack",
		Short: "Inspect immutable country and document packs through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newPackListCommand())
	root.AddCommand(newPackCountryCommand())
	root.AddCommand(newPackDocumentCommand())
	root.AddCommand(newPackSupportCommand())
	return root
}

func newPackListCommand() *cobra.Command {
	options := &packOptions{}
	command := &cobra.Command{
		Use:   "list",
		Short: "List registered pack revisions",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "pack", method: http.MethodGet, path: "/v1/packs"})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newPackCountryCommand() *cobra.Command {
	options := &packOptions{}
	command := &cobra.Command{
		Use:   "country <country>",
		Short: "Get the active pack for a country",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			country, err := validPackCountry(args[0])
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "pack",
				method:    http.MethodGet,
				path:      "/v1/packs/" + country,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newPackDocumentCommand() *cobra.Command {
	options := &packOptions{}
	command := &cobra.Command{
		Use:   "document <country> <type>",
		Short: "Get one active pack document entry",
		Args:  cli.UsageArgs(cobra.ExactArgs(2)),
		RunE: func(command *cobra.Command, args []string) error {
			country, err := validPackCountry(args[0])
			if err != nil {
				return err
			}
			documentType, err := validPackDocumentType(args[1])
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "pack",
				method:    http.MethodGet,
				path:      "/v1/packs/" + country,
				transform: packDocumentProjection(documentType),
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newPackSupportCommand() *cobra.Command {
	options := &packOptions{}
	command := &cobra.Command{
		Use:   "support <country> <type>",
		Short: "Get the honest document support-level projection",
		Args:  cli.UsageArgs(cobra.ExactArgs(2)),
		RunE: func(command *cobra.Command, args []string) error {
			country, err := validPackCountry(args[0])
			if err != nil {
				return err
			}
			documentType, err := validPackDocumentType(args[1])
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "pack",
				method:    http.MethodGet,
				path:      "/v1/document-support",
				query:     url.Values{"country": {country}, "type": {documentType}},
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func validPackCountry(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if !packCountryCode.MatchString(trimmed) {
		return "", cli.UsageError(errors.New("a two- or three-letter ISO 3166-1 country code is required"))
	}
	return strings.ToUpper(trimmed), nil
}

func validPackDocumentType(value string) (string, error) {
	switch strings.TrimSpace(value) {
	case "passport":
		return "passport", nil
	case "national_id":
		return "national_id", nil
	case "driver_licence", "drivers_license":
		return "driver_licence", nil
	default:
		return "", cli.UsageError(errors.New("type must be passport, national_id, or driver_licence"))
	}
}

func packDocumentProjection(documentType string) func([]byte) ([]byte, error) {
	return func(raw []byte) ([]byte, error) {
		var envelope struct {
			Pack struct {
				Documents []json.RawMessage `json:"documents"`
			} `json:"pack"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			return nil, errors.New("decode pack API response")
		}
		for _, document := range envelope.Pack.Documents {
			var header struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal(document, &header); err != nil {
				return nil, errors.New("decode pack document")
			}
			if header.Type == documentType {
				return document, nil
			}
		}
		return nil, errors.New("the active pack has no such document")
	}
}
