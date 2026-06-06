package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAndPath(t *testing.T) {
	repo := testRepoRoot(t)
	workDir := testWorkDir(t, repo)

	installerUKI := writeTestFile(t, workDir, "installer.efi", "installer uki")
	installerKernel := writeTestFile(t, workDir, "vmlinuz", "kernel")
	installerInitrd := writeTestFile(t, workDir, "initrd", "initrd")
	runtimeUKI := writeTestFile(t, workDir, "runtime.efi", "runtime uki")
	runtimeRoot := writeTestFile(t, workDir, "runtime-root.squashfs", "runtime root")
	katlosImage := writeTestFile(t, workDir, "katlos image.squashfs", "katlos image")
	for _, path := range []string{runtimeUKI, runtimeRoot, katlosImage} {
		writeTestChecksum(t, path)
		writeTestJSON(t, path+".json", map[string]any{"path": filepath.Base(path)})
	}

	indexPath := filepath.Join(workDir, "artifacts.json")
	var stdout bytes.Buffer
	err := run([]string{"write", indexPath}, &stdout, &bytes.Buffer{}, []string{
		"KATL_BUILD_COMMIT=test-build",
		"KATL_VERSION=0.1.\"quoted\"\\version",
		"KATL_ARCHITECTURE=x86_64",
		"KATL_INSTALLER_INTERFACE=katl-installer-test",
		"KATL_INSTALLER_UKI=" + installerUKI,
		"KATL_INSTALLER_KERNEL=" + installerKernel,
		"KATL_INSTALLER_INITRD=" + installerInitrd,
		"KATL_RUNTIME_UKI=" + runtimeUKI,
		"KATL_RUNTIME_UKI_METADATA=" + runtimeUKI + ".json",
		"KATL_RUNTIME_UKI_CHECKSUM=" + runtimeUKI + ".sha256",
		"KATL_RUNTIME_ARTIFACT=" + runtimeRoot,
		"KATL_RUNTIME_METADATA=" + runtimeRoot + ".json",
		"KATL_RUNTIME_CHECKSUM=" + runtimeRoot + ".sha256",
		"KATL_KATLOS_IMAGE=" + katlosImage,
		"KATL_KATLOS_IMAGE_METADATA=" + katlosImage + ".json",
		"KATL_KATLOS_IMAGE_CHECKSUM=" + katlosImage + ".sha256",
	})
	if err != nil {
		t.Fatalf("write error = %v", err)
	}
	if got := stdout.String(); !strings.Contains(got, "artifact index: ") {
		t.Fatalf("stdout = %q", got)
	}

	var index artifactIndex
	readTestJSON(t, indexPath, &index)
	if index.SchemaVersion != 1 || index.Generation != "test-build" {
		t.Fatalf("index header = %#v", index)
	}
	if len(index.Artifacts) != 6 {
		t.Fatalf("artifact count = %d, want 6: %#v", len(index.Artifacts), index.Artifacts)
	}
	byKind := map[string]artifactEntry{}
	for _, artifact := range index.Artifacts {
		byKind[artifact.Kind] = artifact
		if artifact.Path == "" || artifact.SHA256 == "" || artifact.SizeBytes == 0 {
			t.Fatalf("artifact missing bytes identity: %#v", artifact)
		}
	}
	if byKind["katlos-install-image"].Path != relPath(repo, katlosImage) {
		t.Fatalf("katlos path = %q, want %q", byKind["katlos-install-image"].Path, relPath(repo, katlosImage))
	}

	var metadata bootMetadata
	readTestJSON(t, installerUKI+".json", &metadata)
	if metadata.Kind != "InstallerBootArtifact" || metadata.ArtifactRole != "installer-uki" {
		t.Fatalf("installer metadata = %#v", metadata)
	}
	if metadata.Version != `0.1."quoted"\version` {
		t.Fatalf("installer metadata version = %q", metadata.Version)
	}
	if metadata.InstallerInterface != "katl-installer-test" {
		t.Fatalf("installer interface = %q", metadata.InstallerInterface)
	}

	stdout.Reset()
	err = run([]string{"path", "runtime-root", indexPath}, &stdout, &bytes.Buffer{}, []string{})
	if err != nil {
		t.Fatalf("path error = %v", err)
	}
	if stdout.String() != runtimeRoot {
		t.Fatalf("runtime-root path = %q, want %q", stdout.String(), runtimeRoot)
	}
}

func TestPathForKindRejectsDuplicate(t *testing.T) {
	repo := testRepoRoot(t)
	indexPath := filepath.Join(testWorkDir(t, repo), "artifacts.json")
	writeTestJSON(t, indexPath, artifactIndex{
		SchemaVersion: 1,
		Artifacts: []artifactEntry{
			{Kind: "runtime-root", Path: "build/mkosi/root-a.squashfs"},
			{Kind: "runtime-root", Path: "build/mkosi/root-b.squashfs"},
		},
	})

	_, err := pathForKind(indexPath, repo, "runtime-root")
	if err == nil || !strings.Contains(err.Error(), "appears more than once") {
		t.Fatalf("pathForKind duplicate error = %v", err)
	}
}

func testRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatalf("repoRoot error = %v", err)
	}
	return root
}

func testWorkDir(t *testing.T, repo string) string {
	t.Helper()
	buildDir := filepath.Join(repo, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", buildDir, err)
	}
	dir, err := os.MkdirTemp(buildDir, "katl-mkosi-artifacts-")
	if err != nil {
		t.Fatalf("MkdirTemp(%s) error = %v", buildDir, err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			t.Fatalf("RemoveAll(%s) error = %v", dir, err)
		}
	})
	return dir
}

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
	return path
}

func writeTestChecksum(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	sum := sha256.Sum256(data)
	content := hex.EncodeToString(sum[:]) + "  " + filepath.Base(path) + "\n"
	if err := os.WriteFile(path+".sha256", []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%s.sha256) error = %v", path, err)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(%s) error = %v", path, err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", path, err)
	}
}

func readTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	if err := json.Unmarshal(data, value); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v\n%s", path, err, data)
	}
}
