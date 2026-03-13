package deployer

import (
	"bufio"
	"fmt"
	"io"
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
	db        *db.DB
	locks     sync.Map // project_id -> *sync.Mutex
	log       *slog.Logger
	listeners []DeployListener
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

func (d *Deployer) AddListener(l DeployListener) {
	d.listeners = append(d.listeners, l)
}

func (d *Deployer) emit(e Event) {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	for _, l := range d.listeners {
		l.OnEvent(e)
	}
}

// Deploy executes a full deployment transaction for a project.
// targetDigests maps service name -> target digest for each polled service.
// Returns nil on success, error on failure (rollback will have been attempted).
func (d *Deployer) Deploy(p *types.Project, targetDigests map[string]string, trigger types.DeployTrigger) error {
	mu := d.getLock(p.ID)
	if !mu.TryLock() {
		return fmt.Errorf("project %s: deployment already in progress", p.ID)
	}
	defer mu.Unlock()

	log := d.log.With("project", p.ID)

	// Get current state
	state, err := d.db.GetState(p.ID)
	if err != nil {
		return fmt.Errorf("get state: %w", err)
	}

	// Record deployment start
	deploy := &types.Deployment{
		ProjectID:   p.ID,
		FromDigests: copyDigestMap(state.CurrentDigests),
		ToDigests:   copyDigestMap(targetDigests),
		Status:      types.DeployInProgress,
		Trigger:     trigger,
		StartedAt:   time.Now().UTC(),
	}
	deployID, err := d.db.InsertDeployment(deploy)
	if err != nil {
		return fmt.Errorf("insert deployment: %w", err)
	}

	// Update state to deploying
	state.Status = types.StatusDeploying
	state.DesiredDigests = copyDigestMap(targetDigests)
	d.db.UpdateState(state)
	d.emit(Event{Type: EventStateChange, ProjectID: p.ID, Status: string(types.StatusDeploying)})

	// Execute deployment
	log.Info("starting deployment")
	deployErr := d.executeDeploy(p, targetDigests, log)

	if deployErr == nil {
		// Health checks: stack-level default first, then per-service overrides
		log.Info("running health check")
		d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "health_check"})
		deployErr = d.runHealthChecks(p)
		if deployErr != nil {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "health_check", Error: true, Message: deployErr.Error()})
		} else {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "health_check"})
		}
	}

	if deployErr != nil {
		// Deployment failed — attempt rollback
		log.Error("deployment failed, attempting rollback", "error", deployErr)
		d.db.FinishDeployment(deployID, types.DeployFailed, deployErr.Error())

		d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "rollback"})
		rollbackErr := d.executeRollback(p, state.CurrentDigests, log)
		if rollbackErr != nil {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "rollback", Error: true, Message: rollbackErr.Error()})
			log.Error("rollback also failed", "error", rollbackErr)
			state.Status = types.StatusFailed
			state.LastError = fmt.Sprintf("deploy: %v; rollback: %v", deployErr, rollbackErr)
		} else {
			d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "rollback"})
			state.Status = types.StatusRolledBack
			state.LastError = fmt.Sprintf("deploy failed: %v (rolled back)", deployErr)
			d.db.FinishDeployment(deployID, types.DeployRolledBack, deployErr.Error())
		}

		d.emit(Event{Type: EventStateChange, ProjectID: p.ID, Status: string(state.Status)})
		now := time.Now().UTC()
		state.LastDeployAt = &now
		d.db.UpdateState(state)
		d.emit(Event{Type: EventDeployDone, ProjectID: p.ID, Status: string(state.Status), Error: true, Message: deployErr.Error()})
		return deployErr
	}

	// Success
	log.Info("deployment succeeded")
	d.db.FinishDeployment(deployID, types.DeploySuccess, "")

	now := time.Now().UTC()
	state.PreviousDigests = copyDigestMap(state.CurrentDigests)
	state.CurrentDigests = copyDigestMap(targetDigests)
	state.Status = types.StatusRunning
	state.LastDeployAt = &now
	state.LastSuccessAt = &now
	state.LastError = ""
	d.db.UpdateState(state)

	d.emit(Event{Type: EventStateChange, ProjectID: p.ID, Status: string(types.StatusRunning)})
	d.emit(Event{Type: EventDeployDone, ProjectID: p.ID, Status: string(types.StatusRunning)})

	return nil
}

func (d *Deployer) executeDeploy(p *types.Project, digests map[string]string, log *slog.Logger) error {
	logFn := func(line string, isErr bool) {
		d.emit(Event{Type: EventLog, ProjectID: p.ID, Message: line, Error: isErr})
	}

	// Build image overrides: service name -> full image@digest ref
	imageOverrides := buildImageOverrides(p, digests)

	if err := WriteComposeFiles(p, imageOverrides); err != nil {
		return fmt.Errorf("write compose files: %w", err)
	}

	log.Info("pulling images")
	d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "pull"})
	if err := composeCmd(p.StackPath, logFn, "pull"); err != nil {
		d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "pull", Error: true, Message: err.Error()})
		return fmt.Errorf("compose pull: %w", err)
	}
	d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "pull"})

	log.Info("bringing up stack")
	d.emit(Event{Type: EventStepStart, ProjectID: p.ID, Step: "up"})
	if err := composeCmd(p.StackPath, logFn, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
		d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "up", Error: true, Message: err.Error()})
		return fmt.Errorf("compose up: %w", err)
	}
	d.emit(Event{Type: EventStepDone, ProjectID: p.ID, Step: "up"})

	return nil
}

func (d *Deployer) executeRollback(p *types.Project, prevDigests map[string]string, log *slog.Logger) error {
	logFn := func(line string, isErr bool) {
		d.emit(Event{Type: EventLog, ProjectID: p.ID, Message: line, Error: isErr})
	}

	if len(prevDigests) == 0 {
		return fmt.Errorf("no previous digests to rollback to")
	}

	log.Info("rolling back")

	imageOverrides := buildImageOverrides(p, prevDigests)
	if err := WriteComposeFiles(p, imageOverrides); err != nil {
		return fmt.Errorf("write rollback compose: %w", err)
	}

	if err := composeCmd(p.StackPath, logFn, "up", "-d", "--force-recreate", "--remove-orphans"); err != nil {
		return fmt.Errorf("compose up rollback: %w", err)
	}

	if err := d.runHealthChecks(p); err != nil {
		return fmt.Errorf("health check after rollback: %w", err)
	}

	return nil
}

// runHealthChecks runs stack-level and per-service health checks.
func (d *Deployer) runHealthChecks(p *types.Project) error {
	// Stack-level health check
	if p.HealthType != types.HealthNone && p.HealthType != "" {
		if err := health.Check(p.HealthType, p.HealthTarget, 30*time.Second, 3); err != nil {
			return err
		}
	}
	// Per-service health checks
	for _, svc := range p.Services {
		if svc.HealthType != "" && svc.HealthType != types.HealthNone {
			if err := health.Check(svc.HealthType, svc.HealthTarget, 30*time.Second, 3); err != nil {
				return fmt.Errorf("service %s health check: %w", svc.Name, err)
			}
		}
	}
	return nil
}

// buildImageOverrides constructs the service -> image@digest map for compose override.
func buildImageOverrides(p *types.Project, digests map[string]string) map[string]string {
	if len(digests) == 0 {
		return nil
	}
	overrides := make(map[string]string)
	for _, svc := range p.Services {
		if digest, ok := digests[svc.Name]; ok && digest != "" {
			overrides[svc.Name] = fmt.Sprintf("%s@%s", svc.RegistryImage, digest)
		}
	}
	if len(overrides) == 0 {
		return nil
	}
	return overrides
}

// ManualRollback allows rolling back via Web UI / CLI
func (d *Deployer) ManualRollback(p *types.Project) error {
	state, err := d.db.GetState(p.ID)
	if err != nil {
		return err
	}
	if len(state.PreviousDigests) == 0 {
		return fmt.Errorf("no previous version to rollback to")
	}
	return d.Deploy(p, state.PreviousDigests, types.TriggerManual)
}

// Stop stops a project's compose stack
func (d *Deployer) Stop(p *types.Project) error {
	if err := composeCmd(p.StackPath, nil, "down"); err != nil {
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
	if err := WriteComposeFiles(p, nil); err != nil {
		return err
	}
	if err := composeCmd(p.StackPath, nil, "up", "-d"); err != nil {
		return err
	}
	state, _ := d.db.GetState(p.ID)
	if state != nil {
		state.Status = types.StatusRunning
		d.db.UpdateState(state)
	}
	return nil
}

func composeCmd(stackPath string, logFn func(line string, isErr bool), args ...string) error {
	composeFile := stackPath + "/compose.yaml"
	overridePath := stackPath + "/compose.override.yaml"

	fileArgs := []string{"-f", composeFile}
	if fileExists(overridePath) {
		fileArgs = append(fileArgs, "-f", overridePath)
	}

	fullArgs := []string{"compose"}
	fullArgs = append(fullArgs, fileArgs...)
	fullArgs = append(fullArgs, args...)
	cmd := exec.Command("docker", fullArgs...)
	cmd.Dir = stackPath

	if logFn == nil {
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %s", strings.Join(args, " "), string(out))
		}
		return nil
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start: %w", err)
	}

	var wg sync.WaitGroup
	scanLines := func(r io.Reader, isErr bool) {
		defer wg.Done()
		scanDockerOutput(r, func(line string) { logFn(line, isErr) })
	}
	wg.Add(2)
	go scanLines(stdout, false)
	go scanLines(stderr, true)
	wg.Wait()

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// scanDockerOutput reads lines from r, cleans docker control characters,
// deduplicates consecutive identical lines, and calls fn for each unique line.
// Docker Compose packs multiple layer updates into a single \n-line separated
// by \r, so we split on \r first to process each segment individually.
func scanDockerOutput(r io.Reader, fn func(string)) {
	scanner := bufio.NewScanner(r)
	var prevLine string
	for scanner.Scan() {
		segments := strings.Split(scanner.Text(), "\r")
		for _, seg := range segments {
			line := stripANSI(seg)
			if line == "" || line == prevLine {
				continue
			}
			prevLine = line
			fn(line)
		}
	}
}

// stripANSI removes ANSI escape sequences and trims whitespace.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until we find the terminating letter (@ through ~)
			i += 2
			for i < len(s) && (s[i] < '@' || s[i] > '~') {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return strings.TrimSpace(b.String())
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

func copyDigestMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}
