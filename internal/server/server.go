package server

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"motd-status-agent/internal/discovery"
	"motd-status-agent/internal/protocol"
)

const (
	RequestLimit  = 8 * 1024
	ResponseLimit = 1 * 1024 * 1024
	CollectLimit  = 750 * time.Millisecond
)

type Collector interface {
	Collect(context.Context) ([]protocol.Workload, error)
}

type CollectFunc func(context.Context) ([]protocol.Workload, error)

func (f CollectFunc) Collect(ctx context.Context) ([]protocol.Workload, error) { return f(ctx) }

type Service struct {
	SocketPath string
	Group      string
	Collector  Collector
	Logger     *log.Logger
	MaxClients int

	listener net.Listener
	server   *http.Server
	cleanup  sync.Once
}

func (s *Service) Serve(ctx context.Context) error {
	if s.SocketPath == "" {
		s.SocketPath = "/var/run/motd-status/agent.sock"
	}
	if s.Collector == nil {
		return errors.New("collector is required")
	}
	listener, err := ListenUnixGroup(s.SocketPath, s.Group)
	if err != nil {
		return err
	}
	s.listener = listener
	defer s.cleanupSocket()

	maxClients := s.MaxClients
	if maxClients <= 0 {
		maxClients = 32
	}
	semaphore := make(chan struct{}, maxClients)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
		case <-r.Context().Done():
			return
		default:
			writeError(w, "internal_error", http.StatusServiceUnavailable)
			return
		}
		s.handle(w, r)
	})
	s.server = &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       3 * time.Second,
		WriteTimeout:      3 * time.Second,
		IdleTimeout:       3 * time.Second,
		MaxHeaderBytes:    RequestLimit,
	}
	if s.Logger != nil {
		s.server.ErrorLog = s.Logger
		s.Logger.Printf("started socket=%s", s.SocketPath)
	}
	go func() {
		<-ctx.Done()
		_ = s.server.Shutdown(context.Background())
	}()
	err = s.server.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Service) handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "method_not_allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/v1/status" {
		writeError(w, "not_found", http.StatusNotFound)
		return
	}
	if r.URL.RawQuery != "" || r.ProtoMajor != 1 || r.ProtoMinor != 1 {
		writeError(w, "bad_request", http.StatusBadRequest)
		return
	}
	if r.ContentLength != 0 || r.Header.Get("Transfer-Encoding") != "" {
		writeError(w, "bad_request", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), CollectLimit)
	defer cancel()
	workloads, err := s.Collector.Collect(ctx)
	if err != nil {
		code, status := classifyError(err)
		writeError(w, code, status)
		if s.Logger != nil {
			s.Logger.Printf("collection failed code=%s", code)
		}
		return
	}
	response := protocol.NewStatus(time.Now().UTC(), workloads)
	if err := protocol.ValidateStatus(response); err != nil {
		writeError(w, "internal_error", http.StatusInternalServerError)
		return
	}
	body, err := protocol.Encode(response)
	if err != nil || len(body) > ResponseLimit {
		writeError(w, "response_too_large", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func writeError(w http.ResponseWriter, code string, status int) {
	body, _ := protocol.Encode(protocol.NewError(code))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func classifyError(err error) (string, int) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "collection_timeout", http.StatusGatewayTimeout
	case errors.Is(err, discovery.ErrRuntimeUnavailable):
		return "runtime_unavailable", http.StatusServiceUnavailable
	case errors.Is(err, discovery.ErrPermissionDenied):
		return "permission_denied", http.StatusForbidden
	default:
		return "internal_error", http.StatusInternalServerError
	}
}

func ListenUnix(path string) (net.Listener, error) {
	return ListenUnixGroup(path, "")
}

func ListenUnixGroup(path, group string) (net.Listener, error) {
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("socket path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("create socket directory: %w", err)
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.IsDir() || info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("refusing non-agent socket collision at %s", path)
		}
		probe, probeErr := net.DialTimeout("unix", path, 100*time.Millisecond)
		if probeErr == nil {
			_ = probe.Close()
			return nil, fmt.Errorf("another listener is active at %s", path)
		}
		marker, markerErr := os.ReadFile(socketMarker(path))
		if markerErr != nil || string(marker) != socketIdentity(info) {
			return nil, fmt.Errorf("refusing unowned stale socket at %s", path)
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("remove stale socket: %w", removeErr)
		}
		_ = os.Remove(socketMarker(path))
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect socket: %w", err)
	}
	listener, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("listen on socket: %w", err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("set socket mode: %w", err)
	}
	if group != "" {
		entry, lookupErr := user.LookupGroup(group)
		if lookupErr != nil {
			_ = listener.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("lookup socket group: %w", lookupErr)
		}
		gid, parseErr := strconv.Atoi(entry.Gid)
		if parseErr != nil || gid < 0 {
			_ = listener.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("invalid socket group id")
		}
		if err := os.Chown(path, -1, gid); err != nil {
			_ = listener.Close()
			_ = os.Remove(path)
			return nil, fmt.Errorf("set socket group: %w", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("inspect listening socket: %w", err)
	}
	if err := os.WriteFile(socketMarker(path), []byte(socketIdentity(info)), 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write socket ownership marker: %w", err)
	}
	return listener, nil
}

func (s *Service) cleanupSocket() {
	s.cleanup.Do(func() {
		if s.listener != nil {
			_ = s.listener.Close()
		}
		if s.SocketPath == "" {
			return
		}
		info, err := os.Lstat(s.SocketPath)
		marker, markerErr := os.ReadFile(socketMarker(s.SocketPath))
		if err == nil && markerErr == nil && info.Mode()&os.ModeSocket != 0 && info.Mode()&os.ModeSymlink == 0 && string(marker) == socketIdentity(info) {
			_ = os.Remove(s.SocketPath)
			_ = os.Remove(socketMarker(s.SocketPath))
		} else if errors.Is(err, os.ErrNotExist) {
			// net.UnixListener may unlink its own path during Close.
			_ = os.Remove(socketMarker(s.SocketPath))
		}
	})
}

func socketMarker(path string) string { return path + ".owner" }

func socketIdentity(info os.FileInfo) string {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	return strconv.FormatUint(uint64(stat.Dev), 10) + ":" + strconv.FormatUint(uint64(stat.Ino), 10)
}
