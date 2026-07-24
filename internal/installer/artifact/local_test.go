package artifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadLocal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-root.squashfs.json")
	data := `{
  "name": "runtime-root",
  "kind": "runtime-root",
  "format": "squashfs",
  "path": "runtime-root.squashfs",
  "sizeBytes": 4096,
  "sha256": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
  "compression": "zstd",
  "generation": "abc123",
  "architecture": "x86_64",
  "runtimeInterface": "katl-runtime-1",
  "compatibleBoot": {
    "kind": "uki",
    "runtimeInterface": "katl-runtime-1",
    "kernelCommandLine": [
      "rootfstype=squashfs",
      "ro"
    ]
  },
  "created": "2026-06-01T00:00:00Z"
}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadLocal(path)
	if err != nil {
		t.Fatalf("ReadLocal() error = %v", err)
	}
	if meta.Format != "squashfs" || meta.Compression != "zstd" {
		t.Fatalf("meta = %#v", meta)
	}
	if meta.CompatibleBoot == nil || meta.CompatibleBoot.Kind != ArtifactUKI {
		t.Fatalf("compatible boot = %#v", meta.CompatibleBoot)
	}

	spec := meta.Spec("https://artifacts.example/katl")
	if spec.URL != "https://artifacts.example/katl/runtime-root.squashfs" {
		t.Fatalf("spec URL = %q", spec.URL)
	}
	if spec.SizeBytes != 4096 || spec.SHA256 != meta.SHA256 {
		t.Fatalf("spec = %#v", spec)
	}
}

func TestReadLocalBad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-root.squashfs.json")
	if err := os.WriteFile(path, []byte(`{"name":"runtime-root"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := ReadLocal(path)
	if !errors.Is(err, ErrInvalidArtifactSpec) {
		t.Fatalf("ReadLocal() error = %v, want ErrInvalidArtifactSpec", err)
	}
}

func TestLocalMetaVerifyFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "artifact.raw")
	content := []byte("verified local artifact")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	meta := LocalMeta{Path: filepath.Base(path), SizeBytes: int64(len(content)), SHA256: hex.EncodeToString(digest[:])}
	if err := meta.VerifyFile(path); err != nil {
		t.Fatalf("VerifyFile() error = %v", err)
	}
	if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := meta.VerifyFile(path); err == nil || (!strings.Contains(err.Error(), "size") && !strings.Contains(err.Error(), "SHA-256")) {
		t.Fatalf("VerifyFile() error = %v", err)
	}
}

func TestReadSysext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "katl-kubernetes.raw.json")
	data := `{
  "name": "kubernetes",
  "kind": "sysext",
  "format": "sysext",
  "path": "katl-kubernetes.raw",
  "sizeBytes": 8192,
  "sha256": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824",
  "version": "abc123",
  "payloadVersion": "v1.34",
  "architecture": "x86_64",
  "sourceRepo": {
    "id": "kubernetes",
    "baseURL": "https://pkgs.k8s.io/core:/stable:/v1.34/rpm/",
    "minor": "v1.34"
  },
  "packageVersions": {
    "kubeadm": "0:1.34.8-150500.1.1",
    "kubelet": "0:1.34.8-150500.1.1",
    "kubectl": "0:1.34.8-150500.1.1",
    "cri-tools": "0:1.34.0-150500.1.1"
  },
  "runtimeInterface": "katl-runtime-1",
  "compatibleRuntime": {
    "interface": "katl-runtime-1",
    "artifactPath": "katl-runtime-root.squashfs",
    "artifactSHA256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  },
  "created": "2026-06-01T00:00:00Z"
}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}

	meta, err := ReadLocal(path)
	if err != nil {
		t.Fatalf("ReadLocal() error = %v", err)
	}
	if meta.Kind != ArtifactSysext || meta.PayloadVersion != "v1.34" {
		t.Fatalf("meta = %#v", meta)
	}
	if meta.CompatibleRuntime == nil || meta.CompatibleRuntime.Interface != "katl-runtime-1" {
		t.Fatalf("compatible runtime = %#v", meta.CompatibleRuntime)
	}
	if meta.SourceRepo == nil || meta.SourceRepo.ID != "kubernetes" {
		t.Fatalf("source repo = %#v", meta.SourceRepo)
	}
	if meta.PackageVersions["kubeadm"] != "0:1.34.8-150500.1.1" {
		t.Fatalf("package versions = %#v", meta.PackageVersions)
	}
}
