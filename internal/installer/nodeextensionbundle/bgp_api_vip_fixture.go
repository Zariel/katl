package nodeextensionbundle

import (
	"fmt"
	"strings"

	"github.com/katl-dev/katl/internal/installer/bgpapivip"
)

const (
	BGPAPIVIPAppID          = bgpapivip.AppID
	BGPAPIVIPPayloadVersion = "bgp-api-vip-v0.1.0-katl.1"
)

type BGPAPIVIPFixtureRequest struct {
	OutputDir         string
	PayloadVersion    string
	ArtifactVersion   string
	Architecture      string
	RuntimeInterfaces []string
	CreatedAt         string
}

func WriteBGPAPIVIPFixture(request BGPAPIVIPFixtureRequest) (Fixture, error) {
	payloadVersion := strings.TrimSpace(request.PayloadVersion)
	if payloadVersion == "" {
		payloadVersion = BGPAPIVIPPayloadVersion
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
		AppID:             BGPAPIVIPAppID,
		PayloadVersion:    payloadVersion,
		ArtifactVersion:   artifactVersion,
		Architecture:      architecture,
		Payload:           bgpAPIVIPFixturePayload(payloadVersion, runtimeInterfaces[0]),
		CreatedAt:         createdAt,
		RuntimeInterfaces: runtimeInterfaces,
		DisplayName:       "BGP API VIP endpoint extension",
		Description:       "Host-advertised Kubernetes API VIP extension fixture for pre-Cilium bootstrap reachability.",
		Capabilities: []Capability{{
			Name:            "dev.katl.api-endpoint.bgp-vip",
			Version:         "v1alpha1",
			ConfigSchemaIDs: []string{"dev.katl.api-endpoint.bgp-vip.config.v1alpha1", "dev.katl.routing.bird.generated.v1alpha1"},
			OperationKinds:  []string{"bgp-api-vip-validate", "bgp-api-vip-status", "bgp-api-vip-withdraw", "bgp-api-vip-advertise"},
		}},
		Compatibility: &Compatibility{
			SupportedRuntimeInterfaces: runtimeInterfaces,
			RequiredKernelModules:      []string{"dummy"},
			RequiredUnits:              []string{"systemd-sysext.service", "systemd-confext.service", "systemd-networkd.service", "katl-app-bird.service"},
			RequiredMounts:             []string{},
			RequiredCapabilities:       []string{"CAP_NET_ADMIN", "CAP_NET_BIND_SERVICE"},
			ActivationPhases:           []string{"pre-kubeadm", "maintenance"},
		},
		Systemd: &Systemd{
			ExtensionID:          "katl-node-extension-bgp-api-vip",
			ExtensionVersion:     payloadVersion,
			SysextLevel:          runtimeInterfaces[0],
			ProvidedUnits:        []string{"katl-app-bgp-api-vip.service", "katl-app-bgp-api-vip-ready.service", "katl-app-bgp-api-vip-status.service"},
			EntrypointUnits:      []string{"katl-app-bgp-api-vip.service"},
			ReadinessUnits:       []string{"katl-app-bgp-api-vip-ready.service"},
			OrderingRequirements: []string{"after=katl-app-bird.service", "after=systemd-networkd.service", "before=kubeadm"},
		},
		Configuration: &Configuration{
			ConfigHandoffPaths: []string{
				bgpapivip.ConfigPath,
			},
			GeneratedDropInPaths: []string{
				bgpapivip.AppDropInPath,
			},
			SupportedConfigSchemaIDs: []string{"dev.katl.api-endpoint.bgp-vip.config.v1alpha1", "dev.katl.routing.bird.generated.v1alpha1"},
			SecretRefKinds:           []string{"bgp-peer-auth", "kube-apiserver-ca"},
		},
		Status: &Status{
			LiveStatusPath:      bgpapivip.LiveStatusPath,
			StatusSchemaID:      "dev.katl.api-endpoint.bgp-vip.status.v1alpha1",
			DurableSnapshotPath: bgpapivip.OperationStatus,
			RedactionVersion:    "inventory-v1",
			HealthStates:        []string{"unknown", "healthy", "unhealthy", "advertised", "withdrawn", "failed"},
		},
		Rollback: &Rollback{
			FailClosedActions:         []string{"withdraw-api-vip", "stop-katl-app-bgp-api-vip"},
			LiveRollbackSupported:     false,
			RequiresRebootForRollback: true,
			ExternalStateWarning:      "The app withdraws only the host-owned API VIP; external fabric state remains operator-owned.",
		},
	})
}

func bgpAPIVIPFixturePayload(payloadVersion, runtimeInterface string) []byte {
	return []byte(fmt.Sprintf(`KATL_NODE_EXTENSION_FIXTURE=bgp-api-vip
PAYLOAD_VERSION=%s
RUNTIME_INTERFACE=%s
PAYLOAD_CONTENTS:
  /usr/lib/systemd/system/katl-app-bgp-api-vip.service
  /usr/lib/systemd/system/katl-app-bgp-api-vip-ready.service
  /usr/lib/systemd/system/katl-app-bgp-api-vip-status.service
  /usr/lib/extension-release.d/extension-release.katl-node-extension-bgp-api-vip
  /usr/lib/tmpfiles.d/katl-app-bgp-api-vip.conf
  /usr/lib/katl/apps/bgp-api-vip/controller
  /usr/lib/katl/apps/bgp-api-vip/status
  /etc/katl/apps/bgp-api-vip/.keep
UNIT_CHECKS:
  systemd-analyze verify katl-app-bgp-api-vip.service katl-app-bgp-api-vip-ready.service katl-app-bgp-api-vip-status.service
STATUS_SCHEMA:
  dev.katl.api-endpoint.bgp-vip.status.v1alpha1
`, payloadVersion, runtimeInterface))
}
