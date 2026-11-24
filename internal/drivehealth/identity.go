package drivehealth

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	byIDDir          = "/dev/disk/by-id"
	sysClassBlockDir = "/sys/class/block"
	sysClassNVMeDir  = "/sys/class/nvme"
	sysClassATAPortDir = "/sys/class/ata_port"
	devDir           = "/dev"
	maxSysfsBytes    = 4096
)

type resolvedDevice struct {
	drive          driveConfig
	blockName      string
	deviceNumber   uint64
	devPath        string
	kind           deviceKind
	controllerPath string
	namespaceID    uint32
	wwid           string
}

func resolveDrive(drive driveConfig) (*resolvedDevice, *result) {
	device := &resolvedDevice{drive: drive, kind: drive.Kind}
	name, number, failure := resolveBlockName(drive.ID)
	if failure != nil {
		return nil, failure
	}
	device.blockName = name
	device.deviceNumber = number
	device.devPath = filepath.Join(devDir, name)
	if failure := verifyWholeDisk(name); failure != nil {
		return device, failure
	}

	switch drive.Kind {
	case kindATA:
		failure = verifyATATopology(device)
	case kindNVMe:
		failure = verifyNVMeTopology(device)
	default:
		failure = unknownFailure(reasonUnsupported)
	}
	return device, failure
}

func resolveBlockName(id string) (string, uint64, *result) {
	target, err := filepath.EvalSymlinks(filepath.Join(byIDDir, id))
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return "", 0, unknownFailure(reasonMissing)
		case errors.Is(err, fs.ErrPermission):
			return "", 0, unknownFailure(reasonAccessDenied)
		default:
			return "", 0, unknownFailure(reasonProbeError)
		}
	}
	if filepath.Dir(target) != devDir {
		return "", 0, unknownFailure(reasonUnsupported)
	}
	info, err := os.Stat(target)
	if err != nil {
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return "", 0, unknownFailure(reasonMissing)
		case errors.Is(err, fs.ErrPermission):
			return "", 0, unknownFailure(reasonAccessDenied)
		default:
			return "", 0, unknownFailure(reasonProbeError)
		}
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return "", 0, unknownFailure(reasonUnsupported)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", 0, unknownFailure(reasonProbeError)
	}
	name := filepath.Base(target)
	if name == "" || name == "." || name == ".." {
		return "", 0, unknownFailure(reasonUnsupported)
	}
	return name, uint64(stat.Rdev), nil
}

func verifyWholeDisk(name string) *result {
	if _, err := os.Lstat(filepath.Join(sysClassBlockDir, name)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return unknownFailure(reasonUnsupported)
		}
		return unknownFailure(reasonProbeError)
	}
	if _, err := os.Lstat(filepath.Join(sysClassBlockDir, name, "partition")); err == nil {
		return unknownFailure(reasonUnsupported)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unknownFailure(reasonProbeError)
	}
	return nil
}

// verifyATATopology checks that the block device belongs to a native libata
// ATA port. It deliberately does not read the SCSI model or serial attributes:
// libata truncates the SCSI model and often omits the serial, while the ATA
// JSON from smartctl carries the full stable identity.
func verifyATATopology(device *resolvedDevice) *result {
	scsiDevice, err := filepath.EvalSymlinks(filepath.Join(sysClassBlockDir, device.blockName, "device"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return unknownFailure(reasonUnsupported)
		}
		return unknownFailure(reasonProbeError)
	}
	if !hasLibataPort(scsiDevice) && !hasLibataDriver(scsiDevice) {
		return unknownFailure(reasonUnsupported)
	}
	vendor, err := readSysfs(filepath.Join(scsiDevice, "vendor"))
	if err == nil {
		if !strings.EqualFold(strings.TrimSpace(vendor), "ATA") {
			return unknownFailure(reasonUnsupported)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return unknownFailure(reasonProbeError)
	}
	return nil
}

func hasLibataPort(scsiDevice string) bool {
	path := scsiDevice
	for range 6 {
		name := filepath.Base(path)
		if isATAPortName(name) {
			// The class entry represents a port child; its device is the ATA ancestor.
			portClass, err := filepath.EvalSymlinks(filepath.Join(sysClassATAPortDir, name, "device"))
			if err == nil && portClass == path {
				return true
			}
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return false
}

func isATAPortName(name string) bool {
	rest := strings.TrimPrefix(name, "ata")
	if rest == "" || len(rest) == len(name) {
		return false
	}
	for index := range len(rest) {
		if rest[index] < '0' || rest[index] > '9' {
			return false
		}
	}
	return true
}

func hasLibataDriver(path string) bool {
	for range 6 {
		if driver, err := filepath.EvalSymlinks(filepath.Join(path, "driver")); err == nil &&
			filepath.Base(driver) == "libata" {
			return true
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return false
}

// verifyNVMeTopology derives the controller from the block-class symlink.
// For native NVMe, /sys/class/block/nvmeXnY resolves to
// .../nvme/nvmeX/nvmeXnY, so the parent is the controller. Multipath
// namespace heads live under nvme-subsys and are not a single native
// controller, so they are unsupported here.
func verifyNVMeTopology(device *resolvedDevice) *result {
	if isNVMePathDevice(device.blockName) {
		return unknownFailure(reasonUnsupported)
	}
	classPath, err := filepath.EvalSymlinks(filepath.Join(sysClassBlockDir, device.blockName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return unknownFailure(reasonUnsupported)
		}
		return unknownFailure(reasonProbeError)
	}
	if filepath.Base(classPath) != device.blockName {
		return unknownFailure(reasonUnsupported)
	}

	controllerPath, ok := nvmeControllerPath(classPath)
	if !ok {
		return unknownFailure(reasonUnsupported)
	}

	deviceLink, err := filepath.EvalSymlinks(filepath.Join(sysClassBlockDir, device.blockName, "device"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return unknownFailure(reasonUnsupported)
		}
		return unknownFailure(reasonProbeError)
	}
	if deviceLink != controllerPath && !strings.HasPrefix(deviceLink, controllerPath+string(filepath.Separator)) {
		return unknownFailure(reasonUnsupported)
	}
	device.controllerPath = controllerPath

	namespaceID, err := readNamespaceID(device.blockName, classPath)
	if err != nil {
		return unknownFailure(reasonProbeError)
	}
	device.namespaceID = namespaceID

	rest := strings.TrimPrefix(device.drive.ID, "nvme-")
	switch {
	case strings.HasPrefix(rest, "eui."), strings.HasPrefix(rest, "nguid."):
		wwid, err := readFirstSysfs(
			filepath.Join(sysClassBlockDir, device.blockName, "wwid"),
			filepath.Join(classPath, "wwid"),
		)
		if err != nil {
			return unknownFailure(reasonProbeError)
		}
		wwid = strings.TrimSpace(wwid)
		if wwid == "" {
			return unknownFailure(reasonProbeError)
		}
		if "nvme-"+wwid != device.drive.ID {
			return unknownFailure(reasonIdentityMismatch)
		}
		device.wwid = wwid
	default:
		// Verify the namespace selector before any probe; the serial part is
		// verified from smartctl's full identity after the safe probe.
		separator := strings.LastIndexByte(rest, '_')
		configuredNamespace, err := strconv.ParseUint(rest[separator+1:], 10, 32)
		if err != nil || uint32(configuredNamespace) != namespaceID {
			return unknownFailure(reasonIdentityMismatch)
		}
	}
	return nil
}

func isNVMePathDevice(name string) bool {
	rest := strings.TrimPrefix(name, "nvme")
	index := 0
	for index < len(rest) && rest[index] >= '0' && rest[index] <= '9' {
		index++
	}
	return index < len(rest) && rest[index] == 'c'
}

func nvmeControllerPath(classPath string) (string, bool) {
	path := filepath.Dir(classPath)
	for range 3 {
		name := filepath.Base(path)
		if strings.HasPrefix(name, "nvme") && !strings.HasPrefix(name, "nvme-subsys") {
			controllerClass, err := filepath.EvalSymlinks(filepath.Join(sysClassNVMeDir, name))
			if err == nil && controllerClass == path {
				return path, true
			}
		}
		parent := filepath.Dir(path)
		if parent == path {
			break
		}
		path = parent
	}
	return "", false
}

func readNamespaceID(blockName, classPath string) (uint32, error) {
	raw, err := readFirstSysfs(
		filepath.Join(sysClassBlockDir, blockName, "nsid"),
		filepath.Join(classPath, "nsid"),
	)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 32)
	if err != nil || value == 0 {
		return 0, errors.New("invalid namespace id")
	}
	return uint32(value), nil
}

func readFirstSysfs(paths ...string) (string, error) {
	var lastErr error
	for _, path := range paths {
		value, err := readSysfs(path)
		if err == nil {
			return value, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fs.ErrNotExist
	}
	return "", lastErr
}

func readSysfs(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSysfsBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxSysfsBytes {
		return "", errors.New("sysfs attribute is too large")
	}
	return strings.TrimRight(string(data), "\x00\n"), nil
}

// encodeDevnode mirrors the udev encoding used for /dev/disk/by-id names:
// whitespace runs are collapsed to one underscore, then bytes outside the
// device-node allow list are replaced with an underscore.
func encodeDevnode(value string) string {
	encoded := []byte(strings.Join(strings.Fields(value), "_"))
	for index := range len(encoded) {
		if !isAllowedDevnodeByte(encoded[index]) {
			encoded[index] = '_'
		}
	}
	return string(encoded)
}

func isAllowedDevnodeByte(value byte) bool {
	switch {
	case value >= '0' && value <= '9':
		return true
	case value >= 'A' && value <= 'Z':
		return true
	case value >= 'a' && value <= 'z':
		return true
	}
	return strings.ContainsRune("#+-.:=@_", rune(value))
}

func unknownFailure(value reason) *result {
	failure := unknown(value)
	return &failure
}
