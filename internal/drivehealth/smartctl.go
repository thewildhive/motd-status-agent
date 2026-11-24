package drivehealth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	smartctlPath = "/usr/sbin/smartctl"

	probeTimeout     = 10 * time.Second
	commandWaitDelay = 250 * time.Millisecond
	maxProbeOutput   = 256 * 1024
	maxVersionBytes  = 64 * 1024

	// Distinct statuses requested from smartctl's -n standby,STATUS,STATUS2.
	// Both values include bit 0, which smartctl sets only when the command line
	// did not parse. This collector always passes fixed, valid arguments, so a
	// normal SMART result cannot collide with either sentinel; smartctl's manual
	// recommends the same convention (-n standby,3,5). The sentinels are used
	// for ATA probes only.
	standbyExitStatus          = 3
	unsupportedCheckExitStatus = 5

	// smartctl 7.3 added -n POWERMODE,STATUS,STATUS2.
	minATASmartctlMajor = 7
	minATASmartctlMinor = 3
)

type smartctlVersion struct {
	Major int
	Minor int
}

func (v smartctlVersion) supportsStandbyStatus() bool {
	return v.Major > minATASmartctlMajor ||
		v.Major == minATASmartctlMajor && v.Minor >= minATASmartctlMinor
}

type commandResult struct {
	stdout    []byte
	exitCode  int
	timedOut  bool
	truncated bool
	err       error
}

func runCommand(ctx context.Context, path string, args []string, limit int) commandResult {
	var result commandResult
	if err := ctx.Err(); err != nil {
		result.err = err
		return result
	}

	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	output := &cappedBuffer{limit: limit}
	command := exec.CommandContext(probeCtx, path, args...)
	command.Stdout = output
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// A version check plus 32 probes at 10s plus this cleanup stays below the
	// service's 6 minute deadline.
	command.WaitDelay = commandWaitDelay
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return err
	}

	runErr := command.Run()
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		result.exitCode = exitErr.ExitCode()
	} else if runErr != nil {
		result.err = runErr
	}

	if err := ctx.Err(); err != nil {
		result.err = err
	} else if probeCtx.Err() == context.DeadlineExceeded {
		result.timedOut = true
		result.err = nil
	}

	result.stdout = output.data
	result.truncated = output.truncated
	return result
}

type cappedBuffer struct {
	data      []byte
	limit     int
	truncated bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if remaining := b.limit - len(b.data); remaining > 0 {
		if len(p) > remaining {
			b.data = append(b.data, p[:remaining]...)
			b.truncated = true
		} else {
			b.data = append(b.data, p...)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return len(p), nil
}

func checkSmartctlVersion(ctx context.Context) (bool, reason) {
	result := runCommand(ctx, smartctlPath, []string{"--version"}, maxVersionBytes)
	if result.err != nil {
		return false, reasonProbeError
	}
	if result.timedOut {
		return false, reasonTimeout
	}
	if result.truncated || result.exitCode != 0 {
		return false, reasonProbeError
	}
	version, ok := parseSmartctlVersion(result.stdout)
	if !ok {
		return false, reasonProbeError
	}
	if !version.supportsStandbyStatus() {
		return false, reasonUnsupported
	}
	return true, ""
}

func parseSmartctlVersion(data []byte) (smartctlVersion, bool) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "smartctl" {
			continue
		}
		majorText, minorText, ok := strings.Cut(fields[1], ".")
		if !ok {
			return smartctlVersion{}, false
		}
		major, ok := leadingNumber(majorText)
		if !ok {
			return smartctlVersion{}, false
		}
		minor, ok := leadingNumber(minorText)
		if !ok {
			return smartctlVersion{}, false
		}
		return smartctlVersion{Major: major, Minor: minor}, true
	}
	return smartctlVersion{}, false
}

func leadingNumber(value string) (int, bool) {
	index := 0
	for index < len(value) && value[index] >= '0' && value[index] <= '9' {
		index++
	}
	if index == 0 {
		return 0, false
	}
	number, err := strconv.Atoi(value[:index])
	if err != nil {
		return 0, false
	}
	return number, true
}

type smartctlReport struct {
	Device struct {
		Type string `json:"type"`
	} `json:"device"`
	ModelName    string `json:"model_name"`
	SerialNumber string `json:"serial_number"`

	SmartSupport *struct {
		Available *bool `json:"available"`
		Enabled   *bool `json:"enabled"`
	} `json:"smart_support"`

	SmartStatus *struct {
		Passed *bool `json:"passed"`
	} `json:"smart_status"`

	ATASmartAttributes *struct {
		Table []ataAttribute `json:"table"`
	} `json:"ata_smart_attributes"`

	NVMeNamespace *struct {
		ID    *uint32 `json:"id"`
		EUI64 *string `json:"eui64"`
		NGUID *string `json:"nguid"`
	} `json:"nvme_namespace"`

	NVMeSMARTHealth *struct {
		CriticalWarning *int `json:"critical_warning"`
	} `json:"nvme_smart_health_information_log"`
}

type ataAttribute struct {
	Value  *int `json:"value"`
	Thresh *int `json:"thresh"`
	Flags  struct {
		Prefailure *bool `json:"prefailure"`
		Updated    *bool `json:"updated"`
	} `json:"flags"`
}

func parseSmartctlReport(data []byte) (smartctlReport, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	var report smartctlReport
	if err := decoder.Decode(&report); err != nil {
		return smartctlReport{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return smartctlReport{}, errors.New("unexpected trailing JSON")
		}
		return smartctlReport{}, err
	}
	return report, nil
}
