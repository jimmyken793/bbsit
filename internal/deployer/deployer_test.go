package deployer

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/kingyoung/bbsit/internal/db"
)

func TestShortDigest(t *testing.T) {
	tests := []struct {
		input    string
		want     string
	}{
		// Longer than 19 chars — truncated to first 19
		{"sha256:abc123def456ghijklmnop", "sha256:abc123def456"}, // 7 + 12 = 19
		// Exactly 19 chars — returned as-is
		{"sha256:abcdefghijkl", "sha256:abcdefghijkl"}, // 7 + 12 = 19
		// Shorter than 19 — returned as-is
		{"sha256:abc", "sha256:abc"},
		{"short", "short"},
		{"", ""},
	}
	for _, tc := range tests {
		got := ShortDigest(tc.input)
		if got != tc.want {
			t.Errorf("ShortDigest(%q) = %q, want %q", tc.input, got, tc.want)
		}
		if len(got) > 19 {
			t.Errorf("ShortDigest(%q) length %d exceeds 19", tc.input, len(got))
		}
	}
}

func testDeployer(t *testing.T) *Deployer {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return New(database, logger)
}

func TestNew(t *testing.T) {
	d := testDeployer(t)
	if d == nil {
		t.Fatal("New returned nil")
	}
	if d.db == nil {
		t.Fatal("db is nil")
	}
	if d.log == nil {
		t.Fatal("log is nil")
	}
}

func TestGetLock(t *testing.T) {
	d := testDeployer(t)

	mu1 := d.getLock("project-a")
	mu2 := d.getLock("project-a")
	mu3 := d.getLock("project-b")

	if mu1 != mu2 {
		t.Error("same project should return same mutex")
	}
	if mu1 == mu3 {
		t.Error("different projects should return different mutexes")
	}
}

func TestFileExists(t *testing.T) {
	dir := t.TempDir()

	// Existing file
	existing := filepath.Join(dir, "exists.txt")
	os.WriteFile(existing, []byte("hello"), 0644)
	if !fileExists(existing) {
		t.Error("expected true for existing file")
	}

	// Non-existing file
	if fileExists(filepath.Join(dir, "nope.txt")) {
		t.Error("expected false for non-existing file")
	}

	// Directory also counts
	if !fileExists(dir) {
		t.Error("expected true for existing directory")
	}
}
