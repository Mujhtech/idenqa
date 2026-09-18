package worker

import (
	"context"
	"testing"

	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestSyntheticLoaderRejectsRealOrUnconfiguredExecution(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		enabled bool
		runner  string
	}{{true, "dojah"}, {false, "synthetic.document"}, {false, "dojah"}} {
		_, err := (syntheticRequests{enabled: test.enabled}).Load(context.Background(), tenant.Scope{}, verification.Check{}, verification.Attempt{Provenance: verification.Provenance{RunnerID: test.runner}})
		if err == nil {
			t.Fatalf("synthetic executor accepted enabled=%v runner=%s", test.enabled, test.runner)
		}
	}
}
