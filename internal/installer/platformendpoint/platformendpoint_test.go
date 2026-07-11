package platformendpoint

import (
	"strings"
	"testing"

	"github.com/katl-dev/katl/internal/installer/bgpapivip"
	"github.com/katl-dev/katl/internal/installer/confext"
)

func TestComposeExternalEndpoint(t *testing.T) {
	plan, err := Compose(Config{
		Mode:     ModeExternal,
		Endpoint: Endpoint{Host: "api.katl.test"},
	})
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	if plan.ControlPlaneEndpoint != "api.katl.test:6443" || plan.StableEndpoint != "api.katl.test:6443" {
		t.Fatalf("endpoint plan = %#v", plan)
	}
	if !plan.StableEndpointBeforeManifests {
		t.Fatalf("external endpoint should be usable for pre-manifest bootstrap readiness")
	}
	if plan.HelperStatus != nil || len(NativeEtcFiles(plan)) != 0 {
		t.Fatalf("external endpoint rendered helper state: %#v", plan)
	}
}

func TestComposeHostAdvertisedBGP(t *testing.T) {
	plan, err := Compose(Config{
		Mode:           ModeHostAdvertisedBGP,
		BGPAPIEndpoint: ptr(minimalBGPConfig()),
	})
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	if plan.ControlPlaneEndpoint != "api.home.example:6443" || plan.StableEndpoint != "api.home.example:6443" {
		t.Fatalf("endpoint plan = %#v", plan)
	}
	if plan.HelperStatus == nil || plan.HelperStatus.AppID != bgpapivip.AppID || plan.HelperStatus.LiveStatusPath != bgpapivip.LiveStatusPath {
		t.Fatalf("helper status = %#v", plan.HelperStatus)
	}
	if plan.BGPAPIEndpoint == nil || plan.BGPAPIEndpoint.Config.Endpoint.Provenance != "platform-host" {
		t.Fatalf("bgp endpoint plan = %#v", plan.BGPAPIEndpoint)
	}
	files := NativeEtcFiles(plan)
	assertNativeFile(t, files, bgpapivip.ConfigPath, "kind: BGPAPIEndpoint\n")
	assertNativeFile(t, files, bgpapivip.BirdConfigPath, "router id 10.0.0.11;\n")
	if len(plan.NativeEtcFiles) == 0 || plan.NativeEtcFiles[0].Path == "" {
		t.Fatalf("native file summary = %#v", plan.NativeEtcFiles)
	}
}

func TestComposeRejectsCiliumAndMismatch(t *testing.T) {
	tests := []struct {
		name   string
		config Config
		want   string
	}{
		{
			name:   "cilium mode",
			config: Config{Mode: ModeCilium, Endpoint: Endpoint{Host: "api.katl.test"}},
			want:   "post-Cilium",
		},
		{
			name: "host advertised cilium provenance",
			config: Config{
				Mode:           ModeHostAdvertisedBGP,
				Endpoint:       Endpoint{Host: "api.home.example", Provenance: "cilium"},
				BGPAPIEndpoint: ptr(minimalBGPConfig()),
			},
			want: "post-Cilium",
		},
		{
			name: "external provenance mismatch",
			config: Config{
				Mode:     ModeExternal,
				Endpoint: Endpoint{Host: "api.katl.test", Provenance: "platform-host"},
			},
			want: "provenance must be external",
		},
		{
			name: "selected endpoint mismatch",
			config: Config{
				Mode:           ModeHostAdvertisedBGP,
				Endpoint:       Endpoint{Host: "other.katl.test"},
				BGPAPIEndpoint: ptr(minimalBGPConfig()),
			},
			want: "does not match",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Compose(tt.config)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Compose() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func minimalBGPConfig() bgpapivip.Config {
	return bgpapivip.Config{
		Endpoint: bgpapivip.Endpoint{
			Host: "api.home.example",
			VIP:  "10.40.0.10/32",
		},
		VIPInterface: bgpapivip.VIPInterface{
			Kind: "dummy",
			Name: "katl-api0",
		},
		Routing: bgpapivip.Routing{
			RouterID:        "10.0.0.11",
			LocalASN:        64512,
			SourceAddress:   "10.0.0.11",
			SourceInterface: "enp1s0",
		},
		FabricPeers: []bgpapivip.Peer{{
			Name:                  "router-a",
			Address:               "10.0.0.1",
			ASN:                   64500,
			AllowedExportPrefixes: []string{"10.40.0.10/32"},
		}},
	}
}

func assertNativeFile(t *testing.T, files []confext.NativeEtcFile, path string, content string) {
	t.Helper()
	for _, file := range files {
		if file.Path == path {
			if !strings.Contains(file.Content, content) {
				t.Fatalf("%s did not contain %q:\n%s", path, content, file.Content)
			}
			return
		}
	}
	t.Fatalf("missing native file %s in %#v", path, files)
}

func ptr(config bgpapivip.Config) *bgpapivip.Config {
	return &config
}
