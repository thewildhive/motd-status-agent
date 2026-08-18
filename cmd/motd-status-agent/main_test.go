package main

import (
	"os/user"
	"strings"
	"testing"
)

func TestRenderServiceUnitUsesSystemUserManagerEnvironment(t *testing.T) {
	target := &user.User{Username: "media", HomeDir: "/home/media", Gid: "1002"}
	unit := renderServiceUnit(target, "motd-status", 1002, "/usr/local/bin/motd-status-agent", "/home/media/.config/motd-status-agent/config.json")
	checks := []string{
		"Requires=user@1002.service",
		"After=user@1002.service",
		"User=media",
		"Group=motd-status",
		"SupplementaryGroups=1002",
		"Environment=HOME=/home/media",
		"Environment=XDG_RUNTIME_DIR=/run/user/1002",
		"Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/1002/bus",
		"ExecStart=/usr/local/bin/motd-status-agent serve --config /home/media/.config/motd-status-agent/config.json --user media",
		"WantedBy=multi-user.target",
	}
	for _, check := range checks {
		if !strings.Contains(unit, check) {
			t.Errorf("service unit does not contain %q:\n%s", check, unit)
		}
	}
	if strings.Contains(unit, "systemctl --user") || strings.Contains(unit, "--machine=") {
		t.Fatalf("service unit contains user-manager orchestration: %s", unit)
	}
}

func TestSystemdValueQuotesUnsafeValues(t *testing.T) {
	got := systemdValue(`/opt/motd agent/bin`)
	if got != `"/opt/motd agent/bin"` {
		t.Fatalf("systemdValue() = %q", got)
	}
}

func TestRenderTmpfilesRule(t *testing.T) {
	if got, want := renderTmpfilesRule("media", "motd-status"), "d /run/motd-status 02750 media motd-status -\n"; got != want {
		t.Fatalf("renderTmpfilesRule() = %q, want %q", got, want)
	}
}

func TestDisplayVersion(t *testing.T) {
	original := VERSION
	t.Cleanup(func() { VERSION = original })
	for input, want := range map[string]string{"dev": "dev", "0.1.0": "v0.1.0", "v0.1.0": "v0.1.0"} {
		VERSION = input
		if got := displayVersion(); got != want {
			t.Errorf("displayVersion() for %q = %q, want %q", input, got, want)
		}
	}
}
