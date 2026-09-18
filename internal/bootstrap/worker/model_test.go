package worker

import (
	"context"
	"testing"

	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestSyntheticModelLoaderRejectsRealOrUnconfiguredExecution(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		enabled bool
		runner  string
	}{{true, "onnx.pad"}, {false, "synthetic.liveness"}, {false, "onnx.pad"}} {
		_, err := (syntheticModelRequests{enabled: test.enabled}).Load(context.Background(), tenant.Scope{}, verification.Check{}, verification.Attempt{Provenance: verification.Provenance{RunnerID: test.runner}})
		if err == nil {
			t.Fatalf("synthetic executor accepted enabled=%v runner=%s", test.enabled, test.runner)
		}
	}
}
