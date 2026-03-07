package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen    string `yaml:"listen"`     // e.g. "0.0.0.0:9090"
	DBPath    string `yaml:"db_path"`    // e.g. "/opt/bbsit/state.db"
	StackRoot string `yaml:"stack_root"` // e.g. "/opt/stacks"
	LogLevel  string `yaml:"log_level"`  // debug | info | warn | error
}

func DefaultConfig() *Config {
	return &Config{
		Listen:    "0.0.0.0:9090",
		DBPath:    "/opt/bbsit/state.db",
		StackRoot: "/opt/stacks",
		LogLevel:  "info",
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
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.DBPath == "" {
		return fmt.Errorf("bbsit config: db_path must not be empty")
	}
	if c.StackRoot == "" {
		return fmt.Errorf("bbsit config: stack_root must not be empty")
	}
	if c.Listen == "" {
		return fmt.Errorf("bbsit config: listen must not be empty")
	}
	dbDir := filepath.Dir(c.DBPath)
	if _, err := os.Stat(dbDir); os.IsNotExist(err) {
		return fmt.Errorf("bbsit config: db_path directory %q does not exist", dbDir)
	}
	if _, err := os.Stat(c.StackRoot); os.IsNotExist(err) {
		return fmt.Errorf("bbsit config: stack_root directory %q does not exist", c.StackRoot)
	}
	return nil
}
