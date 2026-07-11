package kubeadmplan

import (
	"reflect"
	"strings"
	"testing"
)

func TestSupportedControlPlaneProfilingDelta(t *testing.T) {
	live := []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\nclusterName: katl\nkubernetesVersion: v1.36.1\n")
	desired := []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\nclusterName: katl\nkubernetesVersion: v1.36.1\napiServer:\n  extraArgs:\n    - name: profiling\n      value: \"false\"\nscheduler:\n  extraArgs:\n    - name: profiling\n      value: \"false\"\n")
	got, err := SupportedControlPlaneProfilingDelta(desired, live)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ClusterConfiguration.apiServer.extraArgs.profiling=false", "ClusterConfiguration.scheduler.extraArgs.profiling=false"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("delta=%v want %v", got, want)
	}
}

func TestSupportedControlPlaneProfilingDeltaNormalizesEmptyLiveSections(t *testing.T) {
	live := []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\napiServer: {}\ncontrollerManager: {}\nscheduler: {}\n")
	desired := []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\napiServer:\n  extraArgs:\n    - name: profiling\n      value: \"false\"\ncontrollerManager:\n  extraArgs:\n    - name: profiling\n      value: \"false\"\nscheduler:\n  extraArgs:\n    - name: profiling\n      value: \"false\"\n")
	delta, err := SupportedControlPlaneProfilingDelta(desired, live)
	if err != nil {
		t.Fatal(err)
	}
	if len(delta) != 3 {
		t.Fatalf("delta = %v, want three supported fields", delta)
	}
}

func TestSupportedControlPlaneProfilingDeltaRejectsOtherChange(t *testing.T) {
	live := []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\nkubernetesVersion: v1.36.1\n")
	desired := []byte("apiVersion: kubeadm.k8s.io/v1beta4\nkind: ClusterConfiguration\nkubernetesVersion: v1.36.2\napiServer:\n  extraArgs:\n    - name: profiling\n      value: \"false\"\n")
	_, err := SupportedControlPlaneProfilingDelta(desired, live)
	if err == nil || !strings.Contains(err.Error(), "outside profiling") {
		t.Fatalf("error=%v", err)
	}
}
