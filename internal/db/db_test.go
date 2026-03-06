package db

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/kingyoung/bbsit/internal/types"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func sampleProject(id string) *types.Project {
	return &types.Project{
		ID:            id,
		DisplayName:   "Test Project",
		ConfigMode:    types.ConfigModeForm,
		RegistryImage: "registry.example.com/app",
		ImageTag:      "latest",
		StackPath:     "/opt/stacks/" + id,
		HealthType:    types.HealthNone,
		PollInterval:  300,
		Enabled:       true,
	}
}

func TestCreateAndGetProject(t *testing.T) {
	db := openTestDB(t)
	p := sampleProject("proj-a")

	if err := db.CreateProject(p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := db.GetProject("proj-a")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if got.ID != "proj-a" {
		t.Errorf("ID = %q, want proj-a", got.ID)
	}
	if got.DisplayName != "Test Project" {
		t.Errorf("DisplayName = %q, want Test Project", got.DisplayName)
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}
	if got.StackPath != "/opt/stacks/proj-a" {
		t.Errorf("StackPath = %q, want /opt/stacks/proj-a", got.StackPath)
	}
}

func TestCreateProject_DuplicateID(t *testing.T) {
	db := openTestDB(t)
	p := sampleProject("proj-dup")

	if err := db.CreateProject(p); err != nil {
		t.Fatalf("first CreateProject: %v", err)
	}
	if err := db.CreateProject(p); err == nil {
		t.Error("expected error on duplicate ID, got nil")
	}
}

func TestListProjects_OrderedByID(t *testing.T) {
	db := openTestDB(t)

	for _, id := range []string{"proj-c", "proj-a", "proj-b"} {
		if err := db.CreateProject(sampleProject(id)); err != nil {
			t.Fatalf("CreateProject(%q): %v", id, err)
		}
	}

	projects, err := db.ListProjects()
	if err != nil {
		t.Fatalf("ListProjects: %v", err)
	}
	if len(projects) != 3 {
		t.Fatalf("len = %d, want 3", len(projects))
	}
	if projects[0].ID != "proj-a" || projects[1].ID != "proj-b" || projects[2].ID != "proj-c" {
		t.Errorf("wrong order: %v", []string{projects[0].ID, projects[1].ID, projects[2].ID})
	}
}

func TestUpdateProject(t *testing.T) {
	db := openTestDB(t)
	p := sampleProject("proj-upd")

	if err := db.CreateProject(p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	p.DisplayName = "Updated"
	p.ImageTag = "v2"
	p.Enabled = false
	if err := db.UpdateProject(p); err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}

	got, err := db.GetProject("proj-upd")
	if err != nil {
		t.Fatalf("GetProject after update: %v", err)
	}
	if got.DisplayName != "Updated" {
		t.Errorf("DisplayName = %q, want Updated", got.DisplayName)
	}
	if got.ImageTag != "v2" {
		t.Errorf("ImageTag = %q, want v2", got.ImageTag)
	}
	if got.Enabled {
		t.Error("Enabled = true, want false")
	}
}

func TestDeleteProject(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-del")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := db.DeleteProject("proj-del"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	if _, err := db.GetProject("proj-del"); err == nil {
		t.Error("expected error getting deleted project, got nil")
	}
}

func TestProjectWithPortsAndVolumes(t *testing.T) {
	db := openTestDB(t)
	p := sampleProject("proj-pv")
	p.Ports = []types.PortMapping{
		{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"},
	}
	p.Volumes = []types.VolumeMount{
		{HostPath: "/data", ContainerPath: "/app/data", ReadOnly: false},
	}
	p.EnvVars = map[string]string{"KEY": "value"}

	if err := db.CreateProject(p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := db.GetProject("proj-pv")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Ports) != 1 || got.Ports[0].HostPort != 8080 {
		t.Errorf("Ports = %v, want [{8080 80 tcp}]", got.Ports)
	}
	if len(got.Volumes) != 1 || got.Volumes[0].HostPath != "/data" {
		t.Errorf("Volumes = %v", got.Volumes)
	}
	if got.EnvVars["KEY"] != "value" {
		t.Errorf("EnvVars[KEY] = %q, want value", got.EnvVars["KEY"])
	}
}

func TestGetState_Initial(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-st")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	state, err := db.GetState("proj-st")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if state.ProjectID != "proj-st" {
		t.Errorf("ProjectID = %q, want proj-st", state.ProjectID)
	}
	if state.CurrentDigest != "" {
		t.Errorf("CurrentDigest = %q, want empty", state.CurrentDigest)
	}
	if state.LastDeployAt != nil {
		t.Error("LastDeployAt should be nil initially")
	}
}

func TestUpdateState(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-ust")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	now := time.Now().UTC().Truncate(time.Second)
	state := &types.ProjectState{
		ProjectID:      "proj-ust",
		CurrentDigest:  "sha256:abc123",
		PreviousDigest: "sha256:def456",
		Status:         types.StatusRunning,
		LastDeployAt:   &now,
	}
	if err := db.UpdateState(state); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	got, err := db.GetState("proj-ust")
	if err != nil {
		t.Fatalf("GetState after update: %v", err)
	}
	if got.CurrentDigest != "sha256:abc123" {
		t.Errorf("CurrentDigest = %q, want sha256:abc123", got.CurrentDigest)
	}
	if got.PreviousDigest != "sha256:def456" {
		t.Errorf("PreviousDigest = %q, want sha256:def456", got.PreviousDigest)
	}
	if got.Status != types.StatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.LastDeployAt == nil {
		t.Error("LastDeployAt = nil, want non-nil")
	}
}

func TestInsertAndFinishDeployment(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-dep")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	d := &types.Deployment{
		ProjectID:  "proj-dep",
		FromDigest: "sha256:old",
		ToDigest:   "sha256:new",
		Status:     types.DeployInProgress,
		Trigger:    types.TriggerPoll,
		StartedAt:  time.Now().UTC(),
	}

	id, err := db.InsertDeployment(d)
	if err != nil {
		t.Fatalf("InsertDeployment: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero deployment ID")
	}

	if err := db.FinishDeployment(id, types.DeploySuccess, ""); err != nil {
		t.Fatalf("FinishDeployment: %v", err)
	}

	deployments, err := db.ListDeployments("proj-dep", 10)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deployments) != 1 {
		t.Fatalf("len = %d, want 1", len(deployments))
	}
	if deployments[0].Status != types.DeploySuccess {
		t.Errorf("Status = %q, want success", deployments[0].Status)
	}
	if deployments[0].EndedAt == nil {
		t.Error("EndedAt = nil after FinishDeployment")
	}
}

func TestListDeployments_Limit(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-lim")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	for i := 0; i < 5; i++ {
		d := &types.Deployment{
			ProjectID: "proj-lim",
			Status:    types.DeploySuccess,
			Trigger:   types.TriggerManual,
			StartedAt: time.Now().UTC(),
		}
		if _, err := db.InsertDeployment(d); err != nil {
			t.Fatalf("InsertDeployment: %v", err)
		}
	}

	results, err := db.ListDeployments("proj-lim", 3)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("len = %d, want 3 (limit respected)", len(results))
	}
}

func TestDeleteProject_CascadesState(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-cas")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := db.DeleteProject("proj-cas"); err != nil {
		t.Fatalf("DeleteProject: %v", err)
	}
	// State should be cascade-deleted (FK ON DELETE CASCADE)
	if _, err := db.GetState("proj-cas"); err == nil {
		t.Error("expected error getting state of deleted project")
	}
}

func TestSetAndGetPassword(t *testing.T) {
	db := openTestDB(t)

	// No password set yet
	if _, err := db.GetPasswordHash(); err == nil {
		t.Error("expected error before password is set, got nil")
	}

	if err := db.SetPassword("$2b$10$fakehash"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	hash, err := db.GetPasswordHash()
	if err != nil {
		t.Fatalf("GetPasswordHash: %v", err)
	}
	if hash != "$2b$10$fakehash" {
		t.Errorf("hash = %q, want $2b$10$fakehash", hash)
	}

	// UPSERT — update password
	if err := db.SetPassword("$2b$10$newhash"); err != nil {
		t.Fatalf("SetPassword update: %v", err)
	}
	hash, _ = db.GetPasswordHash()
	if hash != "$2b$10$newhash" {
		t.Errorf("updated hash = %q, want $2b$10$newhash", hash)
	}
}

func TestListProjectsWithState(t *testing.T) {
	db := openTestDB(t)

	for _, id := range []string{"proj-ws-a", "proj-ws-b"} {
		if err := db.CreateProject(sampleProject(id)); err != nil {
			t.Fatalf("CreateProject(%q): %v", id, err)
		}
	}

	results, err := db.ListProjectsWithState()
	if err != nil {
		t.Fatalf("ListProjectsWithState: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("len = %d, want 2", len(results))
	}
	for _, ps := range results {
		if ps.State.ProjectID == "" {
			t.Errorf("State.ProjectID empty for project %q", ps.ID)
		}
	}
}
