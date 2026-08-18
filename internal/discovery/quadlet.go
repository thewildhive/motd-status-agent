package discovery

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"motd-status-agent/internal/protocol"
)

var ErrRuntimeUnavailable = errors.New("podman runtime unavailable")
var ErrPermissionDenied = errors.New("permission denied")

type Collector struct {
	Home       string
	UnitDir    string
	Systemctl  string
	Podman     string
	CommandMax int
}

func (c Collector) Collect(ctx context.Context) ([]protocol.Workload, error) {
	definitions, err := c.definitions()
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath(c.Podman); err != nil {
		return nil, ErrRuntimeUnavailable
	}
	var inspections sync.WaitGroup
	for i := range definitions {
		inspections.Add(1)
		go func(index int) {
			defer inspections.Done()
			members, inspectErr := c.inspect(ctx, definitions[index])
			if inspectErr != nil {
				// Enumeration succeeded, so retain the workload instead of hiding it.
				definitions[index].Members = []Member{{Name: definitions[index].Name, State: protocol.StateUnknown, Health: protocol.HealthUnknown}}
				return
			}
			definitions[index].Members = members
		}(i)
	}
	inspections.Wait()
	result := make([]protocol.Workload, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, Normalize(definition))
	}
	Sort(result)
	return result, nil
}

func (c Collector) definitions() ([]Definition, error) {
	dir := c.UnitDir
	if dir == "" {
		dir = filepath.Join(c.Home, ".config", "containers", "systemd")
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Definition{}, nil
	}
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return nil, ErrPermissionDenied
		}
		return nil, err
	}
	var result []Definition
	var containers []Definition
	pods := make(map[string]struct{})
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		ext := filepath.Ext(entry.Name())
		if ext != ".container" && ext != ".pod" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ext)
		if name == "" || strings.ContainsAny(name, "\r\n") {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(dir, entry.Name()))
		if readErr != nil {
			if errors.Is(readErr, os.ErrPermission) {
				return nil, ErrPermissionDenied
			}
			return nil, readErr
		}
		properties := parseQuadlet(data)
		definition := Definition{Name: name, Kind: strings.TrimPrefix(ext, "."), Unit: name + ".service"}
		if ext == ".pod" {
			definition.Unit = name + "-pod.service"
			pods[name] = struct{}{}
		} else {
			member := Member{Name: name, State: protocol.StateUnknown, Health: protocol.HealthUnknown}
			member.HealthApplicable = properties["HealthCmd"] != ""
			member.OneShot = strings.EqualFold(properties["Type"], "oneshot")
			definition.Members = []Member{member}
			containers = append(containers, definition)
			continue
		}
		result = append(result, definition)
	}
	for _, container := range containers {
		pod := strings.TrimSuffix(propertiesForContainer(container, dir), ".pod")
		if _, included := pods[pod]; included {
			for i := range result {
				if result[i].Name == pod {
					result[i].Members = append(result[i].Members, container.Members...)
				}
			}
			continue
		}
		result = append(result, container)
	}
	seen := make(map[string]struct{}, len(result))
	for _, definition := range result {
		if _, exists := seen[definition.Name]; exists {
			return nil, fmt.Errorf("duplicate Quadlet workload name %q", definition.Name)
		}
		seen[definition.Name] = struct{}{}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func propertiesForContainer(definition Definition, dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, definition.Name+".container"))
	if err != nil {
		return ""
	}
	return parseQuadlet(data)["Pod"]
}

func parseQuadlet(data []byte) map[string]string {
	values := make(map[string]string)
	section := ""
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok && (section == "Container" || section == "Service") {
			values[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return values
}

func (c Collector) inspect(ctx context.Context, definition Definition) ([]Member, error) {
	unit := definition.Unit
	if unit == "" {
		unit = definition.Name + ".service"
		if definition.Kind == "pod" {
			unit = definition.Name + "-pod.service"
		}
	}
	show, err := c.run(ctx, c.Systemctl, "--user", "show", unit, "--property=ActiveState,SubState,Result", "--no-pager")
	if err != nil {
		return nil, err
	}
	values := parseProperties(show)
	unitState := protocol.StateUnknown
	switch {
	case values["Result"] == "failed":
		unitState = protocol.StateFailed
	case values["ActiveState"] == "inactive" && (values["Result"] == "success" || values["Result"] == ""):
		unitState = protocol.StateStopped
	case values["ActiveState"] == "active":
		unitState = protocol.StateRunning
	}
	if unitState == protocol.StateStopped && values["Result"] == "success" {
		for i := range definition.Members {
			if definition.Members[i].OneShot {
				definition.Members[i].OneShotSuccessful = true
			}
		}
	}
	if definition.Kind == "pod" && len(definition.Members) > 0 {
		members := make([]Member, len(definition.Members))
		copy(members, definition.Members)
		var inspections sync.WaitGroup
		for i := range members {
			inspections.Add(1)
			go func(index int) {
				defer inspections.Done()
				members[index] = c.inspectMember(ctx, members[index], unitState)
			}(i)
		}
		inspections.Wait()
		return members, nil
	}
	member := Member{Name: definition.Name, Health: protocol.HealthNone}
	if len(definition.Members) == 1 {
		member = definition.Members[0]
	}
	return []Member{c.inspectMember(ctx, member, unitState)}, nil
}

func (c Collector) inspectMember(ctx context.Context, member Member, unitState protocol.State) Member {
	member.State = unitState
	if unitState != protocol.StateRunning && unitState != protocol.StateUnknown {
		if !member.HealthApplicable {
			member.Health = protocol.HealthNone
		}
		return member
	}
	output, err := c.run(ctx, c.Podman, "inspect", "--type", "container", "--format", "{{.State.Status}}\t{{.State.ExitCode}}\t{{if .State.Health}}{{.State.Health.Status}}{{end}}", member.Name)
	if err != nil {
		member.State = protocol.StateUnknown
		if member.HealthApplicable {
			member.Health = protocol.HealthUnknown
		} else {
			member.Health = protocol.HealthNone
		}
		return member
	}
	fields := strings.Split(strings.TrimSpace(string(output)), "\t")
	if len(fields) == 0 {
		member.State = protocol.StateUnknown
		return member
	}
	switch strings.ToLower(fields[0]) {
	case "running":
		member.State = protocol.StateRunning
	case "created", "configured", "exited", "stopped":
		member.State = protocol.StateStopped
	case "paused", "unknown":
		member.State = protocol.StateUnknown
	default:
		member.State = protocol.StateUnknown
	}
	if member.OneShot && strings.ToLower(fields[0]) == "exited" && len(fields) > 1 && strings.TrimSpace(fields[1]) != "0" {
		member.State = protocol.StateFailed
	}
	if member.OneShot && len(fields) > 1 && fields[0] == "exited" && strings.TrimSpace(fields[1]) == "0" {
		member.OneShotSuccessful = true
	}
	if member.HealthApplicable {
		member.Health = protocol.HealthUnknown
		if len(fields) > 2 {
			switch strings.ToLower(strings.TrimSpace(fields[2])) {
			case "healthy":
				member.Health = protocol.HealthHealthy
			case "unhealthy":
				member.Health = protocol.HealthUnhealthy
			case "starting":
				member.Health = protocol.HealthStarting
			}
		}
	} else {
		member.Health = protocol.HealthNone
	}
	return member
}

func (c Collector) run(ctx context.Context, command string, args ...string) ([]byte, error) {
	if command == "" {
		command = "systemctl"
	}
	cmd := exec.CommandContext(ctx, command, args...)
	max := c.CommandMax
	if max <= 0 {
		max = 256 * 1024
	}
	stdout := &boundedBuffer{limit: max}
	stderr := &boundedBuffer{limit: max}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && strings.Contains(strings.ToLower(stderr.String()), "permission") {
			return nil, ErrPermissionDenied
		}
		return nil, fmt.Errorf("%s: %w", filepath.Base(command), err)
	}
	return stdout.Bytes(), nil
}

type boundedBuffer struct {
	bytes.Buffer
	limit int
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if b.Len()+len(data) > b.limit {
		return 0, io.ErrShortWrite
	}
	return b.Buffer.Write(data)
}

func parseProperties(data []byte) map[string]string {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if ok {
			values[key] = value
		}
	}
	return values
}
