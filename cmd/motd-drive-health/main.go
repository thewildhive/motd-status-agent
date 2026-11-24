package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"motd-status-agent/internal/drivehealth"
)

func main() {
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: motd-drive-health")
		os.Exit(2)
	}

	logger := log.New(os.Stderr, "motd-drive-health: ", log.LstdFlags|log.LUTC)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := drivehealth.Run(ctx, logger); err != nil {
		logger.Print(err)
		os.Exit(1)
	}
}
