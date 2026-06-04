package status

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRedactSourceRemovesCredentialsAndQuery(t *testing.T) {
	got := RedactSource("https://user:secret@example.invalid/path/katlos.squashfs?token=secret#frag")
	want := "https://example.invalid/path/katlos.squashfs"
	if got != want {
		t.Fatalf("RedactSource() = %q, want %q", got, want)
	}
}

func TestRedactErrorRemovesEmbeddedURLSecrets(t *testing.T) {
	got := RedactError(errors.New("download failed: https://user:secret@example.invalid/path?token=secret"))
	want := "download failed: https://example.invalid/path"
	if got != want {
		t.Fatalf("RedactError() = %q, want %q", got, want)
	}
}

func TestWriteRuntimeHandoff(t *testing.T) {
	root := t.TempDir()
	record := New(StateKubeadmReady, time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC))
	record.InputMode = InputModePXEPreseed
	record.InputSource = "https://example.invalid/install.json"
	record.RequestDigest = strings.Repeat("a", 64)
	record.KatlosImage = Image{
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

	if err := WriteRuntimeHandoff(root, record); err != nil {
		t.Fatalf("WriteRuntimeHandoff() error = %v", err)
	}

	data, err := os.ReadFile(filepath.Join(root, "var/lib/katl/install/status.json"))
	if err != nil {
		t.Fatalf("read runtime status: %v", err)
	}
	var decoded Record
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if decoded.State != StateWaitingForClusterBootstrap || decoded.FinalHandoff != StateWaitingForClusterBootstrap {
		t.Fatalf("handoff state = %#v", decoded)
	}
	if decoded.RequestDigest != strings.Repeat("a", 64) || decoded.InstalledGeneration != "2026.06.04-001" {
		t.Fatalf("status did not preserve identity fields: %#v", decoded)
	}
}
