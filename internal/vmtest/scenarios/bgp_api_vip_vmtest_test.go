package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/katl-dev/katl/internal/installer/bgpapivip"
	"github.com/katl-dev/katl/internal/installer/nodeextensionbundle"
	"github.com/katl-dev/katl/internal/vmtest"
	vmtestpb "github.com/katl-dev/katl/internal/vmtest/proto"
)

func TestBIRDAndBGPAPIVIPExtensionsVMProof(t *testing.T) {
	if run, ok := bgpAPIVIPWorldRun(t); ok {
		runBGPAPIVIPProof(t, run)
		return
	}
	options := vmtest.DefaultOptions()
	if !options.Enabled {
		t.Skip("set -katl.vmtest.run or KATL_VMTEST_RUN=1 to run BIRD/BGP API VIP VM proof")
	}
	_ = vmtest.RequireWorld(t)
}

type bgpAPIVIPRun struct {
	WorldScenario *vmtest.WorldScenario
	Options       vmtest.Options
	Runner        vmtest.Runner
	Scenario      vmtest.Scenario
	Result        vmtest.Result
	Inputs        bgpAPIVIPInputs
}

type bgpAPIVIPInputs struct {
	ControlPlaneDisk       string                        `json:"controlPlaneDisk"`
	ControlPlaneDiskFormat string                        `json:"controlPlaneDiskFormat"`
	ControlPlaneESP        string                        `json:"controlPlaneESP"`
	ControlPlaneFixture    string                        `json:"controlPlaneFixture"`
	ControlPlaneMetadata   string                        `json:"controlPlaneMetadata"`
	ControlPlaneAddress    string                        `json:"controlPlaneAddress"`
	ControlPlaneMAC        string                        `json:"controlPlaneMAC"`
	WorkerDisk             string                        `json:"workerDisk"`
	WorkerDiskFormat       string                        `json:"workerDiskFormat"`
	WorkerESP              string                        `json:"workerESP"`
	WorkerFixture          string                        `json:"workerFixture"`
	WorkerMetadata         string                        `json:"workerMetadata"`
	WorkerAddress          string                        `json:"workerAddress"`
	WorkerMAC              string                        `json:"workerMAC"`
	WorldProvenance        multiNodeWorldProvenancePaths `json:"worldProvenance"`
}

type bgpAPIVIPExtensionProof struct {
	APIVersion        string                        `json:"apiVersion"`
	Kind              string                        `json:"kind"`
	Bundles           map[string]stagedBundleProof  `json:"bundles"`
	Config            string                        `json:"config"`
	HelperBinary      string                        `json:"helperBinary"`
	ControlPlaneProof string                        `json:"controlPlaneProof"`
	WorkerProof       string                        `json:"workerProof"`
	ControlPlaneFiles map[string]string             `json:"controlPlaneFiles,omitempty"`
	WorkerFiles       map[string]string             `json:"workerFiles,omitempty"`
	WorldProvenance   multiNodeWorldProvenancePaths `json:"worldProvenance"`
}

type stagedBundleProof struct {
	AppID                string `json:"appID"`
	PayloadVersion       string `json:"payloadVersion"`
	ArtifactVersion      string `json:"artifactVersion"`
	BundleManifestDigest string `json:"bundleManifestDigest"`
	SysextPayloadDigest  string `json:"sysextPayloadDigest"`
	BundleDir            string `json:"bundleDir"`
	SysextPath           string `json:"sysextPath"`
	ActivationPath       string `json:"activationPath"`
}

type bgpAPIVIPGuestProof struct {
	Mode                   string               `json:"mode"`
	Rejected               bool                 `json:"rejected"`
	Rejection              string               `json:"rejection"`
	AdvertisementSequence  []bool               `json:"advertisementSequence"`
	ObservedRouteExports   []observedRouteProof `json:"observedRouteExports"`
	ObservedRouteWithdraws []observedRouteProof `json:"observedRouteWithdraws"`
	RouteTable             []string             `json:"routeTable"`
}

type observedRouteProof struct {
	Peer             string   `json:"peer"`
	ExportedPrefixes []string `json:"exportedPrefixes"`
}

func bgpAPIVIPWorldRun(t *testing.T) (bgpAPIVIPRun, bool) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(vmtest.WorldManifestEnv)) == "" {
		return bgpAPIVIPRun{}, false
	}
	world := vmtest.RequireWorld(t)
	repo := katlRepoRoot(t)
	kvm := vmtest.DefaultOptions().KVM
	specs := []vmtest.NodeSpec{
		{Name: "cp-1", Role: vmtest.ControlPlane},
		{Name: "worker-1", Role: vmtest.Worker},
	}
	if err := ensurePublishedRuntimeFixturesForWorld(world, repo, specs, kvm); err != nil {
		failWorldFixtureSetup(t, world, "bird-bgp-api-vip-extension-proof", err)
	}
	run, err := planBGPAPIVIPWorldRun(world, repo, kvm)
	if err != nil {
		failTwoNodeWorldSetup(t, run.WorldScenario, err)
	}
	return run, true
}

func planBGPAPIVIPWorldRun(world vmtest.World, repo string, kvm vmtest.KVMPolicy) (bgpAPIVIPRun, error) {
	scenario, err := world.PlanScenario("bird-bgp-api-vip-extension-proof")
	if err != nil {
		return bgpAPIVIPRun{}, err
	}
	run := bgpAPIVIPRun{WorldScenario: scenario}
	buildRoots := publishedRuntimeBuildRoots(world, repo)
	cp, err := vmtest.AddPublishedInstalledRuntimeNodeFromBuildRoots(scenario, buildRoots, vmtest.NodeSpec{Name: "cp-1", Role: vmtest.ControlPlane})
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	worker, err := vmtest.AddPublishedInstalledRuntimeNodeFromBuildRoots(scenario, buildRoots, vmtest.NodeSpec{Name: "worker-1", Role: vmtest.Worker})
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	options := vmtest.Options{
		Enabled:   true,
		StateRoot: filepath.Join(scenario.Dir, "vm-runs"),
		Keep:      vmtest.KeepFailed,
		KVM:       kvm,
		Missing:   vmtest.MissingFails,
	}
	runner := vmtest.NewRunner(options)
	vmScenario := vmtest.Scenario{Name: "bird-bgp-api-vip-extension-proof"}
	result, err := runner.Plan(vmScenario)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	result.Started = time.Now().UTC()
	return bgpAPIVIPRun{
		WorldScenario: scenario,
		Options:       options,
		Runner:        runner,
		Scenario:      vmScenario,
		Result:        result,
		Inputs: bgpAPIVIPInputs{
			ControlPlaneDisk:       cp.Config.Disk,
			ControlPlaneDiskFormat: string(cp.Config.DiskFormat),
			ControlPlaneESP:        cp.Config.ESPArtifacts,
			ControlPlaneFixture:    cp.Config.FixtureManifest,
			ControlPlaneMetadata:   cp.Config.NodeMetadata,
			ControlPlaneAddress:    cp.Node.Address,
			ControlPlaneMAC:        cp.Node.MACAddress,
			WorkerDisk:             worker.Config.Disk,
			WorkerDiskFormat:       string(worker.Config.DiskFormat),
			WorkerESP:              worker.Config.ESPArtifacts,
			WorkerFixture:          worker.Config.FixtureManifest,
			WorkerMetadata:         worker.Config.NodeMetadata,
			WorkerAddress:          worker.Node.Address,
			WorkerMAC:              worker.Node.MACAddress,
			WorldProvenance:        multiNodeWorldProvenanceForSpecs(world, repo, []vmtest.NodeSpec{{Name: "cp-1", Role: vmtest.ControlPlane}, {Name: "worker-1", Role: vmtest.Worker}}),
		},
	}, nil
}

func runBGPAPIVIPProof(t *testing.T, run bgpAPIVIPRun) {
	t.Helper()
	runner := run.Runner
	scenario := run.Scenario
	result := run.Result
	requireVMHost(t, runner, scenario, result, vmtest.HostRequirements{
		Libvirt: true,
		OVMF:    true,
		KVM:     run.Options.KVM,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()

	staged, err := stageBGPAPIVIPExtensionBundles(ctx, result)
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	helper, err := buildBGPAPIVIPSmokeHelper(ctx, katlRepoRoot(t), result)
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	cpResult, err := vmtest.PlannedInstalledRuntimeNodeResult(result, "cp-1")
	if err != nil {
		t.Fatal(err)
	}
	workerResult, err := vmtest.PlannedInstalledRuntimeNodeResult(result, "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeBGPAPIVIPProofManifest(result, run.Inputs, staged, helper, vmtest.RunningInstalledRuntimeNode{Name: "cp-1", Result: cpResult}, vmtest.RunningInstalledRuntimeNode{Name: "worker-1", Result: workerResult}, "", ""); err != nil {
		t.Fatal(err)
	}

	cpNode, err := vmtest.StartInstalledRuntimeNode(ctx, result, bgpAPIVIPNodeConfig(run, "cp-1", run.Inputs.ControlPlaneDisk, run.Inputs.ControlPlaneESP, run.Inputs.ControlPlaneFixture, run.Inputs.ControlPlaneMetadata, vmtest.DiskFormat(run.Inputs.ControlPlaneDiskFormat), run.Inputs.ControlPlaneMAC), vmtest.VMRunner{})
	if err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatalf("start cp-1 VM: %v", err)
	}
	defer stopNode(t, cpNode)

	workerNode, err := vmtest.StartInstalledRuntimeNode(ctx, result, bgpAPIVIPNodeConfig(run, "worker-1", run.Inputs.WorkerDisk, run.Inputs.WorkerESP, run.Inputs.WorkerFixture, run.Inputs.WorkerMetadata, vmtest.DiskFormat(run.Inputs.WorkerDiskFormat), run.Inputs.WorkerMAC), vmtest.VMRunner{})
	if err != nil {
		collectTwoNodeDiagnostics("", cpNode)
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatalf("start worker-1 VM: %v", err)
	}
	defer stopNode(t, workerNode)

	config := bgpAPIVIPConfigYAML(firstString(cpNode.Result.IPAddress, run.Inputs.ControlPlaneAddress))
	configPath := filepath.Join(result.ManifestDir, "bgp-api-vip.yaml")
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	cpProof, err := runBGPAPIVIPGuestSmoke(ctx, cpNode, helper, config, "control-plane")
	if err != nil {
		collectTwoNodeDiagnostics("", cpNode, workerNode)
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatalf("control-plane BGP API VIP proof: %v", err)
	}
	workerProof, err := runBGPAPIVIPGuestSmoke(ctx, workerNode, helper, config, "worker")
	if err != nil {
		collectTwoNodeDiagnostics("", cpNode, workerNode)
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatalf("worker BGP API VIP proof: %v", err)
	}
	if err := assertBGPAPIVIPGuestProofs(cpProof, workerProof); err != nil {
		collectTwoNodeDiagnostics("", cpNode, workerNode)
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	if err := writeBGPAPIVIPProofManifest(result, run.Inputs, staged, helper, cpNode, workerNode, cpProof, workerProof); err != nil {
		finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusFailed, err.Error())
		t.Fatal(err)
	}
	finishTwoNodeResult(t, runner, scenario, result, vmtest.StatusPassed, "")
}

func bgpAPIVIPNodeConfig(run bgpAPIVIPRun, name, disk, esp, fixture, metadata string, format vmtest.DiskFormat, mac string) vmtest.InstalledRuntimeNodeConfig {
	return vmtest.InstalledRuntimeNodeConfig{
		Name: name,
		Runtime: vmtest.InstalledRuntimeConfig{
			Disk:               disk,
			DiskFormat:         format,
			ESPArtifacts:       esp,
			FixtureManifest:    fixture,
			NodeMetadata:       metadata,
			RequireVMTestAgent: true,
			VM: vmtest.VMConfig{
				KVM:    run.Options.KVM,
				RAMMiB: 2048,
				CPUs:   2,
				Network: vmtest.VMNetworkConfig{
					MAC: mac,
				},
				Timeout: 8 * time.Minute,
				Agent: vmtest.AgentControlConfig{
					RequireHealth: true,
					Timeout:       30 * time.Second,
				},
			},
		},
	}
}

func stageBGPAPIVIPExtensionBundles(ctx context.Context, result vmtest.Result) (map[string]stagedBundleProof, error) {
	root := filepath.Join(result.ManifestDir, "node-extension-bundles")
	birdRoot := filepath.Join(root, nodeextensionbundle.BirdAppID)
	bird, err := nodeextensionbundle.WriteBirdFixture(nodeextensionbundle.BirdFixtureRequest{OutputDir: birdRoot})
	if err != nil {
		return nil, err
	}
	vipRoot := filepath.Join(root, nodeextensionbundle.BGPAPIVIPAppID)
	vip, err := nodeextensionbundle.WriteBGPAPIVIPFixture(nodeextensionbundle.BGPAPIVIPFixtureRequest{OutputDir: vipRoot})
	if err != nil {
		return nil, err
	}
	out := map[string]stagedBundleProof{}
	for _, source := range []struct {
		root    string
		fixture nodeextensionbundle.Fixture
	}{
		{root: birdRoot, fixture: bird},
		{root: vipRoot, fixture: vip},
	} {
		server := httptest.NewTLSServer(http.FileServer(http.Dir(source.root)))
		staged, err := nodeextensionbundle.FetchAndStage(ctx, nodeextensionbundle.Request{
			Source:           server.URL,
			Ref:              nodeExtensionFixtureRef(source.fixture),
			CacheDir:         filepath.Join(result.ManifestDir, "staged-node-extensions"),
			RuntimeInterface: "katl-runtime-1",
			Architecture:     "x86_64",
			Client:           server.Client(),
		})
		server.Close()
		if err != nil {
			return nil, err
		}
		out[staged.AppID] = stagedBundleProof{
			AppID:                staged.AppID,
			PayloadVersion:       staged.PayloadVersion,
			ArtifactVersion:      staged.ArtifactVersion,
			BundleManifestDigest: staged.BundleManifestDigest,
			SysextPayloadDigest:  staged.SysextPayloadDigest,
			BundleDir:            staged.BundleDir,
			SysextPath:           staged.SysextPath,
			ActivationPath:       staged.ExtensionRef.ActivationPath,
		}
	}
	for _, appID := range []string{nodeextensionbundle.BirdAppID, nodeextensionbundle.BGPAPIVIPAppID} {
		if _, ok := out[appID]; !ok {
			return nil, fmt.Errorf("staged extension bundles missing %s", appID)
		}
	}
	return out, nil
}

func nodeExtensionFixtureRef(fixture nodeextensionbundle.Fixture) string {
	var index nodeextensionbundle.Index
	data, err := os.ReadFile(fixture.IndexPath)
	if err != nil {
		panic(err)
	}
	if err := json.Unmarshal(data, &index); err != nil {
		panic(err)
	}
	entry := index.Entries[0]
	return entry.AppID + "/" + entry.PayloadVersion + "@" + entry.BundleManifestDigest
}

func buildBGPAPIVIPSmokeHelper(ctx context.Context, repo string, result vmtest.Result) (string, error) {
	path := filepath.Join(result.ManifestDir, "bgp-api-vip-smoke")
	cmd := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-trimpath", "-ldflags", "-s -w", "-o", path, "./internal/vmtest/testcmd/bgp-api-vip-smoke")
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH=amd64", "GOCACHE="+filepath.Join(result.RunDir, "go-cache"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("build bgp-api-vip-smoke helper: %w\n%s", err, output)
	}
	return path, nil
}

func runBGPAPIVIPGuestSmoke(ctx context.Context, node vmtest.RunningInstalledRuntimeNode, helper string, config string, mode string) (string, error) {
	root := "/var/lib/katl/test-artifacts/bgp-api-vip"
	binary := root + "/bin/bgp-api-vip-smoke"
	configPath := root + "/" + mode + "/config.yaml"
	outputDir := root + "/" + mode + "/evidence"
	data, err := os.ReadFile(helper)
	if err != nil {
		return "", err
	}
	if err := writeNodeFileChunked(ctx, node, binary, data, 0o755); err != nil {
		return "", err
	}
	if err := writeNodeFile(ctx, node, configPath, []byte(config), 0o644, false); err != nil {
		return "", err
	}
	opCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	client, err := vmtest.DialAgent(opCtx, node.VSock.GuestCID, node.VSock.Port, node.Result.Artifacts.VSockTranscript)
	if err != nil {
		return "", err
	}
	defer client.Close()
	guest := vmtest.NewGuestControl(node.Result, client)
	if _, err := guest.RunCommand(opCtx, vmtest.GuestCommandRequest{
		Name: "bgp-api-vip-smoke-" + mode,
		Argv: []string{binary},
		Environment: []*vmtestpb.EnvVar{
			{Name: "KATL_BGP_API_VIP_CONFIG", Value: configPath},
			{Name: "KATL_BGP_API_VIP_SMOKE_MODE", Value: mode},
			{Name: "KATL_BGP_API_VIP_SMOKE_OUTPUT", Value: outputDir},
		},
		StdoutLimit: 32 << 10,
		StderrLimit: 32 << 10,
		Timeout:     90 * time.Second,
	}); err != nil {
		return "", err
	}
	proofPath := outputDir + "/proof.json"
	proof, err := readNodeFile(ctx, node, proofPath, 256<<10)
	if err != nil {
		return "", err
	}
	hostPath := filepath.Join(node.Result.Artifacts.GuestDir, "bgp-api-vip", mode+"-proof.json")
	if err := os.MkdirAll(filepath.Dir(hostPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(hostPath, proof, 0o644); err != nil {
		return "", err
	}
	for _, guestPath := range []string{
		outputDir + "/status-live.json",
		outputDir + "/status-operation.json",
		outputDir + "/rendered/etc/katl/apps/bird/bird.conf",
		outputDir + "/rendered/etc/katl/apps/bgp-api-vip/config.yaml",
	} {
		data, err := readNodeFile(ctx, node, guestPath, 512<<10)
		if err != nil {
			continue
		}
		target := filepath.Join(node.Result.Artifacts.GuestDir, "bgp-api-vip", mode, strings.TrimPrefix(guestPath, root+"/"))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return "", err
		}
	}
	return hostPath, nil
}

func assertBGPAPIVIPGuestProofs(cpProofPath, workerProofPath string) error {
	var cp bgpAPIVIPGuestProof
	if err := readProofJSON(cpProofPath, &cp); err != nil {
		return err
	}
	if cp.Mode != "control-plane" || len(cp.ObservedRouteExports) != 1 || len(cp.ObservedRouteExports[0].ExportedPrefixes) != 1 || cp.ObservedRouteExports[0].ExportedPrefixes[0] != "10.40.0.10/32" {
		return fmt.Errorf("control-plane proof = %#v", cp)
	}
	if len(cp.RouteTable) != 0 {
		return fmt.Errorf("control-plane route table = %v, want withdrawn", cp.RouteTable)
	}
	var worker bgpAPIVIPGuestProof
	if err := readProofJSON(workerProofPath, &worker); err != nil {
		return err
	}
	if worker.Mode != "worker" || !worker.Rejected || !strings.Contains(worker.Rejection, "cannot advertise BGP API VIP") {
		return fmt.Errorf("worker proof = %#v", worker)
	}
	return nil
}

func readProofJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func bgpAPIVIPConfigYAML(sourceAddress string) string {
	sourceAddress = strings.TrimSpace(sourceAddress)
	if sourceAddress == "" {
		sourceAddress = "192.0.2.10"
	}
	return fmt.Sprintf(`apiVersion: %s
kind: %s
spec:
  endpoint:
    host: api.home.example
    vip: 10.40.0.10/32
  vipInterface:
    kind: dummy
    name: katl-api0
    mtu: 1500
  routing:
    routerID: %s
    localASN: 64512
    sourceAddress: %s
    sourceInterface: enp1s0
    exportPolicy:
      communities:
        - "64512:100"
      localPreference: 100
  devHostPeers:
    - name: dev-host
      address: 10.0.0.1
      asn: 64500
      allowedExportPrefixes:
        - 10.40.0.10/32
`, bgpapivip.APIVersion, bgpapivip.Kind, sourceAddress, sourceAddress)
}

func writeBGPAPIVIPProofManifest(result vmtest.Result, inputs bgpAPIVIPInputs, bundles map[string]stagedBundleProof, helper string, cpNode, workerNode vmtest.RunningInstalledRuntimeNode, cpProof, workerProof string) error {
	proof := bgpAPIVIPExtensionProof{
		APIVersion:        vmtest.WorldAPIVersion,
		Kind:              "BIRDAndBGPAPIVIPExtensionProof",
		Bundles:           bundles,
		Config:            filepath.Join(result.ManifestDir, "bgp-api-vip.yaml"),
		HelperBinary:      helper,
		ControlPlaneProof: cpProof,
		WorkerProof:       workerProof,
		WorldProvenance:   inputs.WorldProvenance,
		ControlPlaneFiles: map[string]string{
			"result":     cpNode.Result.Artifacts.Result,
			"serial":     cpNode.Result.Artifacts.RuntimeSerial,
			"transcript": cpNode.Result.Artifacts.VSockTranscript,
		},
		WorkerFiles: map[string]string{
			"result":     workerNode.Result.Artifacts.Result,
			"serial":     workerNode.Result.Artifacts.RuntimeSerial,
			"transcript": workerNode.Result.Artifacts.VSockTranscript,
		},
	}
	data, err := json.MarshalIndent(proof, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(result.ManifestDir, "bird-bgp-api-vip-extension-proof.json"), append(data, '\n'), 0o644)
}
