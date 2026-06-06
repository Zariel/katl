package vmtest

import (
	"errors"
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
