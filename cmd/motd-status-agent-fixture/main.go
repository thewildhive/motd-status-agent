// Command motd-status-agent-fixture serves a v1 fixture through the real
// motd-status-agent Unix-socket HTTP server for cross-repository tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"motd-status-agent/internal/protocol"
	"motd-status-agent/internal/server"
)

func main() {
	socketPath := flag.String("socket", "", "absolute Unix socket path")
	fixturePath := flag.String("fixture", "", "v1 status fixture JSON path")
	flag.Parse()
	if err := run(*socketPath, *fixturePath); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(socketPath, fixturePath string) error {
	if !filepath.IsAbs(socketPath) {
		return errors.New("socket must be an absolute path")
	}
	data, err := os.ReadFile(fixturePath)
	if err != nil {
		return fmt.Errorf("read fixture: %w", err)
	}
	var fixture protocol.StatusResponse
	if err := json.Unmarshal(data, &fixture); err != nil {
		return fmt.Errorf("decode fixture: %w", err)
	}
	if err := protocol.ValidateStatus(fixture); err != nil {
		return fmt.Errorf("validate fixture: %w", err)
	}
	fixture.ObservedAt = time.Now().UTC()

	service := &server.Service{
		SocketPath: socketPath,
		Collector: server.CollectFunc(func(context.Context) ([]protocol.Workload, error) {
			return fixture.Workloads, nil
		}),
		Logger: log.New(os.Stderr, "motd-status-agent-fixture: ", log.LstdFlags|log.LUTC),
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return service.Serve(ctx)
}
