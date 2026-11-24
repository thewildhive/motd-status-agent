package drivehealth

import "fmt"

type state string

const (
	stateHealthy state = "healthy"
	stateWarning state = "warning"
	stateFailed  state = "failed"
	stateUnknown state = "unknown"
)

type reason string

const (
	reasonPassed        reason = "passed"
	reasonOverallFailed reason = "overall_failed"
	reasonNVMeCritical  reason = "nvme_critical"

	reasonATAPrefailNow reason = "ata_prefail_now"
	reasonNVMeAdvisory  reason = "nvme_advisory"

	reasonMissing          reason = "missing"
	reasonIdentityMismatch reason = "identity_mismatch"
	reasonAccessDenied     reason = "access_denied"
	reasonSleeping         reason = "sleeping"
	reasonUnsupported      reason = "unsupported"
	reasonTimeout          reason = "timeout"
	reasonInvalidResponse  reason = "invalid_response"
	reasonProbeError       reason = "probe_error"
)

type result struct {
	State  state
	Reason reason
}

func healthy(value reason) result {
	return result{State: stateHealthy, Reason: value}
}

func warning(value reason) result {
	return result{State: stateWarning, Reason: value}
}

func failed(value reason) result {
	return result{State: stateFailed, Reason: value}
}

func unknown(value reason) result {
	return result{State: stateUnknown, Reason: value}
}

func (r result) validate() error {
	switch r.State {
	case stateHealthy:
		if r.Reason == reasonPassed {
			return nil
		}
	case stateFailed:
		if r.Reason == reasonOverallFailed || r.Reason == reasonNVMeCritical {
			return nil
		}
	case stateWarning:
		if r.Reason == reasonATAPrefailNow || r.Reason == reasonNVMeAdvisory {
			return nil
		}
	case stateUnknown:
		switch r.Reason {
		case reasonMissing,
			reasonIdentityMismatch,
			reasonAccessDenied,
			reasonSleeping,
			reasonUnsupported,
			reasonTimeout,
			reasonInvalidResponse,
			reasonProbeError:
			return nil
		}
	}
	return fmt.Errorf("invalid drive health result %q/%q", r.State, r.Reason)
}
