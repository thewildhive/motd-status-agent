package drivehealth

import (
	"strconv"
	"strings"
)

const (
	smartctlExitCommandLine  = 1 << 0
	smartctlExitDeviceOpen   = 1 << 1
	smartctlExitSMARTCommand = 1 << 2
	smartctlExitDiskFailing  = 1 << 3
	smartctlExitPrefailNow   = 1 << 4
)

const (
	nvmeCriticalAvailableSpare     = 0x01
	nvmeCriticalTemperature        = 0x02
	nvmeCriticalReliability        = 0x04
	nvmeCriticalMediaReadOnly      = 0x08
	nvmeCriticalVolatileMemory     = 0x10
	nvmeCriticalPersistentReadOnly = 0x20
	nvmeCriticalWriteProtected     = 0x40

	nvmeCriticalFailureMask = nvmeCriticalAvailableSpare | nvmeCriticalReliability | nvmeCriticalMediaReadOnly
	nvmeCriticalWarningMask = nvmeCriticalTemperature | nvmeCriticalVolatileMemory |
		nvmeCriticalPersistentReadOnly | nvmeCriticalWriteProtected
)

func verifySmartctlIdentity(device resolvedDevice, report smartctlReport) *result {
	switch device.kind {
	case kindATA:
		if report.Device.Type == "" {
			return unknownFailure(reasonInvalidResponse)
		}
		if report.Device.Type != "ata" {
			return unknownFailure(reasonUnsupported)
		}
		model := strings.TrimSpace(report.ModelName)
		serial := strings.TrimSpace(report.SerialNumber)
		if model == "" || serial == "" {
			return unknownFailure(reasonInvalidResponse)
		}
		expected := "ata-" + encodeDevnode(model) + "_" + encodeDevnode(serial)
		if expected != device.drive.ID {
			return unknownFailure(reasonIdentityMismatch)
		}
	case kindNVMe:
		if report.Device.Type == "" {
			return unknownFailure(reasonInvalidResponse)
		}
		if report.Device.Type != "nvme" {
			return unknownFailure(reasonUnsupported)
		}
		serial := strings.TrimSpace(report.SerialNumber)
		if serial == "" {
			return unknownFailure(reasonInvalidResponse)
		}
		if report.NVMeNamespace != nil && report.NVMeNamespace.ID != nil &&
			*report.NVMeNamespace.ID != device.namespaceID {
			return unknownFailure(reasonIdentityMismatch)
		}
		rest := strings.TrimPrefix(device.drive.ID, "nvme-")
		switch {
		case strings.HasPrefix(rest, "eui."), strings.HasPrefix(rest, "nguid."):
			if device.wwid == "" {
				return unknownFailure(reasonProbeError)
			}
			if failure := verifySmartctlNVMeWWID(rest, report); failure != nil {
				return failure
			}
		default:
			namespace := strconv.FormatUint(uint64(device.namespaceID), 10)
			if !strings.HasSuffix(rest, "_"+encodeDevnode(serial)+"_"+namespace) {
				return unknownFailure(reasonIdentityMismatch)
			}
		}
	}
	return nil
}

func verifySmartctlNVMeWWID(rest string, report smartctlReport) *result {
	if report.NVMeNamespace == nil {
		return nil
	}
	var expected, reported string
	switch {
	case strings.HasPrefix(rest, "eui."):
		expected = strings.ToLower(strings.TrimPrefix(rest, "eui."))
		if len(expected) == 32 {
			if report.NVMeNamespace.NGUID != nil {
				reported = *report.NVMeNamespace.NGUID
			}
		} else if report.NVMeNamespace.EUI64 != nil {
			reported = *report.NVMeNamespace.EUI64
		}
	case strings.HasPrefix(rest, "nguid."):
		expected = strings.ToLower(strings.TrimPrefix(rest, "nguid."))
		if report.NVMeNamespace.NGUID != nil {
			reported = *report.NVMeNamespace.NGUID
		}
	}
	if reported == "" {
		return nil
	}
	normalized := normalizeHex(reported)
	if normalized == "" {
		return nil
	}
	if normalized != expected {
		return unknownFailure(reasonIdentityMismatch)
	}
	return nil
}

func normalizeHex(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for index := range len(value) {
		character := value[index]
		switch {
		case character >= '0' && character <= '9', character >= 'a' && character <= 'f':
			builder.WriteByte(character)
		case character >= 'A' && character <= 'F':
			builder.WriteByte(character + 'a' - 'A')
		case character == '-', character == ':', character == ' ', character == '_':
		default:
			return ""
		}
	}
	return builder.String()
}

func classifyATA(report smartctlReport, exitStatus int) result {
	if exitStatus&smartctlExitCommandLine != 0 {
		return unknown(reasonProbeError)
	}
	if exitStatus&smartctlExitDeviceOpen != 0 {
		return unknown(reasonAccessDenied)
	}

	var passed *bool
	if report.SmartStatus != nil {
		passed = report.SmartStatus.Passed
	}
	if passed != nil && !*passed || exitStatus&smartctlExitDiskFailing != 0 {
		return failed(reasonOverallFailed)
	}

	if report.SmartSupport == nil || report.SmartSupport.Available == nil || report.SmartSupport.Enabled == nil {
		return unknown(reasonInvalidResponse)
	}
	if !*report.SmartSupport.Available || !*report.SmartSupport.Enabled {
		return unknown(reasonUnsupported)
	}
	if passed == nil {
		return unknown(reasonInvalidResponse)
	}
	if report.ATASmartAttributes == nil || report.ATASmartAttributes.Table == nil {
		return unknown(reasonInvalidResponse)
	}

	prefailNow, incomplete := currentPrefail(report.ATASmartAttributes.Table)
	if incomplete {
		return unknown(reasonInvalidResponse)
	}
	if prefailNow {
		return warning(reasonATAPrefailNow)
	}
	if exitStatus&smartctlExitPrefailNow != 0 {
		return unknown(reasonInvalidResponse)
	}
	if exitStatus&smartctlExitSMARTCommand != 0 {
		return unknown(reasonInvalidResponse)
	}
	if exitStatus&^0xff != 0 {
		return unknown(reasonProbeError)
	}
	return healthy(reasonPassed)
}

func currentPrefail(table []ataAttribute) (bool, bool) {
	if len(table) == 0 {
		return false, true
	}
	for _, attribute := range table {
		if attribute.Flags.Prefailure == nil || !*attribute.Flags.Prefailure {
			continue
		}
		if attribute.Flags.Updated == nil || !*attribute.Flags.Updated {
			continue
		}
		if attribute.Thresh == nil || *attribute.Thresh <= 0 {
			continue
		}
		if attribute.Value == nil {
			return false, true
		}
		if *attribute.Value <= *attribute.Thresh {
			return true, false
		}
	}
	return false, false
}

func classifyNVMe(report smartctlReport, exitStatus int) result {
	if exitStatus&smartctlExitCommandLine != 0 {
		return unknown(reasonProbeError)
	}
	if exitStatus&smartctlExitDeviceOpen != 0 {
		return unknown(reasonAccessDenied)
	}

	var passed *bool
	if report.SmartStatus != nil {
		passed = report.SmartStatus.Passed
	}
	if passed != nil && !*passed || exitStatus&smartctlExitDiskFailing != 0 {
		return failed(reasonOverallFailed)
	}

	var critical *int
	if report.NVMeSMARTHealth != nil {
		critical = report.NVMeSMARTHealth.CriticalWarning
	}
	if critical == nil {
		return unknown(reasonInvalidResponse)
	}
	if *critical < 0 || *critical > 0xff {
		return unknown(reasonInvalidResponse)
	}
	warningBits := *critical
	if warningBits&nvmeCriticalFailureMask != 0 {
		return failed(reasonNVMeCritical)
	}
	if passed == nil {
		return unknown(reasonInvalidResponse)
	}
	if warningBits&nvmeCriticalWarningMask != 0 {
		return warning(reasonNVMeAdvisory)
	}
	if warningBits != 0 {
		return unknown(reasonProbeError)
	}
	if exitStatus&(smartctlExitSMARTCommand|smartctlExitPrefailNow) != 0 {
		return unknown(reasonInvalidResponse)
	}
	if exitStatus&^0xff != 0 {
		return unknown(reasonProbeError)
	}
	return healthy(reasonPassed)
}
