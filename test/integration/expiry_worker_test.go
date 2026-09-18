//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

func TestVerificationExpiryWorkerRecoversOfflineDeadline(t *testing.T) {
	f := newLifecycleFixture(t)
	migrator, err := taskheadgate.OpenMigrator(t.Context(), f.database.url, "headgate", 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	f.database.grantHeadgateRuntime(t, f.role)
	clearIntegrationIDENQAEnvironment(t)
	t.Setenv("IDENQA_DATABASE_URL", f.database.url)
	t.Setenv("IDENQA_DATABASE_ROLE", f.role)
	t.Setenv("IDENQA_HEADGATE_INSTALLATION_ID", "idenqa-test")
	configuration, err := config.LoadWorker("")
	if err != nil {
		t.Fatal(err)
	}
	configuration.ProgressPollInterval = 20 * time.Millisecond
	run := func() {
		process, err := bootstrapworker.NewProcess(t.Context(), configuration, slog.New(slog.NewJSONHandler(io.Discard, nil)), buildinfo.Info{Version: "integration"}, task.NewRegistry())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- process.Run(ctx) }()
		defer func() {
			cancel()
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
			if err := process.Close(context.WithoutCancel(t.Context())); err != nil {
				t.Error(err)
			}
		}()
		// A serializable conflict may schedule the selected reconciliation retry.
		retry := verificationtask.ReconcileRetry
		deadline := time.NewTimer(retry.InitialBackoff*time.Duration(100+retry.JitterPercent)/100 + 15*time.Second)
		defer deadline.Stop()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			var state string
			if err := f.admin.Native().QueryRow(t.Context(), `SELECT state FROM idenqa.verification_sessions WHERE id=$1`, f.verificationID.String()).Scan(&state); err != nil {
				t.Fatal(err)
			}
			if state == "expired" {
				return
			}
			select {
			case <-deadline.C:
				t.Fatal("worker did not expire offline session")
			case <-ticker.C:
			}
		}
	}
	run()
	run()
	f.assertState(t, verification.SessionStateExpired, 2, 1)
}
