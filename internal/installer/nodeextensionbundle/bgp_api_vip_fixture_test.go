package nodeextensionbundle

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/zariel/katl/internal/installer/bgpapivip"
)

func TestWriteBGPAPIVIPFixture(t *testing.T) {
	root := t.TempDir()
	fixture, err := WriteBGPAPIVIPFixture(BGPAPIVIPFixtureRequest{
		OutputDir: root,
		CreatedAt: "2026-06-19T12:00:00Z",
	})
	if err != nil {
		t.Fatalf("WriteBGPAPIVIPFixture() error = %v", err)
	}

	var bundle Bundle
	readJSON(t, fixture.BundlePath, &bundle)
	if bundle.AppID != bgpapivip.AppID || bundle.PayloadVersion != BGPAPIVIPPayloadVersion || bundle.DisplayName != "BGP API VIP endpoint extension" {
		t.Fatalf("bundle identity = %#v", bundle)
	}
	if len(bundle.Capabilities) != 1 || bundle.Capabilities[0].Name != "dev.katl.api-endpoint.bgp-vip" || bundle.Capabilities[0].ConfigSchemaIDs[0] != "dev.katl.api-endpoint.bgp-vip.config.v1alpha1" {
		t.Fatalf("bundle capabilities = %#v", bundle.Capabilities)
	}
	if !contains(bundle.Compatibility.RequiredKernelModules, "dummy") || !contains(bundle.Compatibility.RequiredCapabilities, "CAP_NET_ADMIN") {
		t.Fatalf("bundle compatibility = %#v", bundle.Compatibility)
	}
	for _, path := range []string{bgpapivip.ConfigPath} {
		if !contains(bundle.Configuration.ConfigHandoffPaths, path) {
			t.Fatalf("config paths missing %s: %#v", path, bundle.Configuration.ConfigHandoffPaths)
		}
	}
	if bundle.Status.LiveStatusPath != bgpapivip.LiveStatusPath || bundle.Status.DurableSnapshotPath != bgpapivip.OperationStatus || bundle.Status.StatusSchemaID != "dev.katl.api-endpoint.bgp-vip.status.v1alpha1" {
		t.Fatalf("status = %#v", bundle.Status)
	}
	payload := readBlobText(t, root, fixture.PayloadDigest)
	for _, want := range []string{
		"KATL_NODE_EXTENSION_FIXTURE=bgp-api-vip",
		"/usr/lib/systemd/system/katl-app-bgp-api-vip.service",
		"/usr/lib/extension-release.d/extension-release.katl-node-extension-bgp-api-vip",
		"systemd-analyze verify katl-app-bgp-api-vip.service",
		"dev.katl.api-endpoint.bgp-vip.status.v1alpha1",
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
	if strings.Contains(payload, "kubeadm") || strings.Contains(payload, "kubectl") || strings.Contains(payload, "containerd") {
		t.Fatalf("BGP API VIP fixture payload contains unrelated runtime surface:\n%s", payload)
	}
}

func TestBGPAPIVIPFixtureCanBeFetchedAndStaged(t *testing.T) {
	root := t.TempDir()
	fixture, err := WriteBGPAPIVIPFixture(BGPAPIVIPFixtureRequest{OutputDir: root})
	if err != nil {
		t.Fatalf("WriteBGPAPIVIPFixture() error = %v", err)
	}
	server := stageFixtureServer(t, root)
	staged, err := FetchAndStage(context.Background(), Request{
		Source:           server.URL,
		Ref:              stageRef(t, fixture),
		CacheDir:         t.TempDir(),
		RuntimeInterface: "katl-runtime-1",
		Architecture:     "x86_64",
		Client:           server.Client(),
	})
	if err != nil {
		t.Fatalf("FetchAndStage() error = %v", err)
	}
	if staged.AppID != bgpapivip.AppID || staged.ExtensionRef.Name != bgpapivip.AppID || staged.ExtensionRef.ActivationPath != "/run/extensions/katl-node-extension-bgp-api-vip.raw" {
		t.Fatalf("staged = %#v", staged)
	}
	payload, err := os.ReadFile(staged.SysextPath)
	if err != nil {
		t.Fatalf("read staged payload: %v", err)
	}
	if !strings.Contains(string(payload), "PAYLOAD_VERSION="+BGPAPIVIPPayloadVersion) {
		t.Fatalf("staged payload = %s", payload)
	}
}
