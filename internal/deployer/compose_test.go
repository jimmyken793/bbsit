package deployer

import (
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
