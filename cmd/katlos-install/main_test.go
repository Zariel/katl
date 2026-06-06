package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zariel/katl/internal/installer"
	"github.com/zariel/katl/internal/installer/discovery"
	"github.com/zariel/katl/internal/installer/disk"
	"github.com/zariel/katl/internal/installer/katlosimage"
	installstatus "github.com/zariel/katl/internal/installer/status"
)

func TestVersion(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	version, commit, date = "dev", "abc123", "2026-06-01T00:00:00Z"
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), "katlos-install version=dev commit=abc123 date=2026-06-01T00:00:00Z\n"; got != want {
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
		"--seed-wait", "0s",
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

func TestBootInput(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run")
	etcDir := filepath.Join(root, "etc")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	inputPath := filepath.Join(runDir, "install-input.json")
	inputJSON := `{"manifestPath":"/run/katl/install-manifest.json","installMode":"auto"}`
	if err := os.WriteFile(inputPath, []byte(inputJSON), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	input, err := bootInput(runDir, etcDir)
	if err != nil {
		t.Fatalf("bootInput() error = %v", err)
	}
	if input.Action != installer.InstallActionRun || !input.CanMutateDisks() {
		t.Fatalf("action = %s canMutate = %t, want run", input.Action, input.CanMutateDisks())
	}
	if got := bootInputMode(input); got != installstatus.InputModeOfflineMedia {
		t.Fatalf("boot input mode = %q, want offline media", got)
	}
}

func TestBootInputMode(t *testing.T) {
	tests := []struct {
		name  string
		input installer.BootInput
		want  string
	}{
		{
			name: "run path",
			input: installer.BootInput{SelectedSources: map[string]installer.InputSource{
				"manifestPath": installer.InputSourceRunKatl,
			}},
			want: installstatus.InputModeOfflineMedia,
		},
		{
			name: "etc path",
			input: installer.BootInput{SelectedSources: map[string]installer.InputSource{
				"manifestPath": installer.InputSourceEtcKatl,
			}},
			want: installstatus.InputModeOfflineMedia,
		},
		{
			name: "embedded path",
			input: installer.BootInput{SelectedSources: map[string]installer.InputSource{
				"manifestPath": installer.InputSourceEmbeddedMedia,
			}},
			want: installstatus.InputModeOfflineMedia,
		},
		{
			name: "kernel path",
			input: installer.BootInput{SelectedSources: map[string]installer.InputSource{
				"manifestPath": installer.InputSourceKernelCmdline,
			}},
			want: installstatus.InputModePXEPreseed,
		},
		{
			name:  "manifest URL",
			input: installer.BootInput{ManifestURL: "https://example.invalid/install.json"},
			want:  installstatus.InputModePXEPreseed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bootInputMode(tt.input); got != tt.want {
				t.Fatalf("bootInputMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestManifestRunnerContextConfiguresImageResolver(t *testing.T) {
	root := t.TempDir()
	manifestPath := filepath.Join(root, "media", "install-manifest.json")
	stateDir := filepath.Join(root, "state")

	install, err := manifestRunnerContext(manifestPath, stateDir, installstatus.InputModePXEPreseed, manifestPath)
	if err != nil {
		t.Fatalf("manifestRunnerContext() error = %v", err)
	}

	resolver, ok := install.KatlosResolver.(katlosimage.Resolver)
	if !ok {
		t.Fatalf("KatlosResolver = %T, want katlosimage.Resolver", install.KatlosResolver)
	}
	if resolver.MediaRoot != filepath.Dir(manifestPath) {
		t.Fatalf("MediaRoot = %q, want %q", resolver.MediaRoot, filepath.Dir(manifestPath))
	}
	if resolver.WorkDir != filepath.Join(stateDir, "katlos-image") {
		t.Fatalf("WorkDir = %q, want state-backed image workdir", resolver.WorkDir)
	}
	if resolver.Commands == nil || install.Commands == nil {
		t.Fatalf("command runners are not configured: resolver=%#v install=%#v", resolver.Commands, install.Commands)
	}
	if source, ok := install.Discovery.(discovery.CommandDiscoverySource); !ok || source.Commands == nil {
		t.Fatalf("Discovery = %#v, want command-backed discovery", install.Discovery)
	}
	if _, ok := install.RootSlotOpener.(disk.FileRootSlotDeviceOpener); !ok {
		t.Fatalf("RootSlotOpener = %T, want disk.FileRootSlotDeviceOpener", install.RootSlotOpener)
	}
	if install.IdentityRandom == nil {
		t.Fatal("IdentityRandom is nil")
	}
	if install.Chown == nil {
		t.Fatal("Chown is nil")
	}
}

func TestBootWait(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	runDir := filepath.Join(t.TempDir(), "run")
	etcDir := filepath.Join(t.TempDir(), "etc")
	var stdout bytes.Buffer
	err := runBootWithHandoff(ctx, runDir, etcDir, "127.0.0.1:0", &stdout, func(ctx context.Context, gotRunDir, gotAddr string, stdout io.Writer) error {
		if gotRunDir != runDir {
			t.Fatalf("run dir = %q, want %q", gotRunDir, runDir)
		}
		if gotAddr != "127.0.0.1:0" {
			t.Fatalf("handoff addr = %q", gotAddr)
		}
		fmt.Fprintln(stdout, "katlos-install waiting for config at http://127.0.0.1:0/v1/install")
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runBoot() error = %v, want deadline", err)
	}
	if got := stdout.String(); !strings.Contains(got, "waiting for config") {
		t.Fatalf("stdout = %q, want handoff announcement", got)
	}
}

func TestBootHold(t *testing.T) {
	root := t.TempDir()
	runDir := filepath.Join(root, "run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "install-input.json"), []byte(`{"holdForDebug":true}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	var stdout bytes.Buffer
	err := runBoot(ctx, runDir, filepath.Join(root, "etc"), "127.0.0.1:0", &stdout)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runBoot() error = %v, want deadline", err)
	}
	if got := stdout.String(); !strings.Contains(got, "debug hold active") {
		t.Fatalf("stdout = %q, want debug hold log", got)
	}
}
