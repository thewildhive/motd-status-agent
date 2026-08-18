package protocol

import (
	"strings"
	"testing"
	"time"
)

func TestNewStatusSortsAndUsesUTC(t *testing.T) {
	workloads := []Workload{{Name: "z", State: StateStopped, Health: HealthNone}, {Name: "a", State: StateRunning, Health: HealthHealthy}}
	status := NewStatus(time.Date(2026, 8, 18, 12, 0, 0, 0, time.FixedZone("test", 3600)), workloads)
	if status.Workloads[0].Name != "a" || status.ObservedAt.Location() != time.UTC {
		t.Fatalf("status was not normalized: %+v", status)
	}
	body, err := Encode(status)
	if err != nil || !strings.Contains(string(body), `"protocol_version":1`) {
		t.Fatalf("unexpected encoding: %s (%v)", body, err)
	}
}

func TestValidateStatusRejectsInvalidData(t *testing.T) {
	err := ValidateStatus(StatusResponse{ProtocolVersion: Version, ObservedAt: time.Now(), Workloads: []Workload{{Name: "", State: StateRunning, Health: HealthNone}}})
	if err == nil {
		t.Fatal("expected empty name to be rejected")
	}
}
