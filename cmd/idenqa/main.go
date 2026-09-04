// Package main runs the Idenqa command-line interface.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	bootstrapidenqa "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return bootstrapidenqa.RunContext(ctx, os.Stdout, os.Stderr, buildinfo.Current())
}
