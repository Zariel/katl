package resourcetest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const (
	APIVersion = "katl.dev/v1alpha1"
	Kind       = "ResourceTestManifest"
)

type Status string

const (
	StatusPassed      Status = "passed"
	StatusFailed      Status = "failed"
	StatusSetupFailed Status = "setup-failed"
	StatusHostSkipped Status = "host-skipped"
	StatusDisabled    Status = "disabled"
)

type HarnessStatus string

const (
	HarnessPassed  HarnessStatus = "passed"
	HarnessFailed  HarnessStatus = "failed"
	HarnessSkipped HarnessStatus = "skipped"
)

type Manifest struct {
	APIVersion       string           `json:"apiVersion"`
	Kind             string           `json:"kind"`
	RunID            string           `json:"runID"`
	Created          time.Time        `json:"created"`
	Git              GitState         `json:"git"`
	Tools            []Tool           `json:"tools,omitempty"`
	MkosiProfiles    []MkosiProfile   `json:"mkosiProfiles,omitempty"`
	PackageSets      []PackageSet     `json:"packageSets,omitempty"`
	HostCapabilities []HostCapability `json:"hostCapabilities,omitempty"`
	Artifacts        []Artifact       `json:"artifacts,omitempty"`
	Fixtures         []Fixture        `json:"fixtures,omitempty"`
	Scenarios        []Scenario       `json:"scenarios,omitempty"`
}

type GitState struct {
	Revision string `json:"revision"`
	Dirty    bool   `json:"dirty"`
}

type Tool struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Path    string `json:"path,omitempty"`
	Digest  string `json:"sha256,omitempty"`
}

type MkosiProfile struct {
	Name          string `json:"name"`
	Path          string `json:"path"`
	ConfigDigest  string `json:"configSHA256,omitempty"`
	PackageSetRef string `json:"packageSetRef,omitempty"`
}

type PackageSet struct {
	Name     string    `json:"name"`
	Source   string    `json:"source,omitempty"`
	Digest   string    `json:"sha256,omitempty"`
	Packages []Package `json:"packages,omitempty"`
}

type Package struct {
	Name     string `json:"name"`
	NEVRA    string `json:"nevra"`
	Checksum string `json:"sha256,omitempty"`
}

type HostCapability struct {
	Name    string `json:"name"`
	Present bool   `json:"present"`
	Detail  string `json:"detail,omitempty"`
}

type Artifact struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Path      string    `json:"path"`
	Digest    string    `json:"sha256"`
	SizeBytes int64     `json:"sizeBytes,omitempty"`
	Created   time.Time `json:"created,omitempty"`
}

type Fixture struct {
	Name         string   `json:"name"`
	Kind         string   `json:"kind"`
	Path         string   `json:"path"`
	Digest       string   `json:"sha256,omitempty"`
	Manifest     string   `json:"manifest,omitempty"`
	ArtifactRefs []string `json:"artifactRefs,omitempty"`
}

type Scenario struct {
	Name                 string   `json:"name"`
	Suite                string   `json:"suite"`
	Status               Status   `json:"status"`
	ResultPath           string   `json:"resultPath,omitempty"`
	RunDir               string   `json:"runDir,omitempty"`
	FailureSummary       string   `json:"failureSummary,omitempty"`
	FixtureRefs          []string `json:"fixtureRefs,omitempty"`
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty"`
}

type ScenarioObservation struct {
	Enabled            bool
	SetupComplete      bool
	HarnessStatus      HarnessStatus
	HostCapabilitySkip bool
}

func NewManifest(runID string, git GitState) Manifest {
	return Manifest{
		APIVersion: APIVersion,
		Kind:       Kind,
		RunID:      runID,
		Created:    time.Now().UTC(),
		Git:        git,
	}
}

func DecodeManifest(r io.Reader) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, err
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %q", APIVersion)
	}
	if manifest.Kind != Kind {
		return fmt.Errorf("kind must be %q", Kind)
	}
	if strings.TrimSpace(manifest.RunID) == "" {
		return errors.New("runID is required")
	}
	if strings.TrimSpace(manifest.Git.Revision) == "" {
		return errors.New("git.revision is required")
	}

	artifacts := map[string]bool{}
	for i, artifact := range manifest.Artifacts {
		if err := validateArtifact(artifact); err != nil {
			return fmt.Errorf("artifacts[%d]: %w", i, err)
		}
		if artifacts[artifact.Name] {
			return fmt.Errorf("artifacts[%d]: duplicate artifact %q", i, artifact.Name)
		}
		artifacts[artifact.Name] = true
	}

	fixtures := map[string]bool{}
	for i, fixture := range manifest.Fixtures {
		if err := validateFixture(fixture, artifacts); err != nil {
			return fmt.Errorf("fixtures[%d]: %w", i, err)
		}
		if fixtures[fixture.Name] {
			return fmt.Errorf("fixtures[%d]: duplicate fixture %q", i, fixture.Name)
		}
		fixtures[fixture.Name] = true
	}

	capabilities := map[string]bool{}
	for i, capability := range manifest.HostCapabilities {
		if strings.TrimSpace(capability.Name) == "" {
			return fmt.Errorf("hostCapabilities[%d]: name is required", i)
		}
		if capabilities[capability.Name] {
			return fmt.Errorf("hostCapabilities[%d]: duplicate capability %q", i, capability.Name)
		}
		capabilities[capability.Name] = true
	}

	packageSets := map[string]bool{}
	for i, set := range manifest.PackageSets {
		if strings.TrimSpace(set.Name) == "" {
			return fmt.Errorf("packageSets[%d]: name is required", i)
		}
		if set.Digest != "" && !validSHA256(set.Digest) {
			return fmt.Errorf("packageSets[%d]: sha256 must be lowercase SHA-256", i)
		}
		if packageSets[set.Name] {
			return fmt.Errorf("packageSets[%d]: duplicate package set %q", i, set.Name)
		}
		packageSets[set.Name] = true
		for j, pkg := range set.Packages {
			if strings.TrimSpace(pkg.Name) == "" || strings.TrimSpace(pkg.NEVRA) == "" {
				return fmt.Errorf("packageSets[%d].packages[%d]: name and nevra are required", i, j)
			}
			if pkg.Checksum != "" && !validSHA256(pkg.Checksum) {
				return fmt.Errorf("packageSets[%d].packages[%d]: sha256 must be lowercase SHA-256", i, j)
			}
		}
	}

	for i, profile := range manifest.MkosiProfiles {
		if strings.TrimSpace(profile.Name) == "" || strings.TrimSpace(profile.Path) == "" {
			return fmt.Errorf("mkosiProfiles[%d]: name and path are required", i)
		}
		if profile.ConfigDigest != "" && !validSHA256(profile.ConfigDigest) {
			return fmt.Errorf("mkosiProfiles[%d]: configSHA256 must be lowercase SHA-256", i)
		}
		if profile.PackageSetRef != "" && !packageSets[profile.PackageSetRef] {
			return fmt.Errorf("mkosiProfiles[%d]: packageSetRef %q is not defined", i, profile.PackageSetRef)
		}
	}

	for i, tool := range manifest.Tools {
		if strings.TrimSpace(tool.Name) == "" {
			return fmt.Errorf("tools[%d]: name is required", i)
		}
		if tool.Digest != "" && !validSHA256(tool.Digest) {
			return fmt.Errorf("tools[%d]: sha256 must be lowercase SHA-256", i)
		}
	}

	for i, scenario := range manifest.Scenarios {
		if err := validateScenario(scenario, fixtures, capabilities); err != nil {
			return fmt.Errorf("scenarios[%d]: %w", i, err)
		}
	}
	return nil
}

func ClassifyScenario(observation ScenarioObservation) Status {
	if !observation.Enabled {
		return StatusDisabled
	}
	if !observation.SetupComplete {
		return StatusSetupFailed
	}
	if observation.HostCapabilitySkip {
		return StatusHostSkipped
	}
	switch observation.HarnessStatus {
	case HarnessPassed:
		return StatusPassed
	case HarnessFailed:
		return StatusFailed
	case HarnessSkipped:
		return StatusSetupFailed
	default:
		return StatusSetupFailed
	}
}

func validateArtifact(artifact Artifact) error {
	if strings.TrimSpace(artifact.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(artifact.Kind) == "" {
		return errors.New("kind is required")
	}
	if strings.TrimSpace(artifact.Path) == "" {
		return errors.New("path is required")
	}
	if !validSHA256(artifact.Digest) {
		return errors.New("sha256 must be lowercase SHA-256")
	}
	if artifact.SizeBytes < 0 {
		return errors.New("sizeBytes must not be negative")
	}
	return nil
}

func validateFixture(fixture Fixture, artifacts map[string]bool) error {
	if strings.TrimSpace(fixture.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(fixture.Kind) == "" {
		return errors.New("kind is required")
	}
	if strings.TrimSpace(fixture.Path) == "" && strings.TrimSpace(fixture.Manifest) == "" {
		return errors.New("path or manifest is required")
	}
	if fixture.Digest != "" && !validSHA256(fixture.Digest) {
		return errors.New("sha256 must be lowercase SHA-256")
	}
	for _, ref := range fixture.ArtifactRefs {
		if !artifacts[ref] {
			return fmt.Errorf("artifactRef %q is not defined", ref)
		}
	}
	return nil
}

func validateScenario(scenario Scenario, fixtures, capabilities map[string]bool) error {
	if strings.TrimSpace(scenario.Name) == "" {
		return errors.New("name is required")
	}
	if strings.TrimSpace(scenario.Suite) == "" {
		return errors.New("suite is required")
	}
	if !validStatus(scenario.Status) {
		return fmt.Errorf("status %q is unsupported", scenario.Status)
	}
	if scenario.Status == StatusPassed && strings.TrimSpace(scenario.ResultPath) == "" {
		return errors.New("passed scenario requires resultPath")
	}
	for _, ref := range scenario.FixtureRefs {
		if !fixtures[ref] {
			return fmt.Errorf("fixtureRef %q is not defined", ref)
		}
	}
	for _, capability := range scenario.RequiredCapabilities {
		if !capabilities[capability] {
			return fmt.Errorf("required capability %q is not defined", capability)
		}
	}
	return nil
}

func validStatus(status Status) bool {
	switch status {
	case StatusPassed, StatusFailed, StatusSetupFailed, StatusHostSkipped, StatusDisabled:
		return true
	default:
		return false
	}
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validSHA256(value string) bool {
	return sha256Pattern.MatchString(value)
}
