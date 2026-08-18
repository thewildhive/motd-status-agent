package discovery

import (
	"testing"

	"motd-status-agent/internal/protocol"
)

func TestNormalizeRequiredMembersAndHealth(t *testing.T) {
	got := Normalize(Definition{Name: "media", Members: []Member{
		{State: protocol.StateRunning, Health: protocol.HealthHealthy, HealthApplicable: true},
		{State: protocol.StateRunning, Health: protocol.HealthUnhealthy, HealthApplicable: true},
		{State: protocol.StateRunning, Health: protocol.HealthHealthy, HealthApplicable: true, OneShot: true, OneShotSuccessful: true},
	}})
	if got.State != protocol.StateRunning || got.Health != protocol.HealthUnhealthy {
		t.Fatalf("unexpected normalized workload: %+v", got)
	}
}

func TestNormalizeFailedAndStoppedMembers(t *testing.T) {
	failed := Normalize(Definition{Name: "failed", Members: []Member{{State: protocol.StateFailed, Health: protocol.HealthNone}}})
	stopped := Normalize(Definition{Name: "stopped", Members: []Member{{State: protocol.StateStopped, Health: protocol.HealthNone}}})
	if failed.State != protocol.StateFailed || stopped.State != protocol.StateStopped {
		t.Fatalf("unexpected states: %q %q", failed.State, stopped.State)
	}
}

func TestNormalizeSuccessfulOneShotAsRunning(t *testing.T) {
	got := Normalize(Definition{Name: "configarr", Members: []Member{{
		State:             protocol.StateStopped,
		Health:            protocol.HealthNone,
		OneShot:           true,
		OneShotSuccessful: true,
	}}})
	if got.State != protocol.StateRunning || got.Health != protocol.HealthNone {
		t.Fatalf("successful one-shot should be healthy, got %+v", got)
	}
}

func TestNormalizeFailedOneShotAsFailed(t *testing.T) {
	got := Normalize(Definition{Name: "configarr", Members: []Member{{
		State:   protocol.StateFailed,
		Health:  protocol.HealthNone,
		OneShot: true,
	}}})
	if got.State != protocol.StateFailed {
		t.Fatalf("failed one-shot should remain failed, got %+v", got)
	}
}

func TestNormalizeHealthDeclarationIsNotDropped(t *testing.T) {
	got := Normalize(Definition{Name: "healthy", Members: []Member{{
		State:            protocol.StateRunning,
		Health:           protocol.HealthHealthy,
		HealthApplicable: true,
	}}})
	if got.Health != protocol.HealthHealthy {
		t.Fatalf("health declaration was not retained: %+v", got)
	}
}
