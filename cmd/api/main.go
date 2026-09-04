// Package main runs the Idenqa public API process.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func main() {
	os.Exit(execute())
}

func execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return bootstrapapi.RunContext(ctx, os.Stdout, os.Stderr, buildinfo.Current())
}
