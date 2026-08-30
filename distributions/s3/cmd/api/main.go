// Package main starts the S3-backed Idenqa Core API distribution.
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
	os.Exit(execute())
}

func execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return bootstrap.RunContext(ctx, os.Stdout, os.Stderr, buildinfo.Current())
}
