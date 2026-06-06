package vmtest

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type InstalledRuntimeWorldNode struct {
	Node    Node
	Fixture InstalledRuntimeFixture
	Config  InstalledRuntimeConfig
}

type PublishedFirstInstallRuntimeFixture struct {
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

func AddPublishedInstalledRuntimeNode(scenario *WorldScenario, repo string, spec NodeSpec) (InstalledRuntimeWorldNode, error) {
	node, err := scenario.AddNode(spec)
	if err != nil {
		return InstalledRuntimeWorldNode{}, err
	}
	published, err := FindPublishedFirstInstallRuntimeFixture(repo, spec)
	if err != nil {
		return InstalledRuntimeWorldNode{Node: node}, err
	}
	factory := scenario.NodeFixtures(node)
	format := DiskFormat(published.DiskFormat)
	if format == "" {
		format = DiskQCOW2
	}
	fixture, err := factory.PublishInstalledRuntimeFromFirstInstall(published.FixtureManifest, format)
	if err != nil {
		return InstalledRuntimeWorldNode{Node: node}, err
	}
	return InstalledRuntimeWorldNode{
		Node:    node,
		Fixture: fixture,
		Config: InstalledRuntimeConfig{
			Disk:            fixture.Disk,
			DiskFormat:      fixture.DiskFormat,
			ESPArtifacts:    fixture.ESPArtifacts,
			FixtureManifest: fixture.ManifestPath,
			NodeMetadata:    fixture.NodeMetadata,
		},
	}, nil
}

func FindPublishedFirstInstallRuntimeFixture(repo string, spec NodeSpec) (PublishedFirstInstallRuntimeFixture, error) {
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
		return PublishedFirstInstallRuntimeFixture{}, err
	}
	var best PublishedFirstInstallRuntimeFixture
	var bestTime time.Time
	for _, candidate := range candidates {
		published, err := ReadPublishedFirstInstallRuntimeFixture(candidate.Path)
		if err != nil {
			return PublishedFirstInstallRuntimeFixture{}, err
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
		return PublishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture is missing: run the first-install fixture contract")
	}
	return best, nil
}

func ReadPublishedFirstInstallRuntimeFixture(path string) (PublishedFirstInstallRuntimeFixture, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return PublishedFirstInstallRuntimeFixture{}, err
	}
	var published PublishedFirstInstallRuntimeFixture
	if err := json.Unmarshal(data, &published); err != nil {
		return PublishedFirstInstallRuntimeFixture{}, err
	}
	if published.APIVersion != WorldAPIVersion || published.Kind != "PublishedFirstInstallRuntimeFixture" {
		return PublishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture has unsupported apiVersion or kind")
	}
	if strings.TrimSpace(published.NodeName) == "" || strings.TrimSpace(published.SystemRole) == "" {
		return PublishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture identity is incomplete")
	}
	if strings.TrimSpace(published.FixtureManifest) == "" {
		return PublishedFirstInstallRuntimeFixture{}, errors.New("published installed runtime fixture manifest is required")
	}
	if !filepath.IsAbs(published.FixtureManifest) {
		published.FixtureManifest = filepath.Join(filepath.Dir(path), published.FixtureManifest)
	}
	if published.DiskFormat == "" {
		published.DiskFormat = string(DiskQCOW2)
	}
	return published, nil
}
