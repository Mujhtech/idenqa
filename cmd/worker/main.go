// Command worker runs the Idenqa Core background worker.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := bootstrapworker.RunContext(ctx, os.Stdout, os.Stderr, buildinfo.Current())
	stop()
	os.Exit(code)
}
