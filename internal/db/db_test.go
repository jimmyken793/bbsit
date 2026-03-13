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
		ID:          id,
		DisplayName: "Test Project",
		ConfigMode:  types.ConfigModeForm,
		Services: []types.ServiceConfig{{
			Name:          id,
			RegistryImage: "registry.example.com/app",
			ImageTag:      "latest",
			Polled:        true,
		}},
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
	if len(state.CurrentDigests) != 0 {
		t.Errorf("CurrentDigests = %v, want empty", state.CurrentDigests)
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
		ProjectID:       "proj-ust",
		CurrentDigests:  map[string]string{"app": "sha256:abc123"},
		PreviousDigests: map[string]string{"app": "sha256:def456"},
		Status:          types.StatusRunning,
		LastDeployAt:    &now,
	}
	if err := db.UpdateState(state); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	got, err := db.GetState("proj-ust")
	if err != nil {
		t.Fatalf("GetState after update: %v", err)
	}
	if got.CurrentDigests["app"] != "sha256:abc123" {
		t.Errorf("CurrentDigests[app] = %q, want sha256:abc123", got.CurrentDigests["app"])
	}
	if got.PreviousDigests["app"] != "sha256:def456" {
		t.Errorf("PreviousDigests[app] = %q, want sha256:def456", got.PreviousDigests["app"])
	}
	if got.Status != types.StatusRunning {
		t.Errorf("Status = %q, want running", got.Status)
	}
	if got.LastDeployAt == nil {
		t.Error("LastDeployAt = nil, want non-nil")
	}
}

func TestUpdateState_MultiServiceDigests(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-multi")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	state := &types.ProjectState{
		ProjectID: "proj-multi",
		CurrentDigests: map[string]string{
			"app":   "sha256:aaa111",
			"redis": "sha256:bbb222",
		},
		PreviousDigests: map[string]string{
			"app":   "sha256:old111",
			"redis": "sha256:old222",
		},
		DesiredDigests: map[string]string{
			"app":   "sha256:new111",
			"redis": "sha256:bbb222",
		},
		Status: types.StatusRunning,
	}
	if err := db.UpdateState(state); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	got, err := db.GetState("proj-multi")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got.CurrentDigests["app"] != "sha256:aaa111" {
		t.Errorf("CurrentDigests[app] = %q", got.CurrentDigests["app"])
	}
	if got.CurrentDigests["redis"] != "sha256:bbb222" {
		t.Errorf("CurrentDigests[redis] = %q", got.CurrentDigests["redis"])
	}
	if got.DesiredDigests["app"] != "sha256:new111" {
		t.Errorf("DesiredDigests[app] = %q", got.DesiredDigests["app"])
	}
}

func TestResetStaleStates(t *testing.T) {
	db := openTestDB(t)

	// Create two projects: one deploying (stale), one running (healthy)
	if err := db.CreateProject(sampleProject("proj-stale")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	if err := db.CreateProject(sampleProject("proj-ok")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// Set proj-stale to deploying
	staleState := &types.ProjectState{
		ProjectID: "proj-stale",
		Status:    types.StatusDeploying,
	}
	if err := db.UpdateState(staleState); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	// Set proj-ok to running
	okState := &types.ProjectState{
		ProjectID: "proj-ok",
		Status:    types.StatusRunning,
	}
	if err := db.UpdateState(okState); err != nil {
		t.Fatalf("UpdateState: %v", err)
	}

	// Insert an in_progress deployment for the stale project
	d := &types.Deployment{
		ProjectID: "proj-stale",
		Status:    types.DeployInProgress,
		Trigger:   types.TriggerManual,
		StartedAt: time.Now().UTC(),
	}
	if _, err := db.InsertDeployment(d); err != nil {
		t.Fatalf("InsertDeployment: %v", err)
	}

	// Reset
	if err := db.ResetStaleStates(); err != nil {
		t.Fatalf("ResetStaleStates: %v", err)
	}

	// Stale project should now be failed
	got, err := db.GetState("proj-stale")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got.Status != types.StatusFailed {
		t.Errorf("stale Status = %q, want failed", got.Status)
	}
	if got.LastError != "interrupted by restart" {
		t.Errorf("stale LastError = %q, want 'interrupted by restart'", got.LastError)
	}

	// Running project should be untouched
	got2, err := db.GetState("proj-ok")
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if got2.Status != types.StatusRunning {
		t.Errorf("ok Status = %q, want running", got2.Status)
	}

	// In-progress deployment should be marked failed
	deps, err := db.ListDeployments("proj-stale", 10)
	if err != nil {
		t.Fatalf("ListDeployments: %v", err)
	}
	if len(deps) != 1 {
		t.Fatalf("len = %d, want 1", len(deps))
	}
	if deps[0].Status != types.DeployFailed {
		t.Errorf("deployment Status = %q, want failed", deps[0].Status)
	}
	if deps[0].EndedAt == nil {
		t.Error("deployment EndedAt = nil, want non-nil")
	}
}

func TestInsertAndFinishDeployment(t *testing.T) {
	db := openTestDB(t)

	if err := db.CreateProject(sampleProject("proj-dep")); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	d := &types.Deployment{
		ProjectID:   "proj-dep",
		FromDigests: map[string]string{"app": "sha256:old"},
		ToDigests:   map[string]string{"app": "sha256:new"},
		Status:      types.DeployInProgress,
		Trigger:     types.TriggerPoll,
		StartedAt:   time.Now().UTC(),
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
	if deployments[0].FromDigests["app"] != "sha256:old" {
		t.Errorf("FromDigests[app] = %q, want sha256:old", deployments[0].FromDigests["app"])
	}
	if deployments[0].ToDigests["app"] != "sha256:new" {
		t.Errorf("ToDigests[app] = %q, want sha256:new", deployments[0].ToDigests["app"])
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

func TestProjectWithServices(t *testing.T) {
	db := openTestDB(t)
	p := &types.Project{
		ID:          "proj-svc",
		DisplayName: "Multi Service",
		ConfigMode:  types.ConfigModeForm,
		Services: []types.ServiceConfig{
			{Name: "app", RegistryImage: "registry.example.com/app", ImageTag: "latest", Polled: true,
				Ports: []types.PortMapping{{HostPort: 8080, ContainerPort: 80}}},
			{Name: "redis", RegistryImage: "redis", ImageTag: "7", Polled: false},
		},
		StackPath:    "/opt/stacks/proj-svc",
		HealthType:   types.HealthHTTP,
		HealthTarget: "http://127.0.0.1:8080/healthz",
		PollInterval: 300,
		Enabled:      true,
	}

	if err := db.CreateProject(p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	got, err := db.GetProject("proj-svc")
	if err != nil {
		t.Fatalf("GetProject: %v", err)
	}
	if len(got.Services) != 2 {
		t.Fatalf("Services len = %d, want 2", len(got.Services))
	}
	if got.Services[0].Name != "app" || got.Services[0].RegistryImage != "registry.example.com/app" {
		t.Errorf("Services[0] = %+v", got.Services[0])
	}
	if !got.Services[0].Polled {
		t.Error("Services[0].Polled should be true")
	}
	if got.Services[1].Name != "redis" || got.Services[1].Polled {
		t.Errorf("Services[1] = %+v", got.Services[1])
	}
	if len(got.Services[0].Ports) != 1 || got.Services[0].Ports[0].HostPort != 8080 {
		t.Errorf("Services[0].Ports = %v", got.Services[0].Ports)
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
