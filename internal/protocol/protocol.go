package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

const Version = 1

type State string

const (
	StateRunning State = "running"
	StateStopped State = "stopped"
	StateFailed  State = "failed"
	StateUnknown State = "unknown"
)

type Health string

const (
	HealthHealthy   Health = "healthy"
	HealthUnhealthy Health = "unhealthy"
	HealthStarting  Health = "starting"
	HealthNone      Health = "none"
	HealthUnknown   Health = "unknown"
)

type Workload struct {
	Name   string `json:"name"`
	State  State  `json:"state"`
	Health Health `json:"health"`
}

type StatusResponse struct {
	ProtocolVersion int        `json:"protocol_version"`
	ObservedAt      time.Time  `json:"observed_at"`
	Workloads       []Workload `json:"workloads"`
}

type ErrorBody struct {
	ProtocolVersion int `json:"protocol_version"`
	Error           struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func NewStatus(observedAt time.Time, workloads []Workload) StatusResponse {
	if workloads == nil {
		workloads = []Workload{}
	} else {
		workloads = append([]Workload(nil), workloads...)
	}
	sort.Slice(workloads, func(i, j int) bool { return workloads[i].Name < workloads[j].Name })
	return StatusResponse{ProtocolVersion: Version, ObservedAt: observedAt.UTC(), Workloads: workloads}
}

func NewError(code string) ErrorBody {
	messages := map[string]string{
		"bad_request":         "invalid HTTP request",
		"not_found":           "status endpoint not found",
		"method_not_allowed":  "method is not allowed",
		"runtime_unavailable": "workload runtime is unavailable",
		"permission_denied":   "permission denied while collecting workload status",
		"collection_timeout":  "workload status collection timed out",
		"response_too_large":  "workload status response is too large",
		"internal_error":      "internal status collection error",
	}
	message, ok := messages[code]
	if !ok {
		code, message = "internal_error", messages["internal_error"]
	}
	result := ErrorBody{ProtocolVersion: Version}
	result.Error.Code, result.Error.Message = code, message
	return result
}

func ValidateStatus(response StatusResponse) error {
	if response.ProtocolVersion != Version || response.ObservedAt.IsZero() {
		return fmt.Errorf("invalid protocol version or observation timestamp")
	}
	seen := make(map[string]struct{}, len(response.Workloads))
	for _, workload := range response.Workloads {
		if workload.Name == "" {
			return fmt.Errorf("workload name is empty")
		}
		if _, exists := seen[workload.Name]; exists {
			return fmt.Errorf("duplicate workload name %q", workload.Name)
		}
		seen[workload.Name] = struct{}{}
		if !validState(workload.State) || !validHealth(workload.Health) {
			return fmt.Errorf("invalid state or health for workload %q", workload.Name)
		}
	}
	return nil
}

func validState(value State) bool {
	return value == StateRunning || value == StateStopped || value == StateFailed || value == StateUnknown
}

func validHealth(value Health) bool {
	return value == HealthHealthy || value == HealthUnhealthy || value == HealthStarting || value == HealthNone || value == HealthUnknown
}

func Encode(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
