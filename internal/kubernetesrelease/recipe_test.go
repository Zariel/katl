package kubernetesrelease

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecipeDigestChangesWithProductionInput(t *testing.T) {
	root := writeRecipeFixture(t)
	first, err := RecipeDigest(root)
	if err != nil {
		t.Fatalf("RecipeDigest() error = %v", err)
	}
	path := filepath.Join(root, "scripts", "mkosi")
	if err := os.WriteFile(path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := RecipeDigest(root)
	if err != nil {
		t.Fatalf("RecipeDigest() error = %v", err)
	}
	if first == second {
		t.Fatal("recipe digest did not change")
	}
}

func TestRecipeDigestIgnoresTests(t *testing.T) {
	root := writeRecipeFixture(t)
	path := filepath.Join(root, "cmd", "katl-mkosi-artifacts", "main_test.go")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := RecipeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	second, err := RecipeDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("test-only change affected recipe digest")
	}
}

func TestRefreshRecipeAdvancesEverySupportedArtifact(t *testing.T) {
	root := writeRecipeFixture(t)
	supported := SupportedVersions{
		APIVersion:   APIVersion,
		Kind:         Kind,
		RecipeDigest: "sha256:" + strings.Repeat("a", 64),
		Versions: []SupportedVersion{
			testSupportedVersion("v1.35.9", 2),
			testSupportedVersion("v1.36.3", 1),
		},
	}
	updated, changed, err := RefreshRecipe(root, supported)
	if err != nil {
		t.Fatalf("RefreshRecipe() error = %v", err)
	}
	if !changed || updated.Versions[0].ArtifactRevision != 3 || updated.Versions[1].ArtifactRevision != 2 {
		t.Fatalf("updated = %#v, changed = %t", updated, changed)
	}
	again, changed, err := RefreshRecipe(root, updated)
	if err != nil {
		t.Fatalf("RefreshRecipe() error = %v", err)
	}
	if changed || again.Versions[0].ArtifactRevision != 3 || again.Versions[1].ArtifactRevision != 2 {
		t.Fatalf("second refresh = %#v, changed = %t", again, changed)
	}
}

func TestDefaultRecipeDigestMatchesRepository(t *testing.T) {
	supported, err := DefaultSupportedVersions()
	if err != nil {
		t.Fatal(err)
	}
	digest, err := RecipeDigest(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if supported.RecipeDigest != digest {
		t.Fatalf("recipe digest = %s, want %s; run go run ./cmd/katl-kubernetes-release refresh-rebuilds", supported.RecipeDigest, digest)
	}
}

func testSupportedVersion(payload string, revision int) SupportedVersion {
	numeric := strings.TrimPrefix(payload, "v")
	minor := numeric[:strings.LastIndex(numeric, ".")]
	return SupportedVersion{
		PayloadVersion:   payload,
		ArtifactRevision: revision,
		Packages: PackageVersions{
			Kubeadm:  "0:" + numeric + "-1",
			Kubelet:  "0:" + numeric + "-1",
			Kubectl:  "0:" + numeric + "-1",
			CRITools: "0:" + minor + ".0-1",
		},
	}
}

func writeRecipeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, input := range recipeInputs {
		path := filepath.Join(root, filepath.FromSlash(input))
		if recipeDirectories[input] {
			if err := os.MkdirAll(path, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(path, "input"), []byte(input), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
