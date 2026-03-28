package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/types"
)

type fakeDeployer struct {
	mu          sync.Mutex
	calls       []deployCall
	returnError error
	applyState  func(p *types.Project, targetDigests map[string]string) // simulate state update
	database    *db.DB
}

type deployCall struct {
	projectID string
	digests   map[string]string
	trigger   types.DeployTrigger
}

func (f *fakeDeployer) Deploy(p *types.Project, targetDigests map[string]string, trigger types.DeployTrigger) error {
	f.mu.Lock()
	cp := make(map[string]string, len(targetDigests))
	for k, v := range targetDigests {
		cp[k] = v
	}
	f.calls = append(f.calls, deployCall{projectID: p.ID, digests: cp, trigger: trigger})
	f.mu.Unlock()
	if f.applyState != nil {
		f.applyState(p, targetDigests)
	}
	return f.returnError
}

func (f *fakeDeployer) Emit(e deployer.Event) {}

func (f *fakeDeployer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeDeployer) lastCall() deployCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func newTestScheduler(t *testing.T, digestFn DigestFunc) (*Scheduler, *db.DB, *fakeDeployer) {
	t.Helper()
	dbPath := t.TempDir() + "/test.db"
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	fake := &fakeDeployer{database: database}
	fake.applyState = func(p *types.Project, targetDigests map[string]string) {
		// Simulate a successful deploy: mark state current = target
		state, err := database.GetState(p.ID)
		if err != nil {
			return
		}
		cp := make(map[string]string, len(targetDigests))
		for k, v := range targetDigests {
			cp[k] = v
		}
		state.PreviousDigests = state.CurrentDigests
		state.CurrentDigests = cp
		state.Status = types.StatusRunning
		database.UpdateState(state)
	}

	s := &Scheduler{
		db:       database,
		deployer: fake,
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		runtime:  "docker",
		digestFn: digestFn,
	}
	return s, database, fake
}

// TestReconcile_MixedPolled verifies that when a project has both polled and
// unpolled services, the polled one is still checked and its updates trigger
// a deployment. Regression test for: "when one container is set to not poll,
// the whole project stops checking for updates".
func TestReconcile_MixedPolled(t *testing.T) {
	digests := map[string]string{
		"registry.example.com/app": "sha256:v1",
	}
	digestFn := func(runtime, image, tag, platform string) (string, error) {
		return digests[image], nil
	}

	s, database, fake := newTestScheduler(t, digestFn)

	p := &types.Project{
		ID:          "mixed",
		DisplayName: "Mixed",
		ConfigMode:  types.ConfigModeForm,
		Services: []types.ServiceConfig{
			{Name: "app", RegistryImage: "registry.example.com/app", ImageTag: "latest", Polled: true},
			{Name: "db", RegistryImage: "postgres", ImageTag: "16", Polled: false},
		},
		StackPath:    "/tmp/stacks/mixed",
		PollInterval: 300,
		Enabled:      true,
	}
	if err := database.CreateProject(p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	// First reconcile: app is new (no current digest) → deploy.
	s.reconcileAll(context.Background(), types.TriggerStartup)

	if fake.callCount() != 1 {
		t.Fatalf("expected 1 Deploy call after first reconcile, got %d", fake.callCount())
	}
	call := fake.lastCall()
	if call.digests["app"] != "sha256:v1" {
		t.Errorf("Deploy target digest for 'app' = %q, want sha256:v1", call.digests["app"])
	}
	if _, hasDB := call.digests["db"]; hasDB {
		t.Errorf("Deploy should not pass a digest for unpolled service 'db': got %v", call.digests)
	}

	// Second reconcile with unchanged remote: no deploy.
	s.reconcileAll(context.Background(), types.TriggerStartup)
	if fake.callCount() != 1 {
		t.Errorf("expected no additional Deploy when remote digest unchanged, got %d total", fake.callCount())
	}

	// Remote digest for 'app' bumps → should trigger deploy.
	digests["registry.example.com/app"] = "sha256:v2"
	s.reconcileAll(context.Background(), types.TriggerStartup)
	if fake.callCount() != 2 {
		t.Fatalf("expected Deploy after 'app' digest bump, got %d total", fake.callCount())
	}
	call = fake.lastCall()
	if call.digests["app"] != "sha256:v2" {
		t.Errorf("second Deploy target digest for 'app' = %q, want sha256:v2", call.digests["app"])
	}
	if _, hasDB := call.digests["db"]; hasDB {
		t.Errorf("Deploy should still not pass a digest for unpolled service 'db': got %v", call.digests)
	}
}

// TestReconcile_AllUnpolled verifies that a project with no polled services is
// skipped entirely (nothing to check).
func TestReconcile_AllUnpolled(t *testing.T) {
	digestFn := func(runtime, image, tag, platform string) (string, error) {
		t.Fatalf("digestFn should not be called when no services are polled")
		return "", nil
	}

	s, database, fake := newTestScheduler(t, digestFn)

	p := &types.Project{
		ID:          "frozen",
		DisplayName: "Frozen",
		ConfigMode:  types.ConfigModeForm,
		Services: []types.ServiceConfig{
			{Name: "app", RegistryImage: "my/app", ImageTag: "v1", Polled: false},
			{Name: "db", RegistryImage: "postgres", ImageTag: "16", Polled: false},
		},
		StackPath:    "/tmp/stacks/frozen",
		PollInterval: 300,
		Enabled:      true,
	}
	if err := database.CreateProject(p); err != nil {
		t.Fatalf("CreateProject: %v", err)
	}

	s.reconcileAll(context.Background(), types.TriggerStartup)
	if fake.callCount() != 0 {
		t.Errorf("expected no Deploy for all-unpolled project, got %d", fake.callCount())
	}
}
