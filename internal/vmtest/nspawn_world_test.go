package vmtest

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zariel/katl/internal/nspawntest"
)

type nspawnWorldRun struct {
	Scenario *WorldScenario
	Runner   nspawntest.Runner
	Root     NspawnUserspaceFixture
}

func nspawnWorldRunFor(t *testing.T, name string) (nspawnWorldRun, bool) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(WorldManifestEnv)) == "" {
		return nspawnWorldRun{}, false
	}
	world := RequireWorld(t)
	run, err := planNspawnWorldRun(world, name, repoRoot(t), envBool("KATL_NSPAWN_ALLOW_UNPRIVILEGED"))
	if err != nil {
		failWorldSetup(t, run.Scenario, err)
	}
	return run, true
}

func planNspawnWorldRun(world World, name, repo string, allowUnprivileged bool) (nspawnWorldRun, error) {
	scenario, err := world.PlanScenario(name)
	if err != nil {
		return nspawnWorldRun{}, err
	}
	run := nspawnWorldRun{Scenario: scenario}
	if err := scenario.WriteManifest(); err != nil {
		return run, err
	}
	source, err := defaultNspawnUserspaceSource(repo)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	var fixture NspawnUserspaceFixture
	if source.Root != "" {
		fixture, err = scenario.NspawnUserspaceRoot(source.Root)
	} else {
		fixture, err = scenario.NspawnUserspaceImage(source.Image)
	}
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	options := nspawntest.Options{
		Enabled:           true,
		StateRoot:         filepath.Join(scenario.Dir, "nspawn-runs"),
		Root:              fixture.Root,
		Image:             fixture.Image,
		Keep:              nspawntest.KeepFailed,
		Missing:           nspawntest.MissingSkips,
		AllowUnprivileged: allowUnprivileged,
	}
	run.Runner = nspawntest.NewRunner(options)
	run.Root = fixture
	return run, nil
}

type nspawnUserspaceSource struct {
	Root  string
	Image string
}

func defaultNspawnUserspaceSource(repo string) (nspawnUserspaceSource, error) {
	image := filepath.Join(repo, "build", "mkosi", "katl-runtime-root.squashfs")
	if info, err := os.Stat(image); err == nil && info.Mode().IsRegular() {
		return nspawnUserspaceSource{Image: image}, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nspawnUserspaceSource{}, err
	}
	root := filepath.Join(repo, "build", "mkosi", "katl-runtime-root")
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return nspawnUserspaceSource{Root: root}, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nspawnUserspaceSource{}, err
	}
	return nspawnUserspaceSource{}, errors.New("nspawn userspace source is missing: run scripts/mkosi build-runtime")
}

func failWorldSetup(t *testing.T, scenario *WorldScenario, err error) {
	t.Helper()
	if scenario == nil {
		t.Fatalf("%v", err)
	}
	if writeErr := scenario.WriteSetupFailure(err); writeErr != nil {
		t.Fatalf("write VM world setup failure: %v; original error: %v", writeErr, err)
	}
	t.Fatalf("%v\nworld scenario dir: %s", err, scenario.Dir)
}
