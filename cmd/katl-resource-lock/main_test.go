package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zariel/katl/internal/resourcetest"
)

func TestRunRefreshAndVerify(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "resource-manifest.json")
	lockPath := filepath.Join(dir, "mkosi.profiles", "resource-package-lock.json")
	manifest := commandManifest("")
	writeTestManifest(t, manifestPath, manifest)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := run([]string{"refresh", "--manifest", manifestPath, "--output", lockPath}, &stdout, &stderr); err != nil {
		t.Fatalf("refresh error = %v stderr=%s", err, stderr.String())
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	manifest.PackageSets[0].LockDigest = resourcetest.PackageLockDigest(lockData)
	writeTestManifest(t, manifestPath, manifest)

	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"verify", "--manifest", manifestPath, "--lock", lockPath}, &stdout, &stderr); err != nil {
		t.Fatalf("verify error = %v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "verified:") {
		t.Fatalf("stdout = %q, want verified output", stdout.String())
	}
}

func TestRunVerifyRejectsPackageDrift(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "resource-manifest.json")
	lockPath := filepath.Join(dir, "resource-package-lock.json")
	manifest := commandManifest("")
	writeTestManifest(t, manifestPath, manifest)
	if err := run([]string{"refresh", "--manifest", manifestPath, "--output", lockPath}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	lockData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	manifest.PackageSets[0].LockDigest = resourcetest.PackageLockDigest(lockData)
	manifest.PackageSets[0].Packages[0].NEVRA = "systemd-0:259.7-1.fc44.x86_64"
	writeTestManifest(t, manifestPath, manifest)

	err = run([]string{"verify", "--manifest", manifestPath, "--lock", lockPath}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "NEVRA drift") {
		t.Fatalf("verify error = %v, want NEVRA drift", err)
	}
}

func TestRunRequiresManifest(t *testing.T) {
	err := run([]string{"verify"}, &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--manifest is required") {
		t.Fatalf("run() error = %v, want manifest requirement", err)
	}
}

func TestRunAddRPMPackageSet(t *testing.T) {
	oldQuery := queryRPMPackages
	t.Cleanup(func() { queryRPMPackages = oldQuery })
	queryRPMPackages = func(root string) ([]resourcetest.Package, error) {
		if root != "build/mkosi/katl-runtime-root" {
			t.Fatalf("root = %q", root)
		}
		return []resourcetest.Package{{
			Name:  "systemd",
			NEVRA: "systemd-0:259.6-1.fc44.x86_64",
		}}, nil
	}

	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "resource-manifest.json")
	lockManifestPath := filepath.Join(dir, "lock-source-manifest.json")
	outputPath := filepath.Join(dir, "updated-manifest.json")
	lockPath := filepath.Join(dir, "resource-package-lock.json")
	writeTestManifest(t, lockManifestPath, commandManifest(""))
	if err := run([]string{"refresh", "--manifest", lockManifestPath, "--output", lockPath}, &bytes.Buffer{}, &bytes.Buffer{}); err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	writeTestManifest(t, manifestPath, commandManifestSkeleton())

	var stdout bytes.Buffer
	err := run([]string{
		"add-rpm-package-set",
		"--manifest", manifestPath,
		"--output", outputPath,
		"--name", "runtime",
		"--source", "mkosi.profiles/runtime",
		"--root", "build/mkosi/katl-runtime-root",
		"--lock", lockPath,
		"--distribution", "fedora",
		"--release", "44",
		"--architecture", "x86_64",
		"--repository", "fedora=https://example.invalid/fedora/44",
		"--profile-name", "runtime",
		"--profile-path", "mkosi.profiles/runtime",
		"--profile-config-sha256", strings.Repeat("a", 64),
	}, &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("add-rpm-package-set error = %v", err)
	}
	updated, err := readManifest(outputPath)
	if err != nil {
		t.Fatalf("read updated manifest: %v", err)
	}
	if updated.PackageSets[0].LockDigest == "" || updated.PackageSets[0].Repositories[0].ID != "fedora" {
		t.Fatalf("updated manifest package set = %#v", updated.PackageSets[0])
	}
	if !strings.Contains(stdout.String(), "packages: 1") {
		t.Fatalf("stdout = %q, want package count", stdout.String())
	}
}

func commandManifest(lockDigest string) resourcetest.Manifest {
	manifest := commandManifestSkeleton()
	manifest.PackageSets = []resourcetest.PackageSet{{
		Name:         "runtime",
		Source:       "mkosi.profiles/runtime",
		Digest:       strings.Repeat("b", 64),
		LockDigest:   lockDigest,
		Distribution: "fedora",
		Release:      "44",
		Architecture: "x86_64",
		Repositories: []resourcetest.PackageRepository{{
			ID:      "fedora",
			BaseURL: "https://example.invalid/fedora/44",
		}},
		Packages: []resourcetest.Package{{
			Name:     "systemd",
			NEVRA:    "systemd-0:259.6-1.fc44.x86_64",
			Checksum: strings.Repeat("c", 64),
		}},
	},
	}
	return manifest
}

func commandManifestSkeleton() resourcetest.Manifest {
	return resourcetest.Manifest{
		APIVersion: resourcetest.APIVersion,
		Kind:       resourcetest.Kind,
		RunID:      "resource-run",
		Created:    time.Date(2026, 6, 6, 12, 0, 0, 0, time.UTC),
		Git:        resourcetest.GitState{Revision: "baf1ac7"},
		Tools: []resourcetest.Tool{{
			Name:    "mkosi",
			Version: "26",
		}},
		MkosiProfiles: []resourcetest.MkosiProfile{{
			Name:          "runtime",
			Path:          "mkosi.profiles/runtime",
			ConfigDigest:  strings.Repeat("a", 64),
			PackageSetRef: "runtime",
		}},
	}
}

func writeTestManifest(t *testing.T, path string, manifest resourcetest.Manifest) {
	t.Helper()
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
