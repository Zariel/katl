package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKubeadmPlanActionRequiredSnapshot(t *testing.T) {
	root := kubeadmPlanFixture(t, clusterConfig("v1.36.1"))
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"kubeadm", "plan", "--root", root}, &stdout, &stderr)
	assertCommandExit(t, err, kubeadmPlanExitActionRequired)
	want, readErr := os.ReadFile("testdata/kubeadm-plan-action-required.json")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if stdout.String() != string(want) {
		t.Fatalf("plan output:\n%s\nwant:\n%s", stdout.String(), want)
	}
}

func TestKubeadmPlanExitClasses(t *testing.T) {
	t.Run("no changes", func(t *testing.T) {
		root := kubeadmPlanFixture(t, clusterConfig("v1.36.1"))
		writeKubeadmFixture(t, root, "live/kubeadm-config/ClusterConfiguration.yaml", clusterConfig("v1.36.1"))
		var stdout, stderr bytes.Buffer
		if err := run(t.Context(), []string{"kubeadm", "plan", "--root", root, "--live-state-dir", filepath.Join(root, "live")}, &stdout, &stderr); err != nil {
			t.Fatalf("run() error = %v", err)
		}
		if !strings.Contains(stdout.String(), `"classification": "no-op"`) || !strings.Contains(stdout.String(), `"exitCode": 0`) {
			t.Fatalf("output = %s", stdout.String())
		}
	})

	t.Run("manual unsupported", func(t *testing.T) {
		root := kubeadmPlanFixture(t, strings.Replace(clusterConfig("v1.36.1"), "clusterName: katl", "clusterName: katl\ncertificatesDir: /etc/kubernetes/pki", 1))
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"kubeadm", "plan", "--root", root}, &stdout, &stderr)
		assertCommandExit(t, err, kubeadmPlanExitManual)
		if !strings.Contains(stdout.String(), `"classification": "unsupported-manual"`) || !strings.Contains(stdout.String(), `"automaticApply": false`) {
			t.Fatalf("missing manual refusal output = %s", stdout.String())
		}
	})

	t.Run("collection failure", func(t *testing.T) {
		root := t.TempDir()
		var stdout, stderr bytes.Buffer
		err := run(t.Context(), []string{"kubeadm", "plan", "--root", root, "--name", "control-plane"}, &stdout, &stderr)
		assertCommandExit(t, err, kubeadmPlanExitCollection)
		if !strings.Contains(stdout.String(), `"kind":"KubeadmPlanFailure"`) || strings.Contains(stdout.String(), root) {
			t.Fatalf("failure output = %s", stdout.String())
		}
	})
}

func assertCommandExit(t *testing.T, err error, want int) {
	t.Helper()
	var exit commandExitError
	if !errors.As(err, &exit) || exit.code != want {
		t.Fatalf("error = %#v, want command exit %d", err, want)
	}
}

func kubeadmPlanFixture(t *testing.T, config string) string {
	t.Helper()
	root := t.TempDir()
	writeKubeadmFixture(t, root, "etc/katl/node.json", `{"kubeadm":{"configRef":"control-plane"},"kubernetes":{"payloadVersion":"v1.36.1"}}`)
	writeKubeadmFixture(t, root, "etc/katl/kubeadm/control-plane/config.yaml", config)
	return root
}

func writeKubeadmFixture(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func clusterConfig(version string) string {
	return "apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\nclusterName: katl\nkubernetesVersion: " + version + "\n"
}
