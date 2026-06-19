package nodeextensionbundle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteBirdFixture(t *testing.T) {
	root := t.TempDir()
	fixture, err := WriteBirdFixture(BirdFixtureRequest{
		OutputDir:   root,
		CreatedAt:   "2026-06-19T12:00:00Z",
		BirdVersion: "2.17.1",
	})
	if err != nil {
		t.Fatalf("WriteBirdFixture() error = %v", err)
	}

	var bundle Bundle
	readJSON(t, fixture.BundlePath, &bundle)
	if bundle.AppID != "bird" || bundle.PayloadVersion != BirdPayloadVersion || bundle.DisplayName != "Generic BIRD routing extension" {
		t.Fatalf("bundle identity = %#v", bundle)
	}
	if len(bundle.Capabilities) != 1 || bundle.Capabilities[0].Name != "dev.katl.routing.bird" || bundle.Capabilities[0].ConfigSchemaIDs[0] != "dev.katl.routing.bird.generated.v1alpha1" {
		t.Fatalf("bundle capabilities = %#v", bundle.Capabilities)
	}
	if !contains(bundle.Compatibility.RequiredCapabilities, "CAP_NET_ADMIN") || !contains(bundle.Compatibility.RequiredCapabilities, "CAP_NET_BIND_SERVICE") {
		t.Fatalf("bundle capabilities = %#v", bundle.Compatibility.RequiredCapabilities)
	}
	for _, unit := range []string{"katl-app-bird.target", "katl-app-bird.service", "katl-app-bird-ready.service", "katl-app-bird-status.service"} {
		if !contains(bundle.Systemd.ProvidedUnits, unit) {
			t.Fatalf("provided units missing %s: %#v", unit, bundle.Systemd.ProvidedUnits)
		}
	}
	if bundle.Configuration.ConfigHandoffPaths[0] != "/etc/katl/apps/bird/config.yaml" || bundle.Configuration.ConfigHandoffPaths[1] != "/etc/katl/apps/bird/bird.conf" {
		t.Fatalf("config paths = %#v", bundle.Configuration.ConfigHandoffPaths)
	}
	if bundle.Status.LiveStatusPath != "/run/katl/apps/bird/status.json" || bundle.Status.StatusSchemaID != "dev.katl.routing.bird.status.v1alpha1" {
		t.Fatalf("status = %#v", bundle.Status)
	}
	payload := readBlobText(t, root, fixture.PayloadDigest)
	for _, want := range []string{
		"KATL_NODE_EXTENSION_FIXTURE=bird",
		"BIRD_VERSION=2.17.1",
		"/usr/sbin/bird",
		"/usr/sbin/birdc",
		"/usr/lib/extension-release.d/extension-release.katl-node-extension-bird",
		"systemd-analyze verify katl-app-bird.service",
		"bird --version == 2.17.1",
	} {
		if !strings.Contains(payload, want) {
			t.Fatalf("payload missing %q:\n%s", want, payload)
		}
	}
	if strings.Contains(payload, "kubeadm") || strings.Contains(payload, "kubectl") || strings.Contains(payload, "containerd") {
		t.Fatalf("BIRD fixture payload contains unrelated runtime surface:\n%s", payload)
	}
	if _, err := os.Stat(filepath.Join(root, "checksums.txt")); err != nil {
		t.Fatalf("checksums missing: %v", err)
	}
}

func TestBirdFixtureCanBeFetchedAndStaged(t *testing.T) {
	root := t.TempDir()
	fixture, err := WriteBirdFixture(BirdFixtureRequest{OutputDir: root})
	if err != nil {
		t.Fatalf("WriteBirdFixture() error = %v", err)
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
	if staged.AppID != "bird" || staged.ExtensionRef.Name != "bird" || staged.ExtensionRef.ActivationPath != "/run/extensions/katl-node-extension-bird.raw" {
		t.Fatalf("staged = %#v", staged)
	}
	payload, err := os.ReadFile(staged.SysextPath)
	if err != nil {
		t.Fatalf("read staged payload: %v", err)
	}
	if !strings.Contains(string(payload), "PAYLOAD_VERSION="+BirdPayloadVersion) {
		t.Fatalf("staged payload = %s", payload)
	}
}

func readBlobText(t *testing.T, root string, digest string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "blobs", "sha256", strings.TrimPrefix(digest, "sha256:")))
	if err != nil {
		t.Fatalf("read blob %s: %v", digest, err)
	}
	return string(data)
}
