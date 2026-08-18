package server

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestListenUnixSetsModeAndRefusesFileCollision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := mustStat(t, path).Mode() & 0o777; mode != 0o660 {
		t.Fatalf("unexpected socket mode: %#o", mode)
	}
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(path); err == nil {
		t.Fatal("expected regular file collision to be refused")
	}
}

func TestListenUnixRejectsActiveListener(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.sock")
	listener, err := ListenUnix(path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if _, err := net.Dial("unix", path); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenUnix(path); err == nil {
		t.Fatal("expected active listener to be refused")
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
