package backup

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/runtime"
	"github.com/kingyoung/bbsit/internal/types"
)

// RestoreOptions controls a single restore invocation.
type RestoreOptions struct {
	// File is the host-side path to a backup file. May be inside the project's
	// backup dir, or anywhere reachable by bbsit (we copy it into the dir
	// before invoking restore_command).
	File string
	// AsProjectID, when non-empty, requests a "verify restore": clone the
	// project under a new ID with reassigned random ports, deploy it fresh,
	// then run restore_command. Used to smoke-test backups without touching
	// the production project.
	AsProjectID string
}

// Restore runs the project's restore_command. If opts.AsProjectID is set we
// clone the project to a fresh stack first; otherwise we restore in place.
//
// The deployer is required for clone deploy + start. It may be nil if the
// caller will only ever do in-place restores.
func (s *Service) Restore(ctx context.Context, p *types.Project, opts RestoreOptions, dep *deployer.Deployer) (*types.Project, error) {
	if p.Backup == nil {
		return nil, fmt.Errorf("project %s has no backup spec", p.ID)
	}
	if p.Backup.RestoreCommand == "" {
		return nil, fmt.Errorf("project %s has no restore_command configured", p.ID)
	}
	if opts.File == "" {
		return nil, fmt.Errorf("backup file path required")
	}

	target := p
	if opts.AsProjectID != "" {
		if dep == nil {
			return nil, fmt.Errorf("--as restore requires deployer")
		}
		clone, err := s.cloneForRestore(p, opts.AsProjectID)
		if err != nil {
			return nil, err
		}
		// Persist the clone, deploy fresh stack so volumes (including the
		// auto-injected backups mount) are wired up.
		if _, dbErr := s.db.GetProject(clone.ID); dbErr == nil {
			return nil, fmt.Errorf("project %s already exists — choose another --as id", clone.ID)
		}
		if err := s.db.CreateProject(clone); err != nil {
			return nil, fmt.Errorf("create clone: %w", err)
		}
		if err := dep.Start(clone); err != nil {
			return nil, fmt.Errorf("start clone: %w", err)
		}
		target = clone
	}

	mu := s.lockFor(target.ID)
	if !mu.TryLock() {
		return target, fmt.Errorf("project %s: backup or restore already in progress", target.ID)
	}
	defer mu.Unlock()

	hostDir := target.BackupHostDir()
	if err := os.MkdirAll(hostDir, 0750); err != nil {
		return target, fmt.Errorf("ensure backup dir: %w", err)
	}

	// Materialise the file inside the target's backups dir so the container
	// can see it via the auto-injected mount. If the file already lives there,
	// skip the copy.
	srcAbs, err := filepath.Abs(opts.File)
	if err != nil {
		return target, fmt.Errorf("resolve file: %w", err)
	}
	dstName := filepath.Base(srcAbs)
	dst := filepath.Join(hostDir, dstName)
	if srcAbs != dst {
		if err := copyFile(srcAbs, dst); err != nil {
			return target, fmt.Errorf("copy backup file into stack: %w", err)
		}
	}
	containerPath := filepath.Join(target.Backup.OutputPath, dstName)

	timeout := time.Duration(target.Backup.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = time.Hour
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	running, err := s.runner.IsServiceRunning(execCtx, target.StackPath, target.Backup.Service)
	if err != nil {
		return target, fmt.Errorf("check service running: %w", err)
	}
	if !running {
		return target, fmt.Errorf("service %s is not running — start it before restore", target.Backup.Service)
	}

	s.log.Info("restore start",
		"project", target.ID, "service", target.Backup.Service,
		"file", containerPath, "as_clone", opts.AsProjectID != "")

	res, err := s.runner.Exec(execCtx, runtime.ExecOptions{
		StackPath: target.StackPath,
		Service:   target.Backup.Service,
		User:      target.Backup.User,
		Env:       map[string]string{"BBSIT_BACKUP_FILE": containerPath},
		Command:   target.Backup.RestoreCommand,
	})
	if err != nil {
		s.log.Error("restore exec failed",
			"project", target.ID, "error", err,
			"stdout", res.Stdout, "stderr", res.Stderr)
		return target, fmt.Errorf("restore command failed: %w", err)
	}
	s.log.Info("restore done", "project", target.ID)
	return target, nil
}

// cloneForRestore deep-copies the project, swaps in a new ID, places it next
// to the source stack, and reassigns every host port to a random free port.
// Tunnel publish bindings are stripped: the clone is a throwaway test
// instance and shouldn't fight the original for traffic.
func (s *Service) cloneForRestore(src *types.Project, newID string) (*types.Project, error) {
	if newID == src.ID {
		return nil, fmt.Errorf("--as id must differ from source project id")
	}
	if src.StackPath == "" {
		return nil, fmt.Errorf("source project stack_path required for --as restore")
	}
	clone := *src
	clone.ID = newID
	clone.DisplayName = src.DisplayName + " (restore-verify)"
	clone.StackPath = filepath.Join(filepath.Dir(src.StackPath), newID)
	clone.IsSystem = false
	clone.CreatedAt = time.Time{}
	clone.UpdatedAt = time.Time{}

	// Deep-copy services and reassign host ports.
	clone.Services = make([]types.ServiceConfig, len(src.Services))
	for i, svc := range src.Services {
		// Copy ports
		ports := make([]types.PortMapping, len(svc.Ports))
		for j, pm := range svc.Ports {
			port, err := freePort()
			if err != nil {
				return nil, fmt.Errorf("allocate free port: %w", err)
			}
			pm.HostPort = strconv.Itoa(port)
			ports[j] = pm
		}
		// Copy volumes, but swap the auto-injected backups mount for a fresh
		// relative path so it lands under the clone's stack dir.
		vols := make([]types.VolumeMount, len(svc.Volumes))
		copy(vols, svc.Volumes)
		clone.Services[i] = svc
		clone.Services[i].Ports = ports
		clone.Services[i].Volumes = vols
		clone.Services[i].PublicHostnames = nil // never publish a verify clone
	}

	// Deep-copy maps so the clone doesn't share state with the original.
	if src.EnvVars != nil {
		clone.EnvVars = make(map[string]string, len(src.EnvVars))
		for k, v := range src.EnvVars {
			clone.EnvVars[k] = v
		}
	}
	if src.Backup != nil {
		bClone := *src.Backup
		clone.Backup = &bClone
	}

	return &clone, nil
}

// freePort asks the kernel for an unused TCP port by listening on :0 and
// reading the chosen port. There is a small TOCTOU window between Close and
// the new container binding the port — for a one-off verify deploy this is
// acceptable; the container will fail to start and the operator can retry.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0750); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0640)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
