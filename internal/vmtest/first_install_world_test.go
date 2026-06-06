package vmtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type firstInstallWorldRun struct {
	Scenario *WorldScenario
	Runner   Runner
	Config   FirstInstallConfig
	Repo     string
}

type firstInstallWorldInput struct {
	Installer       InstallerBootConfig
	RuntimeArtifact string
	RuntimeESP      string
	NodeMetadata    string
	InstallManifest string
	Mode            firstInstallWorldMode
	UseInstalledESP bool
	TargetDiskSize  string
}

type firstInstallWorldMode string

const (
	firstInstallWorldPreseed      firstInstallWorldMode = "preseed"
	firstInstallWorldGuestHandoff firstInstallWorldMode = "guest-handoff"
)

func firstInstallWorldRunFor(t *testing.T, name string, spec NodeSpec, useInstalledESP bool) (firstInstallWorldRun, bool) {
	t.Helper()
	return firstInstallWorldRunForMode(t, name, spec, useInstalledESP, firstInstallWorldPreseed)
}

func firstInstallWorldRunForMode(t *testing.T, name string, spec NodeSpec, useInstalledESP bool, mode firstInstallWorldMode) (firstInstallWorldRun, bool) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(WorldManifestEnv)) == "" {
		return firstInstallWorldRun{}, false
	}
	world := RequireWorld(t)
	run, err := planFirstInstallWorldRun(world, name, repoRoot(t), spec, firstInstallWorldInput{
		Installer:       firstInstallInstallerBootFromEnv(),
		RuntimeArtifact: strings.TrimSpace(os.Getenv("KATL_RUNTIME_ARTIFACT")),
		RuntimeESP:      strings.TrimSpace(first(os.Getenv("KATL_RUNTIME_ESP_ARTIFACTS"), os.Getenv("KATL_INSTALLED_ESP_ARTIFACTS"))),
		NodeMetadata:    strings.TrimSpace(first(os.Getenv("KATL_RUNTIME_NODE_METADATA"), os.Getenv("KATL_INSTALLED_NODE_METADATA"))),
		InstallManifest: strings.TrimSpace(os.Getenv("KATL_INSTALL_MANIFEST")),
		Mode:            mode,
		UseInstalledESP: useInstalledESP,
		TargetDiskSize:  first(os.Getenv("KATL_FIRST_INSTALL_TARGET_DISK_SIZE"), "20G"),
	}, DefaultOptions().KVM)
	if err != nil {
		failWorldSetup(t, run.Scenario, err)
	}
	return run, true
}

func planFirstInstallWorldRun(world World, name, repo string, spec NodeSpec, input firstInstallWorldInput, kvm KVMPolicy) (firstInstallWorldRun, error) {
	scenario, err := world.PlanScenario(name)
	if err != nil {
		return firstInstallWorldRun{}, err
	}
	run := firstInstallWorldRun{Scenario: scenario, Repo: repo}
	node, err := scenario.AddNode(spec)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	factory := scenario.NodeFixtures(node)
	input, err = resolveFirstInstallWorldInput(scenario, repo, spec, input)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	installer, err := factory.InstallerBoot(input.Installer)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	runtime, err := factory.RuntimeArtifact(input.RuntimeArtifact)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	installManifest, err := factory.InstallManifest(input.InstallManifest)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	runtimeESP := ""
	if !input.UseInstalledESP {
		esp, err := factory.ESPArtifacts(input.RuntimeESP)
		if err != nil {
			_ = scenario.WriteSetupFailure(err)
			return run, err
		}
		runtimeESP = esp.Path
	}
	nodeMetadata := ""
	if strings.TrimSpace(input.NodeMetadata) != "" {
		metadata, err := factory.NodeMetadata(input.NodeMetadata)
		if err != nil {
			_ = scenario.WriteSetupFailure(err)
			return run, err
		}
		nodeMetadata = metadata.Path
	}
	target, err := factory.FirstInstallTargetDisk("root", DiskQCOW2, input.TargetDiskSize)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	installer.RuntimeArtifact = runtime.Path
	mode := input.Mode
	if mode == "" {
		mode = firstInstallWorldPreseed
	}
	run.Runner = NewRunner(Options{
		Enabled:   true,
		StateRoot: filepath.Join(scenario.Dir, "vm-runs"),
		Keep:      KeepAlways,
		KVM:       firstKVM(kvm, KVMAuto),
		Missing:   MissingFails,
	})
	run.Config = FirstInstallConfig{
		Installer: installer,
		Runtime: InstalledRuntimeConfig{
			ESPArtifacts: runtimeESP,
			NodeMetadata: nodeMetadata,
		},
		UseInstalledESP: input.UseInstalledESP,
		ManifestPath:    installManifest.Path,
		TargetDisk:      target,
	}
	switch mode {
	case firstInstallWorldPreseed:
		run.Config.PreseedManifest = true
	case firstInstallWorldGuestHandoff:
		run.Config.GuestHandoff = true
	default:
		err := errors.New("unsupported first-install world mode: " + string(mode))
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	return run, nil
}

type mkosiArtifactIndex struct {
	Artifacts []mkosiArtifact `json:"artifacts"`
}

type mkosiArtifact struct {
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	MetadataPath string `json:"metadataPath"`
	SHA256       string `json:"sha256"`
	SizeBytes    uint64 `json:"sizeBytes"`
}

type katlosImageMetadata struct {
	Version          string `json:"version"`
	Architecture     string `json:"architecture"`
	RuntimeInterface string `json:"runtimeInterface"`
	Role             string `json:"role"`
	ImageRole        string `json:"imageRole"`
	SHA256           string `json:"sha256"`
	SizeBytes        uint64 `json:"sizeBytes"`
}

func resolveFirstInstallWorldInput(scenario *WorldScenario, repo string, spec NodeSpec, input firstInstallWorldInput) (firstInstallWorldInput, error) {
	indexPath := defaultMkosiArtifactIndexPath(repo)
	index, err := readMkosiArtifactIndex(indexPath, repo)
	if err != nil && (strings.TrimSpace(os.Getenv("KATL_MKOSI_ARTIFACT_INDEX")) != "" || !errors.Is(err, os.ErrNotExist)) {
		return input, err
	}
	if input.Installer.InstallerUKI == "" && input.Installer.InstallerKernel == "" && input.Installer.InstallerInitrd == "" {
		if artifact, ok := index.artifact("installer-uki"); ok {
			input.Installer.InstallerUKI = artifact.Path
		}
	}
	if input.RuntimeArtifact == "" {
		if artifact, ok := index.artifact("runtime-root"); ok {
			input.RuntimeArtifact = artifact.Path
		}
	}
	if !input.UseInstalledESP && strings.TrimSpace(input.RuntimeESP) == "" {
		input.UseInstalledESP = true
	}
	if input.InstallManifest == "" {
		manifestPath, err := writeFirstInstallWorldManifestSource(scenario, repo, spec, index)
		if err != nil {
			return input, err
		}
		input.InstallManifest = manifestPath
	}
	return input, nil
}

func defaultMkosiArtifactIndexPath(repo string) string {
	if path := strings.TrimSpace(os.Getenv("KATL_MKOSI_ARTIFACT_INDEX")); path != "" {
		return path
	}
	return filepath.Join(repo, "build", "mkosi", "artifacts.json")
}

func readMkosiArtifactIndex(path, repo string) (mkosiArtifactIndex, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mkosiArtifactIndex{}, err
	}
	var index mkosiArtifactIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return mkosiArtifactIndex{}, err
	}
	for i := range index.Artifacts {
		index.Artifacts[i].Path = repoAbs(repo, index.Artifacts[i].Path)
		index.Artifacts[i].MetadataPath = repoAbs(repo, index.Artifacts[i].MetadataPath)
	}
	return index, nil
}

func (index mkosiArtifactIndex) artifact(kind string) (mkosiArtifact, bool) {
	for _, artifact := range index.Artifacts {
		if artifact.Kind == kind {
			return artifact, true
		}
	}
	return mkosiArtifact{}, false
}

func writeFirstInstallWorldManifestSource(scenario *WorldScenario, repo string, spec NodeSpec, index mkosiArtifactIndex) (string, error) {
	image, ok := index.artifact("katlos-install-image")
	if !ok {
		var err error
		image, err = discoverKatlOSInstallImage(repo)
		if err != nil {
			return "", err
		}
	}
	metadata, err := readKatlOSImageMetadata(image)
	if err != nil {
		return "", err
	}
	if metadata.Role == "" {
		metadata.Role = metadata.ImageRole
	}
	if metadata.Role == "" {
		metadata.Role = "install"
	}
	if metadata.SHA256 == "" {
		metadata.SHA256 = image.SHA256
	}
	if metadata.SizeBytes == 0 {
		metadata.SizeBytes = image.SizeBytes
	}
	sourceDir := filepath.Join(scenario.Dir, "inputs", "install-manifest-source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		return "", err
	}
	localRef := filepath.Base(image.Path)
	localImage := filepath.Join(sourceDir, localRef)
	if _, err := os.Lstat(localImage); errors.Is(err, os.ErrNotExist) {
		if err := os.Symlink(image.Path, localImage); err != nil {
			return "", err
		}
	} else if err != nil {
		return "", err
	}
	manifest := map[string]any{
		"apiVersion": "install.katl.dev/v1alpha1",
		"kind":       "InstallManifest",
		"node": map[string]any{
			"identity": map[string]any{
				"hostname": spec.Name,
				"ssh": map[string]any{
					"authorizedKeys": []string{"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIKatlExampleRuntimeKeyReplaceMe katl@example"},
				},
			},
			"systemRole": string(spec.Role),
		},
		"install": map[string]any{
			"allowDestructiveInstall": true,
			"targetDisk": map[string]any{
				"byID":       "/dev/disk/by-id/virtio-katl-root",
				"minSizeMiB": 32,
			},
		},
		"katlosImage": map[string]any{
			"localRef":         localRef,
			"sha256":           metadata.SHA256,
			"sizeBytes":        metadata.SizeBytes,
			"version":          metadata.Version,
			"architecture":     metadata.Architecture,
			"runtimeInterface": metadata.RuntimeInterface,
			"role":             metadata.Role,
		},
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", err
	}
	manifestPath := filepath.Join(sourceDir, "install-manifest.json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return manifestPath, nil
}

func discoverKatlOSInstallImage(repo string) (mkosiArtifact, error) {
	matches, err := filepath.Glob(filepath.Join(repo, "build", "mkosi", "katlos-install-*.squashfs"))
	if err != nil {
		return mkosiArtifact{}, err
	}
	if len(matches) != 1 {
		return mkosiArtifact{}, fmt.Errorf("install manifest source is required: expected one local KatlOS install image, found %d", len(matches))
	}
	info, err := os.Stat(matches[0])
	if err != nil {
		return mkosiArtifact{}, err
	}
	return mkosiArtifact{Kind: "katlos-install-image", Path: matches[0], MetadataPath: matches[0] + ".json", SizeBytes: uint64(info.Size())}, nil
}

func readKatlOSImageMetadata(image mkosiArtifact) (katlosImageMetadata, error) {
	if strings.TrimSpace(image.MetadataPath) == "" {
		return katlosImageMetadata{}, errors.New("KatlOS install image metadata is required")
	}
	data, err := os.ReadFile(image.MetadataPath)
	if err != nil {
		return katlosImageMetadata{}, err
	}
	var metadata katlosImageMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return katlosImageMetadata{}, err
	}
	return metadata, nil
}

func repoAbs(repo, path string) string {
	if strings.TrimSpace(path) == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(repo, path)
}

func firstInstallInstallerBootFromEnv() InstallerBootConfig {
	kernel := strings.TrimSpace(os.Getenv("KATL_INSTALLER_KERNEL"))
	initrd := strings.TrimSpace(os.Getenv("KATL_INSTALLER_INITRD"))
	if kernel != "" || initrd != "" {
		return InstallerBootConfig{
			InstallerKernel: kernel,
			InstallerInitrd: initrd,
			CommandLine: []string{
				"console=ttyS0,115200n8",
				"systemd.log_target=console",
				"loglevel=6",
			},
		}
	}
	return InstallerBootConfig{InstallerUKI: strings.TrimSpace(os.Getenv("KATL_INSTALLER_UKI"))}
}

func TestPlanFirstInstallWorldRunStagesInputs(t *testing.T) {
	world := testWorld(t)
	repo := t.TempDir()
	sourceDir := t.TempDir()
	installer := writeFixtureFile(t, filepath.Join(sourceDir, "katl-installer.efi"), "installer")
	runtime := writeFixtureFile(t, filepath.Join(sourceDir, "katl-runtime-root.squashfs"), "runtime")
	esp := writeFixtureESP(t, filepath.Join(sourceDir, "esp"))
	manifest := writeFixtureFile(t, filepath.Join(sourceDir, "install-manifest.json"), firstManifest())
	metadata := writeFixtureNodeMetadata(t, filepath.Join(sourceDir, "node.json"), Node{Name: "cp-1", Role: ControlPlane})

	run, err := planFirstInstallWorldRun(world, "first install world", repo, NodeSpec{Name: "cp-1", Role: ControlPlane}, firstInstallWorldInput{
		Installer:       InstallerBootConfig{InstallerUKI: installer},
		RuntimeArtifact: runtime,
		RuntimeESP:      esp,
		NodeMetadata:    metadata,
		InstallManifest: manifest,
		TargetDiskSize:  "20G",
	}, KVMOff)
	if err != nil {
		t.Fatalf("planFirstInstallWorldRun() error = %v", err)
	}
	if run.Repo != repo || run.Runner.options().Missing != MissingFails || run.Runner.options().Keep != KeepAlways {
		t.Fatalf("run metadata = %#v", run)
	}
	if run.Config.Installer.InstallerUKI == installer || run.Config.Installer.RuntimeArtifact == runtime {
		t.Fatalf("installer inputs were not staged: %#v", run.Config.Installer)
	}
	if run.Config.Runtime.ESPArtifacts == esp || run.Config.Runtime.NodeMetadata == metadata {
		t.Fatalf("runtime inputs were not staged: %#v", run.Config.Runtime)
	}
	if run.Config.ManifestPath == manifest || !run.Config.PreseedManifest {
		t.Fatalf("install config = %#v", run.Config)
	}
	if run.Config.TargetDisk.Kind != DiskTarget || run.Config.TargetDisk.Size != "20G" {
		t.Fatalf("target disk = %#v", run.Config.TargetDisk)
	}
	scenarioManifest := readScenarioManifest(t, run.Scenario.ManifestPath)
	for _, kind := range []string{FixtureInstallerUKI, FixtureRuntimeArtifact, FixtureESPArtifacts, FixtureNodeMetadata, FixtureInstallManifest, FixtureFirstInstallDisk} {
		if !hasFixtureKind(scenarioManifest.Fixtures, kind) {
			t.Fatalf("scenario fixtures missing %s: %#v", kind, scenarioManifest.Fixtures)
		}
	}
}

func TestPlanFirstInstallWorldRunGuestHandoffMode(t *testing.T) {
	world := testWorld(t)
	sourceDir := t.TempDir()
	installer := writeFixtureFile(t, filepath.Join(sourceDir, "katl-installer.efi"), "installer")
	runtime := writeFixtureFile(t, filepath.Join(sourceDir, "katl-runtime-root.squashfs"), "runtime")
	esp := writeFixtureESP(t, filepath.Join(sourceDir, "esp"))
	manifest := writeFixtureFile(t, filepath.Join(sourceDir, "install-manifest.json"), firstManifest())

	run, err := planFirstInstallWorldRun(world, "guest handoff world", t.TempDir(), NodeSpec{Name: "cp-1", Role: ControlPlane}, firstInstallWorldInput{
		Installer:       InstallerBootConfig{InstallerUKI: installer},
		RuntimeArtifact: runtime,
		RuntimeESP:      esp,
		InstallManifest: manifest,
		Mode:            firstInstallWorldGuestHandoff,
		TargetDiskSize:  "20G",
	}, KVMOff)
	if err != nil {
		t.Fatalf("planFirstInstallWorldRun() error = %v", err)
	}
	if !run.Config.GuestHandoff || run.Config.PreseedManifest {
		t.Fatalf("handoff mode config = %#v", run.Config)
	}
}

func TestPlanFirstInstallWorldRunResolvesLocalMkosiArtifacts(t *testing.T) {
	world := testWorld(t)
	repo := t.TempDir()
	mkosiDir := filepath.Join(repo, "build", "mkosi")
	installer := writeFixtureFile(t, filepath.Join(mkosiDir, "katl-installer.efi"), "installer")
	runtime := writeFixtureFile(t, filepath.Join(mkosiDir, "katl-runtime-root.squashfs"), "runtime")
	image := writeFixtureFile(t, filepath.Join(mkosiDir, "katlos-install-0.0.0-dev-x86_64.squashfs"), "katlos-image")
	writeFixtureFile(t, image+".json", `{
  "apiVersion": "katl.dev/v1alpha1",
  "kind": "KatlOSImageArtifact",
  "imageRole": "install",
  "version": "0.0.0-dev",
  "architecture": "x86_64",
  "runtimeInterface": "katl-runtime-1",
  "sizeBytes": 11,
  "sha256": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}`)
	writeFixtureFile(t, filepath.Join(mkosiDir, "artifacts.json"), `{
  "artifacts": [
    {"kind":"installer-uki","path":"build/mkosi/katl-installer.efi"},
    {"kind":"runtime-root","path":"build/mkosi/katl-runtime-root.squashfs"}
  ]
}`)

	run, err := planFirstInstallWorldRun(world, "local mkosi first install", repo, NodeSpec{Name: "cp-1", Role: ControlPlane}, firstInstallWorldInput{
		TargetDiskSize: "20G",
	}, KVMOff)
	if err != nil {
		t.Fatalf("planFirstInstallWorldRun() error = %v", err)
	}
	if run.Config.Installer.InstallerUKI == installer || run.Config.Installer.InstallerUKI == "" {
		t.Fatalf("installer UKI was not staged: %#v", run.Config.Installer)
	}
	if run.Config.Installer.RuntimeArtifact == runtime || run.Config.Installer.RuntimeArtifact == "" {
		t.Fatalf("runtime artifact was not staged: %#v", run.Config.Installer)
	}
	if !run.Config.UseInstalledESP || run.Config.Runtime.ESPArtifacts != "" {
		t.Fatalf("installed ESP fallback was not selected: %#v", run.Config)
	}
	if run.Config.ManifestPath == "" || strings.Contains(run.Config.ManifestPath, "install-manifest-source") {
		t.Fatalf("install manifest was not staged: %q", run.Config.ManifestPath)
	}
	stagedImage := filepath.Join(filepath.Dir(run.Config.ManifestPath), filepath.Base(image))
	if data, err := os.ReadFile(stagedImage); err != nil || string(data) != "katlos-image" {
		t.Fatalf("staged KatlOS image = %q, err = %v", data, err)
	}
	manifestData, err := os.ReadFile(run.Config.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", run.Config.ManifestPath, err)
	}
	if !strings.Contains(string(manifestData), `"hostname": "cp-1"`) || !strings.Contains(string(manifestData), `"localRef": "katlos-install-0.0.0-dev-x86_64.squashfs"`) {
		t.Fatalf("generated manifest = %s", manifestData)
	}
	scenarioManifest := readScenarioManifest(t, run.Scenario.ManifestPath)
	for _, kind := range []string{FixtureInstallerUKI, FixtureRuntimeArtifact, FixtureInstallManifest, FixtureKatlOSInstallImage, FixtureFirstInstallDisk} {
		if !hasFixtureKind(scenarioManifest.Fixtures, kind) {
			t.Fatalf("scenario fixtures missing %s: %#v", kind, scenarioManifest.Fixtures)
		}
	}
}

func TestPlanFirstInstallWorldRunWritesSetupFailureForMissingSource(t *testing.T) {
	world := testWorld(t)
	sourceDir := t.TempDir()
	installer := writeFixtureFile(t, filepath.Join(sourceDir, "katl-installer.efi"), "installer")

	run, err := planFirstInstallWorldRun(world, "missing first install input", t.TempDir(), NodeSpec{Name: "cp-1", Role: ControlPlane}, firstInstallWorldInput{
		Installer:       InstallerBootConfig{InstallerUKI: installer},
		RuntimeArtifact: "",
		InstallManifest: writeFixtureFile(t, filepath.Join(sourceDir, "install-manifest.json"), firstManifest()),
		UseInstalledESP: true,
		TargetDiskSize:  "20G",
	}, KVMAuto)
	if err == nil || !strings.Contains(err.Error(), "runtime-artifact source is required") {
		t.Fatalf("planFirstInstallWorldRun() error = %v, want runtime source failure", err)
	}
	if run.Scenario == nil {
		t.Fatal("planFirstInstallWorldRun() did not return scenario on setup failure")
	}
	var result scenarioResult
	readJSONForTest(t, run.Scenario.ResultPath, &result)
	if result.Status != WorldStatusSetupFailed || !strings.Contains(result.FailureSummary, "runtime-artifact source is required") {
		t.Fatalf("result = %#v", result)
	}
}
