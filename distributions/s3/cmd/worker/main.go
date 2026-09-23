// Command worker runs the S3-backed Idenqa Core background worker.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/Mujhtech/idenqa/distributions/s3/internal/bootstrap"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := bootstrap.RunWorkerContext(ctx, os.Stdout, os.Stderr, buildinfo.Current())
	stop()
	os.Exit(code)
}
