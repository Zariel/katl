package nodeextensionbundle

import (
	"fmt"
	"strings"
)

const (
	BirdAppID          = "bird"
	BirdPayloadVersion = "bird-v2.17.1-katl.1"
)

type BirdFixtureRequest struct {
	OutputDir         string
	PayloadVersion    string
	ArtifactVersion   string
	Architecture      string
	RuntimeInterfaces []string
	CreatedAt         string
	BirdVersion       string
}

func WriteBirdFixture(request BirdFixtureRequest) (Fixture, error) {
	payloadVersion := strings.TrimSpace(request.PayloadVersion)
	if payloadVersion == "" {
		payloadVersion = BirdPayloadVersion
	}
	birdVersion := strings.TrimSpace(request.BirdVersion)
	if birdVersion == "" {
		birdVersion = "2.17.1"
	}
	runtimeInterfaces := append([]string(nil), request.RuntimeInterfaces...)
	if len(runtimeInterfaces) == 0 {
		runtimeInterfaces = []string{"katl-runtime-1"}
	}
	artifactVersion := strings.TrimSpace(request.ArtifactVersion)
	if artifactVersion == "" {
		artifactVersion = payloadVersion + "-fixture.1"
	}
	createdAt := strings.TrimSpace(request.CreatedAt)
	if createdAt == "" {
		createdAt = "2026-06-19T00:00:00Z"
	}
	architecture := strings.TrimSpace(request.Architecture)
	if architecture == "" {
		architecture = "x86_64"
	}

	return WriteFixture(FixtureRequest{
		OutputDir:         request.OutputDir,
		AppID:             BirdAppID,
		PayloadVersion:    payloadVersion,
		ArtifactVersion:   artifactVersion,
		Architecture:      architecture,
		Payload:           birdFixturePayload(payloadVersion, birdVersion, runtimeInterfaces[0]),
		CreatedAt:         createdAt,
		RuntimeInterfaces: runtimeInterfaces,
		DisplayName:       "Generic BIRD routing extension",
		Description:       "Generic BIRD daemon node extension fixture for Katl routing apps.",
		Capabilities: []Capability{{
			Name:            "dev.katl.routing.bird",
			Version:         "v1alpha1",
			ConfigSchemaIDs: []string{"dev.katl.routing.bird.generated.v1alpha1"},
			OperationKinds:  []string{"bird-config-validate"},
		}},
		Compatibility: &Compatibility{
			SupportedRuntimeInterfaces: runtimeInterfaces,
			RequiredKernelModules:      []string{},
			RequiredUnits:              []string{"systemd-sysext.service", "systemd-confext.service", "systemd-networkd.service"},
			RequiredMounts:             []string{},
			RequiredCapabilities:       []string{"CAP_NET_ADMIN", "CAP_NET_BIND_SERVICE"},
			ActivationPhases:           []string{"pre-kubeadm", "maintenance"},
		},
		Systemd: &Systemd{
			ExtensionID:          "katl-node-extension-bird",
			ExtensionVersion:     payloadVersion,
			SysextLevel:          runtimeInterfaces[0],
			ProvidedUnits:        []string{"katl-app-bird.target", "katl-app-bird.service", "katl-app-bird-ready.service", "katl-app-bird-status.service"},
			EntrypointUnits:      []string{"katl-app-bird.target", "katl-app-bird.service"},
			ReadinessUnits:       []string{"katl-app-bird-ready.service"},
			OrderingRequirements: []string{"after=systemd-sysext.service", "after=systemd-confext.service", "after=network-online.target"},
		},
		Configuration: &Configuration{
			ConfigHandoffPaths:       []string{"/etc/katl/apps/bird/config.yaml", "/etc/katl/apps/bird/bird.conf"},
			GeneratedDropInPaths:     []string{"/etc/systemd/system/katl-app-bird.service.d/10-katl-config.conf"},
			SupportedConfigSchemaIDs: []string{"dev.katl.routing.bird.generated.v1alpha1"},
			SecretRefKinds:           []string{"bgp-peer-auth"},
		},
		Status: &Status{
			LiveStatusPath:      "/run/katl/apps/bird/status.json",
			StatusSchemaID:      "dev.katl.routing.bird.status.v1alpha1",
			DurableSnapshotPath: "/var/lib/katl/operations/<operation-id>/apps/bird/status.json",
			RedactionVersion:    "inventory-v1",
			HealthStates:        []string{"unknown", "ready", "not-ready", "failed"},
		},
		Rollback: &Rollback{
			FailClosedActions:         []string{"withdraw-owned-routes", "stop-katl-app-bird"},
			LiveRollbackSupported:     false,
			RequiresRebootForRollback: true,
			ExternalStateWarning:      "BIRD may have advertised routes before rollback; consuming apps own withdrawal proof.",
		},
	})
}

func birdFixturePayload(payloadVersion, birdVersion, runtimeInterface string) []byte {
	return []byte(fmt.Sprintf(`KATL_NODE_EXTENSION_FIXTURE=bird
PAYLOAD_VERSION=%s
BIRD_VERSION=%s
RUNTIME_INTERFACE=%s
PAYLOAD_CONTENTS:
  /usr/sbin/bird
  /usr/sbin/birdc
  /usr/lib/systemd/system/katl-app-bird.target
  /usr/lib/systemd/system/katl-app-bird.service
  /usr/lib/systemd/system/katl-app-bird-ready.service
  /usr/lib/systemd/system/katl-app-bird-status.service
  /usr/lib/extension-release.d/extension-release.katl-node-extension-bird
  /usr/lib/tmpfiles.d/katl-app-bird.conf
  /usr/lib/katl/apps/bird/status
  /etc/katl/apps/bird/.keep
UNIT_CHECKS:
  systemd-analyze verify katl-app-bird.service katl-app-bird-ready.service katl-app-bird-status.service
PACKAGE_CHECKS:
  bird --version == %s
  birdc exists
`, payloadVersion, birdVersion, runtimeInterface, birdVersion))
}
