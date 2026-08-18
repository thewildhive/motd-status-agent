package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const DefaultSocket = "/var/run/motd-status/agent.sock"

type Config struct {
	User       string `json:"user"`
	Group      string `json:"group"`
	SocketPath string `json:"socket_path"`
	Linger     bool   `json:"linger"`
}

func (c Config) Normalize() Config {
	if c.SocketPath == "" {
		c.SocketPath = DefaultSocket
	}
	if c.Group == "" {
		c.Group = "motd-status"
	}
	return c
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var value Config
	if err := json.Unmarshal(data, &value); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	return value.Normalize(), nil
}

func Save(path string, value Config) error {
	value = value.Normalize()
	if !filepath.IsAbs(path) {
		return fmt.Errorf("config path must be absolute")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".motd-status-config-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}
