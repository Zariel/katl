package platformendpoint

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/katl-dev/katl/internal/installer/bgpapivip"
	"github.com/katl-dev/katl/internal/installer/confext"
)

const (
	ModeExternal          = "external"
	ModeHostAdvertisedBGP = "hostAdvertisedBGP"
	ModeCilium            = "cilium"
)

type Config struct {
	Mode           string            `yaml:"mode" json:"mode"`
	Endpoint       Endpoint          `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	BGPAPIEndpoint *bgpapivip.Config `yaml:"bgpAPIEndpoint,omitempty" json:"bgpAPIEndpoint,omitempty"`
}

type Endpoint struct {
	Host       string `yaml:"host" json:"host"`
	Port       int    `yaml:"port,omitempty" json:"port,omitempty"`
	Provenance string `yaml:"provenance,omitempty" json:"provenance,omitempty"`
}

type Plan struct {
	Mode                          string       `json:"mode"`
	ControlPlaneEndpoint          string       `json:"controlPlaneEndpoint"`
	StableEndpoint                string       `json:"stableEndpoint,omitempty"`
	StableEndpointBeforeManifests bool         `json:"stableEndpointBeforeManifests,omitempty"`
	HelperStatus                  *Status      `json:"helperStatus,omitempty"`
	BGPAPIEndpoint                *BGPAPIPlan  `json:"bgpAPIEndpoint,omitempty"`
	NativeEtcFiles                []NativeFile `json:"nativeEtcFiles,omitempty"`
	files                         []confext.NativeEtcFile
}

type Status struct {
	AppID               string `json:"appID"`
	APIVersion          string `json:"apiVersion"`
	Kind                string `json:"kind"`
	LiveStatusPath      string `json:"liveStatusPath"`
	OperationStatusPath string `json:"operationStatusPath"`
}

type BGPAPIPlan struct {
	Config bgpapivip.Config `json:"config"`
}

type NativeFile struct {
	Path string `json:"path"`
}

func Compose(config Config) (Plan, error) {
	mode := strings.TrimSpace(config.Mode)
	switch mode {
	case ModeExternal:
		return composeExternal(config)
	case ModeHostAdvertisedBGP:
		return composeHostAdvertisedBGP(config)
	case ModeCilium:
		return Plan{}, fmt.Errorf("platformAPIEndpoint.mode cilium is post-Cilium state and cannot satisfy pre-Cilium bootstrap readiness")
	case "":
		return Plan{}, fmt.Errorf("platformAPIEndpoint.mode is required")
	default:
		return Plan{}, fmt.Errorf("platformAPIEndpoint.mode %q is unsupported", config.Mode)
	}
}

func composeExternal(config Config) (Plan, error) {
	if config.BGPAPIEndpoint != nil {
		return Plan{}, fmt.Errorf("platformAPIEndpoint.external must not set bgpAPIEndpoint")
	}
	endpoint, err := normalizeExternalEndpoint(config.Endpoint)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		Mode:                          ModeExternal,
		ControlPlaneEndpoint:          endpoint,
		StableEndpoint:                endpoint,
		StableEndpointBeforeManifests: true,
	}, nil
}

func composeHostAdvertisedBGP(config Config) (Plan, error) {
	if config.BGPAPIEndpoint == nil {
		return Plan{}, fmt.Errorf("platformAPIEndpoint.hostAdvertisedBGP requires bgpAPIEndpoint")
	}
	if strings.EqualFold(strings.TrimSpace(config.Endpoint.Provenance), ModeCilium) {
		return Plan{}, fmt.Errorf("platformAPIEndpoint.endpoint.provenance cilium is post-Cilium state and cannot satisfy pre-Cilium bootstrap readiness")
	}
	bgpPlan, err := bgpapivip.RenderNativeEtcFiles(bgpapivip.RenderRequest{
		Config:   *config.BGPAPIEndpoint,
		NodeRole: "control-plane",
	})
	if err != nil {
		return Plan{}, fmt.Errorf("platformAPIEndpoint.bgpAPIEndpoint: %w", err)
	}
	endpoint := endpointString(bgpPlan.Config.Endpoint.Host, bgpPlan.Config.Endpoint.Port)
	if err := validateEndpointSelection(config.Endpoint, endpoint); err != nil {
		return Plan{}, err
	}
	files := make([]NativeFile, 0, len(bgpPlan.Files))
	for _, file := range bgpPlan.Files {
		files = append(files, NativeFile{Path: file.Path})
	}
	return Plan{
		Mode:                          ModeHostAdvertisedBGP,
		ControlPlaneEndpoint:          endpoint,
		StableEndpoint:                endpoint,
		StableEndpointBeforeManifests: true,
		HelperStatus: &Status{
			AppID:               bgpapivip.AppID,
			APIVersion:          bgpapivip.StatusAPIVersion,
			Kind:                bgpapivip.StatusKind,
			LiveStatusPath:      bgpapivip.LiveStatusPath,
			OperationStatusPath: bgpapivip.OperationStatus,
		},
		BGPAPIEndpoint: &BGPAPIPlan{Config: bgpPlan.Config},
		NativeEtcFiles: files,
		files:          bgpPlan.NativeEtcFiles(),
	}, nil
}

func NativeEtcFiles(plan Plan) []confext.NativeEtcFile {
	files := make([]confext.NativeEtcFile, len(plan.files))
	copy(files, plan.files)
	return files
}

func normalizeExternalEndpoint(endpoint Endpoint) (string, error) {
	endpoint.Host = strings.TrimSpace(endpoint.Host)
	endpoint.Provenance = strings.TrimSpace(endpoint.Provenance)
	if endpoint.Provenance == "" {
		endpoint.Provenance = ModeExternal
	}
	if endpoint.Provenance != ModeExternal {
		return "", fmt.Errorf("platformAPIEndpoint.external endpoint.provenance must be external")
	}
	return normalizeEndpoint(endpoint.Host, endpoint.Port)
}

func validateEndpointSelection(endpoint Endpoint, selected string) error {
	host := strings.TrimSpace(endpoint.Host)
	provenance := strings.TrimSpace(endpoint.Provenance)
	if provenance != "" && provenance != "platform-host" {
		return fmt.Errorf("platformAPIEndpoint.hostAdvertisedBGP endpoint.provenance must be platform-host")
	}
	if host == "" && endpoint.Port == 0 {
		return nil
	}
	candidate, err := normalizeEndpoint(host, endpoint.Port)
	if err != nil {
		return err
	}
	if candidate != selected {
		return fmt.Errorf("platformAPIEndpoint.endpoint %q does not match bgpAPIEndpoint selected endpoint %q", candidate, selected)
	}
	return nil
}

func normalizeEndpoint(host string, port int) (string, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return "", fmt.Errorf("platformAPIEndpoint.endpoint.host is required")
	}
	if strings.Contains(host, "://") || strings.ContainsAny(host, `/\`) {
		return "", fmt.Errorf("platformAPIEndpoint.endpoint.host must be a host name or address, not a URL or path")
	}
	if port == 0 {
		port = 6443
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("platformAPIEndpoint.endpoint.port must be between 1 and 65535")
	}
	return endpointString(host, port), nil
}

func endpointString(host string, port int) string {
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port))
}
