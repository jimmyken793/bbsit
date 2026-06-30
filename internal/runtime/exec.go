// Package runtime wraps the container runtime binary (podman or docker) with
// helpers used by features that need to act on running containers — mainly
// `compose exec` for application-aware backups.
package runtime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Runner runs commands against a project's stack via `<runtime> compose ...`.
//
// The deployer already speaks compose for deploy/start/stop. Runner adds the
// missing piece — `compose exec` for invoking commands inside running service
// containers. It deliberately mirrors the deployer's compose-file resolution
// (compose.yaml + optional compose.override.yaml) so digest pins are honored
// when execing.
type Runner struct {
	binary string // "docker" or "podman"
}

func New(binary string) *Runner {
	return &Runner{binary: binary}
}

// Binary returns the underlying runtime binary name.
func (r *Runner) Binary() string { return r.binary }

// ExecOptions controls a single `compose exec` invocation.
type ExecOptions struct {
	StackPath string            // dir containing compose.yaml (and optional compose.override.yaml)
	Service   string            // compose service name
	User      string            // optional --user; e.g. "git", "1000:1000"
	Env       map[string]string // extra environment variables passed to the exec'd process
	Shell     string            // shell to invoke; default "sh"
	Command   string            // shell command to run (passed to "sh -c <command>")
}

// ExecResult captures output and exit code of an exec call.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Exec runs a shell command inside a project's compose service. It blocks
// until the command finishes or ctx is canceled. Stdout/stderr are buffered
// and returned together with the exit code; non-zero exit returns an error
// whose message includes the trimmed stderr for human-readable reporting.
func (r *Runner) Exec(ctx context.Context, opts ExecOptions) (*ExecResult, error) {
	if opts.StackPath == "" {
		return nil, fmt.Errorf("exec: stack_path required")
	}
	if opts.Service == "" {
		return nil, fmt.Errorf("exec: service required")
	}
	if opts.Command == "" {
		return nil, fmt.Errorf("exec: command required")
	}
	shell := opts.Shell
	if shell == "" {
		shell = "sh"
	}

	args := []string{"compose", "-f", filepath.Join(opts.StackPath, "compose.yaml")}
	override := filepath.Join(opts.StackPath, "compose.override.yaml")
	if fileExists(override) {
		args = append(args, "-f", override)
	}
	// -T disables TTY allocation so this is safe when called from a daemon
	// without a controlling terminal (and from HTTP handlers).
	args = append(args, "exec", "-T")
	if opts.User != "" {
		args = append(args, "--user", opts.User)
	}
	for k, v := range opts.Env {
		args = append(args, "--env", k+"="+v)
	}
	args = append(args, opts.Service, shell, "-c", opts.Command)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Dir = opts.StackPath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	exitCode := -1
	if cmd.ProcessState != nil {
		exitCode = cmd.ProcessState.ExitCode()
	}
	res := &ExecResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}
	if runErr != nil {
		// Distinguish exec startup failures from non-zero exits.
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return res, fmt.Errorf("%s exited %d: %s",
				opts.Service, res.ExitCode, trimErr(res.Stderr))
		}
		return res, fmt.Errorf("compose exec %s: %w (%s)",
			opts.Service, runErr, trimErr(res.Stderr))
	}
	return res, nil
}

// IsServiceRunning reports whether the named service has at least one running
// container under the project's compose stack. Used as a precondition check
// before backup/restore so we surface a clear error instead of an exec failure.
func (r *Runner) IsServiceRunning(ctx context.Context, stackPath, service string) (bool, error) {
	if stackPath == "" || service == "" {
		return false, fmt.Errorf("stack_path and service required")
	}
	args := []string{"compose", "-f", filepath.Join(stackPath, "compose.yaml")}
	override := filepath.Join(stackPath, "compose.override.yaml")
	if fileExists(override) {
		args = append(args, "-f", override)
	}
	// `compose ps -q <svc>` prints container IDs (one per line); empty = not running.
	args = append(args, "ps", "-q", service)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	cmd.Dir = stackPath
	out, err := cmd.Output()
	if err != nil {
		return false, fmt.Errorf("compose ps: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func trimErr(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
