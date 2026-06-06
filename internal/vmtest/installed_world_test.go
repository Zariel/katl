package vmtest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type installedRuntimeWorldRun struct {
	Scenario *WorldScenario
	Runner   Runner
	Fixture  InstalledRuntimeFixture
	Config   InstalledRuntimeConfig
}

type publishedFirstInstallRuntimeFixture struct {
	APIVersion      string `json:"apiVersion"`
	Kind            string `json:"kind"`
	NodeName        string `json:"nodeName"`
	SystemRole      string `json:"systemRole"`
	FixtureManifest string `json:"fixtureManifest"`
	DiskFormat      string `json:"diskFormat"`
}

type publishedFixtureCandidate struct {
	Path    string
	ModTime time.Time
}

func installedRuntimeWorldRunFor(t *testing.T, name string, spec NodeSpec) (installedRuntimeWorldRun, bool) {
	t.Helper()
	if strings.TrimSpace(os.Getenv(WorldManifestEnv)) == "" {
		return installedRuntimeWorldRun{}, false
	}
	world := RequireWorld(t)
	run, err := planInstalledRuntimeWorldRun(world, name, repoRoot(t), spec, DefaultOptions().KVM)
	if err != nil {
		failWorldSetup(t, run.Scenario, err)
	}
	return run, true
}

func planInstalledRuntimeWorldRun(world World, name, repo string, spec NodeSpec, kvm KVMPolicy) (installedRuntimeWorldRun, error) {
	scenario, err := world.PlanScenario(name)
	if err != nil {
		return installedRuntimeWorldRun{}, err
	}
	run := installedRuntimeWorldRun{Scenario: scenario}
	node, err := scenario.AddNode(spec)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	published, err := findPublishedFirstInstallRuntimeFixture(repo, spec)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	factory := scenario.NodeFixtures(node)
	format := DiskFormat(published.DiskFormat)
	if format == "" {
		format = DiskQCOW2
	}
	fixture, err := factory.PublishInstalledRuntimeFromFirstInstall(published.FixtureManifest, format)
	if err != nil {
		_ = scenario.WriteSetupFailure(err)
		return run, err
	}
	options := Options{
		Enabled:   true,
		StateRoot: filepath.Join(scenario.Dir, "vm-runs"),
		Keep:      KeepFailed,
		KVM:       firstKVM(kvm, KVMAuto),
		Missing:   MissingSkips,
	}
	run.Runner = NewRunner(options)
	run.Fixture = fixture
	run.Config = InstalledRuntimeConfig{
		Disk:            fixture.Disk,
		DiskFormat:      fixture.DiskFormat,
		ESPArtifacts:    fixture.ESPArtifacts,
		FixtureManifest: fixture.ManifestPath,
		NodeMetadata:    fixture.NodeMetadata,
	}
	return run, nil
}

func findPublishedFirstInstallRuntimeFixture(repo string, spec NodeSpec) (publishedFirstInstallRuntimeFixture, error) {
	root := filepath.Join(repo, "build")
	var candidates []publishedFixtureCandidate
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() || entry.Name() != "published-first-install-runtime-fixture.json" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		candidates = append(candidates, publishedFixtureCandidate{Path: path, ModTime: info.ModTime()})
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return publishedFirstInstallRuntimeFixture{}, err
	}
	var best publishedFirstInstallRuntimeFixture
	var bestTime time.Time
	for _, candidate := range candidates {
		published, err := readPublishedFirstInstallRuntimeFixture(candidate.Path)
		if err != nil {
			return publishedFirstInstallRuntimeFixture{}, err
		}
		if spec.Name != "" && published.NodeName != spec.Name {
			continue
		}
		if spec.Role != "" && NodeRole(published.SystemRole) != spec.Role {
			continue
		}
		if best.FixtureManifest == "" || candidate.ModTime.After(bestTime) {
			best = published
			bestTime = candidate.ModTime
		}
	}
	if best.FixtureManifest == "" {
		return publishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture is missing: run the first-install fixture contract")
	}
	return best, nil
}

func readPublishedFirstInstallRuntimeFixture(path string) (publishedFirstInstallRuntimeFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return publishedFirstInstallRuntimeFixture{}, err
	}
	var published publishedFirstInstallRuntimeFixture
	if err := json.Unmarshal(data, &published); err != nil {
		return publishedFirstInstallRuntimeFixture{}, err
	}
	if published.APIVersion != WorldAPIVersion || published.Kind != "PublishedFirstInstallRuntimeFixture" {
		return publishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture has unsupported apiVersion or kind")
	}
	if strings.TrimSpace(published.NodeName) == "" || strings.TrimSpace(published.SystemRole) == "" {
		return publishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture identity is incomplete")
	}
	if strings.TrimSpace(published.FixtureManifest) == "" {
		return publishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture manifest is required")
	}
	if !filepath.IsAbs(published.FixtureManifest) {
		published.FixtureManifest = filepath.Join(filepath.Dir(path), published.FixtureManifest)
	}
	if published.DiskFormat == "" {
		published.DiskFormat = string(DiskQCOW2)
	}
	return published, nil
}

func firstKVM(value, fallback KVMPolicy) KVMPolicy {
	if value != "" {
		return value
	}
	return fallback
}
