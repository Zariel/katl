package vmtest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
)

const (
	WorldManifestEnv = "KATL_VMTEST_WORLD_MANIFEST"
	WorldAPIVersion  = "katl.dev/v1alpha1"
	WorldKind        = "VMTestWorld"
)

type World struct {
	APIVersion   string                 `json:"apiVersion"`
	Kind         string                 `json:"kind"`
	RunID        string                 `json:"runID"`
	RunDir       string                 `json:"runDir"`
	ArtifactDir  string                 `json:"artifactDir"`
	ScenarioDir  string                 `json:"scenarioDir"`
	Network      WorldNetwork           `json:"network"`
	Capabilities map[string]WorldStatus `json:"capabilities"`
}

type WorldNetwork struct {
	Backend   NetworkBackend `json:"backend"`
	Bridge    string         `json:"bridge,omitempty"`
	CIDR      string         `json:"cidr"`
	Gateway   string         `json:"gateway"`
	LeaseFile string         `json:"leaseFile"`
}

type NetworkBackend string

const (
	NetworkBridge NetworkBackend = "bridge"
)

type WorldStatus string

const (
	WorldStatusPassed      WorldStatus = "passed"
	WorldStatusFailed      WorldStatus = "failed"
	WorldStatusSetupFailed WorldStatus = "setup-failed"
	WorldStatusHostSkipped WorldStatus = "host-skipped"
	WorldStatusDisabled    WorldStatus = "disabled"
)

func DecodeWorld(reader io.Reader) (World, error) {
	var world World
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&world); err != nil {
		return World{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return World{}, errors.New("world manifest must contain exactly one JSON document")
	}
	if err := ValidateWorld(world); err != nil {
		return World{}, err
	}
	return world, nil
}

func LoadWorld(path string) (World, error) {
	file, err := os.Open(path)
	if err != nil {
		return World{}, err
	}
	defer file.Close()
	world, err := DecodeWorld(file)
	if err != nil {
		return World{}, fmt.Errorf("%s: %w", path, err)
	}
	return world, nil
}

func LoadWorldFromEnv() (World, error) {
	path := os.Getenv(WorldManifestEnv)
	if path == "" {
		return World{}, fmt.Errorf("%s is not set", WorldManifestEnv)
	}
	return LoadWorld(path)
}

func RequireWorld(t interface {
	Helper()
	Fatalf(format string, args ...any)
}) World {
	t.Helper()
	world, err := LoadWorldFromEnv()
	if err != nil {
		t.Fatalf("VM test world setup failed: %v; run enabled VM tests with scripts/vmtest-run", err)
	}
	return world
}

func ValidateWorld(world World) error {
	if world.APIVersion != WorldAPIVersion {
		return fmt.Errorf("unsupported apiVersion %q", world.APIVersion)
	}
	if world.Kind != WorldKind {
		return fmt.Errorf("unsupported kind %q", world.Kind)
	}
	if strings.TrimSpace(world.RunID) == "" {
		return errors.New("runID is required")
	}
	for name, path := range map[string]string{
		"runDir":      world.RunDir,
		"artifactDir": world.ArtifactDir,
		"scenarioDir": world.ScenarioDir,
		"leaseFile":   world.Network.LeaseFile,
	} {
		if strings.TrimSpace(path) == "" {
			return fmt.Errorf("%s is required", name)
		}
		if !filepath.IsAbs(path) {
			return fmt.Errorf("%s must be an absolute path: %s", name, path)
		}
	}
	if err := validateWorldNetwork(world.Network); err != nil {
		return err
	}
	if len(world.Capabilities) == 0 {
		return errors.New("capabilities are required")
	}
	for name, status := range world.Capabilities {
		if strings.TrimSpace(name) == "" {
			return errors.New("capability name is required")
		}
		if !validWorldStatus(status) {
			return fmt.Errorf("unsupported capability status %q for %q", status, name)
		}
	}
	return nil
}

func validateWorldNetwork(network WorldNetwork) error {
	switch network.Backend {
	case NetworkBridge:
		if strings.TrimSpace(network.Bridge) == "" {
			return errors.New("network.bridge is required for bridge backend")
		}
		if err := validateBridgeName(network.Bridge); err != nil {
			return fmt.Errorf("network.bridge: %w", err)
		}
	case "":
		return errors.New("network.backend is required")
	default:
		return fmt.Errorf("unsupported network.backend %q", network.Backend)
	}
	_, cidr, err := net.ParseCIDR(network.CIDR)
	if err != nil {
		return fmt.Errorf("network.cidr %q is invalid: %w", network.CIDR, err)
	}
	gateway := net.ParseIP(network.Gateway)
	if gateway == nil {
		return fmt.Errorf("network.gateway %q is invalid", network.Gateway)
	}
	if !cidr.Contains(gateway) {
		return fmt.Errorf("network.gateway %q is outside network.cidr %q", network.Gateway, network.CIDR)
	}
	return nil
}

func validWorldStatus(status WorldStatus) bool {
	switch status {
	case WorldStatusPassed, WorldStatusFailed, WorldStatusSetupFailed, WorldStatusHostSkipped, WorldStatusDisabled:
		return true
	default:
		return false
	}
}
