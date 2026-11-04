package db

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	secretKeyBytes = 32
	secretPrefix   = "bbsit:secret:v1:"
)

// secretCipher encrypts sensitive database fields. The field identity is
// authenticated as AAD so ciphertext cannot be copied between projects or
// between different secret fields.
type secretCipher struct {
	aead cipher.AEAD
}

func loadSecretCipher(path string) (*secretCipher, error) {
	key, err := loadOrCreateSecretKey(path)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret AEAD: %w", err)
	}
	return &secretCipher{aead: aead}, nil
}

func loadOrCreateSecretKey(path string) ([]byte, error) {
	if path == "" {
		return nil, fmt.Errorf("secret key path must not be empty")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("secret key path %q must not be a symlink", path)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("secret key path %q is not a regular file", path)
		}
		if err := os.Chmod(path, 0600); err != nil {
			return nil, fmt.Errorf("chmod secret key: %w", err)
		}
		key, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read secret key: %w", err)
		}
		if len(key) != secretKeyBytes {
			return nil, fmt.Errorf("secret key %q has %d bytes; want %d", path, len(key), secretKeyBytes)
		}
		return key, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat secret key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("create secret key directory: %w", err)
	}
	key := make([]byte, secretKeyBytes)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("generate secret key: %w", err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, fmt.Errorf("create secret key: %w", err)
	}
	if _, err := f.Write(key); err != nil {
		f.Close()
		return nil, fmt.Errorf("write secret key: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return nil, fmt.Errorf("sync secret key: %w", err)
	}
	if err := f.Close(); err != nil {
		return nil, fmt.Errorf("close secret key: %w", err)
	}
	return key, nil
}

func (c *secretCipher) encrypt(aad, plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate secret nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(aad))
	return secretPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

func (c *secretCipher) decrypt(aad, value string) (string, error) {
	if !isEncryptedSecret(value) {
		return "", fmt.Errorf("secret field is not encrypted")
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimPrefix(value, secretPrefix))
	if err != nil {
		return "", fmt.Errorf("decode encrypted secret: %w", err)
	}
	nonceSize := c.aead.NonceSize()
	if len(raw) < nonceSize {
		return "", fmt.Errorf("encrypted secret is truncated")
	}
	plain, err := c.aead.Open(nil, raw[:nonceSize], raw[nonceSize:], []byte(aad))
	if err != nil {
		return "", fmt.Errorf("decrypt secret: %w", err)
	}
	return string(plain), nil
}

func isEncryptedSecret(value string) bool {
	return strings.HasPrefix(value, secretPrefix)
}

func fileContainsEncryptedSecrets(path string) (bool, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return bytes.Contains(data, []byte(secretPrefix)), nil
}

func projectEnvAAD(id string) string {
	return "projects/" + id + "/env_vars"
}

func tunnelSecretAAD(id, field string) string {
	return "tunnels/" + id + "/" + field
}
