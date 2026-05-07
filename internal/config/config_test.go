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

func TestLoad_ValidFile(t *testing.T) {
	tmpDir := t.TempDir()
	stackDir := filepath.Join(tmpDir, "stacks")

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
	if _, err := os.Stat(stackDir); err != nil {
		t.Errorf("stack_root %q should have been auto-created: %v", stackDir, err)
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

func TestValidate_AutoCreatesDirectories(t *testing.T) {
	tmpDir := t.TempDir()
	dbDir := filepath.Join(tmpDir, "dbdir")
	stackRoot := filepath.Join(tmpDir, "stacks")

	cfg := &Config{
		Listen:    ":9090",
		DBPath:    filepath.Join(dbDir, "state.db"),
		StackRoot: stackRoot,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	for _, dir := range []string{dbDir, stackRoot} {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("expected %q to be auto-created as a directory, got: %v", dir, err)
		}
	}
}

func TestValidate_UncreatableDirectoryReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	readOnly := filepath.Join(tmpDir, "ro")
	if err := os.Mkdir(readOnly, 0500); err != nil {
		t.Fatalf("setup: %v", err)
	}
	cfg := &Config{
		Listen:    ":9090",
		DBPath:    "/tmp/state.db",
		StackRoot: filepath.Join(readOnly, "stacks"),
	}
	if err := cfg.Validate(); err == nil || !contains(err.Error(), "stack_root directory") {
		t.Errorf("expected stack_root creation error, got: %v", err)
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
