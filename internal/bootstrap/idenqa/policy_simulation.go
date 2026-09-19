package idenqa

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/spf13/cobra"
)

const policyComputationResponseMaximum = 1 << 20

type policyComputationOptions struct {
	apiURL, keyFile string
	outputJSON      bool
}

type policySimulationOptions struct {
	policyComputationOptions
	bodyFile string
}

type policyDiffOptions struct {
	policyComputationOptions
	policyID string
	from, to uint32
}

type policyComputationRequest struct {
	method, path string
	query        url.Values
	body         []byte
}

func addPolicySimulationCommands(root *cobra.Command) {
	root.AddCommand(newPolicySimulateCommand())
	root.AddCommand(newPolicyRegressionCommand())
	root.AddCommand(newPolicyDiffCommand())
}

func newPolicySimulateCommand() *cobra.Command {
	options := &policySimulationOptions{}
	command := &cobra.Command{
		Use:   "simulate",
		Short: "Evaluate one portable synthetic policy simulation",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readPolicyComputationBody(command, options.bodyFile, policy.MaximumSimulationInputBytes)
			if err != nil {
				return err
			}
			raw, err := runPolicyComputationHTTP(command, &options.policyComputationOptions, policyComputationRequest{method: http.MethodPost, path: "/v1/policy-simulations", body: body})
			if err != nil {
				return err
			}
			var report policy.SimulationReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return cli.RuntimeError("policy simulate", errors.New("decode policy simulation report"))
			}
			if options.outputJSON {
				return writePolicyComputationJSON(command.OutOrStdout(), raw)
			}
			_, err = fmt.Fprintf(
				command.OutOrStdout(),
				"policy_simulation policy_id=%s policy_revision=%d directive=%s outcome=%s assurance=%s authorises_completion=%t evaluation_digest=%s bundle_digest=%s\n",
				report.PolicyID, report.PolicyRevision, report.Directive, report.Outcome,
				report.Assurance, report.AuthorisesCompletion, report.EvaluationDigest, report.BundleDigest,
			)
			return outputError(err)
		},
	}
	addPolicyComputationFlags(command, &options.policyComputationOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON simulation input file, or - for stdin")
	command.Flags().BoolVar(&options.outputJSON, "json", false, "print the canonical report as JSON")
	return command
}

func newPolicyRegressionCommand() *cobra.Command {
	options := &policySimulationOptions{}
	command := &cobra.Command{
		Use:   "regression",
		Short: "Evaluate one bounded portable policy scenario suite",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readPolicyComputationBody(command, options.bodyFile, policy.MaximumScenarioSuiteBytes)
			if err != nil {
				return err
			}
			raw, err := runPolicyComputationHTTP(command, &options.policyComputationOptions, policyComputationRequest{method: http.MethodPost, path: "/v1/policy-regressions", body: body})
			if err != nil {
				return err
			}
			var report policy.ScenarioSuiteReport
			if err := json.Unmarshal(raw, &report); err != nil {
				return cli.RuntimeError("policy regression", errors.New("decode policy regression report"))
			}
			if options.outputJSON {
				if err := writePolicyComputationJSON(command.OutOrStdout(), raw); err != nil {
					return err
				}
			} else {
				for _, item := range report.Cases {
					if _, err := fmt.Fprintf(
						command.OutOrStdout(),
						"policy_regression_case name=%s matches=%t expected_evaluation_digest=%s actual_evaluation_digest=%s\n",
						item.Name, item.Matches, item.ExpectedEvaluationDigest, item.Actual.EvaluationDigest,
					); err != nil {
						return cli.RuntimeError("policy regression", err)
					}
				}
				if _, err := fmt.Fprintf(
					command.OutOrStdout(),
					"policy_regression passed=%t cases=%d digest=%s\n",
					report.Passed, len(report.Cases), report.Digest,
				); err != nil {
					return cli.RuntimeError("policy regression", err)
				}
			}
			if !report.Passed {
				return cli.RuntimeError("policy regression", errors.New("one or more scenarios did not match"))
			}
			return nil
		},
	}
	addPolicyComputationFlags(command, &options.policyComputationOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON scenario suite file, or - for stdin")
	command.Flags().BoolVar(&options.outputJSON, "json", false, "print the canonical report as JSON")
	return command
}

func newPolicyDiffCommand() *cobra.Command {
	options := &policyDiffOptions{}
	command := &cobra.Command{
		Use:   "diff",
		Short: "Compare two stored immutable policy revisions",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.from == 0 || options.to == 0 {
				return cli.UsageError(errors.New("from and to revisions must be positive"))
			}
			if _, err := id.ParsePolicy(options.policyID); err != nil {
				return cli.UsageError(errors.New("a valid policy id is required"))
			}
			query := url.Values{
				"from_revision": {strconv.FormatUint(uint64(options.from), 10)},
				"to_revision":   {strconv.FormatUint(uint64(options.to), 10)},
			}
			raw, err := runPolicyComputationHTTP(command, &options.policyComputationOptions, policyComputationRequest{
				method: http.MethodGet, path: "/v1/policies/" + options.policyID + "/diff", query: query,
			})
			if err != nil {
				return err
			}
			var diff policy.RevisionDiff
			if err := json.Unmarshal(raw, &diff); err != nil {
				return cli.RuntimeError("policy diff", errors.New("decode policy revision diff"))
			}
			if options.outputJSON {
				return writePolicyComputationJSON(command.OutOrStdout(), raw)
			}
			if _, err := fmt.Fprintf(
				command.OutOrStdout(),
				"policy_diff policy_id=%s from_revision=%d to_revision=%d identical=%t truncated=%t change_count=%d digest=%s\n",
				diff.PolicyID, diff.From.Revision, diff.To.Revision, diff.Identical,
				diff.Truncated, diff.ChangeCount, diff.Digest,
			); err != nil {
				return cli.RuntimeError("policy diff", err)
			}
			for _, change := range diff.Changes {
				if _, err := fmt.Fprintf(
					command.OutOrStdout(),
					"policy_diff_change path=%s kind=%s old=%s new=%s\n",
					change.Path, change.Kind, change.Old, change.New,
				); err != nil {
					return cli.RuntimeError("policy diff", err)
				}
			}
			return nil
		},
	}
	addPolicyComputationFlags(command, &options.policyComputationOptions)
	command.Flags().StringVar(&options.policyID, "policy-id", "", "tenant-owned policy identifier")
	command.Flags().Uint32Var(&options.from, "from", 0, "immutable source revision")
	command.Flags().Uint32Var(&options.to, "to", 0, "immutable target revision")
	command.Flags().BoolVar(&options.outputJSON, "json", false, "print the canonical diff as JSON")
	return command
}

func addPolicyComputationFlags(command *cobra.Command, options *policyComputationOptions) {
	command.Flags().StringVar(&options.apiURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	command.Flags().StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
}

func readPolicyComputationBody(command *cobra.Command, path string, maximum int) ([]byte, error) {
	if path == "" {
		return nil, cli.UsageError(errors.New("body-file is required"))
	}
	input := command.InOrStdin()
	if path != "-" {
		file, err := os.Open(path) //nolint:gosec // operator-supplied CLI body file, not request input
		if err != nil {
			return nil, cli.RuntimeError("policy computation", errors.New("open policy body file"))
		}
		defer func() { _ = file.Close() }()
		input = file
	}
	material, err := io.ReadAll(io.LimitReader(input, int64(maximum)+1))
	if err != nil || len(material) == 0 || len(material) > maximum || !json.Valid(material) {
		clear(material)
		return nil, cli.UsageError(errors.New("body-file must contain one bounded JSON request body"))
	}
	return material, nil
}

func runPolicyComputationHTTP(
	command *cobra.Command,
	options *policyComputationOptions,
	request policyComputationRequest,
) ([]byte, error) {
	base, err := url.Parse(options.apiURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return nil, cli.UsageError(errors.New("a valid HTTPS API URL or local loopback HTTP URL is required"))
	}
	credential := os.Getenv("IDENQA_API_KEY")
	if options.keyFile != "" {
		file, err := os.Open(options.keyFile)
		if err != nil {
			return nil, cli.RuntimeError("policy computation", errors.New("read API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return nil, cli.RuntimeError("policy computation", errors.New("read bounded API credential file"))
		}
		credential = strings.TrimSpace(string(material))
		clear(material)
	}
	if credential == "" || strings.ContainsAny(credential, "\r\n") {
		return nil, cli.UsageError(errors.New("an API credential file or IDENQA_API_KEY is required"))
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + request.path
	if len(request.query) > 0 {
		target.RawQuery = request.query.Encode()
	}
	apiRequest, err := http.NewRequestWithContext(command.Context(), request.method, target.String(), bytes.NewReader(request.body))
	if err != nil {
		return nil, cli.RuntimeError("policy computation", errors.New("construct policy API request"))
	}
	apiRequest.Header.Set("Authorization", "Bearer "+credential)
	if request.body != nil {
		apiRequest.Header.Set("Content-Type", "application/json")
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(apiRequest)
	if err != nil {
		return nil, cli.RuntimeError("policy computation", errors.New("policy API request failed"))
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, policyComputationResponseMaximum+1))
	if err != nil || len(raw) > policyComputationResponseMaximum {
		return nil, cli.RuntimeError("policy computation", errors.New("read bounded policy API response"))
	}
	if response.StatusCode != http.StatusOK {
		clear(raw)
		return nil, cli.RuntimeError("policy computation", fmt.Errorf("policy API returned status %d", response.StatusCode))
	}
	if !json.Valid(raw) {
		clear(raw)
		return nil, cli.RuntimeError("policy computation", errors.New("invalid policy API response"))
	}
	return raw, nil
}

func writePolicyComputationJSON(writer io.Writer, raw []byte) error {
	if _, err := writer.Write(raw); err != nil {
		return cli.RuntimeError("policy computation", err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		if _, err := writer.Write([]byte("\n")); err != nil {
			return cli.RuntimeError("policy computation", err)
		}
	}
	return nil
}
