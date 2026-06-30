// Package backup orchestrates application-aware project backups: it execs the
// project's configured backup_command inside the target service's container,
// finds the resulting file in the host-visible backups directory, and records
// the run in the database. The actual long-term storage of backup files is
// out of scope — operators are expected to ship them off-host with their tool
// of choice (restic, rclone, borg, etc.).
package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/runtime"
	"github.com/kingyoung/bbsit/internal/types"
)

// Service is the entry point for backup/restore operations on projects.
type Service struct {
	db      *db.DB
	runner  *runtime.Runner
	log     *slog.Logger
	locks   sync.Map // project_id -> *sync.Mutex (one in-flight backup/restore per project)
}

func New(database *db.DB, r *runtime.Runner, log *slog.Logger) *Service {
	return &Service{db: database, runner: r, log: log}
}

func (s *Service) lockFor(projectID string) *sync.Mutex {
	v, _ := s.locks.LoadOrStore(projectID, &sync.Mutex{})
	return v.(*sync.Mutex)
}

// Run executes the project's backup_command inside the target service. The
// command is expected to drop a file under the configured output_path
// (mounted to {StackPath}/backups on the host); Run waits for it, computes a
// sha256, and records the result in backup_runs. Trigger is a free-form tag
// (e.g. "manual", "verify") for the audit trail.
func (s *Service) Run(ctx context.Context, p *types.Project, trigger string) (*types.BackupRun, error) {
	if p.Backup == nil {
		return nil, fmt.Errorf("project %s has no backup spec", p.ID)
	}
	mu := s.lockFor(p.ID)
	if !mu.TryLock() {
		return nil, fmt.Errorf("project %s: backup or restore already in progress", p.ID)
	}
	defer mu.Unlock()

	hostDir := p.BackupHostDir()
	if err := os.MkdirAll(hostDir, 0750); err != nil {
		return nil, fmt.Errorf("ensure backup dir: %w", err)
	}

	running, err := s.runner.IsServiceRunning(ctx, p.StackPath, p.Backup.Service)
	if err != nil {
		return nil, fmt.Errorf("check service running: %w", err)
	}
	if !running {
		return nil, fmt.Errorf("service %s is not running — start the project before backup", p.Backup.Service)
	}

	now := time.Now().UTC()
	run := &types.BackupRun{
		ProjectID: p.ID,
		Status:    types.BackupInProgress,
		Trigger:   trigger,
		StartedAt: now,
	}
	runID, err := s.db.InsertBackupRun(run)
	if err != nil {
		return nil, fmt.Errorf("insert backup run: %w", err)
	}
	run.ID = runID

	timeout := time.Duration(p.Backup.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = time.Hour
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	s.log.Info("backup start",
		"project", p.ID, "run", runID, "service", p.Backup.Service)

	startWatermark := now

	res, execErr := s.runner.Exec(execCtx, runtime.ExecOptions{
		StackPath: p.StackPath,
		Service:   p.Backup.Service,
		User:      p.Backup.User,
		Command:   p.Backup.BackupCommand,
	})
	if execErr != nil {
		s.db.FinishBackupRun(runID, types.BackupFailed, "", "", 0, execErr.Error())
		s.log.Error("backup exec failed",
			"project", p.ID, "run", runID, "error", execErr,
			"stdout", res.Stdout, "stderr", res.Stderr)
		return run, fmt.Errorf("backup command failed: %w", execErr)
	}

	pattern := p.Backup.OutputPattern
	if pattern == "" {
		pattern = "*"
	}
	file, err := newestMatch(hostDir, pattern, startWatermark)
	if err != nil {
		s.db.FinishBackupRun(runID, types.BackupFailed, "", "", 0, err.Error())
		return run, fmt.Errorf("locate backup output: %w", err)
	}
	if file == "" {
		msg := fmt.Sprintf("no file matching %q in %s after backup command", pattern, hostDir)
		s.db.FinishBackupRun(runID, types.BackupFailed, "", "", 0, msg)
		return run, fmt.Errorf("%s", msg)
	}

	sum, size, err := hashFile(file)
	if err != nil {
		s.db.FinishBackupRun(runID, types.BackupFailed, file, "", 0, err.Error())
		return run, fmt.Errorf("hash backup file: %w", err)
	}

	if err := s.db.FinishBackupRun(runID, types.BackupSuccess, file, sum, size, ""); err != nil {
		return run, fmt.Errorf("record backup run: %w", err)
	}
	end := time.Now().UTC()
	run.EndedAt = &end
	run.Status = types.BackupSuccess
	run.FilePath = file
	run.SHA256 = sum
	run.Bytes = size
	s.log.Info("backup done",
		"project", p.ID, "run", runID, "file", file, "bytes", size,
		"elapsed", end.Sub(now).Round(time.Millisecond))
	return run, nil
}

// List returns the backup files currently sitting in {StackPath}/backups,
// ordered newest first. It does NOT consult backup_runs — operators may also
// drop files into this dir manually (e.g. fetched from cold storage).
func (s *Service) List(p *types.Project) ([]types.BackupFile, error) {
	hostDir := p.BackupHostDir()
	pattern := "*"
	if p.Backup != nil && p.Backup.OutputPattern != "" {
		pattern = p.Backup.OutputPattern
	}
	entries, err := filepath.Glob(filepath.Join(hostDir, pattern))
	if err != nil {
		return nil, err
	}
	var out []types.BackupFile
	for _, path := range entries {
		fi, err := os.Stat(path)
		if err != nil || fi.IsDir() {
			continue
		}
		out = append(out, types.BackupFile{
			Name:     filepath.Base(path),
			Path:     path,
			Bytes:    fi.Size(),
			Modified: fi.ModTime().UTC(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	return out, nil
}

// History returns recent backup runs from the audit table.
func (s *Service) History(projectID string, limit int) ([]types.BackupRun, error) {
	return s.db.ListBackupRuns(projectID, limit)
}

// newestMatch returns the path of the newest file in dir matching pattern with
// modification time >= since (so we don't pick up a stale file from a previous
// run). Returns "" if no match.
func newestMatch(dir, pattern string, since time.Time) (string, error) {
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return "", err
	}
	var best string
	var bestMtime time.Time
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || fi.IsDir() {
			continue
		}
		// Allow a small clock skew: treat 5s before backup start as "recent"
		// so backups whose mtime sits at exactly the start instant aren't lost.
		if fi.ModTime().Before(since.Add(-5 * time.Second)) {
			continue
		}
		if fi.ModTime().After(bestMtime) {
			best = m
			bestMtime = fi.ModTime()
		}
	}
	return best, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
