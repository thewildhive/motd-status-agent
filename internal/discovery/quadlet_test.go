package discovery

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"motd-status-agent/internal/protocol"
)

func TestDefinitionsOnlyIncludeSupportedQuadlets(t *testing.T) {
	directory := t.TempDir()
	for _, name := range []string{"media.container", "backup.pod", "ignored.kube", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(directory, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	definitions, err := (Collector{UnitDir: directory}).definitions()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 2 || definitions[0].Name != "backup" || definitions[1].Name != "media" {
		t.Fatalf("unexpected definitions: %+v", definitions)
	}
}

func TestDefinitionsSuppressPodMembers(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "media.pod"), []byte("[Pod]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "jellyfin.container"), []byte("[Container]\nPod=media.pod\nHealthCmd=/health\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	definitions, err := (Collector{UnitDir: directory}).definitions()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 1 || definitions[0].Name != "media" || len(definitions[0].Members) != 1 || !definitions[0].Members[0].HealthApplicable {
		t.Fatalf("unexpected pod definitions: %+v", definitions)
	}
}

func TestDefinitionsMissingDirectoryIsEmptySuccess(t *testing.T) {
	definitions, err := (Collector{UnitDir: filepath.Join(t.TempDir(), "missing")}).definitions()
	if err != nil || definitions == nil || len(definitions) != 0 {
		t.Fatalf("expected empty definitions, got %+v, %v", definitions, err)
	}
}

func TestInspectMarksSuccessfulOneShotAsSuccessful(t *testing.T) {
	systemctl := filepath.Join(t.TempDir(), "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\nprintf 'ActiveState=inactive\\nResult=success\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	definition := Definition{
		Name: "configarr",
		Unit: "configarr.service",
		Members: []Member{{
			Name:    "configarr",
			OneShot: true,
		}},
	}
	members, err := (Collector{Systemctl: systemctl}).inspect(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || !members[0].OneShotSuccessful {
		t.Fatalf("successful one-shot was not recorded: %+v", members)
	}
	if got := Normalize(Definition{Name: definition.Name, Members: members}); got.State != protocol.StateRunning {
		t.Fatalf("successful one-shot should be online, got %+v", got)
	}
}

func TestInspectKeepsFailedOneShotFailed(t *testing.T) {
	systemctl := filepath.Join(t.TempDir(), "systemctl")
	if err := os.WriteFile(systemctl, []byte("#!/bin/sh\nprintf 'ActiveState=inactive\\nResult=failed\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}

	definition := Definition{
		Name: "configarr",
		Unit: "configarr.service",
		Members: []Member{{
			Name:    "configarr",
			OneShot: true,
		}},
	}
	members, err := (Collector{Systemctl: systemctl}).inspect(context.Background(), definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].OneShotSuccessful {
		t.Fatalf("failed one-shot was incorrectly marked successful: %+v", members)
	}
	if got := Normalize(Definition{Name: definition.Name, Members: members}); got.State != protocol.StateFailed {
		t.Fatalf("failed one-shot should remain failed, got %+v", got)
	}
}
