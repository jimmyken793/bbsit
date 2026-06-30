package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/scheduler"
	"github.com/kingyoung/bbsit/internal/types"
)

// adminTestServer creates a Server with the admin mux mounted on httptest.
// Skips auth so we can hit the same endpoints bbsit-ctl uses over Unix socket.
func adminTestServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	stackRoot := t.TempDir()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dep := deployer.New(database, logger, "docker")
	sched := scheduler.New(database, dep, logger, "docker")
	srv := NewServer(database, dep, sched, nil, nil, logger, stackRoot)
	ts := httptest.NewServer(srv.AdminMux())
	t.Cleanup(ts.Close)
	return srv, ts, stackRoot
}

func TestAdminExportProjectYAML(t *testing.T) {
	_, ts, _ := adminTestServer(t)

	body := `{"id":"web","display_name":"Web","config_mode":"form","registry_image":"reg/app","image_tag":"latest","health_type":"none","poll_interval":300,"enabled":true}`
	resp, err := ts.Client().Post(ts.URL+"/api/projects/import", "application/x-yaml", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("import: %d", resp.StatusCode)
	}

	resp, err = ts.Client().Get(ts.URL + "/api/projects/web/export")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)

	var got types.Project
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml parse: %v", err)
	}
	if got.ID != "web" {
		t.Errorf("id: want web, got %q", got.ID)
	}
	if got.StackPath != "" {
		t.Errorf("StackPath should be cleared in export, got %q", got.StackPath)
	}
}

func TestAdminExportImportRoundTripWithTarball(t *testing.T) {
	srv, ts, stackRoot := adminTestServer(t)

	// Create a project with a relative volume so export packs the data dir
	p := &types.Project{
		ID:          "demo",
		DisplayName: "Demo",
		ConfigMode:  types.ConfigModeForm,
		Services: []types.ServiceConfig{{
			Name:          "demo",
			RegistryImage: "reg/demo",
			ImageTag:      "latest",
			Polled:        true,
			Volumes: []types.VolumeMount{{
				HostPath:      "data",
				ContainerPath: "/var/data",
			}},
		}},
		StackPath:    filepath.Join(stackRoot, "demo"),
		HealthType:   types.HealthNone,
		PollInterval: 300,
		Enabled:      true,
	}
	if err := srv.db.CreateProject(p); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Seed a file inside the data dir
	dataDir := filepath.Join(p.StackPath, "data")
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "hello.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	// Export as tar.gz
	resp, err := ts.Client().Get(ts.URL + "/api/projects/demo/export?format=tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("export: %d", resp.StatusCode)
	}
	tarball, _ := io.ReadAll(resp.Body)

	// Inspect tar contents
	hasYAML, hasFile := false, false
	gz, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar: %v", err)
		}
		switch filepath.Clean(h.Name) {
		case "project.yaml":
			hasYAML = true
		case "data/hello.txt":
			hasFile = true
			b, _ := io.ReadAll(tr)
			if string(b) != "hi" {
				t.Errorf("hello.txt content: want %q, got %q", "hi", string(b))
			}
		}
	}
	if !hasYAML {
		t.Error("tarball missing project.yaml")
	}
	if !hasFile {
		t.Error("tarball missing data/hello.txt")
	}

	// Delete the project + stack, then import back
	if err := srv.db.DeleteProject("demo"); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(p.StackPath)

	resp, err = ts.Client().Post(ts.URL+"/api/projects/import", "application/gzip", bytes.NewReader(tarball))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("import: %d %s", resp.StatusCode, body)
	}

	got, err := srv.db.GetProject("demo")
	if err != nil {
		t.Fatalf("project not restored: %v", err)
	}
	if got.StackPath == "" {
		t.Error("StackPath should be defaulted on import")
	}
	restored, err := os.ReadFile(filepath.Join(got.StackPath, "data", "hello.txt"))
	if err != nil {
		t.Fatalf("data not restored: %v", err)
	}
	if string(restored) != "hi" {
		t.Errorf("restored content: want %q, got %q", "hi", string(restored))
	}
}

func TestAdminExportProjectNotFound(t *testing.T) {
	_, ts, _ := adminTestServer(t)
	resp, err := ts.Client().Get(ts.URL + "/api/projects/missing/export")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestAdminExportBadFormat(t *testing.T) {
	srv, ts, stackRoot := adminTestServer(t)
	p := &types.Project{
		ID: "x", ConfigMode: types.ConfigModeForm,
		Services:   []types.ServiceConfig{{Name: "x", RegistryImage: "r/x", ImageTag: "latest"}},
		StackPath:  filepath.Join(stackRoot, "x"),
		HealthType: types.HealthNone,
	}
	if err := srv.db.CreateProject(p); err != nil {
		t.Fatal(err)
	}
	resp, _ := ts.Client().Get(ts.URL + "/api/projects/x/export?format=xml")
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400, got %d", resp.StatusCode)
	}
}

func TestAdminListProjectsNoAuth(t *testing.T) {
	_, ts, _ := adminTestServer(t)
	// admin mux must NOT require auth
	resp, err := ts.Client().Get(ts.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200 (no auth on admin mux), got %d", resp.StatusCode)
	}
	var got []any
	json.NewDecoder(resp.Body).Decode(&got)
	if got == nil {
		t.Error("nil result")
	}
}

// --- Unix socket end-to-end ---

func TestServeAdminSocket(t *testing.T) {
	srv, _, _ := adminTestServer(t)
	sockPath := filepath.Join(t.TempDir(), "admin.sock")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := srv.ServeAdminSocket(ctx, sockPath, "", 0660); err != nil {
		t.Fatalf("serve socket: %v", err)
	}

	// Wait briefly for the listener to come up
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(sockPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket file did not appear")
		}
		time.Sleep(20 * time.Millisecond)
	}

	hc := &http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", sockPath)
			},
		},
	}
	resp, err := hc.Get("http://unix/api/projects")
	if err != nil {
		t.Fatalf("get over unix sock: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("want 200, got %d", resp.StatusCode)
	}
}

func TestImportTarballMissingYAML(t *testing.T) {
	_, ts, _ := adminTestServer(t)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "data/x.txt", Size: 1, Mode: 0644})
	tw.Write([]byte("y"))
	tw.Close()
	gz.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/projects/import", "application/gzip", &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for missing project.yaml, got %d", resp.StatusCode)
	}
}

func TestImportTarballPathTraversal(t *testing.T) {
	_, ts, _ := adminTestServer(t)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// Malicious entry trying to escape via ..
	tw.WriteHeader(&tar.Header{Name: "../etc/passwd", Size: 1, Mode: 0644})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()

	resp, err := ts.Client().Post(ts.URL+"/api/projects/import", "application/gzip", &buf)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("want 400 for path traversal, got %d", resp.StatusCode)
	}
}
