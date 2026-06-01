package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyInput(t *testing.T) {
	root := t.TempDir()
	preseed := filepath.Join(root, "seed")
	runDir := filepath.Join(root, "run")
	etcDir := filepath.Join(root, "etc")
	writeTestFile(t, filepath.Join(preseed, "install-input.json"), `{"manifestPath":"/run/katl/install-manifest.json"}`)
	writeTestFile(t, filepath.Join(preseed, "etc/katl/install-manifest.json"), `{"kind":"InstallManifest"}`)

	var stdout bytes.Buffer
	if err := ApplyInput(InputApplyRequest{
		PreseedDirs: []string{preseed},
		RunDir:      runDir,
		EtcDir:      etcDir,
		Stdout:      &stdout,
	}); err != nil {
		t.Fatalf("ApplyInput() error = %v", err)
	}

	assertFile(t, filepath.Join(runDir, "install-input.json"), `{"manifestPath":"/run/katl/install-manifest.json"}`)
	assertFile(t, filepath.Join(etcDir, "install-manifest.json"), `{"kind":"InstallManifest"}`)
	if got := stdout.String(); !strings.Contains(got, "copied") {
		t.Fatalf("stdout = %q, want copied log", got)
	}
}

func TestApplyInputNone(t *testing.T) {
	var stdout bytes.Buffer
	if err := ApplyInput(InputApplyRequest{
		PreseedDirs: []string{filepath.Join(t.TempDir(), "missing")},
		Stdout:      &stdout,
	}); err != nil {
		t.Fatalf("ApplyInput() error = %v", err)
	}
	if got, want := stdout.String(), "katl input: no preseed files found\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestApplyInputJSON(t *testing.T) {
	root := t.TempDir()
	preseed := filepath.Join(root, "seed")
	writeTestFile(t, filepath.Join(preseed, "install-input.json"), `{`)

	err := ApplyInput(InputApplyRequest{PreseedDirs: []string{preseed}, RunDir: filepath.Join(root, "run")})
	if err == nil || !strings.Contains(err.Error(), "not valid JSON") {
		t.Fatalf("ApplyInput() error = %v, want JSON error", err)
	}
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
