package idenqa

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	"github.com/spf13/cobra"
)

const exportLineMaximum = 8 << 20

type exportOptions struct {
	apiURL      string
	keyFile     string
	output      string
	collections string
	force       bool
}

func newExportCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "export",
		Short: "Export tenant-owned portable data through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(*cobra.Command, []string) error {
			return cli.UsageError(errors.New("export requires an operation"))
		},
	}
	root.AddCommand(newExportTenantCommand())
	return root
}

func newExportTenantCommand() *cobra.Command {
	options := &exportOptions{}
	command := &cobra.Command{
		Use:   "tenant",
		Short: "Stream the authenticated tenant's portable NDJSON export",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runExportTenant(command, options)
		},
	}
	command.Flags().StringVar(&options.output, "output", "", "destination file path, or - for stdout")
	command.Flags().BoolVar(&options.force, "force", false, "overwrite an existing destination file")
	command.Flags().StringVar(&options.collections, "collections", "", "comma-separated subset of export collections")
	command.Flags().StringVar(&options.apiURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	command.Flags().StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
	return command
}

func runExportTenant(command *cobra.Command, options *exportOptions) error {
	const operation = "export tenant"
	if options.output == "" {
		return cli.UsageError(errors.New("--output is required, or - for stdout"))
	}
	selection, err := tenantexport.ParseCollections(options.collections)
	if err != nil {
		return cli.UsageError(errors.New("collections must be a bounded list of known export collections"))
	}
	base, err := url.Parse(options.apiURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" ||
		(base.Scheme != "https" && (base.Scheme != "http" ||
			(base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return cli.UsageError(errors.New("a valid HTTPS API URL or local loopback HTTP URL is required"))
	}
	key := os.Getenv("IDENQA_API_KEY")
	if options.keyFile != "" {
		file, err := os.Open(options.keyFile)
		if err != nil {
			return cli.RuntimeError(operation, errors.New("read API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return cli.RuntimeError(operation, errors.New("read bounded API credential file"))
		}
		key = strings.TrimSpace(string(material))
		clear(material)
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return cli.UsageError(errors.New("an API credential file or IDENQA_API_KEY is required"))
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/v1/tenant/export"
	if len(selection) > 0 {
		target.RawQuery = url.Values{"collections": {options.collections}}.Encode()
	}
	apiRequest, err := http.NewRequestWithContext(command.Context(), http.MethodGet, target.String(), nil)
	if err != nil {
		return cli.RuntimeError(operation, errors.New("construct export API request"))
	}
	apiRequest.Header.Set("Authorization", "Bearer "+key)
	apiRequest.Header.Set("Accept", "application/x-ndjson")
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(apiRequest)
	if err != nil {
		return cli.RuntimeError(operation, errors.New("export API request failed"))
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return cli.RuntimeError(operation, fmt.Errorf("export API returned status %d", response.StatusCode))
	}
	destination, cleanup, err := openExportOutput(command, options)
	if err != nil {
		return cli.RuntimeError(operation, err)
	}
	succeeded := false
	defer func() { cleanup(!succeeded) }()
	buffered := bufio.NewWriterSize(destination, 64<<10)
	if err := streamTenantExport(response.Body, buffered); err != nil {
		return cli.RuntimeError(operation, err)
	}
	if err := buffered.Flush(); err != nil {
		return cli.RuntimeError(operation, errors.New("flush tenant export output"))
	}
	succeeded = true
	return nil
}

func openExportOutput(command *cobra.Command, options *exportOptions) (io.Writer, func(bool), error) {
	if options.output == "-" {
		return command.OutOrStdout(), func(bool) {}, nil
	}
	flags := os.O_WRONLY | os.O_CREATE
	removeOnFailure := !options.force
	if options.force {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}
	file, err := os.OpenFile(options.output, flags, 0600)
	if err != nil {
		return nil, nil, errors.New("create exclusive owner-only tenant export file")
	}
	cleanup := func(failed bool) {
		_ = file.Close()
		if failed && removeOnFailure {
			_ = os.Remove(options.output)
		}
	}
	return file, cleanup, nil
}

// streamTenantExport copies canonical NDJSON to writer while hashing every
// byte before the footer line. It fails when the footer digest does not match
// the locally computed digest or when the stream is truncated or extended.
func streamTenantExport(body io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64<<10), exportLineMaximum)
	digest := sha256.New()
	footerSeen := false
	for scanner.Scan() {
		line := scanner.Bytes()
		if footerSeen {
			return errors.New("tenant export contains records after its footer")
		}
		if isExportFooter(line) {
			var footer struct {
				Record string `json:"record"`
				Digest string `json:"digest"`
			}
			if err := json.Unmarshal(line, &footer); err != nil || footer.Record != "footer" || !validExportDigest(footer.Digest) {
				return errors.New("tenant export footer is invalid")
			}
			if footer.Digest != "sha256:"+hex.EncodeToString(digest.Sum(nil)) {
				return errors.New("tenant export digest does not match its contents")
			}
			footerSeen = true
		} else {
			_, _ = digest.Write(line)
			_, _ = digest.Write([]byte{'\n'})
		}
		if _, err := writer.Write(line); err != nil {
			return errors.New("write tenant export output")
		}
		if _, err := writer.Write([]byte{'\n'}); err != nil {
			return errors.New("write tenant export output")
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("read tenant export stream")
	}
	if !footerSeen {
		return errors.New("tenant export footer is missing")
	}
	return nil
}

func isExportFooter(line []byte) bool {
	return bytes.HasPrefix(line, []byte(`{"record":"footer"`))
}

func validExportDigest(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[len("sha256:"):])
	return err == nil
}
