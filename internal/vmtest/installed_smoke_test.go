package vmtest

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestInstalledRuntimeVMTestAgentSmoke(t *testing.T) {
	options := DefaultOptions()
	if !options.Enabled {
		t.Skip("set -katl.vmtest.run or KATL_VMTEST_RUN=1 to run installed runtime vmtest agent smoke")
	}
	options.Missing = MissingSkips
	disk, esp := requireInstalledRuntimeFixture(t, options, "installed-runtime-vmtest-agent")

	runner := NewRunner(options)
	runner.RequireHost(t, HostRequirements{
		QEMU: true,
		OVMF: true,
		KVM:  options.KVM,
	})
	result, err := runner.Plan(Scenario{
		Name: "installed-runtime-vmtest-agent",
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	result = RunInstalledRuntime(ctx, result, InstalledRuntimeConfig{
		Disk:               disk,
		DiskFormat:         DiskFormat(first(os.Getenv("KATL_INSTALLED_DISK_FORMAT"), string(DiskRaw))),
		ESPArtifacts:       esp,
		RequireVMTestAgent: true,
		VM: VMConfig{
			KVM:     options.KVM,
			Timeout: 3 * time.Minute,
			VSock: VSockConfig{
				Enabled: true,
			},
			Agent: AgentControlConfig{
				RequireHealth: true,
				Timeout:       20 * time.Second,
			},
		},
	}, VMRunner{})
	if err := runner.Write(Scenario{Name: "installed-runtime-vmtest-agent"}, result); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("Status = %q, failure = %q, run dir = %s", result.Status, result.FailureSummary, result.RunDir)
	}
	transcript, err := os.ReadFile(result.Artifacts.VSockTranscript)
	if err != nil {
		t.Fatalf("read vsock transcript: %v", err)
	}
	if !strings.Contains(string(transcript), `"method":"Health"`) || !strings.Contains(string(transcript), `"status":"ok"`) {
		t.Fatalf("vsock transcript did not record successful health: %s", transcript)
	}
}

func TestInstalledRuntimeKubeadmAPISmoke(t *testing.T) {
	options := DefaultOptions()
	if !options.Enabled {
		t.Skip("set -katl.vmtest.run or KATL_VMTEST_RUN=1 to run installed runtime kubeadm API smoke")
	}
	options.Missing = MissingSkips
	disk, esp := requireInstalledRuntimeFixture(t, options, "installed-runtime-kubeadm-api-smoke")

	runner := NewRunner(options)
	runner.RequireHost(t, HostRequirements{
		QEMU: true,
		OVMF: true,
		KVM:  options.KVM,
	})
	result, err := runner.Plan(Scenario{
		Name: "installed-runtime-kubeadm-api-smoke",
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	result = RunInstalledKubeadmAPISmoke(ctx, result, KubeadmAPISmokeConfig{
		Runtime: InstalledRuntimeConfig{
			Disk:         disk,
			DiskFormat:   DiskFormat(first(os.Getenv("KATL_INSTALLED_DISK_FORMAT"), string(DiskRaw))),
			ESPArtifacts: esp,
			VM: VMConfig{
				KVM:     options.KVM,
				RAMMiB:  4096,
				CPUs:    2,
				Timeout: 18 * time.Minute,
				VSock: VSockConfig{
					Enabled: true,
				},
			},
		},
	}, VMRunner{})
	if err := runner.Write(Scenario{Name: "installed-runtime-kubeadm-api-smoke"}, result); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if result.Status != StatusPassed {
		t.Fatalf("Status = %q, failure = %q, run dir = %s", result.Status, result.FailureSummary, result.RunDir)
	}
}

func requireInstalledRuntimeFixture(t *testing.T, options Options, scenarioName string) (string, string) {
	t.Helper()
	disk := os.Getenv("KATL_INSTALLED_DISK")
	esp := os.Getenv("KATL_INSTALLED_ESP_ARTIFACTS")
	if disk != "" && esp != "" {
		return disk, esp
	}
	var missing []string
	if disk == "" {
		missing = append(missing, "KATL_INSTALLED_DISK")
	}
	if esp == "" {
		missing = append(missing, "KATL_INSTALLED_ESP_ARTIFACTS")
	}
	message := fmt.Sprintf("set %s or run scripts/resolve-installed-runtime-fixture", strings.Join(missing, " and "))
	runner := NewRunner(options)
	result, err := runner.Plan(Scenario{Name: scenarioName})
	if err == nil {
		now := runner.time()
		result.start(now)
		result.finish(StatusSkipped, message, now)
		result.Missing = append(result.Missing, MissingPrerequisite{
			Name:   strings.Join(missing, ","),
			Detail: message,
		})
		_ = runner.Write(Scenario{Name: scenarioName}, result)
	}
	t.Skip(message)
	return "", ""
}
