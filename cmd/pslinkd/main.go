package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dudeofawesome/pslinkd/internal/command"
	"github.com/dudeofawesome/pslinkd/internal/config"
	"github.com/dudeofawesome/pslinkd/internal/daemon"
	"github.com/dudeofawesome/pslinkd/internal/logging"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	runtimeStarted := false
	err := command.Execute(os.Args[1:], os.Getenv, func(cfg config.Config) error {
		runtimeStarted = true
		logger := logging.New(os.Stdout, cfg.Logging.Level, nil)
		return daemon.Run(ctx, cfg, logger, daemon.ProductionDependencies())
	})
	if err == nil {
		return
	}
	if !runtimeStarted {
		fmt.Fprintln(os.Stderr, err)
	}
	os.Exit(2)
}
