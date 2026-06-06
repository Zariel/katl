package installer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type InputApplyRequest struct {
	Context     context.Context
	PreseedDirs []string
	SeedDevices []string
	SeedMount   string
	SeedWait    time.Duration
	Commands    CommandRunner
	RunDir      string
	EtcDir      string
	Stdout      io.Writer
}

const (
	DefaultSeedMount = "/run/katl/preseed"
)

var DefaultSeedDevices = []string{
	"/dev/disk/by-label/KATLSEED",
	"/dev/disk/by-id/virtio-katl-seed",
}

func DefaultPreseedDirs() []string {
	return []string{
		"/usr/lib/katl/preseed",
		"/run/katl/preseed",
		"/etc/katl/preseed",
	}
}

func ApplyInput(request InputApplyRequest) error {
	ctx := request.Context
	if ctx == nil {
		ctx = context.Background()
	}
	runDir := request.RunDir
	if runDir == "" {
		runDir = "/run/katl"
	}
	etcDir := request.EtcDir
	if etcDir == "" {
		etcDir = "/etc/katl"
	}
	stdout := request.Stdout
	if stdout == nil {
		stdout = io.Discard
	}
	if err := mountSeedDevice(ctx, request, stdout); err != nil {
		return err
	}

	applied := 0
	for _, dir := range request.PreseedDirs {
		n, err := applyDir(dir, runDir, etcDir, stdout)
		if err != nil {
			return err
		}
		applied += n
	}
	if applied == 0 {
		fmt.Fprintln(stdout, "katl input: no preseed files found")
	}
	return nil
}

func mountSeedDevice(ctx context.Context, request InputApplyRequest, stdout io.Writer) error {
	devices := request.SeedDevices
	if len(devices) == 0 {
		return nil
	}
	device, err := waitSeedDevice(ctx, devices, request.SeedWait)
	if err != nil {
		return err
	}
	if device == "" {
		writeMissingSeedDevice(stdout, devices, request.SeedWait)
		return nil
	}
	mountPoint := request.SeedMount
	if mountPoint == "" {
		mountPoint = DefaultSeedMount
	}
	commands := request.Commands
	if commands == nil {
		commands = NewExecCommandRunner()
	}
	if err := os.MkdirAll(mountPoint, 0o755); err != nil {
		return fmt.Errorf("create seed mount %s: %w", mountPoint, err)
	}
	if err := commands.Run(ctx, "mount", "-o", "ro", device, mountPoint); err != nil {
		return fmt.Errorf("mount seed device %s: %w", device, err)
	}
	fmt.Fprintf(stdout, "katl input: mounted seed device %s at %s\n", device, mountPoint)
	return nil
}

func writeMissingSeedDevice(stdout io.Writer, devices []string, wait time.Duration) {
	checked := compactDeviceList(devices)
	if len(checked) == 0 {
		return
	}
	fmt.Fprintf(stdout, "katl input: seed device not found after %s; checked %s\n", wait, joinComma(checked))
	for _, dir := range seedDeviceParents(checked) {
		exists := true
		if _, err := os.Stat(dir); err != nil {
			exists = false
		}
		fmt.Fprintf(stdout, "katl input: seed device directory %s exists=%t\n", dir, exists)
	}
}

func compactDeviceList(devices []string) []string {
	out := make([]string, 0, len(devices))
	for _, device := range devices {
		if device == "" {
			continue
		}
		out = append(out, device)
	}
	return out
}

func seedDeviceParents(devices []string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, device := range devices {
		dir := filepath.Dir(device)
		if dir == "." || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	sort.Strings(out)
	return out
}

func joinComma(values []string) string {
	out := ""
	for i, value := range values {
		if i > 0 {
			out += ", "
		}
		out += value
	}
	return out
}

func waitSeedDevice(ctx context.Context, devices []string, wait time.Duration) (string, error) {
	deadline := time.Now().Add(wait)
	for {
		for _, candidate := range devices {
			if candidate == "" {
				continue
			}
			if _, err := os.Stat(candidate); err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return "", fmt.Errorf("stat seed device %s: %w", candidate, err)
			}
			return candidate, nil
		}
		if wait <= 0 || !time.Now().Before(deadline) {
			return "", nil
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
	}
}

func applyDir(dir, runDir, etcDir string, stdout io.Writer) (int, error) {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("stat preseed dir %s: %w", dir, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("preseed path %s is not a directory", dir)
	}

	applied := 0
	for _, item := range preseedItems(dir, runDir, etcDir) {
		ok, err := copyInput(item.src, item.dst)
		if err != nil {
			return applied, err
		}
		if ok {
			applied++
			fmt.Fprintf(stdout, "katl input: copied %s to %s\n", item.src, item.dst)
		}
	}
	return applied, nil
}

type preseedItem struct {
	src string
	dst string
}

func preseedItems(dir, runDir, etcDir string) []preseedItem {
	return []preseedItem{
		{filepath.Join(dir, "install-input.json"), filepath.Join(runDir, "install-input.json")},
		{filepath.Join(dir, "install-manifest.json"), filepath.Join(runDir, "install-manifest.json")},
		{filepath.Join(dir, "run/katl/install-input.json"), filepath.Join(runDir, "install-input.json")},
		{filepath.Join(dir, "run/katl/install-manifest.json"), filepath.Join(runDir, "install-manifest.json")},
		{filepath.Join(dir, "etc/katl/install-input.json"), filepath.Join(etcDir, "install-input.json")},
		{filepath.Join(dir, "etc/katl/install-manifest.json"), filepath.Join(etcDir, "install-manifest.json")},
	}
}

func copyInput(src, dst string) (bool, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read preseed file %s: %w", src, err)
	}
	if !json.Valid(data) {
		return false, fmt.Errorf("preseed file %s is not valid JSON", src)
	}
	if _, err := os.Stat(dst); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, fmt.Errorf("stat destination %s: %w", dst, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return false, fmt.Errorf("create destination dir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		return false, fmt.Errorf("write destination %s: %w", dst, err)
	}
	return true, nil
}
