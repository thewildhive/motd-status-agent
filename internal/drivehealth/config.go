// Package drivehealth collects the daily drive health snapshot published for
// go-motd. It owns smartctl probing, identity verification, classification and
// atomic snapshot publication.
//
// The configuration file is /etc/motd-drive-health/config.json:
//
//	{
//	  "drives": [
//	    {"id": "ata-WDC_WD20EFRX-68EUZN0_WD-WCC4M1234567", "label": "Archive", "kind": "ata"},
//	    {"id": "nvme-Samsung_SSD_970_EVO_1TB_S4EWNX0M123456_1", "label": "Cache", "kind": "nvme"}
//	  ]
//	}
//
// id is the exact basename of a whole-disk link in /dev/disk/by-id. Only these
// schemes are accepted, and kind must agree with the scheme:
//
//   - ata-<model>_<serial> with kind "ata": a native libata ATA disk. The
//     serial suffix is the verified identity.
//   - nvme-<model>_<serial>_<namespace> with kind "nvme": a native NVMe
//     namespace. The serial and namespace id are the verified identity.
//   - nvme-eui.<16 or 32 hex digits> or nvme-nguid.<32 hex digits> with kind
//     "nvme": verified against the namespace wwid exposed by sysfs.
//
// Partition selectors, scsi-/wwn-/virtio-/usb- links, the nvme-uuid.* scheme,
// the nvme-nvme.* fallback and the obsolete namespace-less nvme serial form are
// rejected. Identifiers must be single printable ASCII by-id basenames with no
// path separator. Labels are 1-32 printable ASCII bytes (0x20-0x7e) with no
// leading or trailing space.
//
// The collector invokes the fixed path /usr/sbin/smartctl. ATA probing needs
// smartctl 7.3 or newer for -n standby,STATUS,STATUS2; an older or unreadable
// tool leaves ATA rows unknown without opening the drive.
package drivehealth

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
)

const (
	configPath    = "/etc/motd-drive-health/config.json"
	maxConfigSize = 64 * 1024
	maxIDLength   = 255
	maxLabelSize  = 32
	maxDrives     = 32
)

type deviceKind string

const (
	kindATA  deviceKind = "ata"
	kindNVMe deviceKind = "nvme"
)

type config struct {
	Drives []driveConfig `json:"drives"`
}

type driveConfig struct {
	ID    string     `json:"id"`
	Label string     `json:"label"`
	Kind  deviceKind `json:"kind"`
}

func loadConfig(path string) (config, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return config{}, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return config{}, fmt.Errorf("stat configuration: %w", err)
	}
	if !info.Mode().IsRegular() {
		return config{}, errors.New("configuration must be a regular file")
	}
	if info.Size() > maxConfigSize {
		return config{}, fmt.Errorf("configuration exceeds %d bytes", maxConfigSize)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return config{}, errors.New("configuration ownership is unavailable")
	}
	if stat.Uid != 0 {
		return config{}, errors.New("configuration must be owned by root")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return config{}, errors.New("configuration must not be writable by group or others")
	}

	data, err := io.ReadAll(io.LimitReader(file, maxConfigSize+1))
	if err != nil {
		return config{}, fmt.Errorf("read configuration: %w", err)
	}
	if len(data) > maxConfigSize {
		return config{}, fmt.Errorf("configuration exceeds %d bytes", maxConfigSize)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value config
	if err := decoder.Decode(&value); err != nil {
		return config{}, fmt.Errorf("parse configuration: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return config{}, errors.New("parse configuration: unexpected trailing data")
		}
		return config{}, fmt.Errorf("parse configuration: %w", err)
	}
	return value, nil
}

func (c config) validate() error {
	if len(c.Drives) == 0 {
		return errors.New("configuration: drives must not be empty")
	}
	if len(c.Drives) > maxDrives {
		return fmt.Errorf("configuration: at most %d drives may be configured", maxDrives)
	}

	ids := make(map[string]int, len(c.Drives))
	labels := make(map[string]int, len(c.Drives))
	for index, drive := range c.Drives {
		if err := validateLabel(drive.Label); err != nil {
			return fmt.Errorf("configuration: drives[%d]: %w", index, err)
		}
		if err := validateID(drive.ID, drive.Kind); err != nil {
			return fmt.Errorf("configuration: drives[%d]: %w", index, err)
		}
		if previous, ok := ids[drive.ID]; ok {
			return fmt.Errorf("configuration: drives[%d] and drives[%d] share an identifier", previous, index)
		}
		ids[drive.ID] = index
		if previous, ok := labels[drive.Label]; ok {
			return fmt.Errorf("configuration: drives[%d] and drives[%d] share label %q", previous, index, drive.Label)
		}
		labels[drive.Label] = index
	}
	return nil
}

func validateLabel(label string) error {
	if len(label) == 0 || len(label) > maxLabelSize {
		return errors.New("label must be 1-32 printable ASCII characters")
	}
	for i := range len(label) {
		if label[i] < 0x20 || label[i] > 0x7e {
			return errors.New("label must be 1-32 printable ASCII characters")
		}
	}
	if label[0] == ' ' || label[len(label)-1] == ' ' {
		return errors.New("label must not begin or end with a space")
	}
	return nil
}

func validateID(id string, kind deviceKind) error {
	if len(id) == 0 || len(id) > maxIDLength {
		return errors.New("id must be a nonempty /dev/disk/by-id basename")
	}
	for i := range len(id) {
		if id[i] < 0x21 || id[i] > 0x7e || id[i] == '/' {
			return errors.New("id must contain only printable ASCII without a path separator")
		}
	}
	if id == "." || id == ".." || hasPartitionSelector(id) {
		return errors.New("id must select a whole drive, not a partition")
	}

	switch kind {
	case kindATA:
		if !strings.HasPrefix(id, "ata-") {
			return errors.New("ATA id must use the ata-<model>_<serial> by-id scheme")
		}
		return validateATARest(strings.TrimPrefix(id, "ata-"))
	case kindNVMe:
		if !strings.HasPrefix(id, "nvme-") {
			return errors.New("NVMe id must use an nvme-* by-id scheme")
		}
		return validateNVMeRest(strings.TrimPrefix(id, "nvme-"))
	default:
		return errors.New(`kind must be "ata" or "nvme"`)
	}
}

func validateATARest(rest string) error {
	separator := strings.LastIndexByte(rest, '_')
	if separator <= 0 || separator == len(rest)-1 {
		return errors.New("ATA id must include model and serial separated by an underscore")
	}
	return nil
}

func validateNVMeRest(rest string) error {
	switch {
	case strings.HasPrefix(rest, "eui."):
		value := strings.TrimPrefix(rest, "eui.")
		if (len(value) != 16 && len(value) != 32) || !isHex(value) {
			return errors.New("NVMe eui id must contain 16 or 32 hexadecimal digits")
		}
		return nil
	case strings.HasPrefix(rest, "nguid."):
		value := strings.TrimPrefix(rest, "nguid.")
		if len(value) != 32 || !isHex(value) {
			return errors.New("NVMe nguid id must contain 32 hexadecimal digits")
		}
		return nil
	case strings.HasPrefix(rest, "uuid."):
		return errors.New("NVMe uuid.* identifiers are not supported; use an eui, nguid or serial_namespace id")
	case strings.HasPrefix(rest, "nvme."):
		return errors.New("NVMe nvme.* fallback identifiers are not supported")
	}

	separator := strings.LastIndexByte(rest, '_')
	if separator <= 0 || separator == len(rest)-1 {
		return errors.New("NVMe id must include model, serial and a namespace separated by underscores")
	}
	namespace := rest[separator+1:]
	if !isDecimal(namespace) {
		return errors.New("NVMe id must end with a decimal namespace")
	}
	value, err := strconv.ParseUint(namespace, 10, 32)
	if err != nil || value == 0 || strconv.FormatUint(value, 10) != namespace {
		return errors.New("NVMe namespace must be a canonical nonzero 32-bit decimal")
	}
	modelAndSerial := rest[:separator]
	serialSeparator := strings.LastIndexByte(modelAndSerial, '_')
	if serialSeparator <= 0 || serialSeparator == len(modelAndSerial)-1 {
		return errors.New("NVMe id must include model and serial before the namespace")
	}
	return nil
}

func hasPartitionSelector(id string) bool {
	index := strings.LastIndex(id, "-part")
	if index < 0 || index+len("-part") == len(id) {
		return false
	}
	return isDecimal(id[index+len("-part"):])
}

func isDecimal(value string) bool {
	if value == "" {
		return false
	}
	for i := range len(value) {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}

func isHex(value string) bool {
	if value == "" {
		return false
	}
	for i := range len(value) {
		c := value[i]
		if c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' {
			continue
		}
		return false
	}
	return true
}
