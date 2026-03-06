package config

import (
	"os"

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
			return cfg, nil // use defaults
		}
		return nil, err
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}
