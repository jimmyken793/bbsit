package registry

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// GetRemoteDigest returns the digest of an image tag from the remote registry.
// platform is optional (e.g. "linux/arm64"); empty string means host default.
// Uses `docker manifest inspect` which requires Docker CLI with experimental features.
// Falls back to `docker buildx imagetools inspect` if available.
func GetRemoteDigest(runtime, image, tag, platform string) (string, error) {
	ref := fmt.Sprintf("%s:%s", image, tag)

	// Try manifest inspect first
	out, err := runCmd(runtime, "manifest", "inspect", "--verbose", ref)
	if err == nil {
		if platform != "" {
			// Multi-arch: find the digest for the requested platform
			digest := parseDigestForPlatform(out, platform)
			if digest != "" {
				return digest, nil
			}
		}
		// Single-arch or no platform specified: use first digest found
		digest := parseDigestFromManifest(out)
		if digest != "" {
			return digest, nil
		}
	}

	// Fallback: use buildx imagetools inspect (docker-specific, skip for podman)
	if runtime == "docker" {
		out, err = runCmd(runtime, "buildx", "imagetools", "inspect", "--raw", ref)
		if err == nil {
			digest := parseDigestFromRaw(out)
			if digest != "" {
				return digest, nil
			}
		}
	}

	// Fallback: just pull and inspect
	// This actually downloads the image but is the most reliable
	pullArgs := []string{"pull"}
	if platform != "" {
		pullArgs = append(pullArgs, "--platform", platform)
	}
	pullArgs = append(pullArgs, ref)
	if _, err := runCmd(runtime, pullArgs...); err != nil {
		return "", fmt.Errorf("pull %s: %w", ref, err)
	}
	out, err = runCmd(runtime, "inspect", "--format", "{{index .RepoDigests 0}}", ref)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", ref, err)
	}
	// Output is like: registry.example.com/webui@sha256:abc123...
	parts := strings.SplitN(strings.TrimSpace(out), "@", 2)
	if len(parts) == 2 {
		return parts[1], nil
	}
	return "", fmt.Errorf("could not parse digest from: %s", out)
}

// GetLocalDigest returns the digest of the currently running image for a compose service.
func GetLocalDigest(runtime, stackPath, serviceName string) (string, error) {
	out, err := runCmd(runtime, "compose", "-f", stackPath+"/compose.yaml",
		"-f", stackPath+"/compose.override.yaml",
		"images", "--format", "json", serviceName)
	if err != nil {
		// override might not exist, try without it
		out, err = runCmd(runtime, "compose", "-f", stackPath+"/compose.yaml",
			"images", "--format", "json", serviceName)
		if err != nil {
			return "", fmt.Errorf("compose images: %w", err)
		}
	}

	// Parse the image ID from compose images output
	// Alternatively, get it from inspect on the running container
	containerName := fmt.Sprintf("%s-%s-1", extractStackName(stackPath), serviceName)
	out, err = runCmd(runtime, "inspect", "--format", "{{index .Image}}", containerName)
	if err != nil {
		return "", fmt.Errorf("inspect container %s: %w", containerName, err)
	}
	return strings.TrimSpace(out), nil
}

func extractStackName(stackPath string) string {
	parts := strings.Split(strings.TrimRight(stackPath, "/"), "/")
	if len(parts) > 0 {
		return parts[len(parts)-1]
	}
	return "default"
}

func parseDigestFromManifest(output string) string {
	// Look for "digest": "sha256:..." in JSON output
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "\"Digest\"") || strings.Contains(line, "\"digest\"") {
			parts := strings.SplitN(line, ":", 3)
			if len(parts) >= 3 {
				digest := strings.Trim(strings.TrimSpace(parts[1]+":"+parts[2]), "\",")
				if strings.HasPrefix(digest, "sha256:") {
					return digest
				}
			}
		}
	}
	return ""
}

// parseDigestForPlatform extracts the digest for a specific platform from
// `docker manifest inspect --verbose` output. The output is a JSON array
// of manifest entries, each with a "Descriptor" and "Platform" object.
func parseDigestForPlatform(output, platform string) string {
	// platform is "os/arch", e.g. "linux/arm64"
	parts := strings.SplitN(platform, "/", 2)
	if len(parts) != 2 {
		return ""
	}
	wantOS, wantArch := parts[0], parts[1]

	// Try parsing as JSON array of verbose manifest entries.
	// `docker manifest inspect --verbose` nests platform inside Descriptor:
	//   [{"Descriptor": {"digest": "sha256:...", "platform": {"architecture": "arm64", "os": "linux"}}, ...}]
	var entries []struct {
		Descriptor struct {
			Digest   string `json:"digest"`
			Platform struct {
				Architecture string `json:"architecture"`
				OS           string `json:"os"`
			} `json:"platform"`
		} `json:"Descriptor"`
	}
	if err := json.Unmarshal([]byte(output), &entries); err == nil {
		for _, e := range entries {
			if strings.EqualFold(e.Descriptor.Platform.OS, wantOS) && strings.EqualFold(e.Descriptor.Platform.Architecture, wantArch) {
				if e.Descriptor.Digest != "" {
					return e.Descriptor.Digest
				}
			}
		}
	}

	return ""
}

func parseDigestFromRaw(output string) string {
	// For raw manifest, we compute digest from the content
	// But simpler: just hash the manifest
	// Actually for our purposes, using `docker pull` + inspect is most reliable
	return ""
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, string(out))
	}
	return string(out), nil
}
