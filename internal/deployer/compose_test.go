package deployer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kingyoung/bbsit/internal/types"
)

func baseProject() *types.Project {
	return &types.Project{
		ID:            "svc",
		RegistryImage: "registry.example.com/svc",
		ImageTag:      "latest",
	}
}

func TestGenerateFormCompose_Basic(t *testing.T) {
	got := generateFormCompose(baseProject())

	for _, want := range []string{
		"services:",
		"  svc:",
		"    image: registry.example.com/svc:latest",
		"    restart: unless-stopped",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in output:\n%s", want, got)
		}
	}
}

func TestGenerateFormCompose_TCPPort(t *testing.T) {
	p := baseProject()
	p.Ports = []types.PortMapping{
		{HostPort: 8080, ContainerPort: 80},
		{HostPort: 9090, ContainerPort: 9090, Protocol: "tcp"},
	}
	got := generateFormCompose(p)

	if !strings.Contains(got, `"127.0.0.1:8080:80"`) {
		t.Errorf("missing TCP port (implicit), got:\n%s", got)
	}
	if !strings.Contains(got, `"127.0.0.1:9090:9090"`) {
		t.Errorf("missing TCP port (explicit), got:\n%s", got)
	}
}

func TestGenerateFormCompose_UDPPort(t *testing.T) {
	p := baseProject()
	p.Ports = []types.PortMapping{
		{HostPort: 5353, ContainerPort: 5353, Protocol: "udp"},
	}
	got := generateFormCompose(p)

	if !strings.Contains(got, `"127.0.0.1:5353:5353/udp"`) {
		t.Errorf("missing UDP port mapping, got:\n%s", got)
	}
}

func TestGenerateFormCompose_Volumes(t *testing.T) {
	p := baseProject()
	p.Volumes = []types.VolumeMount{
		{HostPath: "/data", ContainerPath: "/app/data"},
		{HostPath: "/cfg", ContainerPath: "/app/cfg", ReadOnly: true},
	}
	got := generateFormCompose(p)

	if !strings.Contains(got, `"/data:/app/data"`) {
		t.Errorf("missing volume, got:\n%s", got)
	}
	if !strings.Contains(got, `"/cfg:/app/cfg:ro"`) {
		t.Errorf("missing read-only volume, got:\n%s", got)
	}
}

func TestGenerateFormCompose_EnvFile(t *testing.T) {
	p := baseProject()
	p.EnvVars = map[string]string{"KEY": "val"}
	got := generateFormCompose(p)

	if !strings.Contains(got, "env_file:") {
		t.Errorf("missing env_file section, got:\n%s", got)
	}
	if !strings.Contains(got, "- .env") {
		t.Errorf("missing .env reference, got:\n%s", got)
	}
}

func TestGenerateFormCompose_NoEnvFile(t *testing.T) {
	got := generateFormCompose(baseProject())

	if strings.Contains(got, "env_file:") {
		t.Errorf("unexpected env_file section, got:\n%s", got)
	}
}

func TestGenerateFormCompose_BindHost(t *testing.T) {
	p := baseProject()
	p.Ports = []types.PortMapping{{HostPort: 8080, ContainerPort: 80}}

	// Default (empty) should use 127.0.0.1
	got := generateFormCompose(p)
	if !strings.Contains(got, `"127.0.0.1:8080:80"`) {
		t.Errorf("empty bind_host should default to 127.0.0.1, got:\n%s", got)
	}

	// Explicit 0.0.0.0
	p.BindHost = "0.0.0.0"
	got = generateFormCompose(p)
	if !strings.Contains(got, `"0.0.0.0:8080:80"`) {
		t.Errorf("bind_host 0.0.0.0 not applied, got:\n%s", got)
	}

	// Explicit 127.0.0.1
	p.BindHost = "127.0.0.1"
	got = generateFormCompose(p)
	if !strings.Contains(got, `"127.0.0.1:8080:80"`) {
		t.Errorf("bind_host 127.0.0.1 not applied, got:\n%s", got)
	}
}

func TestGenerateFormCompose_ExtraOptions(t *testing.T) {
	p := baseProject()
	p.ExtraOptions = "network_mode: host\nprivileged: true"
	got := generateFormCompose(p)

	if !strings.Contains(got, "    network_mode: host") {
		t.Errorf("missing indented extra option, got:\n%s", got)
	}
	if !strings.Contains(got, "    privileged: true") {
		t.Errorf("missing indented extra option, got:\n%s", got)
	}
}

func TestGenerateDigestOverride(t *testing.T) {
	got := generateDigestOverride("webui", "sha256:abc123def456")

	for _, want := range []string{
		"services:",
		"  webui:",
		"    image: sha256:abc123def456",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in override output:\n%s", want, got)
		}
	}
}

func TestWriteComposeFiles_FormMode(t *testing.T) {
	dir := t.TempDir()
	p := &types.Project{
		ID:            "test-svc",
		ConfigMode:    types.ConfigModeForm,
		RegistryImage: "registry.example.com/app",
		ImageTag:      "latest",
		StackPath:     dir,
		Ports:         []types.PortMapping{{HostPort: 8080, ContainerPort: 80}},
		Volumes:       []types.VolumeMount{{HostPath: "./data", ContainerPath: "/app/data"}},
		EnvVars:       map[string]string{"KEY": "val"},
	}

	if err := WriteComposeFiles(p, ""); err != nil {
		t.Fatalf("WriteComposeFiles: %v", err)
	}

	// compose.yaml should exist
	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}
	if !strings.Contains(string(compose), "registry.example.com/app:latest") {
		t.Error("compose.yaml missing image")
	}

	// .env should exist
	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(env), "KEY=val") {
		t.Error(".env missing KEY=val")
	}

	// bind mount dir should exist
	if _, err := os.Stat(filepath.Join(dir, "data")); os.IsNotExist(err) {
		t.Error("bind mount directory not created")
	}

	// no override file without digest
	if _, err := os.Stat(filepath.Join(dir, "compose.override.yaml")); !os.IsNotExist(err) {
		t.Error("compose.override.yaml should not exist without digest")
	}
}

func TestWriteComposeFiles_WithDigest(t *testing.T) {
	dir := t.TempDir()
	p := &types.Project{
		ID:            "digest-svc",
		ConfigMode:    types.ConfigModeForm,
		RegistryImage: "registry.example.com/app",
		ImageTag:      "latest",
		StackPath:     dir,
	}

	if err := WriteComposeFiles(p, "sha256:abc123"); err != nil {
		t.Fatalf("WriteComposeFiles: %v", err)
	}

	override, err := os.ReadFile(filepath.Join(dir, "compose.override.yaml"))
	if err != nil {
		t.Fatalf("read compose.override.yaml: %v", err)
	}
	if !strings.Contains(string(override), "sha256:abc123") {
		t.Error("override missing digest")
	}
}

func TestWriteComposeFiles_CustomMode(t *testing.T) {
	dir := t.TempDir()
	p := &types.Project{
		ID:         "custom-svc",
		ConfigMode: types.ConfigModeCustom,
		StackPath:  dir,
		CustomCompose: `registry_image: registry.example.com/custom
image_tag: v2
ports:
  - host_port: 3000
    container_port: 3000
env_vars:
  DB_URL: postgres://localhost/db
`,
	}

	if err := WriteComposeFiles(p, ""); err != nil {
		t.Fatalf("WriteComposeFiles: %v", err)
	}

	compose, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		t.Fatalf("read compose.yaml: %v", err)
	}
	content := string(compose)
	if !strings.Contains(content, "registry.example.com/custom:v2") {
		t.Errorf("compose.yaml missing custom image, got:\n%s", content)
	}
	if !strings.Contains(content, "3000:3000") {
		t.Errorf("compose.yaml missing port, got:\n%s", content)
	}
}

func TestWriteComposeFiles_CustomMode_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	p := &types.Project{
		ID:            "bad-yaml",
		ConfigMode:    types.ConfigModeCustom,
		StackPath:     dir,
		CustomCompose: "registry_image:\n  - not: a string\n  - invalid: structure",
	}

	err := WriteComposeFiles(p, "")
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestWriteEnvFile_Escaping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")

	vars := map[string]string{
		"SIMPLE":  "value",
		"SPACED":  "has space",
		"QUOTED":  `has "quote`,
		"DOLLAR":  "has$dollar",
	}
	if err := writeEnvFile(path, vars); err != nil {
		t.Fatalf("writeEnvFile: %v", err)
	}

	content, _ := os.ReadFile(path)
	s := string(content)
	if !strings.Contains(s, "SIMPLE=value") {
		t.Error("simple value should not be quoted")
	}
	if !strings.Contains(s, `SPACED="has space"`) {
		t.Error("spaced value should be quoted")
	}
}
