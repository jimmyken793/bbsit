package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/registry"
	"github.com/kingyoung/bbsit/internal/types"
)

type Scheduler struct {
	db       *db.DB
	deployer *deployer.Deployer
	log      *slog.Logger
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

func New(database *db.DB, dep *deployer.Deployer, logger *slog.Logger) *Scheduler {
	return &Scheduler{
		db:       database,
		deployer: dep,
		log:      logger,
	}
}

// Start begins the reconciliation loop.
// It checks each enabled project on its own poll interval.
func (s *Scheduler) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.run(ctx)
	}()

	s.log.Info("scheduler started")
}

func (s *Scheduler) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	s.wg.Wait()
	s.log.Info("scheduler stopped")
}

func (s *Scheduler) run(ctx context.Context) {
	// Main tick: check all projects every 30 seconds.
	// Each project has its own poll_interval; we only act if enough time has passed.
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run once immediately on startup
	s.reconcileAll(ctx, types.TriggerStartup)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.reconcileAll(ctx, types.TriggerPoll)
		}
	}
}

func (s *Scheduler) reconcileAll(ctx context.Context, trigger types.DeployTrigger) {
	projects, err := s.db.ListProjects()
	if err != nil {
		s.log.Error("list projects", "error", err)
		return
	}

	for _, p := range projects {
		if ctx.Err() != nil {
			return
		}
		if !p.Enabled {
			continue
		}
		if len(p.PolledServices()) == 0 {
			continue
		}

		state, err := s.db.GetState(p.ID)
		if err != nil {
			s.log.Error("get state", "project", p.ID, "error", err)
			continue
		}

		// Check if enough time has passed since last check
		if trigger == types.TriggerPoll && state.LastCheckAt != nil {
			elapsed := time.Since(*state.LastCheckAt)
			if elapsed < time.Duration(p.PollInterval)*time.Second {
				continue
			}
		}

		s.reconcileOne(ctx, &p, state, trigger)
	}
}

func (s *Scheduler) reconcileOne(ctx context.Context, p *types.Project, state *types.ProjectState, trigger types.DeployTrigger) {
	log := s.log.With("project", p.ID)

	// Update last check time
	now := time.Now().UTC()
	state.LastCheckAt = &now
	s.db.UpdateState(state)

	// Initialize desired digests map if nil
	if state.DesiredDigests == nil {
		state.DesiredDigests = make(map[string]string)
	}
	if state.CurrentDigests == nil {
		state.CurrentDigests = make(map[string]string)
	}

	// Poll each service with Polled: true
	changed := false
	for _, svc := range p.PolledServices() {
		tag := svc.ImageTag
		if tag == "" {
			tag = "latest"
		}
		remoteDigest, err := registry.GetRemoteDigest(svc.RegistryImage, tag)
		if err != nil {
			log.Error("check remote digest", "service", svc.Name, "error", err)
			state.LastError = fmt.Sprintf("service %s: %v", svc.Name, err)
			s.db.UpdateState(state)
			return
		}

		state.DesiredDigests[svc.Name] = remoteDigest
		if remoteDigest != state.CurrentDigests[svc.Name] {
			changed = true
			log.Info("new version detected",
				"service", svc.Name,
				"current", deployer.ShortDigest(state.CurrentDigests[svc.Name]),
				"new", deployer.ShortDigest(remoteDigest))
		}
	}

	s.db.UpdateState(state)

	if !changed {
		log.Debug("all services up to date")
		return
	}

	if state.Status == types.StatusDeploying {
		log.Warn("skipping: deployment already in progress")
		return
	}

	// Any service changed → full stack redeploy with all desired digests
	if err := s.deployer.Deploy(p, state.DesiredDigests, trigger); err != nil {
		log.Error("deployment failed", "error", err)
	}
}

// TriggerManualReconcile forces an immediate check & deploy for a project.
func (s *Scheduler) TriggerManualReconcile(projectID string) error {
	p, err := s.db.GetProject(projectID)
	if err != nil {
		return err
	}
	state, err := s.db.GetState(projectID)
	if err != nil {
		return err
	}
	s.reconcileOne(context.Background(), p, state, types.TriggerManual)
	return nil
}
