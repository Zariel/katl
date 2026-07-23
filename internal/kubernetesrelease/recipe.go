package kubernetesrelease

import (
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var recipeInputs = []string{
	".github/workflows/kubernetes-bundles.yml",
	"Containerfile.mkosi",
	"cmd/katl-kubernetes-release",
	"cmd/katl-mkosi-artifacts",
	"cmd/katl-publish-kubernetes-sysext",
	"containers-policy.json",
	"go.mod",
	"go.sum",
	"internal/installer/artifact",
	"internal/installer/kubernetesbundle",
	"internal/installer/manifest",
	"internal/installer/sysextcatalog",
	"mkosi.conf",
	"mkosi.profiles/kubernetes-sysext",
	"mkosi.profiles/runtime",
	"scripts/build-kubernetes-sysext",
	"scripts/check-kubernetes-sysext",
	"scripts/mkosi",
}

var recipeDirectories = map[string]bool{
	"cmd/katl-kubernetes-release":         true,
	"cmd/katl-mkosi-artifacts":            true,
	"cmd/katl-publish-kubernetes-sysext":  true,
	"internal/installer/artifact":         true,
	"internal/installer/kubernetesbundle": true,
	"internal/installer/manifest":         true,
	"internal/installer/sysextcatalog":    true,
	"mkosi.profiles/kubernetes-sysext":    true,
	"mkosi.profiles/runtime":              true,
}

func RecipeDigest(root string) (string, error) {
	paths, err := recipeFiles(root)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	for _, path := range paths {
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return "", fmt.Errorf("resolve Kubernetes bundle recipe path %s: %w", path, err)
		}
		if _, err := io.WriteString(digest, filepath.ToSlash(relative)); err != nil {
			return "", err
		}
		if _, err := digest.Write([]byte{0}); err != nil {
			return "", err
		}
		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("open Kubernetes bundle recipe input %s: %w", path, err)
		}
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			return "", fmt.Errorf("hash Kubernetes bundle recipe input %s: %w", path, copyErr)
		}
		if closeErr != nil {
			return "", fmt.Errorf("close Kubernetes bundle recipe input %s: %w", path, closeErr)
		}
		if _, err := digest.Write([]byte{0}); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("sha256:%x", digest.Sum(nil)), nil
}

func RefreshRecipe(root string, supported SupportedVersions) (SupportedVersions, bool, error) {
	digest, err := RecipeDigest(root)
	if err != nil {
		return SupportedVersions{}, false, err
	}
	if supported.RecipeDigest == digest {
		return supported, false, nil
	}
	supported.Versions = copyVersions(supported.Versions)
	supported.RecipeDigest = digest
	for index := range supported.Versions {
		supported.Versions[index].ArtifactRevision++
	}
	if err := validateSupportedVersions(supported); err != nil {
		return SupportedVersions{}, false, err
	}
	return supported, true, nil
}

func recipeFiles(root string) ([]string, error) {
	var paths []string
	for _, input := range recipeInputs {
		path := filepath.Join(root, filepath.FromSlash(input))
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("inspect Kubernetes bundle recipe input %s: %w", input, err)
		}
		if !info.IsDir() {
			paths = append(paths, path)
			continue
		}
		if err := filepath.WalkDir(path, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			if !entry.Type().IsRegular() || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			paths = append(paths, path)
			return nil
		}); err != nil {
			return nil, fmt.Errorf("walk Kubernetes bundle recipe input %s: %w", input, err)
		}
	}
	sort.Strings(paths)
	return paths, nil
}
