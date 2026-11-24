package drivehealth

import (
	"context"
	"fmt"
	"io"
	"log"
	"time"
)

type driveState struct {
	drive   driveConfig
	device  *resolvedDevice
	failure *result
}

// Run performs one serial collection and atomically publishes the snapshot.
// Configuration, identity, probe and publication failures are normalized into
// per-drive reason codes; invalid configuration and publication failures are
// returned so the caller exits nonzero without replacing the previous snapshot.
func Run(ctx context.Context, logger *log.Logger) error {
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	configuration, err := loadConfig(configPath)
	if err != nil {
		return err
	}
	if err := configuration.validate(); err != nil {
		return err
	}

	states, err := resolveInventory(configuration.Drives)
	if err != nil {
		return err
	}

	needATA := false
	for _, drive := range states {
		if drive.failure == nil && drive.device != nil && drive.device.kind == kindATA {
			needATA = true
			break
		}
	}

	ataCapable := true
	var ataReason reason
	if needATA {
		if err := ctx.Err(); err != nil {
			return err
		}
		ataCapable, ataReason = checkSmartctlVersion(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ataCapable {
			logger.Printf("ATA probes unavailable: %s", ataReason)
		}
	}

	rows := make([]snapshotDrive, 0, len(states))
	for _, drive := range states {
		if err := ctx.Err(); err != nil {
			return err
		}

		var outcome result
		switch {
		case drive.failure != nil:
			outcome = *drive.failure
		case drive.device == nil:
			outcome = unknown(reasonProbeError)
		case drive.device.kind == kindATA && !ataCapable:
			outcome = unknown(ataReason)
		default:
			outcome = probeDrive(ctx, *drive.device)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := outcome.validate(); err != nil {
			return err
		}
		logger.Printf("drive %q: %s (%s)", drive.drive.Label, outcome.State, outcome.Reason)
		rows = append(rows, snapshotDrive{
			Label:  drive.drive.Label,
			State:  outcome.State,
			Reason: outcome.Reason,
		})
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	return publishSnapshot(time.Now().UTC(), rows)
}

func resolveInventory(drives []driveConfig) ([]driveState, error) {
	states := make([]driveState, len(drives))
	deviceOwners := make(map[uint64]int, len(drives))
	controllerOwners := make(map[string]int, len(drives))

	for index, drive := range drives {
		device, failure := resolveDrive(drive)
		states[index] = driveState{drive: drive, device: device, failure: failure}
		if device == nil {
			continue
		}
		if previous, ok := deviceOwners[device.deviceNumber]; ok {
			return nil, fmt.Errorf("configuration: drives[%d] and drives[%d] resolve to the same whole device", previous, index)
		}
		deviceOwners[device.deviceNumber] = index
		if device.kind == kindNVMe && device.controllerPath != "" {
			if previous, ok := controllerOwners[device.controllerPath]; ok {
				return nil, fmt.Errorf("configuration: drives[%d] and drives[%d] resolve to namespaces of the same NVMe controller", previous, index)
			}
			controllerOwners[device.controllerPath] = index
		}
	}
	return states, nil
}
