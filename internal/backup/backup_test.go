package backup

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/kingyoung/bbsit/internal/types"
)

func TestNewestMatch_PicksLatestRecentFile(t *testing.T) {
	dir := t.TempDir()
	since := time.Now()

	// Stale file (mtime well before `since`) — must be ignored.
	stale := filepath.Join(dir, "old_backup.tar")
	mustWrite(t, stale, "stale")
	staleTime := since.Add(-1 * time.Hour)
	os.Chtimes(stale, staleTime, staleTime)

	// Two new files, newest wins.
	older := filepath.Join(dir, "1_backup.tar")
	mustWrite(t, older, "older")
	olderTime := since.Add(2 * time.Second)
	os.Chtimes(older, olderTime, olderTime)

	newer := filepath.Join(dir, "2_backup.tar")
	mustWrite(t, newer, "newer")
	newerTime := since.Add(10 * time.Second)
	os.Chtimes(newer, newerTime, newerTime)

	// Non-matching file (different glob) — must be ignored.
	mustWrite(t, filepath.Join(dir, "notes.txt"), "ignore me")

	got, err := newestMatch(dir, "*_backup.tar", since)
	if err != nil {
		t.Fatalf("newestMatch: %v", err)
	}
	if got != newer {
		t.Errorf("got %q, want %q", got, newer)
	}
}

func TestNewestMatch_NoMatch(t *testing.T) {
	dir := t.TempDir()
	got, err := newestMatch(dir, "*.tar", time.Now())
	if err != nil {
		t.Fatalf("newestMatch: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestHashFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	mustWrite(t, path, "hello")

	sum, n, err := hashFile(path)
	if err != nil {
		t.Fatalf("hashFile: %v", err)
	}
	// sha256("hello")
	want := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if sum != want {
		t.Errorf("sha256 = %q, want %q", sum, want)
	}
	if n != 5 {
		t.Errorf("size = %d, want 5", n)
	}
}

func TestFreePort_BindsAndReleases(t *testing.T) {
	port, err := freePort()
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Fatalf("port out of range: %d", port)
	}
	// Sanity: we should be able to bind it now (it was just released).
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("rebind freed port: %v", err)
	}
	l.Close()
}

func TestCloneForRestore_ReassignsPortsAndStripsTunnels(t *testing.T) {
	src := &types.Project{
		ID:          "gitlab",
		DisplayName: "GitLab",
		ConfigMode:  types.ConfigModeForm,
		BindHost:    "127.0.0.1",
		Services: []types.ServiceConfig{{
			Name:          "gitlab",
			RegistryImage: "gitlab/gitlab-ce",
			ImageTag:      "latest",
			Polled:        true,
			Ports: []types.PortMapping{
				{HostPort: "18080", ContainerPort: 80, Protocol: "tcp"},
				{HostPort: "127.0.0.1:18443", ContainerPort: 443, Protocol: "tcp"},
			},
			Volumes: []types.VolumeMount{
				{HostPath: "data", ContainerPath: "/var/opt/gitlab"},
				{HostPath: "backups", ContainerPath: "/var/opt/gitlab/backups"},
			},
			PublicHostnames: []types.PublicHostname{
				{TunnelID: "t1", Hostname: "gitlab.example.com", Port: 18080},
			},
		}},
		StackPath:    "/opt/stacks/gitlab",
		HealthType:   types.HealthNone,
		PollInterval: 300,
		Enabled:      true,
		IsSystem:     true, // should be cleared on clone
		EnvVars:      map[string]string{"FOO": "bar"},
		Backup: &types.BackupSpec{
			Service:        "gitlab",
			BackupCommand:  "gitlab-backup create",
			RestoreCommand: "gitlab-backup restore BACKUP=...",
			OutputPath:     "/var/opt/gitlab/backups",
			OutputPattern:  "*_gitlab_backup.tar",
		},
	}

	s := &Service{}
	clone, err := s.cloneForRestore(src, "gitlab-verify")
	if err != nil {
		t.Fatalf("cloneForRestore: %v", err)
	}
	if clone.ID != "gitlab-verify" {
		t.Errorf("ID = %q, want gitlab-verify", clone.ID)
	}
	if clone.IsSystem {
		t.Error("IsSystem should be false on clone")
	}
	if clone.StackPath != "/opt/stacks/gitlab-verify" {
		t.Errorf("StackPath = %q, want /opt/stacks/gitlab-verify", clone.StackPath)
	}
	if clone.Services[0].Ports[0].HostPort == src.Services[0].Ports[0].HostPort {
		t.Error("Ports[0] HostPort should be reassigned")
	}
	if clone.Services[0].Ports[1].HostPort == src.Services[0].Ports[1].HostPort {
		t.Error("Ports[1] HostPort should be reassigned")
	}
	if clone.Services[0].PublicHostnames != nil {
		t.Error("PublicHostnames should be stripped on clone")
	}
	if clone.Backup == nil || clone.Backup.BackupCommand != src.Backup.BackupCommand {
		t.Error("Backup spec should be deep-copied on clone")
	}
	// Mutate clone.Backup; original must be unchanged.
	clone.Backup.BackupCommand = "evil"
	if src.Backup.BackupCommand == "evil" {
		t.Error("Backup deep-copy violated: source mutated")
	}
	clone.EnvVars["FOO"] = "evil"
	if src.EnvVars["FOO"] == "evil" {
		t.Error("EnvVars deep-copy violated: source mutated")
	}
}

func TestCloneForRestore_RejectsSameID(t *testing.T) {
	s := &Service{}
	_, err := s.cloneForRestore(&types.Project{ID: "x"}, "x")
	if err == nil {
		t.Fatal("expected error when --as id matches source id")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
