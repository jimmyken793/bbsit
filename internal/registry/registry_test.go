package registry

import "testing"

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
