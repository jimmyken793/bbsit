package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kingyoung/bbsit/internal/backup"
	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	bbruntime "github.com/kingyoung/bbsit/internal/runtime"
	"github.com/kingyoung/bbsit/internal/scheduler"
)

// adminBackupTestServer wires a Server with a real backup service so we
// exercise the same routing and short-circuit error paths as production.
// Tests that would actually exec into a container are intentionally skipped
// — those need a running runtime and live in the integration suite.
func adminBackupTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	stackRoot := t.TempDir()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dep := deployer.New(database, logger, "docker")
	sched := scheduler.New(database, dep, logger, "docker")
	bk := backup.New(database, bbruntime.New("docker"), logger)
	srv := NewServer(database, dep, sched, nil, bk, logger, stackRoot)
	ts := httptest.NewServer(srv.AdminMux())
	t.Cleanup(ts.Close)
	return ts, stackRoot
}

func importBackupProject(t *testing.T, ts *httptest.Server, withSpec bool) {
	t.Helper()
	yaml := `id: web
display_name: Web
config_mode: form
registry_image: reg/app
image_tag: latest
services:
  - name: web
    registry_image: reg/app
    image_tag: latest
    polled: true
health_type: none
poll_interval: 300
enabled: true
`
	if withSpec {
		yaml += `backup:
  service: web
  backup_command: "true"
  restore_command: "true"
  output_path: /var/backups
  output_pattern: "*.tar"
`
	}
	resp, err := ts.Client().Post(ts.URL+"/api/projects/import", "application/x-yaml", strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import status %d", resp.StatusCode)
	}
}

func TestAdminBackup_404OnMissingProject(t *testing.T) {
	ts, _ := adminBackupTestServer(t)
	resp, err := ts.Client().Post(ts.URL+"/api/projects/ghost/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("got %d, want 404", resp.StatusCode)
	}
}

func TestAdminBackup_400WhenNoSpec(t *testing.T) {
	ts, _ := adminBackupTestServer(t)
	importBackupProject(t, ts, false)

	resp, err := ts.Client().Post(ts.URL+"/api/projects/web/backup", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if !strings.Contains(body["error"], "no backup spec") {
		t.Errorf("error = %q", body["error"])
	}
}

func TestAdminListBackups_EmptyArray(t *testing.T) {
	ts, _ := adminBackupTestServer(t)
	importBackupProject(t, ts, true)

	resp, err := ts.Client().Get(ts.URL + "/api/projects/web/backups")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	// Empty dir → []. Must not be `null` so JSON consumers can iterate.
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("body = %q, want []", string(body))
	}
}

func TestAdminBackupHistory_EmptyArray(t *testing.T) {
	ts, _ := adminBackupTestServer(t)
	importBackupProject(t, ts, true)

	resp, err := ts.Client().Get(ts.URL + "/api/projects/web/backup-runs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != "[]" {
		t.Errorf("body = %q, want []", string(body))
	}
}

func TestAdminRestore_400WithoutFile(t *testing.T) {
	ts, _ := adminBackupTestServer(t)
	importBackupProject(t, ts, true)

	resp, err := ts.Client().Post(ts.URL+"/api/projects/web/restore",
		"application/json", bytes.NewBufferString(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("got %d, want 400", resp.StatusCode)
	}
}
