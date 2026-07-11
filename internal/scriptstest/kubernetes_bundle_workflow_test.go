package scriptstest

import (
	"strings"
	"testing"
)

func TestKubernetesBundleWorkflowContract(t *testing.T) {
	repo := repoRoot(t)
	workflow := string(mustReadFile(t, repo+"/.github/workflows/kubernetes-bundles.yml"))

	required := []string{
		"payload_version:",
		"artifact_version:",
		"publish:",
		"packages: write",
		"oras_1.3.3_linux_amd64.tar.gz",
		"9ce999f8d2de03fc03968b29d743077a58783e545e5eaa53917ca177352d0e59",
		"scripts/check-kubernetes-sysext",
		"go run ./cmd/katl-publish-kubernetes-sysext",
		"ghcr.io/katl-dev/bundles",
		"application/vnd.katl.kubernetes.payload.bundle.v1",
		"kubernetes-sha256-",
		"immutable OCI tag already exists",
		"actions/attest@v4",
	}
	for _, value := range required {
		if !strings.Contains(workflow, value) {
			t.Fatalf("Kubernetes bundle workflow missing %q", value)
		}
	}

	runtimeBuild := strings.Index(workflow, "scripts/mkosi build-runtime")
	sysextBuild := strings.Index(workflow, "scripts/mkosi build-kubernetes-sysext")
	if runtimeBuild < 0 || sysextBuild < 0 || runtimeBuild >= sysextBuild {
		t.Fatalf("Kubernetes bundle workflow must build the compatible runtime before the sysext")
	}

	releaseWorkflow := string(mustReadFile(t, repo+"/.github/workflows/release-artifacts.yml"))
	if !strings.Contains(releaseWorkflow, `- "v*"`) || strings.Contains(releaseWorkflow, `- "**"`) {
		t.Fatalf("KatlOS release workflow must select KatlOS tags without consuming bundle tags")
	}
}
