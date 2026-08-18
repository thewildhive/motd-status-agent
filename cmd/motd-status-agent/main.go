package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"motd-status-agent/internal/config"
	"motd-status-agent/internal/discovery"
	"motd-status-agent/internal/server"
)

const (
	serviceName   = "motd-status-agent.service"
	defaultConfig = ".config/motd-status-agent/config.json"
	systemUnitDir = "/etc/systemd/system"
	tmpfilesDir   = "/etc/tmpfiles.d"
)

var VERSION = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "check":
		err = check(os.Args[2:])
	case "install":
		err = install(os.Args[2:])
	case "uninstall":
		err = uninstall(os.Args[2:])
	case "version":
		fmt.Println(displayVersion())
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: motd-status-agent {serve|check|install|uninstall|version} [flags]")
}

func displayVersion() string {
	if VERSION == "dev" || strings.HasPrefix(VERSION, "v") {
		return VERSION
	}
	return "v" + VERSION
}

func serve(args []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	configPath := flags.String("config", configPath(), "configuration file")
	workloadUser := flags.String("user", currentUsername(), "workload-owning user")
	if err := flags.Parse(args); err != nil {
		return err
	}
	settings, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	current, err := user.Current()
	if err != nil {
		return err
	}
	configuredUser := *workloadUser
	if settings.User != "" {
		configuredUser = settings.User
	}
	if configuredUser != current.Username {
		return fmt.Errorf("configured workload user %q does not match current user %q", configuredUser, current.Username)
	}
	if current.Uid == "0" {
		return errors.New("serve must run as the unprivileged workload user, not root")
	}
	collector := discovery.Collector{Home: current.HomeDir, Systemctl: "systemctl", Podman: "podman", CommandMax: 256 * 1024}
	service := &server.Service{SocketPath: settings.SocketPath, Group: settings.Group, Collector: collector, Logger: log.New(os.Stderr, "motd-status-agent: ", log.LstdFlags|log.LUTC)}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return service.Serve(ctx)
}

func check(args []string) error {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	configPath := flags.String("config", configPath(), "configuration file")
	workloadUser := flags.String("user", "", "workload-owning user; inferred from config when omitted")
	if err := flags.Parse(args); err != nil {
		return err
	}
	settings, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if !filepath.IsAbs(settings.SocketPath) {
		return errors.New("socket path must be absolute")
	}
	if *workloadUser != "" && settings.User != "" && *workloadUser != settings.User {
		return fmt.Errorf("requested user %q does not match configured user %q", *workloadUser, settings.User)
	}
	if _, err := exec.LookPath("podman"); err != nil {
		return errors.New("podman is not available in PATH")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl is not available in PATH")
	}
	fmt.Printf("configuration is valid: socket=%s group=%s linger=%t\n", settings.SocketPath, settings.Group, settings.Linger)
	return nil
}

func install(args []string) error {
	flags := flag.NewFlagSet("install", flag.ContinueOnError)
	configPath := flags.String("config", configPath(), "configuration file")
	workloadUser := flags.String("user", currentUsername(), "workload-owning user")
	group := flags.String("group", "motd-status", "local status access group")
	socketPath := flags.String("socket", config.DefaultSocket, "absolute Unix socket path")
	linger := flags.Bool("linger", true, "keep user manager active after logout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if os.Getuid() != 0 {
		return errors.New("install must run as root to manage the system service")
	}
	if !filepath.IsAbs(*socketPath) {
		return errors.New("socket path must be absolute")
	}
	target, err := lookupWorkloadUser(*workloadUser)
	if err != nil {
		return err
	}
	targetUID, err := numericUID(target)
	if err != nil {
		return err
	}
	if targetUID == 0 {
		return errors.New("the workload user must not be root")
	}
	accessGroup, err := user.LookupGroup(*group)
	if err != nil {
		return fmt.Errorf("lookup access group %q: %w", *group, err)
	}
	groupGID, err := strconv.Atoi(accessGroup.Gid)
	if err != nil {
		return fmt.Errorf("invalid gid for access group %q: %w", *group, err)
	}
	if err := prepareSocketDirectory(*socketPath, targetUID, groupGID); err != nil {
		return err
	}
	settings := config.Config{User: target.Name, Group: *group, SocketPath: *socketPath, Linger: *linger}.Normalize()
	if _, err := exec.LookPath("podman"); err != nil {
		return errors.New("podman is not available in PATH")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return errors.New("systemctl is not available in PATH")
	}
	if *linger {
		if err := ensureLinger(target.Name); err != nil {
			return fmt.Errorf("ensure linger for %s: %w", target.Name, err)
		}
	}
	if err := checkUserRuntime(target); err != nil {
		return errors.New("user systemd manager is unavailable")
	}
	targetConfigPath := *configPath
	if targetConfigPath == configPathForHome(os.Getenv("HOME")) || targetConfigPath == "" {
		targetConfigPath = configPathForHome(target.HomeDir)
	}
	if !filepath.IsAbs(targetConfigPath) {
		return errors.New("config path must be absolute")
	}
	if err := config.Save(targetConfigPath, settings); err != nil {
		return fmt.Errorf("save configuration: %w", err)
	}
	if err := chownTargetPath(targetConfigPath, target); err != nil {
		_ = os.Remove(targetConfigPath)
		return err
	}
	if err := chownTargetPath(filepath.Dir(targetConfigPath), target); err != nil {
		_ = os.Remove(targetConfigPath)
		return err
	}
	if err := writeServiceFile(target, *group, targetConfigPath); err != nil {
		_ = os.Remove(targetConfigPath)
		return err
	}
	if err := writeTmpfilesFile(target, *group); err != nil {
		_ = os.Remove(targetConfigPath)
		return err
	}
	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := runCommand("systemd-tmpfiles", "--create", tmpfilesPath()); err != nil {
		return err
	}
	if err := runCommand("systemctl", "enable", "--now", serviceName); err != nil {
		_ = os.Remove(targetConfigPath)
		return err
	}
	fmt.Printf("installed and started %s for %s at %s\n", serviceName, target.Name, settings.SocketPath)
	return nil
}

func uninstall(args []string) error {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	configPath := flags.String("config", configPath(), "configuration file")
	workloadUser := flags.String("user", "", "workload-owning user; inferred from config when omitted")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if os.Getuid() != 0 {
		return errors.New("uninstall must run as root to manage the system service")
	}
	settings, loadErr := config.Load(*configPath)
	if loadErr != nil && !errors.Is(loadErr, os.ErrNotExist) {
		return fmt.Errorf("load configuration: %w", loadErr)
	}
	targetName := currentUsername()
	if *workloadUser != "" {
		targetName = *workloadUser
	}
	if settings.User != "" {
		targetName = settings.User
	}
	target, err := lookupWorkloadUser(targetName)
	if err != nil {
		return err
	}
	if systemUnitInstalled() {
		if err := runCommand("systemctl", "disable", "--now", serviceName); err != nil {
			return err
		}
	}
	if err := os.Remove(servicePath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Remove(tmpfilesPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	targetConfigPath := *configPath
	if targetConfigPath == configPathForHome(os.Getenv("HOME")) || targetConfigPath == "" {
		targetConfigPath = configPathForHome(target.HomeDir)
	}
	if err := os.Remove(targetConfigPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := runCommand("systemctl", "daemon-reload"); err != nil {
		return err
	}
	fmt.Println("agent service removed; Quadlets, Podman data, group membership, and linger were preserved")
	return nil
}

func writeServiceFile(target *user.User, group, configPath string) error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	uid, err := numericUID(target)
	if err != nil {
		return err
	}
	contents := renderServiceUnit(target, group, uid, executable, configPath)
	if err := os.MkdirAll(systemUnitDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(servicePath(), []byte(contents), 0o644); err != nil {
		return err
	}
	return nil
}

func writeTmpfilesFile(target *user.User, group string) error {
	if err := os.MkdirAll(tmpfilesDir, 0o755); err != nil {
		return err
	}
	contents := renderTmpfilesRule(target.Username, group)
	return os.WriteFile(tmpfilesPath(), []byte(contents), 0o644)
}

func renderTmpfilesRule(username, group string) string {
	return fmt.Sprintf("d /run/motd-status 02750 %s %s -\n", username, group)
}

func renderServiceUnit(target *user.User, group string, uid int, executable, configPath string) string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Rootless Podman MOTD status agent",
		"Requires=user@" + strconv.Itoa(uid) + ".service",
		"After=user@" + strconv.Itoa(uid) + ".service",
		"",
		"[Service]",
		"Type=simple",
		"User=" + target.Username,
		"Group=" + group,
		"SupplementaryGroups=" + target.Gid,
		"Environment=HOME=" + systemdValue(target.HomeDir),
		"Environment=XDG_RUNTIME_DIR=/run/user/" + strconv.Itoa(uid),
		"Environment=DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/" + strconv.Itoa(uid) + "/bus",
		"ExecStart=" + systemdValue(executable) + " serve --config " + systemdValue(configPath) + " --user " + systemdValue(target.Username),
		"Restart=on-failure",
		"RestartSec=2s",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func systemdValue(value string) string {
	if value != "" && strings.IndexFunc(value, func(r rune) bool {
		return strings.ContainsRune(" \t\r\n\"'\\;#", r)
	}) == -1 {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(value) + `"`
}

func servicePath() string { return filepath.Join(systemUnitDir, serviceName) }

func tmpfilesPath() string { return filepath.Join(tmpfilesDir, "motd-status-agent.conf") }

func systemUnitInstalled() bool {
	_, err := os.Stat(servicePath())
	return err == nil
}

func ensureLinger(username string) error {
	output, err := exec.Command("loginctl", "show-user", username, "-p", "Linger", "--value").Output()
	if err == nil && strings.TrimSpace(string(output)) == "yes" {
		return nil
	}
	return runLoginctl("enable-linger", username)
}

func checkUserRuntime(target *user.User) error {
	uid, err := numericUID(target)
	if err != nil {
		return err
	}
	if _, err := exec.LookPath("runuser"); err != nil {
		return errors.New("runuser is not available")
	}
	return runCommand("runuser", "-u", target.Username, "--", "env",
		"HOME="+target.HomeDir,
		"XDG_RUNTIME_DIR=/run/user/"+strconv.Itoa(uid),
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/"+strconv.Itoa(uid)+"/bus",
		"systemctl", "--user", "show-environment")
}

func runLoginctl(args ...string) error {
	return runCommand("loginctl", args...)
}

func runCommand(command string, args ...string) error {
	return runCommandWithEnv(command, os.Environ(), args...)
}

func runCommandWithEnv(command string, environment []string, args ...string) error {
	commandProcess := exec.Command(command, args...)
	commandProcess.Env = environment
	commandProcess.Stdout, commandProcess.Stderr = os.Stdout, os.Stderr
	if err := commandProcess.Run(); err != nil {
		return fmt.Errorf("systemctl %s: %w", args, err)
	}
	return nil
}

func configPath() string { return configPathForHome(os.Getenv("HOME")) }

func configPathForHome(home string) string { return filepath.Join(home, defaultConfig) }

func lookupWorkloadUser(name string) (*user.User, error) {
	if name == "" {
		return nil, errors.New("workload user must not be empty")
	}
	target, err := user.Lookup(name)
	if err != nil {
		return nil, fmt.Errorf("lookup workload user %q: %w", name, err)
	}
	return target, nil
}

func numericUID(target *user.User) (int, error) {
	uid, err := strconv.Atoi(target.Uid)
	if err != nil {
		return 0, fmt.Errorf("invalid uid for workload user %q: %w", target.Username, err)
	}
	return uid, nil
}

func prepareSocketDirectory(socketPath string, uid, gid int) error {
	parent := filepath.Dir(socketPath)
	switch filepath.Clean(parent) {
	case "/", "/var", "/var/run", "/run", "/tmp":
		return fmt.Errorf("socket path must include a dedicated parent directory: %s", parent)
	}
	if err := os.MkdirAll(parent, 0o750); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect socket directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("socket parent is not a directory: %s", parent)
	}
	if err := os.Chown(parent, uid, gid); err != nil {
		return fmt.Errorf("set socket directory ownership: %w", err)
	}
	if err := os.Chmod(parent, 0o2750); err != nil {
		return fmt.Errorf("set socket directory mode: %w", err)
	}
	return nil
}

func chownTargetPath(path string, target *user.User) error {
	uid, err := numericUID(target)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(target.Gid)
	if err != nil {
		return fmt.Errorf("invalid primary gid for workload user %q: %w", target.Username, err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("set ownership for %s: %w", path, err)
	}
	return nil
}

func currentUsername() string {
	current, err := user.Current()
	if err != nil {
		return strconv.Itoa(os.Getuid())
	}
	return current.Username
}
