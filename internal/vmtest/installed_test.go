package vmtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestESPCheck(t *testing.T) {
	esp := espFixture(t)
	if err := CheckESP(esp); err != nil {
		t.Fatalf("CheckESP() error = %v", err)
	}
	entry := filepath.Join(esp, "loader", "entries", "katl.conf")
	data, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	data = []byte(strings.ReplaceAll(string(data), "root=PARTUUID=1111 ", "root=UUID=1111 "))
	if err := os.WriteFile(entry, data, 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	if err := CheckESP(esp); err == nil {
		t.Fatal("CheckESP() succeeded with root auto-discovery")
	}
}

func TestInstalledRuntime(t *testing.T) {
	root := t.TempDir()
	disk := filepath.Join(root, "installed.raw")
	if err := os.WriteFile(disk, []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	esp := espFixture(t)
	result, err := NewRunner(Options{
		StateRoot: root,
		RunID:     "run-1",
	}).Plan(Scenario{Name: "runtime"})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	_, vmConfig := vmFixture(t)
	vmConfig.Expect = "Katl state projection ready"
	runner := VMRunner{
		Executor: vmExec{write: "Katl state projection ready"},
		probe: probe{
			lookPath: func(string) (string, error) { return "/usr/bin/qemu-system-x86_64", nil },
			stat:     os.Stat,
			access:   func(string) error { return nil },
		},
	}
	result = RunInstalledRuntime(context.Background(), result, InstalledRuntimeConfig{
		Disk:         disk,
		DiskFormat:   DiskRaw,
		ESPArtifacts: esp,
		VM:           vmConfig,
	}, runner)
	if result.Status != StatusPassed {
		t.Fatalf("Status = %q, failure = %q", result.Status, result.FailureSummary)
	}
	if _, err := os.Stat(filepath.Join(result.RunDir, "esp", "loader", "entries", "katl.conf")); err != nil {
		t.Fatalf("ESP copy missing: %v", err)
	}
	if serial, err := os.ReadFile(result.Artifacts.RuntimeSerial); err != nil || !strings.Contains(string(serial), "Katl state projection ready") {
		t.Fatalf("runtime serial = %q, err = %v", serial, err)
	}
}

func espFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	entries := filepath.Join(root, "loader", "entries")
	if err := os.MkdirAll(entries, 0o755); err != nil {
		t.Fatalf("mkdir entries: %v", err)
	}
	entry := `title Katl
linux /EFI/Linux/katl.efi
options root=PARTUUID=1111 rootfstype=squashfs ro katl.generation=2026.06.03 systemd.machine_id=0123456789abcdef0123456789abcdef
`
	if err := os.WriteFile(filepath.Join(entries, "katl.conf"), []byte(entry), 0o644); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	return root
}
