package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestVersion(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	version, commit, date = "v0", "abc123", "2026-06-01T00:00:00Z"
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), "katlos-install version=v0 commit=abc123 date=2026-06-01T00:00:00Z\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestApplyInput(t *testing.T) {
	root := t.TempDir()
	preseed := filepath.Join(root, "seed")
	runDir := filepath.Join(root, "run")
	etcDir := filepath.Join(root, "etc")
	if err := os.MkdirAll(preseed, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(preseed, "install-input.json"), []byte(`{"waitForConfig":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"--apply-input",
		"--preseed-dir", preseed,
		"--run-dir", runDir,
		"--etc-dir", etcDir,
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(runDir, "install-input.json")); err != nil {
		t.Fatalf("input file missing: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}
