package confext

import (
	"io/fs"
	"strings"
	"testing"
)

func TestValidateNativeEtcBundleAcceptsKnownConfigPaths(t *testing.T) {
	plans, err := ValidateNativeEtcBundle("/target/var/lib/katl/generations/2026.05.31-001/confext", []NativeEtcFile{
		{Path: "/etc/systemd/network/10-lan.network", Mode: 0o644},
		{Path: "/etc/ssh/sshd_config.d/10-katl.conf", Mode: 0o600},
		{Path: "/etc/katl/kubeadm-init.yaml", Mode: 0o640},
	})
	if err != nil {
		t.Fatalf("ValidateNativeEtcBundle() error = %v", err)
	}
	if len(plans) != 3 {
		t.Fatalf("len(plans) = %d, want 3", len(plans))
	}
	for _, plan := range plans {
		if !strings.HasPrefix(plan.ConfextPath, "/target/var/lib/katl/generations/2026.05.31-001/confext/etc/") {
			t.Fatalf("ConfextPath = %q, want path under generated confext root", plan.ConfextPath)
		}
		if plan.UID != 0 || plan.GID != 0 {
			t.Fatalf("ownership = %d:%d, want root:root", plan.UID, plan.GID)
		}
	}
}

func TestValidateNativeEtcBundleRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name string
		file NativeEtcFile
		want string
	}{
		{
			name: "relative path",
			file: NativeEtcFile{Path: "etc/hostname"},
			want: "must be absolute",
		},
		{
			name: "outside etc",
			file: NativeEtcFile{Path: "/var/lib/katl/node.yaml"},
			want: "must be under /etc",
		},
		{
			name: "path traversal",
			file: NativeEtcFile{Path: "/etc/../root/.ssh/authorized_keys"},
			want: "contains path traversal",
		},
		{
			name: "kubernetes ownership",
			file: NativeEtcFile{Path: "/etc/kubernetes/admin.conf"},
			want: "cannot own kubeadm-managed",
		},
		{
			name: "symlink",
			file: NativeEtcFile{Path: "/etc/hostname", Type: NativeEtcSymlink},
			want: "symlink entries are not allowed",
		},
		{
			name: "device node",
			file: NativeEtcFile{Path: "/etc/hostname", Type: NativeEtcCharDevice},
			want: "char-device entries are not allowed",
		},
		{
			name: "mode type symlink",
			file: NativeEtcFile{Path: "/etc/hostname", Mode: fs.ModeSymlink | 0o777},
			want: "must be a regular file",
		},
		{
			name: "world writable",
			file: NativeEtcFile{Path: "/etc/hostname", Mode: 0o666},
			want: "cannot be group- or world-writable",
		},
		{
			name: "executable",
			file: NativeEtcFile{Path: "/etc/profile.d/katl.sh", Mode: 0o755},
			want: "cannot be executable",
		},
		{
			name: "non root owner",
			file: NativeEtcFile{Path: "/etc/hostname", UID: 1000},
			want: "must be owned by root:root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ValidateNativeEtcBundle("/target/confext", []NativeEtcFile{tt.file})
			if err == nil {
				t.Fatalf("ValidateNativeEtcBundle() error = nil, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err, tt.want)
			}
		})
	}
}

func TestValidateNativeEtcBundleRejectsDuplicateNormalizedPaths(t *testing.T) {
	_, err := ValidateNativeEtcBundle("/target/confext", []NativeEtcFile{
		{Path: "/etc/hostname"},
		{Path: "/etc/./hostname"},
	})
	if err == nil {
		t.Fatal("ValidateNativeEtcBundle() error = nil, want duplicate path error")
	}
	if !strings.Contains(err.Error(), "duplicate /etc file path") {
		t.Fatalf("error = %q, want duplicate path error", err)
	}
}

func TestValidateNativeEtcBundleRequiresAbsoluteConfextRoot(t *testing.T) {
	_, err := ValidateNativeEtcBundle("relative/confext", []NativeEtcFile{{Path: "/etc/hostname"}})
	if err == nil {
		t.Fatal("ValidateNativeEtcBundle() error = nil, want absolute root error")
	}
	if !strings.Contains(err.Error(), "confext root must be absolute") {
		t.Fatalf("error = %q, want absolute root error", err)
	}
}
