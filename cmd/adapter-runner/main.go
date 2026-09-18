// Command adapter-runner executes one isolated tenant provider adapter.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mujhtech/idenqa/internal/bootstrap/adapterrunner"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := adapterrunner.RunContext(ctx, os.Stdout, os.Stderr, buildinfo.Current())
	stop()
	os.Exit(code)
}
