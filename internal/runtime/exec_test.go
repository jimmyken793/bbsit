package runtime

import (
	"context"
	"path/filepath"
	"testing"
)

func TestExecStartupFailureDoesNotPanic(t *testing.T) {
	r := New(filepath.Join(t.TempDir(), "missing-runtime"))

	res, err := r.Exec(context.Background(), ExecOptions{
		StackPath: t.TempDir(),
		Service:   "web",
		Command:   "true",
	})
	if err == nil {
		t.Fatal("expected startup error")
	}
	if res == nil {
		t.Fatal("expected result even on startup error")
	}
	if res.ExitCode != -1 {
		t.Errorf("ExitCode = %d, want -1 for startup failure", res.ExitCode)
	}
}
