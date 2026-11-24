package drivehealth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"syscall"
	"time"
)

const (
	stateDir              = "/var/lib/motd-drive-health"
	snapshotPath          = stateDir + "/status.json"
	snapshotSchemaVersion = 1
	snapshotMode          = 0o644
)

type snapshot struct {
	SchemaVersion int             `json:"schema_version"`
	ObservedAt    string          `json:"observed_at"`
	Drives        []snapshotDrive `json:"drives"`
}

type snapshotDrive struct {
	Label  string `json:"label"`
	State  state  `json:"state"`
	Reason reason `json:"reason"`
}

func publishSnapshot(observedAt time.Time, drives []snapshotDrive) error {
	if observedAt.IsZero() {
		return errors.New("snapshot observation time is missing")
	}
	if len(drives) == 0 {
		return errors.New("snapshot must contain at least one drive")
	}

	sort.Slice(drives, func(left, right int) bool {
		return drives[left].Label < drives[right].Label
	})
	for index, drive := range drives {
		if err := validateLabel(drive.Label); err != nil {
			return fmt.Errorf("snapshot drive %d: %w", index, err)
		}
		if index > 0 && drives[index-1].Label == drive.Label {
			return fmt.Errorf("snapshot drive %d: duplicate label %q", index, drive.Label)
		}
		if err := (result{State: drive.State, Reason: drive.Reason}).validate(); err != nil {
			return fmt.Errorf("snapshot drive %d: %w", index, err)
		}
	}

	value := snapshot{
		SchemaVersion: snapshotSchemaVersion,
		ObservedAt:    observedAt.UTC().Format(time.RFC3339),
		Drives:        drives,
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}
	data = append(data, '\n')

	if err := prepareStateDirectory(stateDir); err != nil {
		return err
	}
	if err := writeSnapshot(data); err != nil {
		return err
	}
	return nil
}

func prepareStateDirectory(path string) error {
	if err := os.MkdirAll(path, 0o755); err != nil {
		return fmt.Errorf("prepare state directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat state directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("state directory must not be a symlink")
	}
	if !info.IsDir() {
		return errors.New("state path must be a directory")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("state directory ownership is unavailable")
	}
	if stat.Uid != 0 {
		return errors.New("state directory must be owned by root")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return errors.New("state directory must not be writable by group or others")
	}
	return nil
}

func writeSnapshot(data []byte) error {
	temporary, err := os.CreateTemp(stateDir, ".status-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)

	if err := temporary.Chmod(snapshotMode); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("set temporary snapshot mode: %w", err)
	}
	info, err := temporary.Stat()
	if err != nil {
		_ = temporary.Close()
		return fmt.Errorf("stat temporary snapshot: %w", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		_ = temporary.Close()
		return errors.New("temporary snapshot ownership is unavailable")
	}
	if stat.Uid != 0 {
		_ = temporary.Close()
		return errors.New("snapshot must be owned by root")
	}
	if info.Mode().Perm() != snapshotMode {
		_ = temporary.Close()
		return fmt.Errorf("snapshot mode must be %04o", snapshotMode)
	}

	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary snapshot: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("sync temporary snapshot: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary snapshot: %w", err)
	}
	if err := os.Rename(temporaryPath, snapshotPath); err != nil {
		return fmt.Errorf("publish snapshot: %w", err)
	}
	if directory, err := os.Open(stateDir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}
