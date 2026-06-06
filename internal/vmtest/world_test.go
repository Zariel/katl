package vmtest

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeWorldValidManifest(t *testing.T) {
	world, err := DecodeWorld(strings.NewReader(validWorldJSON(t)))
	if err != nil {
		t.Fatalf("DecodeWorld() error = %v", err)
	}
	if world.APIVersion != WorldAPIVersion || world.Kind != WorldKind {
		t.Fatalf("world envelope = %#v", world)
	}
	if world.Network.Backend != NetworkBridge || world.Network.Gateway != "10.77.0.1" {
		t.Fatalf("network = %#v", world.Network)
	}
	if world.Capabilities["qemu"] != WorldStatusPassed {
		t.Fatalf("qemu capability = %q", world.Capabilities["qemu"])
	}
}

func TestWorldManifestJSONShape(t *testing.T) {
	got, err := json.MarshalIndent(validWorld(), "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent() error = %v", err)
	}
	want := `{
  "apiVersion": "katl.dev/v1alpha1",
  "kind": "VMTestWorld",
  "runID": "20260606T120000Z-abc123",
  "runDir": "/tmp/katl-vmtest/20260606T120000Z-abc123",
  "artifactDir": "/tmp/katl-vmtest/20260606T120000Z-abc123/artifacts",
  "scenarioDir": "/tmp/katl-vmtest/20260606T120000Z-abc123/scenarios",
  "network": {
    "backend": "bridge",
    "bridge": "katl-vmtest0",
    "cidr": "10.77.0.0/24",
    "gateway": "10.77.0.1",
    "leaseFile": "/tmp/katl-vmtest/20260606T120000Z-abc123/network/leases.json"
  },
  "capabilities": {
    "bridge": "passed",
    "kvm": "passed",
    "ovmf": "passed",
    "qemu": "passed",
    "qemu-img": "passed",
    "vsock": "passed"
  }
}`
	if string(got) != want {
		t.Fatalf("manifest JSON:\n%s", got)
	}
}

func TestDecodeWorldRejectsRequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*World)
		want   string
	}{
		{
			name:   "run id",
			mutate: func(world *World) { world.RunID = "" },
			want:   "runID is required",
		},
		{
			name:   "run dir",
			mutate: func(world *World) { world.RunDir = "" },
			want:   "runDir is required",
		},
		{
			name:   "artifact dir",
			mutate: func(world *World) { world.ArtifactDir = "" },
			want:   "artifactDir is required",
		},
		{
			name:   "scenario dir",
			mutate: func(world *World) { world.ScenarioDir = "" },
			want:   "scenarioDir is required",
		},
		{
			name:   "backend",
			mutate: func(world *World) { world.Network.Backend = "" },
			want:   "network.backend is required",
		},
		{
			name:   "bridge",
			mutate: func(world *World) { world.Network.Bridge = "" },
			want:   "network.bridge is required",
		},
		{
			name:   "cidr",
			mutate: func(world *World) { world.Network.CIDR = "" },
			want:   "network.cidr",
		},
		{
			name:   "gateway",
			mutate: func(world *World) { world.Network.Gateway = "" },
			want:   "network.gateway",
		},
		{
			name:   "lease file",
			mutate: func(world *World) { world.Network.LeaseFile = "" },
			want:   "leaseFile is required",
		},
		{
			name:   "capabilities",
			mutate: func(world *World) { world.Capabilities = nil },
			want:   "capabilities are required",
		},
		{
			name:   "capability name",
			mutate: func(world *World) { world.Capabilities[""] = WorldStatusPassed },
			want:   "capability name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := validWorld()
			tt.mutate(&world)
			err := decodeWorldValue(world)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DecodeWorld() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodeWorldRejectsUnsupportedEnvelope(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*World)
		want   string
	}{
		{
			name:   "api version",
			mutate: func(world *World) { world.APIVersion = "katl.dev/v9" },
			want:   "unsupported apiVersion",
		},
		{
			name:   "kind",
			mutate: func(world *World) { world.Kind = "Other" },
			want:   "unsupported kind",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := validWorld()
			tt.mutate(&world)
			err := decodeWorldValue(world)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DecodeWorld() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodeWorldRejectsInvalidNetwork(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*World)
		want   string
	}{
		{
			name:   "unsupported backend",
			mutate: func(world *World) { world.Network.Backend = "slirp" },
			want:   "unsupported network.backend",
		},
		{
			name:   "invalid cidr",
			mutate: func(world *World) { world.Network.CIDR = "10.77.0.0" },
			want:   "network.cidr",
		},
		{
			name:   "invalid gateway",
			mutate: func(world *World) { world.Network.Gateway = "not-an-ip" },
			want:   "network.gateway",
		},
		{
			name:   "gateway outside cidr",
			mutate: func(world *World) { world.Network.Gateway = "10.78.0.1" },
			want:   "outside network.cidr",
		},
		{
			name:   "invalid bridge",
			mutate: func(world *World) { world.Network.Bridge = "bad/name" },
			want:   "network.bridge",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := validWorld()
			tt.mutate(&world)
			err := decodeWorldValue(world)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DecodeWorld() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodeWorldRejectsRelativePaths(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*World)
		want   string
	}{
		{
			name:   "run dir",
			mutate: func(world *World) { world.RunDir = "build/vmtest/run" },
			want:   "runDir must be an absolute path",
		},
		{
			name:   "artifact dir",
			mutate: func(world *World) { world.ArtifactDir = "artifacts" },
			want:   "artifactDir must be an absolute path",
		},
		{
			name:   "scenario dir",
			mutate: func(world *World) { world.ScenarioDir = "scenarios" },
			want:   "scenarioDir must be an absolute path",
		},
		{
			name:   "lease file",
			mutate: func(world *World) { world.Network.LeaseFile = "network/leases.json" },
			want:   "leaseFile must be an absolute path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			world := validWorld()
			tt.mutate(&world)
			err := decodeWorldValue(world)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("DecodeWorld() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestDecodeWorldCapabilityStatuses(t *testing.T) {
	world := validWorld()
	world.Capabilities = map[string]WorldStatus{
		"qemu":    WorldStatusPassed,
		"fixture": WorldStatusFailed,
		"mkosi":   WorldStatusSetupFailed,
		"kvm":     WorldStatusHostSkipped,
		"suite":   WorldStatusDisabled,
	}
	if err := decodeWorldValue(world); err != nil {
		t.Fatalf("DecodeWorld() error = %v", err)
	}

	world.Capabilities["bad"] = "unknown"
	err := decodeWorldValue(world)
	if err == nil || !strings.Contains(err.Error(), `unsupported capability status "unknown"`) {
		t.Fatalf("DecodeWorld() error = %v, want invalid status", err)
	}
}

func TestDecodeWorldRunIndex(t *testing.T) {
	world := validWorld()
	world.RunIndex = filepath.Join(world.RunDir, "run.json")
	decodedErr := decodeWorldValue(world)
	if decodedErr != nil {
		t.Fatalf("DecodeWorld() error = %v", decodedErr)
	}

	world.RunIndex = "relative/run.json"
	err := decodeWorldValue(world)
	if err == nil || !strings.Contains(err.Error(), "runIndex must be an absolute path") {
		t.Fatalf("DecodeWorld() error = %v, want runIndex path rejection", err)
	}
}

func TestDecodeWorldRejectsUnknownFieldsAndExtraDocuments(t *testing.T) {
	_, err := DecodeWorld(strings.NewReader(`{"apiVersion":"katl.dev/v1alpha1","kind":"VMTestWorld","extra":true}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("DecodeWorld() error = %v, want unknown field", err)
	}

	_, err = DecodeWorld(strings.NewReader(validWorldJSON(t) + "\n{}"))
	if err == nil || !strings.Contains(err.Error(), "exactly one JSON document") {
		t.Fatalf("DecodeWorld() error = %v, want extra document", err)
	}
}

func TestLoadWorldFromEnv(t *testing.T) {
	worldPath := filepath.Join(t.TempDir(), "world.json")
	if err := os.WriteFile(worldPath, []byte(validWorldJSON(t)), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	t.Setenv(WorldManifestEnv, worldPath)

	world, err := LoadWorldFromEnv()
	if err != nil {
		t.Fatalf("LoadWorldFromEnv() error = %v", err)
	}
	if world.RunID != "20260606T120000Z-abc123" {
		t.Fatalf("RunID = %q", world.RunID)
	}
}

func TestRequireWorldReportsRunnerHint(t *testing.T) {
	t.Setenv(WorldManifestEnv, "")
	tb := &fakeTB{}

	_ = RequireWorld(tb)
	if !tb.failed {
		t.Fatal("RequireWorld() did not fail")
	}
	if !strings.Contains(tb.message, WorldManifestEnv) || !strings.Contains(tb.message, "scripts/vmtest-run") {
		t.Fatalf("Fatalf message = %q", tb.message)
	}
}

func decodeWorldValue(world World) error {
	data, err := json.Marshal(world)
	if err != nil {
		return err
	}
	_, err = DecodeWorld(bytes.NewReader(data))
	return err
}

func validWorldJSON(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(validWorld())
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	return string(data)
}

func validWorld() World {
	return World{
		APIVersion:  WorldAPIVersion,
		Kind:        WorldKind,
		RunID:       "20260606T120000Z-abc123",
		RunDir:      "/tmp/katl-vmtest/20260606T120000Z-abc123",
		ArtifactDir: "/tmp/katl-vmtest/20260606T120000Z-abc123/artifacts",
		ScenarioDir: "/tmp/katl-vmtest/20260606T120000Z-abc123/scenarios",
		Network: WorldNetwork{
			Backend:   NetworkBridge,
			Bridge:    "katl-vmtest0",
			CIDR:      "10.77.0.0/24",
			Gateway:   "10.77.0.1",
			LeaseFile: "/tmp/katl-vmtest/20260606T120000Z-abc123/network/leases.json",
		},
		Capabilities: map[string]WorldStatus{
			"bridge":   WorldStatusPassed,
			"kvm":      WorldStatusPassed,
			"ovmf":     WorldStatusPassed,
			"qemu":     WorldStatusPassed,
			"qemu-img": WorldStatusPassed,
			"vsock":    WorldStatusPassed,
		},
	}
}
