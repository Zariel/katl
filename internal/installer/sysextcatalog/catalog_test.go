package sysextcatalog

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/katl-dev/katl/internal/installer/artifact"
)

func TestReadCatalogFixture(t *testing.T) {
	catalog, err := Read(filepath.Join("testdata", "kubernetes-sysext-catalog.json"))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if len(catalog.Entries) != 3 {
		t.Fatalf("entry count = %d, want 3", len(catalog.Entries))
	}

	seen := map[string]bool{}
	for _, entry := range catalog.Entries {
		seen[entry.KubernetesMinor+"/"+entry.PayloadVersion] = true
	}
	for _, key := range []string{"v1.35/v1.35.7", "v1.36/v1.36.0", "v1.36/v1.36.1"} {
		if !seen[key] {
			t.Fatalf("catalog missing %s: %#v", key, seen)
		}
	}

	entry := catalog.Entries[0]
	if entry.Name != "kubernetes" || entry.ArtifactVersion != "6db181a573ef" || entry.PayloadVersion != "v1.36.1" {
		t.Fatalf("first entry identity = %#v", entry)
	}
	if entry.SourceRepo.BaseURL != "https://pkgs.k8s.io/core:/stable:/v1.36/rpm/" {
		t.Fatalf("source repo = %#v", entry.SourceRepo)
	}
	if entry.PackageVersions["kubeadm"] != "0:1.36.1-150500.1.1" {
		t.Fatalf("package versions = %#v", entry.PackageVersions)
	}

	if err := ValidateForRuntime(entry, Runtime{Interface: "katl-runtime-1", Architecture: "x86_64"}); err != nil {
		t.Fatalf("ValidateForRuntime() error = %v", err)
	}
}

func TestMarshalRoundTripValidates(t *testing.T) {
	catalog := validCatalog(t)

	data, err := Marshal(catalog)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if got := string(data[len(data)-1]); got != "\n" {
		t.Fatalf("Marshal() final byte = %q, want newline", got)
	}
}

func TestEntryFromLocalMeta(t *testing.T) {
	meta := artifact.LocalMeta{
		Name:           "kubernetes",
		Kind:           artifact.ArtifactSysext,
		Format:         "sysext",
		Path:           "katl-kubernetes-v1.36.1-x86_64.raw",
		SizeBytes:      301191168,
		SHA256:         "b6d80cca75983945d7a89339562f0c93edf006aaa0c1aee57b77e173071cddde",
		Version:        "6db181a573ef",
		PayloadVersion: "v1.36.1",
		Architecture:   "x86_64",
		SourceRepo: &artifact.SourceRepo{
			ID:      "kubernetes",
			BaseURL: "https://pkgs.k8s.io/core:/stable:/v1.36/rpm/",
			Minor:   "v1.36",
		},
		PackageVersions: map[string]string{
			"kubeadm": "0:1.36.1-150500.1.1",
		},
		RuntimeInterface: "katl-runtime-1",
		CompatibleRuntime: &artifact.Compat{
			Interface: "katl-runtime-1",
		},
	}

	entry, err := EntryFromLocalMeta(meta)
	if err != nil {
		t.Fatalf("EntryFromLocalMeta() error = %v", err)
	}
	if entry.KubernetesMinor != "v1.36" || entry.LocalPath != meta.Path {
		t.Fatalf("entry = %#v", entry)
	}
	if len(entry.RuntimeInterfaces) != 1 || entry.RuntimeInterfaces[0] != "katl-runtime-1" {
		t.Fatalf("runtime interfaces = %#v", entry.RuntimeInterfaces)
	}

	meta.PackageVersions["kubeadm"] = "changed"
	if entry.PackageVersions["kubeadm"] != "0:1.36.1-150500.1.1" {
		t.Fatalf("package versions were not copied: %#v", entry.PackageVersions)
	}
}

func TestEntryFromLocalMetaRejectsNonSysext(t *testing.T) {
	_, err := EntryFromLocalMeta(artifact.LocalMeta{Kind: artifact.ArtifactRuntimeRoot})
	if !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("EntryFromLocalMeta() error = %v, want ErrInvalidCatalog", err)
	}
}

func TestValidateRejectsInvalidCatalog(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Catalog)
	}{
		{
			name: "missing digest",
			edit: func(c *Catalog) {
				c.Entries[0].SHA256 = ""
			},
		},
		{
			name: "uppercase digest",
			edit: func(c *Catalog) {
				c.Entries[0].SHA256 = "B6D80CCA75983945D7A89339562F0C93EDF006AAA0C1AEE57B77E173071CDDDE"
			},
		},
		{
			name: "malformed payload version",
			edit: func(c *Catalog) {
				c.Entries[0].PayloadVersion = "v1.36"
			},
		},
		{
			name: "payload minor mismatch",
			edit: func(c *Catalog) {
				c.Entries[0].KubernetesMinor = "v1.35"
			},
		},
		{
			name: "source repo minor mismatch",
			edit: func(c *Catalog) {
				c.Entries[0].SourceRepo.Minor = "v1.35"
			},
		},
		{
			name: "missing artifact location",
			edit: func(c *Catalog) {
				c.Entries[0].URL = ""
				c.Entries[0].LocalPath = ""
			},
		},
		{
			name: "relative URL",
			edit: func(c *Catalog) {
				c.Entries[0].URL = "/katl-kubernetes.raw"
				c.Entries[0].LocalPath = ""
			},
		},
		{
			name: "missing runtime interfaces",
			edit: func(c *Catalog) {
				c.Entries[0].RuntimeInterfaces = nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			catalog := validCatalog(t)
			tt.edit(&catalog)

			err := Validate(catalog)
			if !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("Validate() error = %v, want ErrInvalidCatalog", err)
			}
		})
	}
}

func TestValidateForRuntimeRejectsMismatch(t *testing.T) {
	entry := validCatalog(t).Entries[0]

	tests := []struct {
		name    string
		runtime Runtime
	}{
		{
			name:    "architecture",
			runtime: Runtime{Interface: "katl-runtime-1", Architecture: "aarch64"},
		},
		{
			name:    "runtime interface",
			runtime: Runtime{Interface: "katl-runtime-2", Architecture: "x86_64"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateForRuntime(entry, tt.runtime)
			if !errors.Is(err, ErrInvalidCatalog) {
				t.Fatalf("ValidateForRuntime() error = %v, want ErrInvalidCatalog", err)
			}
		})
	}
}

func TestKubernetesMinor(t *testing.T) {
	if got := KubernetesMinor("v1.36.1"); got != "v1.36" {
		t.Fatalf("KubernetesMinor() = %q, want v1.36", got)
	}
	if got := KubernetesMinor("v1.36"); got != "" {
		t.Fatalf("KubernetesMinor() = %q, want empty", got)
	}
}

func validCatalog(t *testing.T) Catalog {
	t.Helper()

	catalog, err := Read(filepath.Join("testdata", "kubernetes-sysext-catalog.json"))
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return catalog
}
