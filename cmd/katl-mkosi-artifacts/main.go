package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	defaultVersion            = "0.0.0-dev"
	defaultInstallerInterface = "katl-installer-boot-1"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "katl-mkosi-artifacts: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer, environ []string) error {
	command := "write"
	if len(args) > 0 {
		command = args[0]
		args = args[1:]
	}

	repoRoot, err := repoRoot()
	if err != nil {
		return err
	}
	cfg := configFromEnv(envMap(environ), repoRoot)

	switch command {
	case "write":
		indexPath := cfg.DefaultIndex
		if len(args) > 1 {
			return fmt.Errorf("write accepts at most one INDEX argument")
		}
		if len(args) == 1 {
			indexPath = absPath(repoRoot, args[0])
		}
		if err := writeIndex(indexPath, cfg); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "artifact index: %s\n", relPath(repoRoot, indexPath))
		return nil
	case "path":
		if len(args) < 1 {
			return fmt.Errorf("path requires KIND")
		}
		if len(args) > 2 {
			return fmt.Errorf("path accepts KIND and optional INDEX")
		}
		indexPath := cfg.DefaultIndex
		if len(args) == 2 {
			indexPath = absPath(repoRoot, args[1])
		}
		path, err := pathForKind(indexPath, repoRoot, args[0])
		if err != nil {
			return err
		}
		fmt.Fprint(stdout, path)
		return nil
	case "write-runtime-root":
		return runWriteRuntimeRoot(args, stdout, stderr, cfg)
	case "write-runtime-uki":
		return runWriteRuntimeUKI(args, stdout, stderr, cfg)
	case "write-kubernetes-sysext":
		return runWriteKubernetesSysext(args, stdout, stderr, cfg)
	case "-h", "--help":
		fmt.Fprint(stdout, usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q\n%s", command, usage)
	}
}

const usage = `Usage: scripts/mkosi-artifacts [write [INDEX]]
       scripts/mkosi-artifacts path KIND [INDEX]
       katl-mkosi-artifacts write-runtime-root --artifact PATH
       katl-mkosi-artifacts write-runtime-uki --artifact PATH --runtime-artifact PATH --runtime-sha256 SHA --kernel-version VERSION
       katl-mkosi-artifacts write-kubernetes-sysext --artifact PATH --payload-version VERSION --kubeadm-version VERSION --kubelet-version VERSION --kubectl-version VERSION --cri-tools-version VERSION

Write or query the local mkosi artifact index.

Kinds:
  installer-uki
  installer-kernel
  installer-initrd
  runtime-uki
  runtime-root
  katlos-install-image
`

type config struct {
	RepoRoot           string
	DefaultIndex       string
	InstallerUKI       string
	InstallerKernel    string
	InstallerInitrd    string
	RuntimeUKI         string
	RuntimeUKIMetadata string
	RuntimeUKIChecksum string
	RuntimeRoot        string
	RuntimeMetadata    string
	RuntimeChecksum    string
	KatlOSImage        string
	KatlOSMetadata     string
	KatlOSChecksum     string
	KatlOSExplicit     bool
	Generation         string
	Version            string
	Architecture       string
	InstallerInterface string
}

func configFromEnv(env map[string]string, repo string) config {
	buildDir := filepath.Join(repo, "build", "mkosi")
	version := envDefault(env, "KATL_VERSION", defaultVersion)
	architecture := envDefaultFunc(env, "KATL_ARCHITECTURE", hostArchitecture)
	katlosDefault := filepath.Join(buildDir, "katlos-install-"+version+"-"+architecture+".squashfs")
	runtimeUKI := envPath(env, repo, "KATL_RUNTIME_UKI", filepath.Join(buildDir, "katl-runtime.efi"))
	runtimeRoot := envPath(env, repo, "KATL_RUNTIME_ARTIFACT", filepath.Join(buildDir, "katl-runtime-root.squashfs"))
	katlosImage, katlosExplicit := envPathExplicit(env, repo, "KATL_KATLOS_IMAGE", katlosDefault)

	return config{
		RepoRoot:           repo,
		DefaultIndex:       filepath.Join(buildDir, "artifacts.json"),
		InstallerUKI:       envPath(env, repo, "KATL_INSTALLER_UKI", filepath.Join(buildDir, "katl-installer.efi")),
		InstallerKernel:    envPath(env, repo, "KATL_INSTALLER_KERNEL", filepath.Join(buildDir, "katl-installer.vmlinuz")),
		InstallerInitrd:    envPath(env, repo, "KATL_INSTALLER_INITRD", filepath.Join(buildDir, "katl-installer.initrd")),
		RuntimeUKI:         runtimeUKI,
		RuntimeUKIMetadata: envPath(env, repo, "KATL_RUNTIME_UKI_METADATA", runtimeUKI+".json"),
		RuntimeUKIChecksum: envPath(env, repo, "KATL_RUNTIME_UKI_CHECKSUM", runtimeUKI+".sha256"),
		RuntimeRoot:        runtimeRoot,
		RuntimeMetadata:    envPath(env, repo, "KATL_RUNTIME_METADATA", runtimeRoot+".json"),
		RuntimeChecksum:    envPath(env, repo, "KATL_RUNTIME_CHECKSUM", runtimeRoot+".sha256"),
		KatlOSImage:        katlosImage,
		KatlOSMetadata:     envPath(env, repo, "KATL_KATLOS_IMAGE_METADATA", katlosImage+".json"),
		KatlOSChecksum:     envPath(env, repo, "KATL_KATLOS_IMAGE_CHECKSUM", katlosImage+".sha256"),
		KatlOSExplicit:     katlosExplicit,
		Generation:         envDefaultFunc(env, "KATL_BUILD_COMMIT", func() string { return gitDescribe(repo) }),
		Version:            version,
		Architecture:       architecture,
		InstallerInterface: envDefault(env, "KATL_INSTALLER_INTERFACE", defaultInstallerInterface),
	}
}

type artifactIndex struct {
	SchemaVersion int             `json:"schemaVersion"`
	GeneratedAt   string          `json:"generatedAt"`
	Generation    string          `json:"generation"`
	Artifacts     []artifactEntry `json:"artifacts"`
}

type artifactEntry struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	Format       string `json:"format"`
	SizeBytes    int64  `json:"sizeBytes"`
	SHA256       string `json:"sha256"`
	MetadataPath string `json:"metadataPath,omitempty"`
	ChecksumPath string `json:"checksumPath,omitempty"`
}

type bootMetadata struct {
	APIVersion               string   `json:"apiVersion"`
	Kind                     string   `json:"kind"`
	ArtifactRole             string   `json:"artifactRole"`
	Format                   string   `json:"format"`
	Version                  string   `json:"version"`
	BuildID                  string   `json:"buildID"`
	Architecture             string   `json:"architecture"`
	Path                     string   `json:"path"`
	SizeBytes                int64    `json:"sizeBytes"`
	SHA256                   string   `json:"sha256"`
	CreatedAt                string   `json:"createdAt"`
	InstallerInterface       string   `json:"installerInterface"`
	DefaultKernelCommandLine []string `json:"defaultKernelCommandLine"`
	SupportedInputModes      []string `json:"supportedInputModes"`
}

type localMetadata struct {
	Name              string            `json:"name"`
	Kind              string            `json:"kind"`
	Format            string            `json:"format"`
	Path              string            `json:"path"`
	SizeBytes         int64             `json:"sizeBytes"`
	SHA256            string            `json:"sha256"`
	Compression       string            `json:"compression,omitempty"`
	Generation        string            `json:"generation,omitempty"`
	Version           string            `json:"version,omitempty"`
	PayloadVersion    string            `json:"payloadVersion,omitempty"`
	Architecture      string            `json:"architecture"`
	SourceRepo        *sourceRepo       `json:"sourceRepo,omitempty"`
	PackageVersions   map[string]string `json:"packageVersions,omitempty"`
	RuntimeInterface  string            `json:"runtimeInterface"`
	CompatibleBoot    *bootCompat       `json:"compatibleBoot,omitempty"`
	CompatibleRuntime *runtimeCompat    `json:"compatibleRuntime,omitempty"`
	KernelVersion     string            `json:"kernelVersion,omitempty"`
	KernelCommandLine []string          `json:"kernelCommandLine,omitempty"`
	Created           string            `json:"created"`
}

type bootCompat struct {
	Kind              string   `json:"kind"`
	RuntimeInterface  string   `json:"runtimeInterface"`
	KernelCommandLine []string `json:"kernelCommandLine,omitempty"`
}

type runtimeCompat struct {
	Interface      string `json:"interface"`
	ArtifactPath   string `json:"artifactPath,omitempty"`
	ArtifactSHA256 string `json:"artifactSHA256,omitempty"`
}

type sourceRepo struct {
	ID      string `json:"id"`
	BaseURL string `json:"baseURL"`
	Minor   string `json:"minor"`
}

func runWriteRuntimeRoot(args []string, stdout, stderr io.Writer, cfg config) error {
	flags := flag.NewFlagSet("katl-mkosi-artifacts write-runtime-root", flag.ContinueOnError)
	flags.SetOutput(stderr)
	artifact := flags.String("artifact", filepath.Join("build", "mkosi", "katl-runtime-root.squashfs"), "runtime root SquashFS artifact")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	artifactPath := absPath(cfg.RepoRoot, *artifact)
	size, digest, err := fileInfo(artifactPath)
	if err != nil {
		return err
	}
	if err := writeChecksum(artifactPath); err != nil {
		return err
	}
	metadata := localMetadata{
		Name:             "runtime-root",
		Kind:             "runtime-root",
		Format:           "squashfs",
		Path:             filepath.Base(artifactPath),
		SizeBytes:        size,
		SHA256:           digest,
		Compression:      "zstd",
		Generation:       cfg.Generation,
		Architecture:     cfg.Architecture,
		RuntimeInterface: "katl-runtime-1",
		CompatibleBoot: &bootCompat{
			Kind:              "uki",
			RuntimeInterface:  "katl-runtime-1",
			KernelCommandLine: []string{"rootfstype=squashfs", "ro"},
		},
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(metadataPath(artifactPath), metadata, cfg.RepoRoot); err != nil {
		return err
	}
	fmt.Fprintln(stdout, digest)
	return nil
}

func runWriteRuntimeUKI(args []string, stdout, stderr io.Writer, cfg config) error {
	flags := flag.NewFlagSet("katl-mkosi-artifacts write-runtime-uki", flag.ContinueOnError)
	flags.SetOutput(stderr)
	artifact := flags.String("artifact", filepath.Join("build", "mkosi", "katl-runtime.efi"), "runtime UKI artifact")
	runtimeArtifact := flags.String("runtime-artifact", filepath.Join("build", "mkosi", "katl-runtime-root.squashfs"), "compatible runtime root artifact")
	runtimeSHA := flags.String("runtime-sha256", "", "compatible runtime root SHA-256")
	kernelVersion := flags.String("kernel-version", "", "runtime kernel version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*runtimeSHA) == "" {
		return fmt.Errorf("--runtime-sha256 is required")
	}
	if strings.TrimSpace(*kernelVersion) == "" {
		return fmt.Errorf("--kernel-version is required")
	}

	artifactPath := absPath(cfg.RepoRoot, *artifact)
	runtimePath := absPath(cfg.RepoRoot, *runtimeArtifact)
	size, digest, err := fileInfo(artifactPath)
	if err != nil {
		return err
	}
	if err := writeChecksum(artifactPath); err != nil {
		return err
	}
	metadata := localMetadata{
		Name:             "runtime-uki",
		Kind:             "runtime-uki",
		Format:           "uki",
		Path:             filepath.Base(artifactPath),
		SizeBytes:        size,
		SHA256:           digest,
		Version:          cfg.Generation,
		Architecture:     cfg.Architecture,
		RuntimeInterface: "katl-runtime-1",
		CompatibleRuntime: &runtimeCompat{
			Interface:      "katl-runtime-1",
			ArtifactPath:   filepath.Base(runtimePath),
			ArtifactSHA256: *runtimeSHA,
		},
		KernelVersion:     *kernelVersion,
		KernelCommandLine: []string{"rootfstype=squashfs", "ro"},
		Created:           time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(metadataPath(artifactPath), metadata, cfg.RepoRoot); err != nil {
		return err
	}
	fmt.Fprintln(stdout, digest)
	return nil
}

func runWriteKubernetesSysext(args []string, stdout, stderr io.Writer, cfg config) error {
	flags := flag.NewFlagSet("katl-mkosi-artifacts write-kubernetes-sysext", flag.ContinueOnError)
	flags.SetOutput(stderr)
	artifact := flags.String("artifact", filepath.Join("build", "mkosi", "katl-kubernetes.raw"), "Kubernetes sysext artifact")
	payloadVersion := flags.String("payload-version", "", "Kubernetes payload version")
	kubeadmVersion := flags.String("kubeadm-version", "", "resolved kubeadm package version")
	kubeletVersion := flags.String("kubelet-version", "", "resolved kubelet package version")
	kubectlVersion := flags.String("kubectl-version", "", "resolved kubectl package version")
	criToolsVersion := flags.String("cri-tools-version", "", "resolved cri-tools package version")
	runtimeArtifact := flags.String("runtime-artifact", filepath.Join("build", "mkosi", "katl-runtime-root.squashfs"), "compatible runtime root artifact")
	runtimeMetadata := flags.String("runtime-metadata", filepath.Join("build", "mkosi", "katl-runtime-root.squashfs.json"), "compatible runtime root metadata")
	runtimeSHA := flags.String("runtime-sha256", "", "compatible runtime root SHA-256 override")
	repoID := flags.String("repo-id", "", "Kubernetes package repository ID")
	repoBaseURL := flags.String("repo-base-url", "", "Kubernetes package repository base URL")
	repoMinor := flags.String("repo-minor", "", "Kubernetes package minor")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}
	for name, value := range map[string]string{
		"--payload-version":   *payloadVersion,
		"--kubeadm-version":   *kubeadmVersion,
		"--kubelet-version":   *kubeletVersion,
		"--kubectl-version":   *kubectlVersion,
		"--cri-tools-version": *criToolsVersion,
		"--repo-id":           *repoID,
		"--repo-base-url":     *repoBaseURL,
		"--repo-minor":        *repoMinor,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required", name)
		}
	}

	artifactPath := absPath(cfg.RepoRoot, *artifact)
	runtimePath := absPath(cfg.RepoRoot, *runtimeArtifact)
	sha, err := resolveRuntimeSHA(cfg.RepoRoot, *runtimeSHA, *runtimeMetadata)
	if err != nil {
		return err
	}
	size, digest, err := fileInfo(artifactPath)
	if err != nil {
		return err
	}
	if err := writeChecksum(artifactPath); err != nil {
		return err
	}
	metadata := localMetadata{
		Name:           "kubernetes",
		Kind:           "sysext",
		Format:         "sysext",
		Path:           filepath.Base(artifactPath),
		SizeBytes:      size,
		SHA256:         digest,
		Version:        cfg.Generation,
		PayloadVersion: *payloadVersion,
		Architecture:   cfg.Architecture,
		SourceRepo: &sourceRepo{
			ID:      *repoID,
			BaseURL: *repoBaseURL,
			Minor:   *repoMinor,
		},
		PackageVersions: map[string]string{
			"kubeadm":   *kubeadmVersion,
			"kubelet":   *kubeletVersion,
			"kubectl":   *kubectlVersion,
			"cri-tools": *criToolsVersion,
		},
		RuntimeInterface: "katl-runtime-1",
		CompatibleRuntime: &runtimeCompat{
			Interface:      "katl-runtime-1",
			ArtifactPath:   filepath.Base(runtimePath),
			ArtifactSHA256: sha,
		},
		Created: time.Now().UTC().Format(time.RFC3339),
	}
	if err := writeJSON(metadataPath(artifactPath), metadata, cfg.RepoRoot); err != nil {
		return err
	}
	fmt.Fprintln(stdout, digest)
	return nil
}

func writeIndex(indexPath string, cfg config) error {
	for _, input := range []struct {
		label string
		path  string
	}{
		{"installer UKI", cfg.InstallerUKI},
		{"installer kernel", cfg.InstallerKernel},
		{"installer initrd", cfg.InstallerInitrd},
		{"runtime UKI", cfg.RuntimeUKI},
		{"runtime UKI metadata", cfg.RuntimeUKIMetadata},
		{"runtime UKI checksum", cfg.RuntimeUKIChecksum},
		{"runtime SquashFS", cfg.RuntimeRoot},
		{"runtime metadata", cfg.RuntimeMetadata},
		{"runtime checksum", cfg.RuntimeChecksum},
	} {
		if err := requireFile(input.label, input.path, cfg.RepoRoot); err != nil {
			return err
		}
	}

	includeKatlOS := cfg.KatlOSExplicit || fileExists(cfg.KatlOSImage)
	if includeKatlOS {
		for _, input := range []struct {
			label string
			path  string
		}{
			{"KatlOS install image", cfg.KatlOSImage},
			{"KatlOS install image metadata", cfg.KatlOSMetadata},
			{"KatlOS install image checksum", cfg.KatlOSChecksum},
		} {
			if err := requireFile(input.label, input.path, cfg.RepoRoot); err != nil {
				return err
			}
		}
	}

	created := time.Now().UTC().Format(time.RFC3339)
	for _, artifact := range []struct {
		role   string
		format string
		path   string
	}{
		{"installer-uki", "uki", cfg.InstallerUKI},
		{"installer-kernel", "linux-kernel", cfg.InstallerKernel},
		{"installer-initrd", "initrd", cfg.InstallerInitrd},
	} {
		if err := writeChecksum(artifact.path); err != nil {
			return err
		}
		if err := writeBootMetadata(artifact.role, artifact.format, artifact.path, created, cfg); err != nil {
			return err
		}
	}

	entries := []artifactEntry{}
	for _, artifact := range []struct {
		kind     string
		format   string
		path     string
		metadata string
		checksum string
	}{
		{"installer-uki", "uki", cfg.InstallerUKI, metadataPath(cfg.InstallerUKI), checksumPath(cfg.InstallerUKI)},
		{"installer-kernel", "linux-kernel", cfg.InstallerKernel, metadataPath(cfg.InstallerKernel), checksumPath(cfg.InstallerKernel)},
		{"installer-initrd", "initrd", cfg.InstallerInitrd, metadataPath(cfg.InstallerInitrd), checksumPath(cfg.InstallerInitrd)},
		{"runtime-uki", "uki", cfg.RuntimeUKI, cfg.RuntimeUKIMetadata, cfg.RuntimeUKIChecksum},
		{"runtime-root", "squashfs", cfg.RuntimeRoot, cfg.RuntimeMetadata, cfg.RuntimeChecksum},
	} {
		entry, err := newEntry(artifact.kind, artifact.format, artifact.path, artifact.metadata, artifact.checksum, cfg.RepoRoot)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}
	if includeKatlOS {
		entry, err := newEntry("katlos-install-image", "squashfs", cfg.KatlOSImage, cfg.KatlOSMetadata, cfg.KatlOSChecksum, cfg.RepoRoot)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
	}

	index := artifactIndex{
		SchemaVersion: 1,
		GeneratedAt:   created,
		Generation:    cfg.Generation,
		Artifacts:     entries,
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal artifact index: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
		return fmt.Errorf("create artifact index directory: %w", err)
	}
	if err := os.WriteFile(indexPath, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write artifact index %s: %w", relPath(cfg.RepoRoot, indexPath), err)
	}
	return nil
}

func writeBootMetadata(role, format, artifactPath, created string, cfg config) error {
	size, digest, err := fileInfo(artifactPath)
	if err != nil {
		return err
	}
	rel, err := artifactRel(cfg.RepoRoot, artifactPath)
	if err != nil {
		return err
	}
	metadata := bootMetadata{
		APIVersion:               "katl.dev/v1alpha1",
		Kind:                     "InstallerBootArtifact",
		ArtifactRole:             role,
		Format:                   format,
		Version:                  cfg.Version,
		BuildID:                  cfg.Generation,
		Architecture:             cfg.Architecture,
		Path:                     rel,
		SizeBytes:                size,
		SHA256:                   digest,
		CreatedAt:                created,
		InstallerInterface:       cfg.InstallerInterface,
		DefaultKernelCommandLine: []string{"console=ttyS0,115200n8", "systemd.log_target=console", "loglevel=6"},
		SupportedInputModes:      []string{"pxe-preseed", "local-handoff", "offline-media"},
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal installer boot metadata: %w", err)
	}
	path := metadataPath(artifactPath)
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write installer boot metadata %s: %w", relPath(cfg.RepoRoot, path), err)
	}
	return nil
}

func writeJSON(path string, value any, repoRoot string) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal %s: %w", filepath.Base(path), err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", relPath(repoRoot, path), err)
	}
	return nil
}

func resolveRuntimeSHA(repoRoot, explicit, metadataPath string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return explicit, nil
	}
	path := absPath(repoRoot, metadataPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("read runtime metadata %s: %w", relPath(repoRoot, path), err)
	}
	var metadata struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		return "", fmt.Errorf("decode runtime metadata %s: %w", relPath(repoRoot, path), err)
	}
	if strings.TrimSpace(metadata.SHA256) == "" {
		return "", fmt.Errorf("runtime metadata missing sha256: %s", relPath(repoRoot, path))
	}
	return metadata.SHA256, nil
}

func newEntry(kind, format, path, metadata, checksum, repoRoot string) (artifactEntry, error) {
	size, digest, err := fileInfo(path)
	if err != nil {
		return artifactEntry{}, err
	}
	rel, err := artifactRel(repoRoot, path)
	if err != nil {
		return artifactEntry{}, err
	}
	entry := artifactEntry{
		Kind:      kind,
		Path:      rel,
		Format:    format,
		SizeBytes: size,
		SHA256:    digest,
	}
	if metadata != "" {
		entry.MetadataPath = relPath(repoRoot, metadata)
	}
	if checksum != "" {
		entry.ChecksumPath = relPath(repoRoot, checksum)
	}
	return entry, nil
}

func pathForKind(indexPath, repoRoot, kind string) (string, error) {
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("artifact index not found: %s", relPath(repoRoot, indexPath))
		}
		return "", fmt.Errorf("read artifact index %s: %w", relPath(repoRoot, indexPath), err)
	}
	var index artifactIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return "", fmt.Errorf("decode artifact index %s: %w", relPath(repoRoot, indexPath), err)
	}
	matches := []artifactEntry{}
	for _, artifact := range index.Artifacts {
		if artifact.Kind == kind {
			matches = append(matches, artifact)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("artifact kind not found in %s: %s", relPath(repoRoot, indexPath), kind)
	case 1:
		return absPath(repoRoot, matches[0].Path), nil
	default:
		return "", fmt.Errorf("artifact kind appears more than once in %s: %s", relPath(repoRoot, indexPath), kind)
	}
}

func writeChecksum(path string) error {
	_, digest, err := fileInfo(path)
	if err != nil {
		return err
	}
	content := fmt.Sprintf("%s  %s\n", digest, filepath.Base(path))
	if err := os.WriteFile(checksumPath(path), []byte(content), 0o644); err != nil {
		return fmt.Errorf("write checksum %s: %w", checksumPath(path), err)
	}
	return nil
}

func fileInfo(path string) (int64, string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return 0, "", fmt.Errorf("stat artifact %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return 0, "", fmt.Errorf("artifact is not a regular file: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, "", fmt.Errorf("read artifact %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	return info.Size(), hex.EncodeToString(sum[:]), nil
}

func requireFile(label, path, repoRoot string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s not found: %s", label, relPath(repoRoot, path))
		}
		return fmt.Errorf("stat %s %s: %w", label, relPath(repoRoot, path), err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: %s", label, relPath(repoRoot, path))
	}
	return nil
}

func checksumPath(path string) string {
	return path + ".sha256"
}

func metadataPath(path string) string {
	return path + ".json"
}

func repoRoot() (string, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	output, err := cmd.Output()
	if err == nil {
		return filepath.Clean(strings.TrimSpace(string(output))), nil
	}
	wd, wdErr := os.Getwd()
	if wdErr != nil {
		return "", fmt.Errorf("find repository root: %w", err)
	}
	return wd, nil
}

func gitDescribe(repo string) string {
	cmd := exec.Command("git", "-C", repo, "describe", "--always", "--dirty", "--abbrev=12")
	output, err := cmd.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func hostArchitecture() string {
	cmd := exec.Command("uname", "-m")
	output, err := cmd.Output()
	if err == nil {
		arch := strings.TrimSpace(string(output))
		if arch != "" {
			return arch
		}
	}
	return runtime.GOARCH
}

func envMap(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, item := range environ {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			env[name] = value
		}
	}
	return env
}

func envDefault(env map[string]string, key, fallback string) string {
	if value, ok := env[key]; ok && value != "" {
		return value
	}
	return fallback
}

func envDefaultFunc(env map[string]string, key string, fallback func() string) string {
	if value, ok := env[key]; ok && value != "" {
		return value
	}
	return fallback()
}

func envPath(env map[string]string, repoRoot, key, fallback string) string {
	path, _ := envPathExplicit(env, repoRoot, key, fallback)
	return path
}

func envPathExplicit(env map[string]string, repoRoot, key, fallback string) (string, bool) {
	value, ok := env[key]
	if !ok || value == "" {
		return fallback, false
	}
	return absPath(repoRoot, value), true
}

func absPath(repoRoot, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}
	return filepath.Join(repoRoot, path)
}

func artifactRel(repoRoot, path string) (string, error) {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil {
		return "", fmt.Errorf("relativize artifact path %s: %w", path, err)
	}
	if rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return "", fmt.Errorf("artifact path must be under repository root for local index: %s", path)
	}
	return filepath.ToSlash(rel), nil
}

func relPath(repoRoot, path string) string {
	rel, err := filepath.Rel(repoRoot, path)
	if err != nil || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return path
	}
	return filepath.ToSlash(rel)
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}
