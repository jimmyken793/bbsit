package web

import (
	"fmt"
	"sort"

	"github.com/kingyoung/bbsit/internal/types"
)

// redactedSecret is deliberately stable so an edit round-trip can preserve a
// value without the server ever returning the plaintext to the browser or CLI.
const redactedSecret = "********"

func redactedEnvVars(env map[string]string, secretKeys []string) map[string]string {
	if env == nil {
		return nil
	}
	secrets := secretEnvKeySet(secretKeys)
	masked := make(map[string]string, len(env))
	for key, value := range env {
		if secrets[key] {
			value = redactedSecret
		}
		masked[key] = value
	}
	return masked
}

func redactedProject(p *types.Project) *types.Project {
	if p == nil {
		return nil
	}
	clone := *p
	clone.EnvVars = redactedEnvVars(p.EnvVars, p.SecretEnvKeys)
	clone.SecretEnvKeys = append([]string(nil), p.SecretEnvKeys...)
	return &clone
}

// resolveProjectSecrets replaces redaction placeholders with the existing
// secret values. A nil env_vars map on update means "not supplied" and also
// preserves the current set; an explicit empty map removes all variables.
func resolveProjectSecrets(p *types.Project, existing *types.Project) error {
	if p.EnvVars == nil {
		if existing != nil {
			p.EnvVars = copyEnvVars(existing.EnvVars)
		} else {
			p.EnvVars = map[string]string{}
		}
	}

	// A missing classification is backward-compatible and safe by default:
	// preserve existing classifications and mark newly introduced keys secret.
	desiredSecrets := map[string]bool{}
	if p.SecretEnvKeys == nil {
		existingSecrets := map[string]bool{}
		if existing != nil {
			existingSecrets = secretEnvKeySet(existing.SecretEnvKeys)
		}
		for key := range p.EnvVars {
			_, existed := mapValue(existing, key)
			if existing == nil || !existed || existingSecrets[key] {
				desiredSecrets[key] = true
			}
		}
	} else {
		for _, key := range p.SecretEnvKeys {
			if _, ok := p.EnvVars[key]; !ok {
				return fmt.Errorf("secret environment key %q has no env_vars entry", key)
			}
			desiredSecrets[key] = true
		}
	}

	existingSecrets := map[string]bool{}
	if existing != nil {
		existingSecrets = secretEnvKeySet(existing.SecretEnvKeys)
	}
	for key, value := range p.EnvVars {
		if value != redactedSecret {
			continue
		}
		if !desiredSecrets[key] {
			if existing != nil && existingSecrets[key] {
				old, ok := existing.EnvVars[key]
				if !ok {
					return fmt.Errorf("environment variable %q has no existing value to preserve", key)
				}
				// An explicit Secret -> non-secret transition is itself the
				// authorization to reveal this value after the update succeeds.
				// Preserve it without sending plaintext to the browser first.
				p.EnvVars[key] = old
			}
			continue
		}
		if existing == nil || !existingSecrets[key] {
			return fmt.Errorf("environment variable %q has a redacted value; provide the secret", key)
		}
		old, ok := existing.EnvVars[key]
		if !ok {
			return fmt.Errorf("environment variable %q has no existing secret to preserve", key)
		}
		p.EnvVars[key] = old
	}

	p.SecretEnvKeys = sortedSecretEnvKeys(desiredSecrets)
	return nil
}

func declassifiedSecretEnvKeys(existing, updated *types.Project) []string {
	if existing == nil || updated == nil {
		return nil
	}
	oldSecrets := secretEnvKeySet(existing.SecretEnvKeys)
	newSecrets := secretEnvKeySet(updated.SecretEnvKeys)
	var keys []string
	for key := range oldSecrets {
		if _, remains := updated.EnvVars[key]; remains && !newSecrets[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func mapValue(p *types.Project, key string) (string, bool) {
	if p == nil || p.EnvVars == nil {
		return "", false
	}
	value, ok := p.EnvVars[key]
	return value, ok
}

func secretEnvKeySet(keys []string) map[string]bool {
	set := make(map[string]bool, len(keys))
	for _, key := range keys {
		set[key] = true
	}
	return set
}

func sortedSecretEnvKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func copyEnvVars(env map[string]string) map[string]string {
	if env == nil {
		return nil
	}
	clone := make(map[string]string, len(env))
	for key, value := range env {
		clone[key] = value
	}
	return clone
}
