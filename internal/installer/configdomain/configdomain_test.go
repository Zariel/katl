package configdomain

import (
	"strings"
	"testing"

	"github.com/zariel/katl/internal/installer/kubeadmconfig"
	"github.com/zariel/katl/internal/installer/manifest"
)

func TestNativeEtcFilesRendersKnownDomains(t *testing.T) {
	installManifest, err := manifest.Decode(strings.NewReader(manifestJSON(`,
			"networkd": {
				"files": [
					{"name": "10-lan.network", "content": "[Match]\nName=enp1s0\n"}
				]
			},
			"kubernetes": {
				"kubeadm": {"configRef": "control-plane"}
			}`)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	files, err := NativeEtcFiles(RenderRequest{
		Manifest: installManifest,
		KubeadmConfigs: map[string]kubeadmconfig.Plan{
			"control-plane": {
				Name: "control-plane",
				Config: kubeadmconfig.File{
					RenderPath: "/etc/katl/kubeadm/control-plane/config.yaml",
					Content:    []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: InitConfiguration\n"),
					Mode:       0o644,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NativeEtcFiles() error = %v", err)
	}
	want := []string{
		"/etc/katl/kubeadm/control-plane/config.yaml",
		"/etc/systemd/network/10-lan.network",
	}
	if len(files) != len(want) {
		t.Fatalf("len(files) = %d, want %d: %#v", len(files), len(want), files)
	}
	for i, path := range want {
		if files[i].Path != path {
			t.Fatalf("files[%d].Path = %q, want %q", i, files[i].Path, path)
		}
		if files[i].Mode != 0o644 || files[i].UID != 0 || files[i].GID != 0 {
			t.Fatalf("files[%d] mode/owner = %04o %d:%d", i, files[i].Mode, files[i].UID, files[i].GID)
		}
	}
}

func TestNativeEtcFilesRejectsUnresolvedKubeadmRef(t *testing.T) {
	installManifest, err := manifest.Decode(strings.NewReader(manifestJSON(`,
			"kubernetes": {
				"kubeadm": {"configRef": "control-plane"}
			}`)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_, err = NativeEtcFiles(RenderRequest{Manifest: installManifest})
	if err == nil || !strings.Contains(err.Error(), "was not resolved") {
		t.Fatalf("NativeEtcFiles() error = %v, want unresolved ref", err)
	}
}

func TestNativeEtcFilesRejectsMismatchedKubeadmPlan(t *testing.T) {
	installManifest, err := manifest.Decode(strings.NewReader(manifestJSON(`,
			"kubernetes": {
				"kubeadm": {"configRef": "control-plane"}
			}`)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_, err = NativeEtcFiles(RenderRequest{
		Manifest: installManifest,
		KubeadmConfigs: map[string]kubeadmconfig.Plan{
			"control-plane": {
				Name: "worker",
				Config: kubeadmconfig.File{
					RenderPath: "/etc/katl/kubeadm/worker/config.yaml",
					Content:    []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: JoinConfiguration\n"),
					Mode:       0o644,
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), `resolved to KubeadmConfig "worker"`) {
		t.Fatalf("NativeEtcFiles() error = %v, want mismatched plan", err)
	}
}

func TestNativeEtcFilesRejectsUnsafeRenderedPaths(t *testing.T) {
	installManifest, err := manifest.Decode(strings.NewReader(manifestJSON(`,
			"kubernetes": {
				"kubeadm": {"configRef": "control-plane"}
			}`)))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	_, err = NativeEtcFiles(RenderRequest{
		Manifest: installManifest,
		KubeadmConfigs: map[string]kubeadmconfig.Plan{
			"control-plane": {
				Name: "control-plane",
				Config: kubeadmconfig.File{
					RenderPath: "/etc/kubernetes/admin.conf",
					Content:    []byte("unsafe"),
					Mode:       0o644,
				},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "cannot own kubeadm-managed") {
		t.Fatalf("NativeEtcFiles() error = %v, want unsafe rendered path", err)
	}
}

func manifestJSON(nodeExtra string) string {
	return `{
		"apiVersion": "install.katl.dev/v1alpha1",
		"kind": "InstallManifest",
		"node": {
			"identity": {
				"hostname": "lab-node-01",
				"ssh": {
					"authorizedKeys": [
						"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKatlExampleRuntimeKeyReplaceMe katl@example"
					]
				}
			}` + nodeExtra + `
		},
		"install": {
			"allowDestructiveInstall": true,
			"targetDisk": {"byID": "/dev/disk/by-id/ata-root", "minSizeMiB": 32768}
		},
		"katlosImage": {
			"url": "https://example.invalid/katlos-install.squashfs",
			"sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"sizeBytes": 1073741824,
			"version": "2026.06.04",
			"architecture": "x86_64",
			"runtimeInterface": "katl-runtime-1",
			"role": "install"
		}
	}`
}
