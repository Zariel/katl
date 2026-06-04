package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	installstatus "github.com/zariel/katl/internal/installer/status"
)

func TestRuntimeStatusUpdatesExistingInstallStatus(t *testing.T) {
	root := t.TempDir()
	record := installstatus.New(installstatus.StateRebootRequested, time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC))
	record.InputMode = installstatus.InputModePXEPreseed
	record.InputSource = "https://example.invalid/install.json"
	record.RequestDigest = strings.Repeat("a", 64)
	record.KatlosImage = installstatus.Image{
		URL:              "https://example.invalid/katlos.squashfs",
		SHA256:           strings.Repeat("b", 64),
		Version:          "2026.06.04",
		Architecture:     "x86_64",
		RuntimeInterface: "katl-runtime-1",
		Role:             "install",
	}
	record.TargetDiskStableID = "/dev/disk/by-id/ata-root"
	record.SelectedRootSlot = "root-a"
	record.InstalledGeneration = "2026.06.04-001"
	if err := installstatus.WriteFile(filepath.Join(root, "var/lib/katl/install/status.json"), record); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	var stdout bytes.Buffer
	if err := run(t.Context(), []string{"--root", root}, &stdout); err != nil {
		t.Fatalf("run() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "var/lib/katl/install/status.json"))
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var decoded installstatus.Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if decoded.State != installstatus.StateWaitingForClusterBootstrap || decoded.FinalHandoff != installstatus.StateWaitingForClusterBootstrap {
		t.Fatalf("runtime state = %#v", decoded)
	}
	if decoded.RequestDigest != strings.Repeat("a", 64) || decoded.InstalledGeneration != "2026.06.04-001" {
		t.Fatalf("runtime status did not preserve install identity: %#v", decoded)
	}
	if !strings.Contains(stdout.String(), installstatus.StateWaitingForClusterBootstrap) {
		t.Fatalf("stdout = %q, want handoff state", stdout.String())
	}
}

func TestRuntimeStatusMissingInstallStatusWritesRepairState(t *testing.T) {
	root := t.TempDir()

	err := run(t.Context(), []string{"--root", root}, nil)
	if err == nil {
		t.Fatal("run() error = nil, want missing status failure")
	}

	data, readErr := os.ReadFile(filepath.Join(root, "var/lib/katl/install/status.json"))
	if readErr != nil {
		t.Fatalf("read status: %v", readErr)
	}
	var decoded installstatus.Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if decoded.State != installstatus.StateRuntimeFailedNeedsRepair || decoded.FinalHandoff != "" {
		t.Fatalf("repair status = %#v", decoded)
	}
}

func TestRuntimeStatusIncompleteInstallStatusWritesRepairState(t *testing.T) {
	root := t.TempDir()
	record := installstatus.New(installstatus.StateRebootRequested, time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC))
	record.RequestDigest = strings.Repeat("a", 64)
	if err := installstatus.WriteFile(filepath.Join(root, "var/lib/katl/install/status.json"), record); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	err := run(t.Context(), []string{"--root", root}, nil)
	if err == nil {
		t.Fatal("run() error = nil, want incomplete status failure")
	}

	data, readErr := os.ReadFile(filepath.Join(root, "var/lib/katl/install/status.json"))
	if readErr != nil {
		t.Fatalf("read status: %v", readErr)
	}
	var decoded installstatus.Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if decoded.State != installstatus.StateRuntimeFailedNeedsRepair || decoded.FinalHandoff != "" {
		t.Fatalf("repair status = %#v", decoded)
	}
	if decoded.RequestDigest != strings.Repeat("a", 64) {
		t.Fatalf("repair status did not preserve fields: %#v", decoded)
	}
}
