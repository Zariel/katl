package vmtest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanNspawnWorldRunWritesSetupFailureForMissingSource(t *testing.T) {
	world := testWorld(t)
	run, err := planNspawnWorldRun(world, "missing nspawn source", t.TempDir(), false)
	if err == nil || !strings.Contains(err.Error(), "nspawn userspace source is missing") {
		t.Fatalf("planNspawnWorldRun() error = %v, want missing source", err)
	}
	if run.Scenario == nil {
		t.Fatal("planNspawnWorldRun() did not return scenario on setup failure")
	}
	var result scenarioResult
	readJSONForTest(t, run.Scenario.ResultPath, &result)
	if result.Status != WorldStatusSetupFailed || !strings.Contains(result.FailureSummary, "nspawn userspace source is missing") {
		t.Fatalf("result = %#v", result)
	}
}

func TestDefaultNspawnUserspaceSourcePrefersImage(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, "build", "mkosi", "katl-runtime-root")
	image := filepath.Join(repo, "build", "mkosi", "katl-runtime-root.squashfs")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	writeFixtureFile(t, image, "image")

	source, err := defaultNspawnUserspaceSource(repo)
	if err != nil {
		t.Fatalf("defaultNspawnUserspaceSource() error = %v", err)
	}
	if source.Image != image || source.Root != "" {
		t.Fatalf("source = %#v, want image", source)
	}
}

func TestDefaultNspawnUserspaceSourceFallsBackToRoot(t *testing.T) {
	repo := t.TempDir()
	root := filepath.Join(repo, "build", "mkosi", "katl-runtime-root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}

	source, err := defaultNspawnUserspaceSource(repo)
	if err != nil {
		t.Fatalf("defaultNspawnUserspaceSource() error = %v", err)
	}
	if source.Root != root || source.Image != "" {
		t.Fatalf("source = %#v, want root", source)
	}
}
