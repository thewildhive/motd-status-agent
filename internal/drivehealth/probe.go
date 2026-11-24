package drivehealth

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strconv"
)

func probeDrive(ctx context.Context, device resolvedDevice) result {
	args := []string{"-j", "-i", "-H", "-A", "-d", string(device.kind)}
	if device.kind == kindATA {
		args = append(args, "-n",
			"standby,"+strconv.Itoa(standbyExitStatus)+","+strconv.Itoa(unsupportedCheckExitStatus))
	}
	args = append(args, device.devPath)

	command := runCommand(ctx, smartctlPath, args, maxProbeOutput)
	if command.err != nil {
		return unknown(reasonProbeError)
	}
	if command.timedOut {
		return unknown(reasonTimeout)
	}
	if command.truncated {
		return unknown(reasonInvalidResponse)
	}
	if device.kind == kindATA {
		// These sentinels are requested only for ATA via -n standby,3,5.
		if command.exitCode == standbyExitStatus {
			return unknown(reasonSleeping)
		}
		if command.exitCode == unsupportedCheckExitStatus {
			return unknown(reasonUnsupported)
		}
	}
	if command.exitCode&smartctlExitCommandLine != 0 {
		return unknown(reasonProbeError)
	}
	if command.exitCode&smartctlExitDeviceOpen != 0 {
		if _, err := os.Stat(device.devPath); errors.Is(err, fs.ErrNotExist) {
			return unknown(reasonMissing)
		}
		return unknown(reasonAccessDenied)
	}

	report, err := parseSmartctlReport(command.stdout)
	if err != nil {
		return unknown(reasonInvalidResponse)
	}
	if failure := verifySmartctlIdentity(device, report); failure != nil {
		return *failure
	}
	switch device.kind {
	case kindATA:
		return classifyATA(report, command.exitCode)
	case kindNVMe:
		return classifyNVMe(report, command.exitCode)
	default:
		return unknown(reasonUnsupported)
	}
}
