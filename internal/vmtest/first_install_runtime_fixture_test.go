package vmtest

import (
	"path/filepath"
	"testing"
	"time"
)

func TestFirstInstallFixtureVMConfigsKeepAgentRuntimeOnly(t *testing.T) {
	base := VMConfig{
		KVM:     KVMAuto,
		RAMMiB:  4096,
		CPUs:    2,
		Timeout: 12 * time.Minute,
		VSock: VSockConfig{
			Enabled:  true,
			GuestCID: 2048,
			Port:     10240,
		},
		Agent: AgentControlConfig{
			RequireHealth: true,
			Timeout:       30 * time.Second,
		},
	}

	installer, runtime := firstInstallFixtureVMConfigs(base)

	if installer.VSock.Enabled || installer.Agent.RequireHealth {
		t.Fatalf("installer VM config keeps agent settings: %#v", installer)
	}
	if !runtime.VSock.Enabled || !runtime.Agent.RequireHealth {
		t.Fatalf("runtime VM config lost agent settings: %#v", runtime)
	}
}

func TestPackageFirstInstallRuntimeFixtureWritesTypedFixture(t *testing.T) {
	root := t.TempDir()
	sourceDisk := writeFixtureFile(t, filepath.Join(root, "source", "installed.qcow2"), "disk contents")
	sourceESP := writeFixtureESP(t, filepath.Join(root, "source", "esp"))
	sourceMetadata := writeFixtureNodeMetadata(t, filepath.Join(root, "source", "node.json"), Node{Name: "worker-1", Role: Worker})
	firstResult := Result{
		ManifestDir: filepath.Join(root, "run", "manifests"),
	}

	fixture, err := packageFirstInstallRuntimeFixture(FirstInstallRuntimeFixtureContract{
		NodeMetadata: sourceMetadata,
		Node:         NodeSpec{Name: "worker-1", Role: Worker},
	}, firstResult, sourceDisk, sourceESP)
	if err != nil {
		t.Fatalf("packageFirstInstallRuntimeFixture() error = %v", err)
	}

	wantDir := filepath.Join(firstResult.ManifestDir, "installed-runtime-fixture")
	if fixture.ManifestPath != filepath.Join(wantDir, "installed-runtime-fixture.json") {
		t.Fatalf("fixture manifest = %q", fixture.ManifestPath)
	}
	if fixture.Disk != filepath.Join(wantDir, "installed-runtime.qcow2") {
		t.Fatalf("fixture disk = %q", fixture.Disk)
	}
	if fixture.ESPArtifacts != filepath.Join(wantDir, "esp") {
		t.Fatalf("fixture ESP artifacts = %q", fixture.ESPArtifacts)
	}
	if fixture.Disk == sourceDisk || fixture.ESPArtifacts == sourceESP {
		t.Fatalf("fixture references source artifacts: %#v", fixture)
	}

	record := readInstalledRuntimeFixtureForTest(t, fixture.ManifestPath)
	if record.NodeName != "worker-1" || record.SystemRole != string(Worker) {
		t.Fatalf("fixture identity = %s/%s", record.NodeName, record.SystemRole)
	}
	if record.Disk.Path != "installed-runtime.qcow2" || record.Disk.Format != string(DiskQCOW2) || record.Disk.SHA256 == "" {
		t.Fatalf("fixture disk record = %#v", record.Disk)
	}
	if record.ESPArtifacts.Path != "esp" || record.ESPArtifacts.TreeSHA256 == "" {
		t.Fatalf("fixture ESP record = %#v", record.ESPArtifacts)
	}
	if record.NodeMetadata == nil || record.NodeMetadata.Path != "node.json" || record.NodeMetadata.SHA256 == "" {
		t.Fatalf("fixture metadata record = %#v", record.NodeMetadata)
	}
	if err := validateInstalledRuntimeFixture(fixture.ManifestPath, record, InstalledRuntimeConfig{
		Disk:         fixture.Disk,
		DiskFormat:   DiskQCOW2,
		ESPArtifacts: fixture.ESPArtifacts,
	}, filepath.Join(wantDir, "node.json")); err != nil {
		t.Fatalf("validateInstalledRuntimeFixture() error = %v", err)
	}
}
