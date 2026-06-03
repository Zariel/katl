package generation

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const statePartUUID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func TestRenderState(t *testing.T) {
	assets, err := RenderState(StateRequest{PartitionUUID: statePartUUID})
	if err != nil {
		t.Fatalf("RenderState() error = %v", err)
	}
	wantMount := `[Unit]
Description=Katl writable state partition
Documentation=man:systemd.mount(5)
DefaultDependencies=no
Before=local-fs.target
Conflicts=umount.target
Before=umount.target

[Mount]
What=PARTUUID=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee
Where=/var
Type=auto
Options=rw

[Install]
WantedBy=local-fs.target
`
	if assets.VarMount != wantMount {
		t.Fatalf("var.mount:\n%s\nwant:\n%s", assets.VarMount, wantMount)
	}
	for _, want := range []string{
		"d /var/lib/katl 0755 root root -",
		"d /var/lib/katl/generations 0755 root root -",
		"d /var/lib/katl/install/logs 0755 root root -",
		"d /var/lib/containerd 0755 root root -",
		"d /var/lib/kubelet 0755 root root -",
		"d /var/log/journal 2755 root systemd-journal -",
	} {
		if !strings.Contains(assets.Tmpfiles, want) {
			t.Fatalf("tmpfiles missing %q:\n%s", want, assets.Tmpfiles)
		}
	}
}

func TestWriteState(t *testing.T) {
	root := t.TempDir()
	assets, err := WriteState(root, StateRequest{PartitionUUID: statePartUUID})
	if err != nil {
		t.Fatalf("WriteState() error = %v", err)
	}
	assertFile(t, filepath.Join(root, "etc/systemd/system/var.mount"), assets.VarMount)
	assertFile(t, filepath.Join(root, "etc/tmpfiles.d/katl-state.conf"), assets.Tmpfiles)
	assertDir(t, filepath.Join(root, "var/lib/katl"), 0o755)
	assertDir(t, filepath.Join(root, "var/lib/katl/generations"), 0o755)
	assertDir(t, filepath.Join(root, "var/lib/katl/install/logs"), 0o755)
	assertDir(t, filepath.Join(root, "var/lib/katl/kubernetes/etc-kubernetes"), 0o755)
	assertDir(t, filepath.Join(root, "var/lib/katl/ssh/host-keys"), 0o700)
	assertDir(t, filepath.Join(root, "var/lib/containerd"), 0o755)
	assertDir(t, filepath.Join(root, "var/lib/kubelet"), 0o755)
	assertDir(t, filepath.Join(root, "var/log/journal"), 0o755)
}

func TestRenderStateRejectsUUID(t *testing.T) {
	_, err := RenderState(StateRequest{PartitionUUID: "abc rw"})
	if err == nil || !strings.Contains(err.Error(), "must not contain whitespace") {
		t.Fatalf("RenderState() error = %v, want UUID validation failure", err)
	}
}

func assertFile(t *testing.T, path string, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s:\n%s\nwant:\n%s", path, data, want)
	}
}

func assertDir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", path)
	}
	if got := info.Mode().Perm(); got != mode {
		t.Fatalf("%s mode = %04o, want %04o", path, got, mode)
	}
}
