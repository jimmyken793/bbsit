package main

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func responseClient(t *testing.T, check func(*http.Request), body string) *client {
	t.Helper()
	return &client{hc: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		check(req)
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        make(http.Header),
			Body:          io.NopCloser(strings.NewReader(body)),
			ContentLength: int64(len(body)),
			Request:       req,
		}, nil
	})}}
}

func TestExportSecretsRequiresOutput(t *testing.T) {
	err := cmdExport(nil, []string{"demo", "--export-secrets"})
	if err == nil || !strings.Contains(err.Error(), "requires --output") {
		t.Fatalf("got %v, want output requirement", err)
	}
}

func TestExportSecretsWritesExclusivePrivateFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "project.yaml")
	c := responseClient(t, func(req *http.Request) {
		if req.URL.Query().Get("export_secrets") != "true" {
			t.Errorf("missing export_secrets=true in %s", req.URL.String())
		}
	}, "env_vars:\n  TOKEN: secret-value\n")

	if err := cmdExport(c, []string{"demo", "--export-secrets", "--output", out}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "secret-value") {
		t.Fatal("output missing response body")
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}

	if err := cmdExport(c, []string{"demo", "--export-secrets", "-o", out}); err == nil || !os.IsExist(err) {
		t.Fatalf("existing output error = %v, want file exists", err)
	}
}

func TestPackSecretsRequiresOutput(t *testing.T) {
	err := cmdPack(nil, []string{"demo", "--export-secrets"})
	if err == nil || !strings.Contains(err.Error(), "requires --output") {
		t.Fatalf("got %v, want output requirement", err)
	}
}

func TestPackSecretsWritesExclusivePrivateFile(t *testing.T) {
	out := filepath.Join(t.TempDir(), "project.tar.gz")
	c := responseClient(t, func(req *http.Request) {
		if req.URL.Query().Get("format") != "tar.gz" || req.URL.Query().Get("export_secrets") != "true" {
			t.Errorf("unexpected query: %s", req.URL.RawQuery)
		}
	}, "fake-tarball")

	if err := cmdPack(c, []string{"--export-secrets", "demo", "-o", out}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake-tarball" {
		t.Fatalf("output = %q", data)
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode = %04o, want 0600", info.Mode().Perm())
	}
}

func TestWriteNewFileRemovesPartialOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "partial")
	src := io.MultiReader(strings.NewReader("partial"), errorReader{})
	if err := writeNewFile(out, 0600, src, nil); err == nil {
		t.Fatal("want copy error")
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatalf("partial output remains: %v", err)
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("read failed")
}
