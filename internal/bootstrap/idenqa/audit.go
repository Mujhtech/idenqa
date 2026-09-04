package idenqa

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Mujhtech/idenqa/internal/audit"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/spf13/cobra"
)

type auditVerifyOptions struct {
	exportFile string
	keysFile   string
}

func newAuditCommand() *cobra.Command {
	command := &cobra.Command{Use: "audit", Short: "Verify exported audit history", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(*cobra.Command, []string) error { return cli.UsageError(errors.New("audit requires an operation")) }}
	options := &auditVerifyOptions{}
	verify := &cobra.Command{Use: "verify", Short: "Verify an audit export outside the running core", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error { return executeAuditVerify(command, options) }}
	verify.Flags().StringVar(&options.exportFile, "export-file", "", "audit export JSON file")
	verify.Flags().StringVar(&options.keysFile, "keys-file", "", "checkpoint public-key history JSON file")
	_ = verify.MarkFlagFilename("export-file", "json")
	_ = verify.MarkFlagFilename("keys-file", "json")
	command.AddCommand(verify)
	return command
}

func executeAuditVerify(command *cobra.Command, options *auditVerifyOptions) error {
	if options.exportFile == "" || options.keysFile == "" {
		return cli.UsageError(errors.New("audit verify requires --export-file and --keys-file"))
	}
	encoded, err := readAuditFile(options.exportFile, audit.MaximumExportBytes)
	if err != nil {
		return cli.RuntimeError("read audit export", err)
	}
	var exported audit.Export
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&exported); err != nil {
		return cli.RuntimeError("decode audit export", err)
	}
	if err := requireAuditEOF(decoder); err != nil {
		return cli.RuntimeError("decode audit export", err)
	}
	keyBytes, err := readAuditFile(options.keysFile, 1<<20)
	if err != nil {
		return cli.RuntimeError("read audit keys", err)
	}
	var encodedKeys map[string]string
	keyDecoder := json.NewDecoder(bytes.NewReader(keyBytes))
	keyDecoder.DisallowUnknownFields()
	if err := keyDecoder.Decode(&encodedKeys); err != nil {
		return cli.RuntimeError("decode audit keys", err)
	}
	if err := requireAuditEOF(keyDecoder); err != nil {
		return cli.RuntimeError("decode audit keys", err)
	}
	keys := make(map[string]ed25519.PublicKey, len(encodedKeys))
	for keyID, encodedKey := range encodedKeys {
		decoded, decodeErr := hex.DecodeString(encodedKey)
		if decodeErr != nil || len(decoded) != ed25519.PublicKeySize {
			return cli.RuntimeError("decode audit public key", audit.ErrInvalid)
		}
		keys[keyID] = decoded
	}
	report, err := audit.Verify(exported, keys)
	if err != nil {
		return cli.RuntimeError("verify audit export", err)
	}
	result, err := json.Marshal(report)
	if err != nil {
		return cli.RuntimeError("encode audit report", err)
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\n", result); err != nil {
		return cli.RuntimeError("write audit report", err)
	}
	return nil
}

func requireAuditEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); errors.Is(err, io.EOF) {
		return nil
	}
	return audit.ErrInvalid
}

func readAuditFile(path string, maximum int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() <= 0 || info.Size() > maximum {
		return nil, audit.ErrInvalid
	}
	return os.ReadFile(path) //nolint:gosec // Explicit operator-supplied CLI input, bounded by the preceding stat.
}
