package tunnel

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kingyoung/bbsit/internal/types"
)

func TestWriteConfigYml(t *testing.T) {
	dir := t.TempDir()
	tn := &types.Tunnel{ID: "prod", CFTunnelID: "uuid-123"}
	entries := []ingressEntry{
		{Hostname: "app.example.com", Port: 18081, ProjectID: "webui", Service: "app"},
		{Hostname: "api.example.com", Port: 18082, ProjectID: "api", Service: "app"},
	}

	changed, err := writeConfigYml(dir, tn, entries)
	if err != nil {
		t.Fatalf("writeConfigYml: %v", err)
	}
	if !changed {
		t.Fatal("expected changed=true on first write")
	}

	got, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(got)

	for _, want := range []string{
		"tunnel: uuid-123",
		"credentials-file: /etc/cloudflared/credentials.json",
		"hostname: app.example.com",
		"service: http://localhost:18081",
		"hostname: api.example.com",
		"service: http://localhost:18082",
		"service: http_status:404",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("config.yml missing %q\n%s", want, s)
		}
	}

	// Idempotent: same content should report changed=false
	changed2, err := writeConfigYml(dir, tn, entries)
	if err != nil {
		t.Fatalf("writeConfigYml second: %v", err)
	}
	if changed2 {
		t.Error("expected changed=false on identical second write")
	}

	// Different ingress should report changed=true
	entries2 := append(entries, ingressEntry{Hostname: "extra.example.com", Port: 18083})
	changed3, err := writeConfigYml(dir, tn, entries2)
	if err != nil {
		t.Fatal(err)
	}
	if !changed3 {
		t.Error("expected changed=true after ingress change")
	}
}

func TestWriteCredentials(t *testing.T) {
	dir := t.TempDir()
	tn := &types.Tunnel{
		CFTunnelID:   "uuid-123",
		AccountTag:   "acct-abc",
		TunnelSecret: "secret-xyz",
	}
	if err := writeCredentials(dir, tn); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	var creds types.TunnelCredentials
	if err := json.Unmarshal(got, &creds); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if creds.TunnelID != "uuid-123" || creds.AccountTag != "acct-abc" || creds.TunnelSecret != "secret-xyz" {
		t.Errorf("creds round-trip mismatch: %+v", creds)
	}

	// File should be 0600 (only owner can read secrets)
	info, err := os.Stat(filepath.Join(dir, "credentials.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("credentials.json perm = %v, want 0600", info.Mode().Perm())
	}
}

func TestBuildSystemProject(t *testing.T) {
	tn := &types.Tunnel{ID: "prod", Name: "Production", Enabled: true}
	p := buildSystemProject(tn, "/opt/stacks/cf-tunnel-prod")
	if p.ID != "cf-tunnel-prod" {
		t.Errorf("ID = %q, want cf-tunnel-prod", p.ID)
	}
	if !p.IsSystem {
		t.Error("IsSystem should be true")
	}
	if len(p.Services) != 1 || p.Services[0].Name != "cloudflared" {
		t.Errorf("services = %+v", p.Services)
	}
	svc := p.Services[0]
	if svc.RegistryImage != "cloudflare/cloudflared" {
		t.Errorf("image = %q", svc.RegistryImage)
	}
	if !strings.Contains(svc.ExtraOptions, "network_mode: host") {
		t.Errorf("extra_options missing network_mode: host: %s", svc.ExtraOptions)
	}
	if len(svc.Volumes) != 2 {
		t.Errorf("expected 2 volumes (config + creds), got %d", len(svc.Volumes))
	}
}

func TestSystemProjectID(t *testing.T) {
	if got := SystemProjectID("prod"); got != "cf-tunnel-prod" {
		t.Errorf("SystemProjectID(prod) = %q", got)
	}
}
