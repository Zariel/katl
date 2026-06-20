package scenarios

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zariel/katl/internal/vmtest"
)

func TestInstalledRuntimeTwoNodeWipeReinstallBootstrapSmoke(t *testing.T) {
	if run, ok := wipeReinstallWorldSmokeRun(t); ok {
		runWipeReinstallBootstrapSmoke(t, run)
		return
	}

	options := vmtest.DefaultOptions()
	if !options.Enabled {
		t.Skip("set -katl.vmtest.run or KATL_VMTEST_RUN=1 to run two-node wipe/reinstall bootstrap smoke")
	}
	_ = vmtest.RequireWorld(t)
}

func wipeReinstallWorldSmokeRun(t *testing.T) (operationBackedSmokeRun, bool) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(vmtest.WorldManifestEnv)) == "" {
		return operationBackedSmokeRun{}, false
	}
	world := vmtest.RequireWorld(t)
	world = operationBackedFreshFixtureWorld(world)
	repo := katlRepoRoot(t)
	kvm := vmtest.DefaultOptions().KVM
	specs := twoNodeWorldRuntimeSpecs()
	if err := ensurePublishedRuntimeFixturesForWorld(world, repo, specs, kvm); err != nil {
		failWorldFixtureSetup(t, world, "installed-runtime-two-node-wipe-reinstall-bootstrap", err)
	}
	run, err := planOperationBackedWorldSmokeRunNamed(world, repo, operationBackedKubernetesVersion(t, repo), kvm, "installed-runtime-two-node-wipe-reinstall-bootstrap")
	if err != nil {
		failTwoNodeWorldSetup(t, run.WorldScenario, err)
	}
	missing := twoNodeHostToolPrereqs(exec.LookPath)
	requireSmokePrereqs(t, run.Runner, run.Scenario, run.Result, "two-node wipe/reinstall bootstrap smoke prerequisites missing", missing)
	return run, true
}

type wipeReinstallArtifactManifest struct {
	VMTestRun               string                                      `json:"vmtestRun,omitempty"`
	WorldManifest           string                                      `json:"worldManifest,omitempty"`
	HostCapabilities        string                                      `json:"hostCapabilities,omitempty"`
	ResourceManifest        string                                      `json:"resourceManifest,omitempty"`
	ResourceManifestSHA256  string                                      `json:"resourceManifestSHA256,omitempty"`
	PackageLock             string                                      `json:"packageLock,omitempty"`
	PackageLockSHA256       string                                      `json:"packageLockSHA256,omitempty"`
	MkosiArtifactIndex      string                                      `json:"mkosiArtifactIndex,omitempty"`
	KubernetesPayloadBundle *threeControlPlaneKubernetesPayloadBundle   `json:"kubernetesPayloadBundle,omitempty"`
	InitialBootstrap        wipeReinstallBootstrapEvidence              `json:"initialBootstrap"`
	ReinstallResults        map[string]string                           `json:"reinstallResults,omitempty"`
	ReinstallDisks          map[string]string                           `json:"reinstallDisks,omitempty"`
	ReinstallESPs           map[string]string                           `json:"reinstallESPs,omitempty"`
	CleanGeneration0        map[string]threeNodeGeneration0NodeEvidence `json:"cleanGeneration0,omitempty"`
	PostReinstallBootstrap  wipeReinstallBootstrapEvidence              `json:"postReinstallBootstrap"`
	NetworkLeases           string                                      `json:"networkLeases,omitempty"`
}

type wipeReinstallBootstrapEvidence struct {
	Inventory               string                                    `json:"inventory,omitempty"`
	Kubeconfig              string                                    `json:"kubeconfig,omitempty"`
	KubeconfigMetadata      string                                    `json:"kubeconfigMetadata,omitempty"`
	BootstrapStdout         string                                    `json:"bootstrapStdout,omitempty"`
	BootstrapStderr         string                                    `json:"bootstrapStderr,omitempty"`
	KubectlOutput           string                                    `json:"kubectlOutput,omitempty"`
	KubectlDiagnostics      map[string]string                         `json:"kubectlDiagnostics,omitempty"`
	BootstrapFixture        *bootstrapFixtureInputs                   `json:"bootstrapFixture,omitempty"`
	KubernetesPayloadBundle *threeControlPlaneKubernetesPayloadBundle `json:"kubernetesPayloadBundle,omitempty"`
	CNIFixtures             map[string]nodeCNIFixture                 `json:"cniFixtures,omitempty"`
	ImageFixtures           map[string][]nodeImageFixture             `json:"imageFixtures,omitempty"`
	BootSelectionsBefore    map[string]string                         `json:"bootSelectionsBefore,omitempty"`
	NodeStatus              map[string]string                         `json:"nodeStatus,omitempty"`
	NodeScenarios           map[string]string                         `json:"nodeScenarios,omitempty"`
	NodeResults             map[string]string                         `json:"nodeResults,omitempty"`
	LaunchCommands          map[string]string                         `json:"launchCommands,omitempty"`
	DomainXMLs              map[string]string                         `json:"domainXMLs,omitempty"`
	InstalledRuntimeInputs  map[string]string                         `json:"installedRuntimeInputs,omitempty"`
	VSockTranscripts        map[string]string                         `json:"vsockTranscripts,omitempty"`
	LibvirtLeases           map[string]string                         `json:"libvirtLeases,omitempty"`
	NodeDomains             map[string]string                         `json:"nodeDomains,omitempty"`
	NodeMACs                map[string]string                         `json:"nodeMACs,omitempty"`
	NodeIPs                 map[string]string                         `json:"nodeIPs,omitempty"`
	SerialLogs              map[string]string                         `json:"serialLogs,omitempty"`
	Diagnostics             map[string]string                         `json:"diagnostics,omitempty"`
}

func runWipeReinstallBootstrapSmoke(t *testing.T, run operationBackedSmokeRun) {
	t.Helper()
	runner := run.Runner
	scenario := run.Scenario
	result := run.Result
	inputs := run.Inputs
	requireVMHost(t, runner, scenario, result, vmtest.HostRequirements{
		Libvirt: true,
		OVMF:    true,
		KVM:     run.Options.KVM,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Minute)
	defer cancel()
	artifactManifest := filepath.Join(result.ManifestDir, "wipe-reinstall-bootstrap-artifacts.json")
	kubernetesBundle, bundleServer, err := stageOperationBackedKubernetesPayloadBundle(katlRepoRoot(t), result, run.WorldScenario.World.Network.Gateway, inputs.KubernetesVersion)
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatalf("stage Kubernetes payload bundle: %v", err)
	}
	defer bundleServer.Close()
	liveNodes := []vmtest.RunningInstalledRuntimeNode{}
	defer func() {
		for _, node := range liveNodes {
			stopNode(t, node)
		}
	}()

	initialNodes, err := startOperationBackedNodes(ctx, run, result, inputs.ControlPlaneDisk, inputs.ControlPlaneDiskFormat, inputs.ControlPlaneESP, inputs.ControlPlaneFixture, inputs.ControlPlaneMetadata, inputs.ControlPlaneMAC, inputs.WorkerDisk, inputs.WorkerDiskFormat, inputs.WorkerESP, inputs.WorkerFixture, inputs.WorkerMetadata, inputs.WorkerMAC)
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	liveNodes = initialNodes
	initialEvidence, err := runWipeReinstallBootstrapRound(t, ctx, run, result, "initial", kubernetesBundle, initialNodes)
	if err != nil {
		collectTwoNodeDiagnostics("", initialNodes...)
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	initialOverlays, err := bootOverlaysByNode(initialNodes)
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	for _, node := range liveNodes {
		stopNode(t, node)
	}
	liveNodes = nil

	reinstallResults, reinstallDisks, reinstallESPs, err := runTwoNodeReinstallProofs(ctx, run, result, initialOverlays)
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	postRunner := vmtest.NewRunner(vmtest.Options{
		Enabled:   true,
		StateRoot: filepath.Join(result.RunDir, "post-reinstall-vm-runs"),
		Keep:      vmtest.KeepFailed,
		KVM:       run.Options.KVM,
		Missing:   vmtest.MissingFails,
	})
	postResult, err := postRunner.Plan(vmtest.Scenario{Name: "post-reinstall-bootstrap"})
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	postResult.Started = time.Now().UTC()
	reinstalledNodes, err := startOperationBackedNodes(ctx, run, postResult, reinstallDisks["cp-1"], string(vmtest.DiskQCOW2), reinstallESPs["cp-1"], "", inputs.ControlPlaneMetadata, inputs.ControlPlaneMAC, reinstallDisks["worker-1"], string(vmtest.DiskQCOW2), reinstallESPs["worker-1"], "", inputs.WorkerMetadata, inputs.WorkerMAC)
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	liveNodes = reinstalledNodes
	cleanEvidence, err := collectWipeReinstallGeneration0Evidence(ctx, postResult, reinstalledNodes)
	if err != nil {
		collectTwoNodeDiagnostics("", reinstalledNodes...)
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	postEvidence, err := runWipeReinstallBootstrapRound(t, ctx, run, postResult, "post-reinstall", kubernetesBundle, reinstalledNodes)
	if err != nil {
		collectTwoNodeDiagnostics("", reinstalledNodes...)
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	if err := writeWipeReinstallArtifactManifest(artifactManifest, inputs, initialEvidence, reinstallResults, reinstallDisks, reinstallESPs, cleanEvidence, postEvidence); err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusPassed, "")
}

func startOperationBackedNodes(ctx context.Context, run operationBackedSmokeRun, result vmtest.Result, cpDisk, cpFormat, cpESP, cpFixture, cpMetadata, cpMAC, workerDisk, workerFormat, workerESP, workerFixture, workerMetadata, workerMAC string) ([]vmtest.RunningInstalledRuntimeNode, error) {
	cpNode, err := vmtest.StartInstalledRuntimeNode(ctx, result, vmtest.InstalledRuntimeNodeConfig{
		Name: "cp-1",
		Runtime: vmtest.InstalledRuntimeConfig{
			Disk:            cpDisk,
			DiskFormat:      vmtest.DiskFormat(cpFormat),
			ESPArtifacts:    cpESP,
			FixtureManifest: cpFixture,
			NodeMetadata:    cpMetadata,
			VM:              operationBackedVMConfigForRun(run, cpMAC, 0),
		},
	}, vmtest.VMRunner{})
	if err != nil {
		return nil, fmt.Errorf("start control-plane VM: %w", err)
	}
	workerNode, err := vmtest.StartInstalledRuntimeNode(ctx, result, vmtest.InstalledRuntimeNodeConfig{
		Name: "worker-1",
		Runtime: vmtest.InstalledRuntimeConfig{
			Disk:            workerDisk,
			DiskFormat:      vmtest.DiskFormat(workerFormat),
			ESPArtifacts:    workerESP,
			FixtureManifest: workerFixture,
			NodeMetadata:    workerMetadata,
			VM:              operationBackedVMConfigForRun(run, workerMAC, 0),
		},
	}, vmtest.VMRunner{})
	if err != nil {
		_ = cpNode.StopFailure("worker VM failed to start")
		return nil, fmt.Errorf("start worker VM: %w", err)
	}
	return []vmtest.RunningInstalledRuntimeNode{cpNode, workerNode}, nil
}

func runWipeReinstallBootstrapRound(t *testing.T, ctx context.Context, run operationBackedSmokeRun, result vmtest.Result, name string, kubernetesBundle threeControlPlaneKubernetesPayloadBundle, nodes []vmtest.RunningInstalledRuntimeNode) (wipeReinstallBootstrapEvidence, error) {
	t.Helper()
	roundDir := filepath.Join(result.RunDir, name)
	manifestDir := filepath.Join(result.ManifestDir, name)
	evidenceDir := filepath.Join(roundDir, "evidence")
	if err := os.MkdirAll(evidenceDir, 0o755); err != nil {
		return wipeReinstallBootstrapEvidence{}, err
	}
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		return wipeReinstallBootstrapEvidence{}, err
	}
	inventoryPath := filepath.Join(manifestDir, "bootstrap-inventory.yaml")
	kubeconfigPath := filepath.Join(roundDir, "operator-kubeconfig.yaml")
	kubeconfigMetadataPath := filepath.Join(roundDir, "operator-kubeconfig-metadata.json")
	stdoutPath := filepath.Join(roundDir, "katlctl-bootstrap.stdout")
	stderrPath := filepath.Join(roundDir, "katlctl-bootstrap.stderr")
	kubectlOut := filepath.Join(roundDir, "kubectl-get-nodes.txt")
	bootstrapFixture, err := stageBootstrapFixtureInputs(manifestDir, bootstrapFixtureInputsForRun(katlRepoRoot(t)))
	if err != nil {
		return wipeReinstallBootstrapEvidence{}, err
	}
	cpNode := nodeByName(nodes, "cp-1")
	workerNode := nodeByName(nodes, "worker-1")
	if cpNode.Name == "" || workerNode.Name == "" {
		return wipeReinstallBootstrapEvidence{}, fmt.Errorf("bootstrap round %s requires cp-1 and worker-1 nodes", name)
	}
	cpAddress, err := liveNodeIPv4Address(ctx, cpNode, firstString(cpNode.Result.IPAddress, run.Inputs.ControlPlaneAddress))
	if err != nil {
		return wipeReinstallBootstrapEvidence{}, fmt.Errorf("read control-plane IP address: %w", err)
	}
	workerAddress, err := liveNodeIPv4Address(ctx, workerNode, firstString(workerNode.Result.IPAddress, run.Inputs.WorkerAddress))
	if err != nil {
		return wipeReinstallBootstrapEvidence{}, fmt.Errorf("read worker IP address: %w", err)
	}
	for _, node := range nodes {
		if err := installKubernetesBundleCA(ctx, node, kubernetesBundle); err != nil {
			return wipeReinstallBootstrapEvidence{}, fmt.Errorf("install Kubernetes bundle CA on %s: %w", node.Name, err)
		}
	}
	cniFixtures, err := stageTwoNodeCNIFixtures(ctx, katlRepoRoot(t), cpNode, workerNode, cpAddress, workerAddress)
	if err != nil {
		return wipeReinstallBootstrapEvidence{}, fmt.Errorf("stage test CNI fixtures: %w", err)
	}
	imageFixtures, err := stageTwoNodeImageFixtures(ctx, katlRepoRoot(t), roundDir, nodes...)
	if err != nil {
		return wipeReinstallBootstrapEvidence{}, fmt.Errorf("stage test workload images: %w", err)
	}
	bootSelectionsBefore := map[string]string{}
	for _, node := range nodes {
		nodeEvidenceDir := filepath.Join(evidenceDir, node.Name)
		if err := os.MkdirAll(nodeEvidenceDir, 0o755); err != nil {
			return wipeReinstallBootstrapEvidence{}, err
		}
		beforeSelection, err := readNodeFileWithRetry(ctx, node, "/var/lib/katl/boot/selection.json", 128<<10, 2*time.Minute)
		if err != nil {
			return wipeReinstallBootstrapEvidence{}, fmt.Errorf("read %s boot selection before %s bootstrap: %w", node.Name, name, err)
		}
		beforeSelectionPath := filepath.Join(nodeEvidenceDir, "boot-selection-before.json")
		if err := os.WriteFile(beforeSelectionPath, beforeSelection, 0o600); err != nil {
			return wipeReinstallBootstrapEvidence{}, err
		}
		assertGeneration0Selection(t, beforeSelection)
		bootSelectionsBefore[node.Name] = beforeSelectionPath
	}
	tokenFiles := map[string]string{
		"cp-1":     filepath.Join(roundDir, "cp-1-katlc-agent.token"),
		"worker-1": filepath.Join(roundDir, "worker-1-katlc-agent.token"),
	}
	for _, node := range nodes {
		token, err := readNodeFileWithRetry(ctx, node, "/var/lib/katl/agent/token", 4<<10, 2*time.Minute)
		if err != nil {
			return wipeReinstallBootstrapEvidence{}, fmt.Errorf("read %s katlc agent token: %w", node.Name, err)
		}
		if err := os.WriteFile(tokenFiles[node.Name], token, 0o600); err != nil {
			return wipeReinstallBootstrapEvidence{}, err
		}
	}
	for _, endpoint := range []struct {
		name    string
		address string
	}{
		{name: "cp-1", address: cpAddress},
		{name: "worker-1", address: workerAddress},
	} {
		if err := waitForKatlcAgentTCP(ctx, endpoint.name, endpoint.address, 2*time.Minute); err != nil {
			return wipeReinstallBootstrapEvidence{}, fmt.Errorf("wait for %s katlc agent TCP endpoint: %w", endpoint.name, err)
		}
	}
	if err := writeOperationBackedInventory(inventoryPath, run.Inputs.KubernetesVersion, kubernetesBundle, cpAddress, workerAddress, tokenFiles); err != nil {
		return wipeReinstallBootstrapEvidence{}, err
	}
	var stdout, stderr bytes.Buffer
	err = runKatlctlCommand(t, ctx, katlRepoRoot(t), appendBootstrapFixtureArgs([]string{
		"cluster", "bootstrap",
		"--inventory", inventoryPath,
		"--init-node", "cp-1",
		"--control-plane-endpoint", cpAddress + ":6443",
		"--kubernetes-bundle-source", kubernetesBundle.Source,
		"--kubernetes-bundle-ref", kubernetesBundle.Ref,
		"--node-address", "cp-1=" + cpAddress,
		"--node-address", "worker-1=" + workerAddress,
		"--kubeconfig-out", kubeconfigPath,
		"--overwrite-kubeconfig",
	}, bootstrapFixture), &stdout, &stderr)
	_ = os.WriteFile(stdoutPath, stdout.Bytes(), 0o644)
	_ = os.WriteFile(stderrPath, stderr.Bytes(), 0o644)
	_ = writeKubeconfigMetadata(kubeconfigPath, kubeconfigMetadataPath)
	if err := bootstrapCommandError(err, stdout.String()); err != nil {
		_ = os.WriteFile(filepath.Join(roundDir, "katlctl-bootstrap-error.txt"), []byte(err.Error()+"\n"), 0o644)
		collectOperationBackedFailureEvidence(ctx, cpNode, filepath.Join(evidenceDir, "cp-1"), "bootstrap-init")
		collectOperationBackedFailureEvidence(ctx, workerNode, filepath.Join(evidenceDir, "worker-1"), "bootstrap-join-worker")
		_ = collectNodeLocalStatusFailureEvidence(ctx, evidenceDir, nodes...)
		collectKubectlDiagnosticsForFailure(ctx, cpNode, kubeconfigPath, roundDir)
		return wipeReinstallBootstrapEvidence{}, fmt.Errorf("%s katlctl cluster bootstrap failed: %w\nstdout:\n%s\nstderr:\n%s", name, err, stdout.String(), stderr.String())
	}
	output, err := waitForKubectlNodes(ctx, kubeconfigPath, kubectlOut, 3*time.Minute, "node/cp-1", "node/worker-1")
	if err != nil {
		collectKubectlDiagnostics(kubeconfigPath, roundDir)
		return wipeReinstallBootstrapEvidence{}, fmt.Errorf("%s kubectl nodes did not converge: %w\n%s", name, err, output)
	}
	collectKubectlDiagnostics(kubeconfigPath, roundDir)
	return wipeReinstallBootstrapEvidence{
		Inventory:               inventoryPath,
		Kubeconfig:              kubeconfigPath,
		KubeconfigMetadata:      kubeconfigMetadataPath,
		BootstrapStdout:         stdoutPath,
		BootstrapStderr:         stderrPath,
		KubectlOutput:           kubectlOut,
		KubectlDiagnostics:      kubectlDiagnosticPaths(roundDir),
		BootstrapFixture:        bootstrapFixture.manifestValue(),
		KubernetesPayloadBundle: &kubernetesBundle,
		CNIFixtures:             cniFixtures,
		ImageFixtures:           imageFixtures,
		BootSelectionsBefore:    bootSelectionsBefore,
		NodeScenarios:           nodeScenarioPaths(nodes),
		NodeResults:             nodeResultPaths(nodes),
		LaunchCommands:          launchCommandPaths(nodes),
		DomainXMLs:              domainXMLPaths(nodes),
		InstalledRuntimeInputs:  installedRuntimeInputPaths(nodes),
		VSockTranscripts:        vsockTranscriptPaths(nodes),
		LibvirtLeases:           libvirtLeasePaths(nodes),
		NodeDomains:             nodeDomainNames(nodes),
		NodeMACs:                nodeMACAddresses(nodes),
		NodeIPs:                 nodeIPAddresses(nodes),
		SerialLogs:              serialLogPaths(nodes),
		Diagnostics:             diagnosticSummaryPaths(nodes),
	}, nil
}

func runTwoNodeReinstallProofs(ctx context.Context, run operationBackedSmokeRun, parent vmtest.Result, overlays map[string]string) (map[string]string, map[string]string, map[string]string, error) {
	reinstallRunner := vmtest.NewRunner(vmtest.Options{
		Enabled:   true,
		StateRoot: filepath.Join(parent.RunDir, "wipe-reinstall-vm-runs"),
		Keep:      vmtest.KeepAlways,
		KVM:       run.Options.KVM,
		Missing:   vmtest.MissingFails,
	})
	results := map[string]string{}
	disks := map[string]string{}
	esps := map[string]string{}
	for _, node := range []struct {
		name       string
		provenance firstInstallProvenance
		mac        string
	}{
		{name: "cp-1", provenance: run.Inputs.ControlPlaneInstall, mac: run.Inputs.ControlPlaneMAC},
		{name: "worker-1", provenance: run.Inputs.WorkerInstall, mac: run.Inputs.WorkerMAC},
	} {
		result, err := runWipeReinstallNode(ctx, run, reinstallRunner, node.name, overlays[node.name], node.provenance, node.mac)
		if err != nil {
			return nil, nil, nil, err
		}
		disk, err := firstInstallResultDisk(result)
		if err != nil {
			return nil, nil, nil, err
		}
		results[node.name] = result.Artifacts.Result
		disks[node.name] = disk
		esps[node.name] = result.Artifacts.InstalledESP
	}
	return results, disks, esps, nil
}

func runWipeReinstallNode(ctx context.Context, run operationBackedSmokeRun, runner vmtest.Runner, name, source string, provenance firstInstallProvenance, mac string) (vmtest.Result, error) {
	if strings.TrimSpace(source) == "" {
		return vmtest.Result{}, fmt.Errorf("%s bootstrapped disk overlay is required", name)
	}
	scenario := vmtest.Scenario{
		Name: "wipe-reinstall-" + name,
		Disks: []vmtest.DiskFixture{
			vmtest.SnapshotDisk("root", source, vmtest.DiskQCOW2),
		},
	}
	config := vmtest.FirstInstallConfig{
		Installer: vmtest.InstallerBootConfig{
			InstallerUKI:    provenance.InstallerUKI,
			InstallerKernel: provenance.InstallerKernel,
			InstallerInitrd: provenance.InstallerInitrd,
			CommandLine:     append([]string(nil), provenance.InstallerCommandLine...),
			RuntimeArtifact: provenance.RuntimeArtifact,
			VM:              operationBackedVMConfigForRun(run, mac, 0),
		},
		Runtime: vmtest.InstalledRuntimeConfig{
			RequireVMTestAgent: true,
			VM: func() vmtest.VMConfig {
				config := operationBackedVMConfigForRun(run, mac, 0)
				config.Agent.RequireHealth = true
				config.Agent.Timeout = 30 * time.Second
				return config
			}(),
		},
		ManifestPath:    provenance.InstallManifest,
		UseInstalledESP: true,
	}
	switch vmtest.FirstInstallWorldMode(provenance.FirstInstallMode) {
	case vmtest.FirstInstallWorldPreseed:
		config.PreseedManifest = true
	case vmtest.FirstInstallWorldGuestHandoff:
		config.GuestHandoff = true
	default:
		return vmtest.Result{}, fmt.Errorf("%s reinstall proof has unsupported first-install mode %q", name, provenance.FirstInstallMode)
	}
	result, err := vmtest.RunFirstInstall(ctx, runner, scenario, config)
	if err != nil {
		return result, err
	}
	if result.Status != vmtest.StatusPassed {
		return result, fmt.Errorf("%s wipe/reinstall status = %q, failure = %q, run dir = %s", name, result.Status, result.FailureSummary, result.RunDir)
	}
	return result, nil
}

func firstInstallResultDisk(result vmtest.Result) (string, error) {
	for _, disk := range result.Disks {
		if disk.Kind == vmtest.DiskTarget || disk.Kind == vmtest.DiskSnapshot {
			return disk.HostPath, nil
		}
	}
	return "", fmt.Errorf("%s has no installed disk", result.ScenarioName)
}

func bootOverlaysByNode(nodes []vmtest.RunningInstalledRuntimeNode) (map[string]string, error) {
	overlays := make(map[string]string, len(nodes))
	for _, node := range nodes {
		path, err := bootOverlayPath(node)
		if err != nil {
			return nil, err
		}
		overlays[node.Name] = path
	}
	return overlays, nil
}

func bootOverlayPath(node vmtest.RunningInstalledRuntimeNode) (string, error) {
	data, err := os.ReadFile(node.Result.Artifacts.DomainXML)
	if err != nil {
		return "", fmt.Errorf("read %s domain XML: %w", node.Name, err)
	}
	var domain struct {
		Devices struct {
			Disks []struct {
				Source struct {
					File string `xml:"file,attr"`
				} `xml:"source"`
				Serial string `xml:"serial"`
			} `xml:"disk"`
		} `xml:"devices"`
	}
	if err := xml.Unmarshal(data, &domain); err != nil {
		return "", fmt.Errorf("decode %s domain XML: %w", node.Name, err)
	}
	for _, disk := range domain.Devices.Disks {
		if disk.Serial != "katl-boot" {
			continue
		}
		if _, err := os.Stat(disk.Source.File); err != nil {
			return "", fmt.Errorf("stat %s boot overlay %s: %w", node.Name, disk.Source.File, err)
		}
		return disk.Source.File, nil
	}
	return "", fmt.Errorf("%s domain XML has no katl-boot disk", node.Name)
}

func collectWipeReinstallGeneration0Evidence(ctx context.Context, result vmtest.Result, nodes []vmtest.RunningInstalledRuntimeNode) (map[string]threeNodeGeneration0NodeEvidence, error) {
	evidence := make(map[string]threeNodeGeneration0NodeEvidence, len(nodes))
	for _, node := range nodes {
		role := "worker"
		if node.Name == "cp-1" {
			role = "control-plane"
		}
		dir := filepath.Join(result.RunDir, "post-reinstall", "generation-0", node.Name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		paths, err := collectGeneration0NodeEvidence(ctx, node, dir)
		if err != nil {
			return nil, err
		}
		if err := assertNoRunIdentity(ctx, node); err != nil {
			return nil, err
		}
		if _, err := assertGeneration0NodeEvidence(paths, threeNodeGeneration0Input{Name: node.Name, Role: role}); err != nil {
			return nil, err
		}
		evidence[node.Name] = threeNodeGeneration0NodeEvidence{
			Name:                node.Name,
			Role:                role,
			Address:             node.Result.IPAddress,
			Result:              node.Result.Artifacts.Result,
			InstalledRuntime:    node.Result.Artifacts.InstalledRuntime,
			GenerationID:        "0",
			GenerationSpec:      paths.Spec,
			GenerationStatus:    paths.Status,
			BootSelection:       paths.Selection,
			NodeMetadata:        paths.Metadata,
			MachineIDPath:       paths.MachineID,
			PersistentMachineID: paths.PersistentMachineID,
			LayoutProbe:         paths.LayoutProbe,
		}
	}
	return evidence, nil
}

func writeWipeReinstallArtifactManifest(path string, inputs operationBackedSmokeInputs, initial wipeReinstallBootstrapEvidence, reinstallResults, reinstallDisks, reinstallESPs map[string]string, clean map[string]threeNodeGeneration0NodeEvidence, post wipeReinstallBootstrapEvidence) error {
	return writeTwoNodeDiagnosticJSON(path, wipeReinstallArtifactManifest{
		VMTestRun:               inputs.WorldProvenance.VMTestRun,
		WorldManifest:           inputs.WorldProvenance.WorldManifest,
		HostCapabilities:        inputs.WorldProvenance.HostCapabilities,
		ResourceManifest:        inputs.WorldProvenance.ResourceManifest,
		ResourceManifestSHA256:  inputs.WorldProvenance.ResourceManifestSHA256,
		PackageLock:             inputs.WorldProvenance.PackageLock,
		PackageLockSHA256:       inputs.WorldProvenance.PackageLockSHA256,
		MkosiArtifactIndex:      inputs.WorldProvenance.MkosiArtifactIndex,
		KubernetesPayloadBundle: initial.KubernetesPayloadBundle,
		InitialBootstrap:        initial,
		ReinstallResults:        reinstallResults,
		ReinstallDisks:          reinstallDisks,
		ReinstallESPs:           reinstallESPs,
		CleanGeneration0:        clean,
		PostReinstallBootstrap:  post,
		NetworkLeases:           inputs.WorldProvenance.NetworkLeaseFile,
	})
}

func nodeByName(nodes []vmtest.RunningInstalledRuntimeNode, name string) vmtest.RunningInstalledRuntimeNode {
	for _, node := range nodes {
		if node.Name == name {
			return node
		}
	}
	return vmtest.RunningInstalledRuntimeNode{}
}
