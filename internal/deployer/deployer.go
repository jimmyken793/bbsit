package deployer

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/health"
	"github.com/kingyoung/bbsit/internal/types"
)

type Deployer struct {
	db    *db.DB
	locks sync.Map // project_id -> *sync.Mutex
	log   *slog.Logger
}

func New(database *db.DB, logger *slog.Logger) *Deployer {
	return &Deployer{
		db:  database,
		log: logger,
	}
}

func (d *Deployer) getLock(projectID string) *sync.Mutex {
	val, _ := d.locks.LoadOrStore(projectID, &sync.Mutex{})
	return val.(*sync.Mutex)
}

// Deploy executes a full deployment transaction for a project.
// Returns nil on success, error on failure (rollback will have been attempted).
func (d *Deployer) Deploy(p *types.Project, targetDigest string, trigger types.DeployTrigger) error {
	mu := d.getLock(p.ID)
	if !mu.TryLock() {
		return fmt.Errorf("project %s: deployment already in progress", p.ID)
	}
	defer mu.Unlock()

	log := d.log.With("project", p.ID, "target", ShortDigest(targetDigest))

	// Get current state
	state, err := d.db.GetState(p.ID)
	if err != nil {
		return fmt.Errorf("get state: %w", err)
	}

	// Record deployment start
	deploy := &types.Deployment{
		ProjectID:  p.ID,
		FromDigest: state.CurrentDigest,
		ToDigest:   targetDigest,
		Status:     types.DeployInProgress,
		Trigger:    trigger,
		StartedAt:  time.Now().UTC(),
	}
	deployID, err := d.db.InsertDeployment(deploy)
	if err != nil {
		return fmt.Errorf("insert deployment: %w", err)
	}

	// Update state to deploying
	state.Status = types.StatusDeploying
	state.DesiredDigest = targetDigest
	d.db.UpdateState(state)

	// Execute deployment
	log.Info("starting deployment")
	deployErr := d.executeDeploy(p, targetDigest, log)

	if deployErr == nil {
		// Health check
		log.Info("running health check")
		deployErr = health.Check(p.HealthType, p.HealthTarget, 30*time.Second, 3)
	}

	if deployErr != nil {
		// Deployment failed — attempt rollback
		log.Error("deployment failed, attempting rollback", "error", deployErr)
		d.db.FinishDeployment(deployID, types.DeployFailed, deployErr.Error())

		rollbackErr := d.executeRollback(p, state.CurrentDigest, log)
		if rollbackErr != nil {
			log.Error("rollback also failed", "error", rollbackErr)
			state.Status = types.StatusFailed
			state.LastError = fmt.Sprintf("deploy: %v; rollback: %v", deployErr, rollbackErr)
		} else {
			state.Status = types.StatusRolledBack
			state.LastError = fmt.Sprintf("deploy failed: %v (rolled back)", deployErr)
			d.db.FinishDeployment(deployID, types.DeployRolledBack, deployErr.Error())
		}

		now := time.Now().UTC()
		state.LastDeployAt = &now
		d.db.UpdateState(state)
		return deployErr
	}

	// Success
	log.Info("deployment succeeded")
	d.db.FinishDeployment(deployID, types.DeploySuccess, "")

	now := time.Now().UTC()
	state.PreviousDigest = state.CurrentDigest
	state.CurrentDigest = targetDigest
	state.Status = types.StatusRunning
	state.LastDeployAt = &now
	state.LastSuccessAt = &now
	state.LastError = ""
	d.db.UpdateState(state)

	return nil
}

func (d *Deployer) executeDeploy(p *types.Project, digest string, log *slog.Logger) error {
	// Step 1: Write compose files (tag-based, no digest pin).
	// Pulling by tag is reliable for multi-arch images; the digest is only
	// used for change detection, not to specify exactly what to pull.
	if err := WriteComposeFiles(p, ""); err != nil {
		return fmt.Errorf("write compose files: %w", err)
	}

	// Step 2: Pull images by tag
	log.Info("pulling images")
	if err := composeCmd(p.StackPath, "pull"); err != nil {
		return fmt.Errorf("compose pull: %w", err)
	}

	// Step 3: Bring up stack (force-recreate ensures the new image is used)
	log.Info("bringing up stack")
	if err := composeCmd(p.StackPath, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
		return fmt.Errorf("compose up: %w", err)
	}

	return nil
}

func (d *Deployer) executeRollback(p *types.Project, previousDigest string, log *slog.Logger) error {
	if previousDigest == "" {
		return fmt.Errorf("no previous digest to rollback to")
	}

	log.Info("rolling back", "to", ShortDigest(previousDigest))

	imageRef := ""
	if p.ConfigMode == types.ConfigModeForm {
		imageRef = fmt.Sprintf("%s@%s", p.RegistryImage, previousDigest)
	}
	if err := WriteComposeFiles(p, imageRef); err != nil {
		return fmt.Errorf("write rollback compose: %w", err)
	}

	if err := composeCmd(p.StackPath, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
		return fmt.Errorf("compose up rollback: %w", err)
	}

	// Verify health after rollback
	if err := health.Check(p.HealthType, p.HealthTarget, 30*time.Second, 3); err != nil {
		return fmt.Errorf("health check after rollback: %w", err)
	}

	return nil
}

// ManualRollback allows rolling back via Web UI / CLI
func (d *Deployer) ManualRollback(p *types.Project) error {
	state, err := d.db.GetState(p.ID)
	if err != nil {
		return err
	}
	if state.PreviousDigest == "" {
		return fmt.Errorf("no previous version to rollback to")
	}
	return d.Deploy(p, state.PreviousDigest, types.TriggerManual)
}

// Stop stops a project's compose stack
func (d *Deployer) Stop(p *types.Project) error {
	if err := composeCmd(p.StackPath, "down"); err != nil {
		return err
	}
	state, _ := d.db.GetState(p.ID)
	if state != nil {
		state.Status = types.StatusStopped
		d.db.UpdateState(state)
	}
	return nil
}

// Start starts a project's compose stack with current config
func (d *Deployer) Start(p *types.Project) error {
	if err := WriteComposeFiles(p, ""); err != nil {
		return err
	}
	if err := composeCmd(p.StackPath, "up", "-d"); err != nil {
		return err
	}
	state, _ := d.db.GetState(p.ID)
	if state != nil {
		state.Status = types.StatusRunning
		d.db.UpdateState(state)
	}
	return nil
}

func composeCmd(stackPath string, args ...string) error {
	var cmd *exec.Cmd

	composeFile := stackPath + "/compose.yaml"
	overridePath := stackPath + "/compose.override.yaml"

	fileArgs := []string{"-f", composeFile}
	if fileExists(overridePath) {
		fileArgs = append(fileArgs, "-f", overridePath)
	}

	fullArgs := []string{"compose"}
	fullArgs = append(fullArgs, fileArgs...)
	fullArgs = append(fullArgs, args...)
	cmd = exec.Command("docker", fullArgs...)

	cmd.Dir = stackPath
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %s", strings.Join(args, " "), string(out))
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ShortDigest(d string) string {
	if len(d) > 19 { // "sha256:" + 12 chars
		return d[:19]
	}
	return d
}
