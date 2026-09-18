// Command model-runner executes one isolated tenant model adapter.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mujhtech/idenqa/internal/bootstrap/modelrunner"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := modelrunner.RunContext(ctx, os.Stdout, os.Stderr, buildinfo.Current())
	stop()
	os.Exit(code)
}
