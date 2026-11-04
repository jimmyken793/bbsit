package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen        string `yaml:"listen"`          // e.g. "0.0.0.0:9090"
	DBPath        string `yaml:"db_path"`         // e.g. "/opt/bbsit/state.db"
	SecretKeyPath string `yaml:"secret_key_path"` // AES-GCM master key; must be backed up securely
	StackRoot     string `yaml:"stack_root"`      // e.g. "/opt/stacks"
	LogLevel      string `yaml:"log_level"`       // debug | info | warn | error
	Runtime       string `yaml:"runtime"`         // "docker", "podman", or "" (auto-detect)

	// Admin Unix socket for bbsit-ctl. Empty AdminSocket disables it.
	AdminSocket string `yaml:"admin_socket"` // e.g. "/run/bbsit/admin.sock"
	AdminGroup  string `yaml:"admin_group"`  // group that may use the socket; empty = root only
}

// ResolvedRuntime returns the container runtime binary name.
// If Runtime is set explicitly, it validates that the binary exists on PATH.
// Otherwise it auto-detects by checking for docker, then podman.
func (c *Config) ResolvedRuntime() (string, error) {
	if c.Runtime != "" {
		if _, err := exec.LookPath(c.Runtime); err != nil {
			return "", fmt.Errorf("configured runtime %q not found on PATH", c.Runtime)
		}
		return c.Runtime, nil
	}
	for _, rt := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(rt); err == nil {
			return rt, nil
		}
	}
	return "", fmt.Errorf("no container runtime found: install docker or podman")
}

func DefaultConfig() *Config {
	return &Config{
		Listen:        "0.0.0.0:9090",
		DBPath:        "/opt/bbsit/state.db",
		SecretKeyPath: "/opt/bbsit/secret.key",
		StackRoot:     "/opt/stacks",
		LogLevel:      "info",
		AdminSocket:   "/run/bbsit/admin.sock",
	}
}

func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := cfg.Validate(); err != nil {
				return nil, fmt.Errorf("no bbsit config found at %s and defaults are invalid: %w", path, err)
			}
			return cfg, nil
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	var present struct {
		SecretKeyPath *string `yaml:"secret_key_path"`
	}
	if err := yaml.Unmarshal(data, &present); err != nil {
		return nil, err
	}
	if present.SecretKeyPath == nil {
		// Existing configs predate secret_key_path. Keep local/dev databases
		// self-contained instead of inheriting the production default path.
		cfg.SecretKeyPath = filepath.Join(filepath.Dir(cfg.DBPath), "secret.key")
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.DBPath == "" {
		return fmt.Errorf("bbsit config: db_path must not be empty")
	}
	if c.SecretKeyPath == "" {
		// Backward-compatible default for callers that construct Config directly.
		c.SecretKeyPath = c.DBPath + ".key"
	}
	if c.StackRoot == "" {
		return fmt.Errorf("bbsit config: stack_root must not be empty")
	}
	if c.Listen == "" {
		return fmt.Errorf("bbsit config: listen must not be empty")
	}
	dbDir := filepath.Dir(c.DBPath)
	if err := os.MkdirAll(dbDir, 0755); err != nil {
		return fmt.Errorf("bbsit config: cannot create db_path directory %q: %w", dbDir, err)
	}
	keyDir := filepath.Dir(c.SecretKeyPath)
	if err := os.MkdirAll(keyDir, 0700); err != nil {
		return fmt.Errorf("bbsit config: cannot create secret_key_path directory %q: %w", keyDir, err)
	}
	if err := os.MkdirAll(c.StackRoot, 0755); err != nil {
		return fmt.Errorf("bbsit config: cannot create stack_root directory %q: %w", c.StackRoot, err)
	}
	return nil
}
