package configdomain

import (
	"fmt"
	"path/filepath"

	"github.com/zariel/katl/internal/installer/confext"
	"github.com/zariel/katl/internal/installer/kubeadmconfig"
	"github.com/zariel/katl/internal/installer/manifest"
)

type RenderRequest struct {
	Manifest       manifest.Manifest
	KubeadmConfigs map[string]kubeadmconfig.Plan
}

func NativeEtcFiles(request RenderRequest) ([]confext.NativeEtcFile, error) {
	files := networkdFiles(request.Manifest.Node.Networkd)
	ref := request.Manifest.Node.Kubernetes.Kubeadm.ConfigRef
	if ref != "" {
		config, ok := request.KubeadmConfigs[ref]
		if !ok {
			return nil, fmt.Errorf("node.kubernetes.kubeadm.configRef %q was not resolved", ref)
		}
		if config.Name != ref {
			return nil, fmt.Errorf("node.kubernetes.kubeadm.configRef %q resolved to KubeadmConfig %q", ref, config.Name)
		}
		files = append(files, config.NativeEtcFiles()...)
	}
	plans, err := confext.ValidateNativeEtcBundle("", files)
	if err != nil {
		return nil, err
	}

	contentByPath := make(map[string]string, len(files))
	for _, file := range files {
		contentByPath[filepath.Clean(file.Path)] = file.Content
	}
	normalizedFiles := make([]confext.NativeEtcFile, 0, len(plans))
	for _, plan := range plans {
		normalizedFiles = append(normalizedFiles, confext.NativeEtcFile{
			Path:    plan.Path,
			Content: contentByPath[plan.Path],
			Mode:    plan.Mode,
			UID:     plan.UID,
			GID:     plan.GID,
		})
	}
	return normalizedFiles, nil
}

func networkdFiles(config manifest.NetworkdConfig) []confext.NativeEtcFile {
	files := make([]confext.NativeEtcFile, 0, len(config.Files))
	for _, file := range config.Files {
		files = append(files, confext.NativeEtcFile{
			Path:    filepath.Join("/etc/systemd/network", file.Name),
			Content: file.Content,
			Mode:    0o644,
			UID:     0,
			GID:     0,
		})
	}
	return files
}
