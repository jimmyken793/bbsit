package deployer

import "testing"

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
