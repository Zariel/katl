package confext

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

type NativeEtcFileType string

const (
	NativeEtcRegularFile NativeEtcFileType = "regular"
	NativeEtcSymlink     NativeEtcFileType = "symlink"
	NativeEtcCharDevice  NativeEtcFileType = "char-device"
	NativeEtcBlockDevice NativeEtcFileType = "block-device"
)

type NativeEtcFile struct {
	Path string
	Type NativeEtcFileType
	Mode fs.FileMode
	UID  int
	GID  int
}

type NativeEtcFilePlan struct {
	Path        string
	ConfextPath string
	Mode        fs.FileMode
	UID         int
	GID         int
}

func ValidateNativeEtcBundle(confextRoot string, files []NativeEtcFile) ([]NativeEtcFilePlan, error) {
	cleanRoot := ""
	if strings.TrimSpace(confextRoot) != "" {
		if !filepath.IsAbs(confextRoot) {
			return nil, fmt.Errorf("confext root must be absolute")
		}
		cleanRoot = filepath.Clean(confextRoot)
	}

	seen := make(map[string]struct{}, len(files))
	plans := make([]NativeEtcFilePlan, 0, len(files))
	for _, file := range files {
		normalizedPath, err := normalizeNativeEtcPath(file.Path)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[normalizedPath]; ok {
			return nil, fmt.Errorf("duplicate /etc file path %q", normalizedPath)
		}
		seen[normalizedPath] = struct{}{}

		fileType := file.Type
		if fileType == "" {
			fileType = NativeEtcRegularFile
		}
		if fileType != NativeEtcRegularFile {
			return nil, fmt.Errorf("%s entries are not allowed in generated confext input: %q", fileType, file.Path)
		}

		mode := file.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := validateNativeEtcMode(file.Path, mode); err != nil {
			return nil, err
		}
		if file.UID != 0 || file.GID != 0 {
			return nil, fmt.Errorf("native /etc file %q must be owned by root:root", file.Path)
		}

		confextPath := ""
		if cleanRoot != "" {
			confextPath = filepath.Join(cleanRoot, strings.TrimPrefix(normalizedPath, "/"))
			if !pathWithinRoot(cleanRoot, confextPath) {
				return nil, fmt.Errorf("native /etc file %q would write outside confext root", file.Path)
			}
		}

		plans = append(plans, NativeEtcFilePlan{
			Path:        normalizedPath,
			ConfextPath: confextPath,
			Mode:        mode.Perm(),
			UID:         file.UID,
			GID:         file.GID,
		})
	}

	return plans, nil
}

func NativeEtcFilesFromManifest(files map[string]string) []NativeEtcFile {
	nativeFiles := make([]NativeEtcFile, 0, len(files))
	for path := range files {
		nativeFiles = append(nativeFiles, NativeEtcFile{
			Path: path,
			Mode: 0o644,
			UID:  0,
			GID:  0,
		})
	}
	return nativeFiles
}

func normalizeNativeEtcPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("native /etc file path is required")
	}
	if strings.ContainsRune(path, 0) {
		return "", fmt.Errorf("native /etc file path %q contains a NUL byte", path)
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("native /etc file path %q must be absolute", path)
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == ".." {
			return "", fmt.Errorf("native /etc file path %q contains path traversal", path)
		}
	}

	normalized := filepath.Clean(path)
	if normalized == "/etc" || !strings.HasPrefix(normalized, "/etc/") {
		return "", fmt.Errorf("native /etc file path %q must be under /etc", path)
	}
	if normalized == "/etc/kubernetes" || strings.HasPrefix(normalized, "/etc/kubernetes/") {
		return "", fmt.Errorf("native /etc file path %q cannot own kubeadm-managed /etc/kubernetes state", path)
	}

	return normalized, nil
}

func validateNativeEtcMode(path string, mode fs.FileMode) error {
	if mode.Type() != 0 {
		return fmt.Errorf("native /etc file %q must be a regular file", path)
	}
	if mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
		return fmt.Errorf("native /etc file %q cannot set special permission bits", path)
	}

	perm := mode.Perm()
	if perm&0o022 != 0 {
		return fmt.Errorf("native /etc file %q cannot be group- or world-writable", path)
	}
	if perm&0o111 != 0 {
		return fmt.Errorf("native /etc file %q cannot be executable", path)
	}
	if perm > 0o644 {
		return fmt.Errorf("native /etc file %q mode %04o is too permissive", path, perm)
	}
	return nil
}

func pathWithinRoot(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, "../") && !filepath.IsAbs(rel)
}
