package kubeadmplan

import (
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPlanNoOpInitConfig(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS:                        desiredFS(initConfig()),
		ConfigPath:                "/etc/katl/kubeadm/control-plane/config.yaml",
		SelectedKubernetesVersion: "v1.36.1",
		Live: LiveSnapshot{
			KubeadmConfigMap: &KubeadmConfigMapSnapshot{
				InitConfiguration:    initDocument(),
				ClusterConfiguration: clusterDocument("v1.36.1"),
			},
			KubeletConfigMap: kubeletDocument(),
			KubeletConfig:    kubeletDocument(),
			KubeadmFlagsEnv:  []byte("KUBELET_KUBEADM_ARGS=--container-runtime-endpoint=unix:///run/containerd/containerd.sock\n"),
		},
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != NoOp {
		t.Fatalf("classification = %s, want %s: %#v", plan.Classification, NoOp, plan.Changes)
	}
	if got, want := docKinds(plan.Desired.Documents), []string{"InitConfiguration", "ClusterConfiguration", "KubeletConfiguration"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("documents = %#v, want %#v", got, want)
	}
}

func TestPlanBootstrapNeededForMissingLiveCluster(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS:         desiredFS(initConfig()),
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != BootstrapNeeded {
		t.Fatalf("classification = %s, want %s", plan.Classification, BootstrapNeeded)
	}
}

func TestPlanBootstrapNeededForJoinConfig(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS:         desiredFS(joinConfig()),
		ConfigPath: "/etc/katl/kubeadm/worker/config.yaml",
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != BootstrapNeeded {
		t.Fatalf("classification = %s, want %s", plan.Classification, BootstrapNeeded)
	}
	if !plan.Desired.HasJoinConfiguration {
		t.Fatalf("desired state = %#v, want join config", plan.Desired)
	}
}

func TestPlanUpgradeNeededForSelectedSysextMismatch(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS:                        desiredFS(initConfig()),
		ConfigPath:                "/etc/katl/kubeadm/control-plane/config.yaml",
		SelectedKubernetesVersion: "v1.36.1",
		Live: LiveSnapshot{
			KubeadmConfigMap: &KubeadmConfigMapSnapshot{
				InitConfiguration:    initDocument(),
				ClusterConfiguration: clusterDocument("v1.35.7"),
			},
			KubeletConfigMap: kubeletDocument(),
		},
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != ExplicitUpgradeNeeded {
		t.Fatalf("classification = %s, want %s: %#v", plan.Classification, ExplicitUpgradeNeeded, plan.Changes)
	}
	if !hasField(plan.Changes, "kubeadm-config.ClusterConfiguration.kubernetesVersion") {
		t.Fatalf("changes = %#v, want cluster version field", plan.Changes)
	}
}

func TestPlanReconfigureNeededForKubeletDiff(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS:         desiredFS(initConfig()),
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
		Live: LiveSnapshot{
			KubeadmConfigMap: &KubeadmConfigMapSnapshot{
				InitConfiguration:    initDocument(),
				ClusterConfiguration: clusterDocument("v1.36.1"),
			},
			KubeletConfigMap: []byte(`apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: cgroupfs
`),
		},
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != ExplicitReconfigureNeeded {
		t.Fatalf("classification = %s, want %s: %#v", plan.Classification, ExplicitReconfigureNeeded, plan.Changes)
	}
	if !hasField(plan.Changes, "kubelet-config") {
		t.Fatalf("changes = %#v, want kubelet diff", plan.Changes)
	}
}

func TestPlanCanonicalizesYAMLKeyOrder(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS: fstest.MapFS{
			"etc/katl/kubeadm/control-plane/config.yaml": &fstest.MapFile{
				Data: []byte(`kind: ClusterConfiguration
apiVersion: kubeadm.k8s.io/v1beta4
networking:
  serviceSubnet: 10.96.0.0/12
  podSubnet: 10.244.0.0/16
clusterName: katl
kubernetesVersion: v1.36.1
`),
				Mode: 0o644,
			},
		},
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
		Live: LiveSnapshot{
			KubeadmConfigMap: &KubeadmConfigMapSnapshot{
				ClusterConfiguration: clusterDocument("v1.36.1"),
			},
		},
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != NoOp {
		t.Fatalf("classification = %s, want %s: %#v", plan.Classification, NoOp, plan.Changes)
	}
}

func TestPlanRejectsUnsupportedDesiredDocument(t *testing.T) {
	_, err := PlanDesiredLive(Request{
		FS: desiredFS(`apiVersion: kubeadm.k8s.io/v1beta4
kind: ResetConfiguration
`),
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported kubeadm YAML document") {
		t.Fatalf("PlanDesiredLive() error = %v, want unsupported document", err)
	}
}

func TestPlanReportsKubeadmFlagsDrift(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS:         desiredFS(initConfig()),
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
		Live: LiveSnapshot{
			KubeadmConfigMap: &KubeadmConfigMapSnapshot{
				InitConfiguration:    initDocument(),
				ClusterConfiguration: clusterDocument("v1.36.1"),
			},
			KubeletConfigMap: kubeletDocument(),
			KubeadmFlagsEnv:  []byte("KUBELET_KUBEADM_ARGS=--container-runtime-endpoint=unix:///run/crio/crio.sock\n"),
		},
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != ExplicitReconfigureNeeded {
		t.Fatalf("classification = %s, want %s: %#v", plan.Classification, ExplicitReconfigureNeeded, plan.Changes)
	}
	if !hasField(plan.Changes, "kubeadm-flags.env") {
		t.Fatalf("changes = %#v, want kubeadm flags drift", plan.Changes)
	}
}

func TestPlanReportsStaticPodManifestReviewWithReconfigure(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS:         desiredFS(initConfig()),
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
		Live: LiveSnapshot{
			KubeadmConfigMap: &KubeadmConfigMapSnapshot{
				InitConfiguration:    initDocument(),
				ClusterConfiguration: clusterDocument("v1.36.1"),
			},
			KubeletConfigMap: []byte(`apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: cgroupfs
`),
			StaticPodManifests: map[string][]byte{
				"kube-apiserver.yaml": []byte("apiVersion: v1\nkind: Pod\n"),
			},
		},
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != ExplicitReconfigureNeeded {
		t.Fatalf("classification = %s, want %s: %#v", plan.Classification, ExplicitReconfigureNeeded, plan.Changes)
	}
	if !hasField(plan.Changes, "staticPodManifests") {
		t.Fatalf("changes = %#v, want static pod manifest review", plan.Changes)
	}
}

func TestPlanUnsupportedManualForDeniedHostPath(t *testing.T) {
	plan, err := PlanDesiredLive(Request{
		FS: desiredFS(`apiVersion: kubeadm.k8s.io/v1beta4
kind: InitConfiguration
nodeRegistration:
  kubeletExtraArgs:
    volume-plugin-dir: /var/lib/kubelet/plugins
`),
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != UnsupportedManualIntervention {
		t.Fatalf("classification = %s, want %s", plan.Classification, UnsupportedManualIntervention)
	}
	if len(plan.Changes) != 1 || plan.Changes[0].Field != "desired" {
		t.Fatalf("changes = %#v", plan.Changes)
	}
	if strings.Contains(plan.Changes[0].Message, "token") {
		t.Fatalf("change leaked secret-like text: %#v", plan.Changes)
	}
}

func TestPlanReadsPatchDigests(t *testing.T) {
	fsys := desiredFS(initConfig())
	fsys["etc/katl/kubeadm/control-plane/patches/kube-apiserver0+merge.yaml"] = &fstest.MapFile{
		Data: []byte("spec:\n  containers: []\n"),
		Mode: 0o644,
	}
	plan, err := PlanDesiredLive(Request{
		FS:         fsys,
		ConfigPath: "/etc/katl/kubeadm/control-plane/config.yaml",
		PatchesDir: "/etc/katl/kubeadm/control-plane/patches",
		Live: LiveSnapshot{
			KubeadmConfigMap: &KubeadmConfigMapSnapshot{
				InitConfiguration:    initDocument(),
				ClusterConfiguration: clusterDocument("v1.36.1"),
			},
			KubeletConfigMap: kubeletDocument(),
		},
	})
	if err != nil {
		t.Fatalf("PlanDesiredLive() error = %v", err)
	}
	if plan.Classification != ExplicitReconfigureNeeded {
		t.Fatalf("classification = %s, want %s", plan.Classification, ExplicitReconfigureNeeded)
	}
	if len(plan.Desired.PatchDigests) != 1 {
		t.Fatalf("patch digests = %#v", plan.Desired.PatchDigests)
	}
}

func desiredFS(config string) fstest.MapFS {
	return fstest.MapFS{
		"etc/katl/kubeadm/control-plane/config.yaml": &fstest.MapFile{
			Data: []byte(config),
			Mode: 0o644,
		},
		"etc/katl/kubeadm/worker/config.yaml": &fstest.MapFile{
			Data: []byte(joinConfig()),
			Mode: 0o644,
		},
	}
}

func docKinds(documents []Document) []string {
	kinds := make([]string, 0, len(documents))
	for _, document := range documents {
		kinds = append(kinds, document.Kind)
	}
	return kinds
}

func hasField(changes []Change, field string) bool {
	for _, change := range changes {
		if change.Field == field {
			return true
		}
	}
	return false
}

func initConfig() string {
	return string(initDocument()) + "---\n" + string(clusterDocument("v1.36.1")) + "---\n" + string(kubeletDocument())
}

func initDocument() []byte {
	return []byte(`apiVersion: kubeadm.k8s.io/v1beta4
kind: InitConfiguration
nodeRegistration:
  criSocket: unix:///run/containerd/containerd.sock
`)
}

func clusterDocument(version string) []byte {
	return []byte(`apiVersion: kubeadm.k8s.io/v1beta4
kind: ClusterConfiguration
clusterName: katl
kubernetesVersion: ` + version + `
networking:
  podSubnet: 10.244.0.0/16
  serviceSubnet: 10.96.0.0/12
`)
}

func kubeletDocument() []byte {
	return []byte(`apiVersion: kubelet.config.k8s.io/v1beta1
kind: KubeletConfiguration
cgroupDriver: systemd
`)
}

func joinConfig() string {
	return `apiVersion: kubeadm.k8s.io/v1beta4
kind: JoinConfiguration
nodeRegistration:
  criSocket: unix:///run/containerd/containerd.sock
discovery:
  file:
    kubeConfigPath: /etc/katl/kubeadm/join-discovery.yaml
`
}

var _ fs.FS = fstest.MapFS{}
