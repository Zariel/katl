package vmtest

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDiskPlan(t *testing.T) {
	result, err := NewRunner(Options{
		StateRoot: "/tmp/vmtest",
		RunID:     "run-1",
	}).Plan(Scenario{
		Name: "install",
		Disks: []DiskFixture{
			TargetDisk("root", "qcow2", "20G"),
			ExtraDisk("data", "raw", "2G"),
		},
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if len(result.Disks) != 2 {
		t.Fatalf("disks = %#v", result.Disks)
	}
	root := result.Disks[0]
	if root.HostPath != "/tmp/vmtest/run-1/disks/00-root.qcow2" {
		t.Fatalf("root HostPath = %q", root.HostPath)
	}
	if root.AttachmentOrder != 0 {
		t.Fatalf("root AttachmentOrder = %d", root.AttachmentOrder)
	}
	if root.GuestSelector != "/dev/disk/by-id/virtio-katl-root" {
		t.Fatalf("root GuestSelector = %q", root.GuestSelector)
	}
	data := result.Disks[1]
	if data.HostPath != "/tmp/vmtest/run-1/disks/01-data.raw" {
		t.Fatalf("data HostPath = %q", data.HostPath)
	}
	if data.AttachmentOrder != 1 {
		t.Fatalf("data AttachmentOrder = %d", data.AttachmentOrder)
	}
}

func TestDiskCreate(t *testing.T) {
	dir := t.TempDir()
	plans, err := planDisks(dir, []DiskFixture{
		TargetDisk("root", "qcow2", "20G"),
		SnapshotDisk("runtime", "/images/runtime.raw", DiskRaw),
	})
	if err != nil {
		t.Fatalf("planDisks() error = %v", err)
	}
	runner := &diskRunner{}
	if err := CreateDisks(context.Background(), runner, plans); err != nil {
		t.Fatalf("CreateDisks() error = %v", err)
	}
	want := [][]string{
		{"qemu-img", "create", "-f", "qcow2", filepath.Join(dir, "00-root.qcow2"), "20G"},
		{"qemu-img", "create", "-f", "qcow2", "-F", "raw", "-b", "/images/runtime.raw", filepath.Join(dir, "01-runtime.snapshot.qcow2")},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v", runner.commands)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("disk dir missing: %v", err)
	}
}

func TestDiskCleanup(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "target.qcow2")
	if err := os.WriteFile(disk, []byte("disk"), 0o644); err != nil {
		t.Fatalf("write disk: %v", err)
	}
	result := Result{
		Status: StatusPassed,
		Keep:   KeepFailed,
		Disks:  []DiskPlan{{HostPath: disk}},
	}
	if err := CleanupDisks(result); err != nil {
		t.Fatalf("CleanupDisks() error = %v", err)
	}
	if _, err := os.Stat(disk); !os.IsNotExist(err) {
		t.Fatalf("disk still exists: %v", err)
	}

	if err := os.WriteFile(disk, []byte("disk"), 0o644); err != nil {
		t.Fatalf("rewrite disk: %v", err)
	}
	result.Status = StatusFailed
	if err := CleanupDisks(result); err != nil {
		t.Fatalf("CleanupDisks() failed result error = %v", err)
	}
	if _, err := os.Stat(disk); err != nil {
		t.Fatalf("failed disk not kept: %v", err)
	}
}

type diskRunner struct {
	commands [][]string
}

func (r *diskRunner) Run(_ context.Context, name string, args ...string) error {
	command := append([]string{name}, args...)
	r.commands = append(r.commands, command)
	return nil
}
