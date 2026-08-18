package discovery

import (
	"sort"

	"motd-status-agent/internal/protocol"
)

type Member struct {
	Name              string
	State             protocol.State
	Health            protocol.Health
	HealthApplicable  bool
	OneShot           bool
	OneShotSuccessful bool
}

type Definition struct {
	Name    string
	Kind    string
	Unit    string
	Members []Member
}

// Normalize turns the state of every required member into the runtime-neutral contract.
// A successful one-shot is intentionally neutral, while every other member is required.
func Normalize(def Definition) protocol.Workload {
	workload := protocol.Workload{Name: def.Name, State: protocol.StateUnknown, Health: protocol.HealthUnknown}
	if workload.Name == "" {
		return workload
	}
	if len(def.Members) == 0 {
		return workload
	}

	state := protocol.StateRunning
	health := protocol.HealthNone
	healthRank := 0
	for _, member := range def.Members {
		if member.OneShot && member.OneShotSuccessful {
			continue
		}
		if member.State == protocol.StateFailed {
			state = protocol.StateFailed
		} else if member.State == protocol.StateStopped && state != protocol.StateFailed {
			state = protocol.StateStopped
		} else if member.State != protocol.StateRunning && state != protocol.StateFailed && state != protocol.StateStopped {
			state = protocol.StateUnknown
		}
		if member.HealthApplicable {
			memberHealth := member.Health
			if memberHealth == protocol.HealthNone {
				memberHealth = protocol.HealthUnknown
			}
			rank := healthRankFor(memberHealth)
			if rank > healthRank {
				health, healthRank = memberHealth, rank
			}
		}
	}
	workload.State, workload.Health = state, health
	return workload
}

func healthRankFor(value protocol.Health) int {
	switch value {
	case protocol.HealthUnhealthy:
		return 4
	case protocol.HealthStarting:
		return 3
	case protocol.HealthUnknown:
		return 2
	case protocol.HealthHealthy:
		return 1
	default:
		return 0
	}
}

func Sort(workloads []protocol.Workload) {
	sort.Slice(workloads, func(i, j int) bool { return workloads[i].Name < workloads[j].Name })
}
