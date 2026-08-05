package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	// Register the default IaC engine set (pulumi). A build wanting a
	// subset imports individual engine packages instead.
	_ "github.com/FynxLabs/reeve/internal/iac/all"
	// Register the default notification channel set (slack, webhook,
	// pagerduty, github_issue, otel_annotation). A build wanting a subset
	// imports individual channel packages instead.
	_ "github.com/FynxLabs/reeve/internal/notify/all"
)

func main() {
	// SIGINT/SIGTERM cancel the root context so a cancelled CI job (GitHub
	// Actions sends SIGTERM, then SIGKILL ~7.5s later) stops engine
	// subprocesses gracefully and releases the locks the run holds instead
	// of leaving holders pinned for the full lease ttl. Subcommands pick
	// this up via cmd.Context().
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := NewRootCmd().ExecuteContext(ctx); err != nil {
		os.Exit(1)
	}
}
