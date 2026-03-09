package deployer

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
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

func TestScanDockerOutput(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "plain lines pass through",
			input: "line1\nline2\nline3\n",
			want:  []string{"line1", "line2", "line3"},
		},
		{
			name:  "consecutive duplicates collapsed",
			input: "Already exists\nAlready exists\nAlready exists\nPull complete\n",
			want:  []string{"Already exists", "Pull complete"},
		},
		{
			name:  "non-consecutive duplicates kept",
			input: "aaa\nbbb\naaa\n",
			want:  []string{"aaa", "bbb", "aaa"},
		},
		{
			name:  "empty and whitespace-only lines skipped",
			input: "hello\n\n  \nworld\n",
			want:  []string{"hello", "world"},
		},
		{
			name:  "ANSI codes stripped and duplicates collapsed",
			input: "\x1b[31mError\x1b[0m\n\x1b[31mError\x1b[0m\nOK\n",
			want:  []string{"Error", "OK"},
		},
		{
			name:  "CR-separated segments each emitted",
			input: "layer1 Pulling\rlayer2 Pulling\rlayer3 Pulling\n",
			want:  []string{"layer1 Pulling", "layer2 Pulling", "layer3 Pulling"},
		},
		{
			name:  "CR duplicate segments collapsed",
			input: "abc Downloading 10%\rabc Downloading 10%\rabc Downloading 50%\n",
			want:  []string{"abc Downloading 10%", "abc Downloading 50%"},
		},
		{
			name: "docker pull simulation",
			input: "abc123 Pulling fs layer\nabc123 Pulling fs layer\nabc123 Pulling fs layer\n" +
				"abc123 Downloading [=>  ] 1MB/10MB\nabc123 Downloading [=>  ] 1MB/10MB\n" +
				"abc123 Download complete\nabc123 Pull complete\n",
			want: []string{
				"abc123 Pulling fs layer",
				"abc123 Downloading [=>  ] 1MB/10MB",
				"abc123 Download complete",
				"abc123 Pull complete",
			},
		},
		{
			name:  "multi-layer CR progress",
			input: "layer1 Downloading 50%\rlayer2 Waiting\rlayer3 Already exists\n",
			want:  []string{"layer1 Downloading 50%", "layer2 Waiting", "layer3 Already exists"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := strings.NewReader(tc.input)
			var got []string
			scanDockerOutput(r, func(line string) {
				got = append(got, line)
			})
			if len(got) != len(tc.want) {
				t.Fatalf("got %d lines %v, want %d lines %v", len(got), got, len(tc.want), tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("line %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

func TestStripANSI(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"plain text", "hello world", "hello world"},
		{"ANSI color codes stripped", "\x1b[31mError\x1b[0m", "Error"},
		{"whitespace trimmed", "  hello  ", "hello"},
		{"progress bar unchanged", "284b41482138 Downloading [==>  ] 1MB/10MB", "284b41482138 Downloading [==>  ] 1MB/10MB"},
		{"erase line code stripped", "\x1b[2KDownloading 100%", "Downloading 100%"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := stripANSI(tc.input)
			if got != tc.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
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
