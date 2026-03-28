package registry

import (
	"testing"
)

func TestExtractStackName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/opt/stacks/webui", "webui"},
		{"/opt/stacks/webui/", "webui"},
		{"/opt/stacks/my-app", "my-app"},
		{"webui", "webui"},
		{"/single", "single"},
	}
	for _, tc := range tests {
		got := extractStackName(tc.input)
		if got != tc.want {
			t.Errorf("extractStackName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestParseDigestFromManifest(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
	}{
		{
			name: "uppercase Digest field",
			input: `{
  "SchemaV2Manifest": {},
  "Digest": "sha256:abc123def456ghi789jkl012mno345pqr"
}`,
			want: "sha256:abc123def456ghi789jkl012mno345pqr",
		},
		{
			name: "lowercase digest field",
			input: `{
  "digest": "sha256:lowercase123abc456def789"
}`,
			want: "sha256:lowercase123abc456def789",
		},
		{
			name:  "no digest field",
			input: `{"no": "digest here"}`,
			want:  "",
		},
		{
			name:  "empty input",
			input: "",
			want:  "",
		},
		{
			name:  "non-sha256 algorithm ignored",
			input: `{"Digest": "md5:abc123"}`,
			want:  "",
		},
		{
			name: "digest with trailing comma",
			input: `{
  "Digest": "sha256:trailingcomma123456789",
  "other": "field"
}`,
			want: "sha256:trailingcomma123456789",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDigestFromManifest(tc.input)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseDigestForPlatform(t *testing.T) {
	// Simulated `docker manifest inspect --verbose` output for a multi-arch image.
	// Docker nests platform inside Descriptor, not at the top level.
	multiArchOutput := `[
  {
    "Ref": "docker.io/pgvector/pgvector:pg17@sha256:amd64digest123456789",
    "Descriptor": {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:amd64digest123456789",
      "size": 1234,
      "platform": {
        "architecture": "amd64",
        "os": "linux"
      }
    },
    "SchemaV2Manifest": {}
  },
  {
    "Ref": "docker.io/pgvector/pgvector:pg17@sha256:arm64digest987654321",
    "Descriptor": {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:arm64digest987654321",
      "size": 1234,
      "platform": {
        "architecture": "arm64",
        "os": "linux"
      }
    },
    "SchemaV2Manifest": {}
  }
]`

	tests := []struct {
		name     string
		output   string
		platform string
		want     string
	}{
		{
			name:     "find arm64 digest",
			output:   multiArchOutput,
			platform: "linux/arm64",
			want:     "sha256:arm64digest987654321",
		},
		{
			name:     "find amd64 digest",
			output:   multiArchOutput,
			platform: "linux/amd64",
			want:     "sha256:amd64digest123456789",
		},
		{
			name:     "platform not found",
			output:   multiArchOutput,
			platform: "linux/s390x",
			want:     "",
		},
		{
			name:     "invalid platform format",
			output:   multiArchOutput,
			platform: "arm64",
			want:     "",
		},
		{
			name:     "empty output",
			output:   "",
			platform: "linux/arm64",
			want:     "",
		},
		{
			name:     "single-arch manifest (not an array)",
			output:   `{"Descriptor": {"digest": "sha256:single123"}, "Platform": {"architecture": "amd64", "os": "linux"}}`,
			platform: "linux/amd64",
			want:     "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseDigestForPlatform(tc.output, tc.platform)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
