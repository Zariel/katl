package generation

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	KubernetesSource = "/var/lib/katl/kubernetes/etc-kubernetes"
	KubernetesTarget = "/etc/kubernetes"
)

type StateRequest struct {
	PartitionUUID string
}

type KubernetesProjectionRequest struct {
	Source string
	Target string
}

type StateAssets struct {
	VarMount           string
	EtcKubernetesMount string
	StateCheckService  string
	Tmpfiles           string
	Dirs               []StateDir
	MountPoints        []StateDir
}

type StateDir struct {
	Path string
	Mode os.FileMode
}

func RenderState(request StateRequest) (StateAssets, error) {
	uuid, err := stateUUID(request.PartitionUUID)
	if err != nil {
		return StateAssets{}, err
	}
	kubernetesMount, err := RenderKubernetesProjection(KubernetesProjectionRequest{})
	if err != nil {
		return StateAssets{}, err
	}
	dirs := stateDirs()
	assets := StateAssets{
		VarMount: strings.Join([]string{
			"[Unit]",
			"Description=Katl writable state partition",
			"Documentation=man:systemd.mount(5)",
			"DefaultDependencies=no",
			"Before=local-fs.target",
			"Conflicts=umount.target",
			"Before=umount.target",
			"",
			"[Mount]",
			"What=PARTUUID=" + uuid,
			"Where=/var",
			"Type=auto",
			"Options=rw",
			"",
			"[Install]",
			"WantedBy=local-fs.target",
			"",
		}, "\n"),
		EtcKubernetesMount: kubernetesMount,
		StateCheckService:  renderStateCheckService(),
		Tmpfiles:           renderTmpfiles(dirs),
		Dirs:               dirs,
		MountPoints:        []StateDir{{Path: KubernetesTarget, Mode: 0o755}},
	}
	return assets, nil
}

func RenderKubernetesProjection(request KubernetesProjectionRequest) (string, error) {
	source, target, err := kubernetesProjectionPaths(request)
	if err != nil {
		return "", err
	}
	return strings.Join([]string{
		"[Unit]",
		"Description=Project persistent Kubernetes configuration",
		"Documentation=man:systemd.mount(5)",
		"After=var.mount systemd-confext.service",
		"Before=kubelet.service katl-kubeadm-ready.target",
		"RequiresMountsFor=" + source,
		"",
		"[Mount]",
		"What=" + source,
		"Where=" + target,
		"Type=none",
		"Options=bind,rw",
		"",
		"[Install]",
		"WantedBy=local-fs.target",
		"",
	}, "\n"), nil
}

func renderStateCheckService() string {
	return strings.Join([]string{
		"[Unit]",
		"Description=Check Katl writable state projections",
		"Requires=var.mount etc-kubernetes.mount",
		"After=var.mount etc-kubernetes.mount",
		"Before=katl-kubeadm-ready.target",
		"",
		"[Service]",
		"Type=oneshot",
		"StandardOutput=journal+console",
		"SyslogIdentifier=katl-state-projection",
		"ExecStart=/usr/bin/printf 'Katl state projection ready\\n'",
		"",
		"[Install]",
		"WantedBy=multi-user.target",
		"",
	}, "\n")
}

func WriteState(root string, request StateRequest) (StateAssets, error) {
	if strings.TrimSpace(root) == "" {
		return StateAssets{}, fmt.Errorf("target root is required")
	}
	assets, err := RenderState(request)
	if err != nil {
		return StateAssets{}, err
	}
	if err := writeFile(root, "etc/systemd/system/var.mount", assets.VarMount, 0o644); err != nil {
		return StateAssets{}, err
	}
	if err := writeFile(root, "etc/systemd/system/etc-kubernetes.mount", assets.EtcKubernetesMount, 0o644); err != nil {
		return StateAssets{}, err
	}
	if err := writeFile(root, "etc/systemd/system/katl-state-projection-check.service", assets.StateCheckService, 0o644); err != nil {
		return StateAssets{}, err
	}
	if err := writeFile(root, "etc/tmpfiles.d/katl-state.conf", assets.Tmpfiles, 0o644); err != nil {
		return StateAssets{}, err
	}
	for _, dir := range append(append([]StateDir{}, assets.Dirs...), assets.MountPoints...) {
		path := filepath.Join(root, strings.TrimPrefix(dir.Path, "/"))
		if err := os.MkdirAll(path, dir.Mode); err != nil {
			return StateAssets{}, fmt.Errorf("create %s: %w", dir.Path, err)
		}
		if err := os.Chmod(path, dir.Mode); err != nil {
			return StateAssets{}, fmt.Errorf("chmod %s: %w", dir.Path, err)
		}
	}
	return assets, nil
}

func stateDirs() []StateDir {
	return []StateDir{
		{Path: "/var/lib/katl", Mode: 0o755},
		{Path: "/var/lib/katl/generations", Mode: 0o755},
		{Path: "/var/lib/katl/install", Mode: 0o755},
		{Path: "/var/lib/katl/install/logs", Mode: 0o755},
		{Path: "/var/lib/katl/identity", Mode: 0o755},
		{Path: "/var/lib/katl/kubernetes", Mode: 0o755},
		{Path: KubernetesSource, Mode: 0o755},
		{Path: "/var/lib/katl/ssh", Mode: 0o755},
		{Path: "/var/lib/katl/ssh/host-keys", Mode: 0o700},
		{Path: "/var/lib/containerd", Mode: 0o755},
		{Path: "/var/lib/kubelet", Mode: 0o755},
		{Path: "/var/log/journal", Mode: 0o2755},
	}
}

func renderTmpfiles(dirs []StateDir) string {
	lines := make([]string, 0, len(dirs)+1)
	lines = append(lines, "# Katl writable state seed directories")
	for _, dir := range dirs {
		group := "root"
		if dir.Path == "/var/log/journal" {
			group = "systemd-journal"
		}
		lines = append(lines, fmt.Sprintf("d %s %04o root %s -", dir.Path, dir.Mode, group))
	}
	return strings.Join(append(lines, ""), "\n")
}

func stateUUID(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("state partition UUID is required")
	}
	if strings.ContainsAny(value, " \t\n\r") {
		return "", fmt.Errorf("state partition UUID must not contain whitespace")
	}
	return value, nil
}

func kubernetesProjectionPaths(request KubernetesProjectionRequest) (string, string, error) {
	source := cleanProjectionPath(request.Source, KubernetesSource)
	target := cleanProjectionPath(request.Target, KubernetesTarget)
	if source != KubernetesSource {
		return "", "", fmt.Errorf("kubernetes source must be %s", KubernetesSource)
	}
	if target != KubernetesTarget {
		return "", "", fmt.Errorf("kubernetes target must be %s", KubernetesTarget)
	}
	return source, target, nil
}

func cleanProjectionPath(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = fallback
	}
	return path.Clean("/" + strings.TrimPrefix(value, "/"))
}

func writeFile(root string, rel string, content string, mode os.FileMode) error {
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create parent for %s: %w", rel, err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return fmt.Errorf("write %s: %w", rel, err)
	}
	return nil
}
