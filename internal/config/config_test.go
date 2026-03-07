package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Listen != "0.0.0.0:9090" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "0.0.0.0:9090")
	}
	if cfg.DBPath != "/opt/bbsit/state.db" {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, "/opt/bbsit/state.db")
	}
	if cfg.StackRoot != "/opt/stacks" {
		t.Errorf("StackRoot = %q, want %q", cfg.StackRoot, "/opt/stacks")
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "info")
	}
}

func TestLoad_MissingFile(t *testing.T) {
	// Missing config file with invalid default paths should return an error
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("expected error when config file missing and default paths don't exist")
	}
}

func TestLoad_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	stackDir := filepath.Join(tmpDir, "stacks")
	os.MkdirAll(stackDir, 0755)

	path := filepath.Join(tmpDir, "config.yaml")
	content := `
listen: "127.0.0.1:8080"
db_path: "` + filepath.Join(tmpDir, "test.db") + `"
stack_root: "` + stackDir + `"
log_level: "debug"
`
	os.WriteFile(path, []byte(content), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "127.0.0.1:8080" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "127.0.0.1:8080")
	}
	if cfg.DBPath != filepath.Join(tmpDir, "test.db") {
		t.Errorf("DBPath = %q, want %q", cfg.DBPath, filepath.Join(tmpDir, "test.db"))
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
}

func TestLoad_PartialOverride(t *testing.T) {
	// Partial override with invalid default paths should fail validation
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte(`log_level: "warn"`), 0644)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error when default paths don't exist")
	}
}

func TestValidate_EmptyFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"empty db_path", Config{Listen: ":9090", DBPath: "", StackRoot: "/tmp"}, "db_path must not be empty"},
		{"empty stack_root", Config{Listen: ":9090", DBPath: "/tmp/db", StackRoot: ""}, "stack_root must not be empty"},
		{"empty listen", Config{Listen: "", DBPath: "/tmp/db", StackRoot: "/tmp"}, "listen must not be empty"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.cfg.Validate()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("error %q should contain %q", err.Error(), tc.want)
			}
		})
	}
}

func TestValidate_MissingDirectories(t *testing.T) {
	cfg := &Config{
		Listen:    ":9090",
		DBPath:    "/nonexistent/path/state.db",
		StackRoot: "/tmp",
	}
	err := cfg.Validate()
	if err == nil || !contains(err.Error(), "db_path directory") {
		t.Errorf("expected db_path directory error, got: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestLoad_InvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("{{not: valid: yaml}}"), 0644)

	_, err := Load(path)
	if err == nil {
		t.Error("expected error for invalid YAML, got nil")
	}
}
