package idenqa

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/Mujhtech/idenqa/internal/acceptance"
)

func TestAcceptanceCommandEmitsScopeBoundReceipt(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	command := newAcceptanceCommand()
	command.SetOut(&output)
	command.SetArgs([]string{"validate", "--file", "../../../docs/examples/provider-acceptance-record-v1.json"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var receipt acceptance.Receipt
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.SchemaVersion != acceptance.ReceiptSchemaVersion || receipt.TupleDigest == "" || receipt.Passed {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}
}
