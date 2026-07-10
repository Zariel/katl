package scriptstest

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestKatlReleaseArtifactVersion(t *testing.T) {
	repo := repoRoot(t)
	script := filepath.Join(repo, "scripts", "katl-release-artifacts")
	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr string
	}{
		{name: "tag", args: []string{"version", "push", "tag", "v0.1.0"}, want: "0.1.0"},
		{name: "tag without v", args: []string{"version", "push", "tag", "nightly"}, want: "nightly"},
		{name: "branch", args: []string{"version", "push", "branch", "release/0.1.0-rc.1"}, want: "0.1.0-rc.1"},
		{name: "versioned branch", args: []string{"version", "push", "branch", "release/v0.1.0"}, want: "0.1.0"},
		{name: "manual", args: []string{"version", "workflow_dispatch", "branch", "main", "0.1.0-test.1"}, want: "0.1.0-test.1"},
		{name: "wrong branch", args: []string{"version", "push", "branch", "main"}, wantErr: "must start with release/"},
		{name: "nested branch", args: []string{"version", "push", "branch", "release/team/0.1.0"}, wantErr: "unsafe characters"},
		{name: "unsafe manual", args: []string{"version", "workflow_dispatch", "branch", "main", ".hidden"}, wantErr: "unsafe characters"},
		{name: "empty manual", args: []string{"version", "workflow_dispatch", "branch", "main", ""}, wantErr: "version is empty"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(script, tt.args...)
			cmd.Dir = repo
			output, err := cmd.CombinedOutput()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(string(output), tt.wantErr) {
					t.Fatalf("error = %v, output = %q, want %q", err, output, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("version failed: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != tt.want {
				t.Fatalf("version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestKatlReleaseArtifactStage(t *testing.T) {
	repo := repoRoot(t)
	buildDir := t.TempDir()
	output := filepath.Join(t.TempDir(), "dist")
	version := "0.1.0"
	names := []string{
		"katl-installer.efi",
		"katl-installer.vmlinuz",
		"katl-installer.initrd",
		"katlos-install-0.1.0-x86_64.squashfs",
	}
	for _, name := range names {
		writeReleaseArtifact(t, buildDir, name)
	}
	writeReleaseArtifact(t, buildDir, "katl-runtime.efi")
	if err := os.WriteFile(filepath.Join(buildDir, "artifacts.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(filepath.Join(repo, "scripts", "katl-release-artifacts"), "stage", version, output)
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"KATL_MKOSI_BUILD_DIR="+buildDir,
		"KATL_ARCHITECTURE=x86_64",
	)
	if result, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("stage failed: %v\n%s", err, result)
	}

	entries, err := os.ReadDir(output)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, entry := range entries {
		got = append(got, entry.Name())
	}
	sort.Strings(got)
	want := []string{"UNSIGNED.txt"}
	for _, name := range names {
		want = append(want, name, name+".json", name+".sha256")
	}
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("staged files = %#v, want %#v", got, want)
	}
	unsigned, err := os.ReadFile(filepath.Join(output, "UNSIGNED.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unsigned), "unsigned") || !strings.Contains(string(unsigned), "do not establish publisher authenticity") {
		t.Fatalf("unsigned marker = %q", unsigned)
	}
}

func TestKatlReleaseArtifactStageRejectsDigestMismatch(t *testing.T) {
	repo := repoRoot(t)
	buildDir := t.TempDir()
	for _, name := range []string{
		"katl-installer.efi",
		"katl-installer.vmlinuz",
		"katl-installer.initrd",
		"katlos-install-0.1.0-x86_64.squashfs",
	} {
		writeReleaseArtifact(t, buildDir, name)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "katl-installer.efi"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(filepath.Join(repo, "scripts", "katl-release-artifacts"), "stage", "0.1.0", filepath.Join(t.TempDir(), "dist"))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"KATL_MKOSI_BUILD_DIR="+buildDir,
		"KATL_ARCHITECTURE=x86_64",
	)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "FAILED") {
		t.Fatalf("error = %v, output = %q, want checksum failure", err, output)
	}
}

func TestKatlReleaseArtifactStageRejectsMetadataMismatch(t *testing.T) {
	repo := repoRoot(t)
	buildDir := t.TempDir()
	for _, name := range []string{
		"katl-installer.efi",
		"katl-installer.vmlinuz",
		"katl-installer.initrd",
		"katlos-install-0.1.0-x86_64.squashfs",
	} {
		writeReleaseArtifact(t, buildDir, name)
	}
	metadata, err := json.Marshal(struct {
		SHA256    string `json:"sha256"`
		SizeBytes int    `json:"sizeBytes"`
	}{
		SHA256:    strings.Repeat("0", sha256.Size*2),
		SizeBytes: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "katl-installer.efi.json"), append(metadata, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(filepath.Join(repo, "scripts", "katl-release-artifacts"), "stage", "0.1.0", filepath.Join(t.TempDir(), "dist"))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(),
		"KATL_MKOSI_BUILD_DIR="+buildDir,
		"KATL_ARCHITECTURE=x86_64",
	)
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "metadata does not match artifact") {
		t.Fatalf("error = %v, output = %q, want metadata failure", err, output)
	}
}

func writeReleaseArtifact(t *testing.T, dir, name string) {
	t.Helper()
	data := []byte("artifact " + name + "\n")
	digest := sha256.Sum256(data)
	digestHex := hex.EncodeToString(digest[:])
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(struct {
		SHA256    string `json:"sha256"`
		SizeBytes int    `json:"sizeBytes"`
	}{
		SHA256:    digestHex,
		SizeBytes: len(data),
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata = append(metadata, '\n')
	if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
	checksum := fmt.Sprintf("%s  %s\n", digestHex, name)
	if err := os.WriteFile(filepath.Join(dir, name+".sha256"), []byte(checksum), 0o644); err != nil {
		t.Fatal(err)
	}
}
