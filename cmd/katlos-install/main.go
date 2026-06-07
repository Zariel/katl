package main

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zariel/katl/internal/installer"
	"github.com/zariel/katl/internal/installer/discovery"
	"github.com/zariel/katl/internal/installer/disk"
	"github.com/zariel/katl/internal/installer/handoff"
	"github.com/zariel/katl/internal/installer/katlosimage"
	"github.com/zariel/katl/internal/installer/kubeadmconfig"
	installstatus "github.com/zariel/katl/internal/installer/status"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "katlos-install: %v\n", err)
		os.Exit(1)
	}
}

func runManifest(ctx context.Context, manifestPath, stateDir, inputMode, inputSource string, stdout io.Writer) error {
	if manifestPath == "" {
		return fmt.Errorf("--manifest is required unless --list-states, --version, --apply-input, or --boot is set")
	}
	if stdout == nil {
		stdout = io.Discard
	}
	if strings.TrimSpace(inputSource) == "" {
		inputSource = manifestPath
	}

	install, err := manifestRunnerContext(manifestPath, stateDir, inputMode, inputSource)
	if err != nil {
		return err
	}
	runner := installer.NewRunner(installer.PreseededManifestPlan(), install)

	if err := runner.Run(ctx); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "katlos-install completed manifest=%s\n", manifestPath)
	return nil
}

func manifestRunnerContext(manifestPath, stateDir, inputMode, inputSource string) (*installer.Context, error) {
	mediaRoot, err := manifestMediaRoot(manifestPath)
	if err != nil {
		return nil, err
	}
	kubeadmConfigs, err := loadKubeadmConfigs(mediaRoot)
	if err != nil {
		return nil, err
	}
	commands := installer.NewExecCommandRunner()
	return &installer.Context{
		ManifestPath: manifestPath,
		StateDir:     stateDir,
		TargetRoot:   "/mnt/target",
		Commands:     commands,
		Store:        installer.NewFileStateStore(stateDir),
		KatlosResolver: katlosimage.Resolver{
			MediaRoot: mediaRoot,
			WorkDir:   filepath.Join(stateDir, "katlos-image"),
			Commands:  commands,
		},
		Discovery:      discovery.NewCommandDiscoverySource(commands),
		RootSlotOpener: disk.FileRootSlotDeviceOpener{},
		IdentityRandom: rand.Reader,
		Chown:          os.Chown,
		KubeadmConfigs: kubeadmConfigs,
		InputMode:      inputMode,
		InputSource:    inputSource,
	}, nil
}

func manifestMediaRoot(manifestPath string) (string, error) {
	path, err := filepath.Abs(manifestPath)
	if err != nil {
		return "", fmt.Errorf("resolve manifest path: %w", err)
	}
	return filepath.Dir(path), nil
}

func loadKubeadmConfigs(mediaRoot string) (map[string]kubeadmconfig.Plan, error) {
	objectDir := filepath.Join(mediaRoot, installer.KubeadmConfigObjectsDir)
	entries, err := os.ReadDir(objectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read kubeadm config object dir: %w", err)
	}
	configs := make(map[string]kubeadmconfig.Plan)
	for _, entry := range entries {
		if entry.IsDir() || !isKubeadmConfigObjectFile(entry.Name()) {
			continue
		}
		path := filepath.Join(objectDir, entry.Name())
		file, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("open kubeadm config object %s: %w", path, err)
		}
		object, err := kubeadmconfig.Decode(file)
		closeErr := file.Close()
		if err != nil {
			return nil, fmt.Errorf("decode kubeadm config object %s: %w", path, err)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close kubeadm config object %s: %w", path, closeErr)
		}
		if _, exists := configs[object.Metadata.Name]; exists {
			return nil, fmt.Errorf("duplicate kubeadm config %q", object.Metadata.Name)
		}
		plan, err := kubeadmconfig.Resolve(kubeadmconfig.ResolveRequest{RepoRoot: mediaRoot, Object: object})
		if err != nil {
			return nil, fmt.Errorf("resolve kubeadm config %q: %w", object.Metadata.Name, err)
		}
		configs[object.Metadata.Name] = plan
	}
	return configs, nil
}

func isKubeadmConfigObjectFile(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func runBoot(ctx context.Context, runDir, etcDir, handoffAddr string, stdout io.Writer) error {
	return runBootWithHandoff(ctx, runDir, etcDir, handoffAddr, stdout, runHandoff)
}

func runBootWithHandoff(ctx context.Context, runDir, etcDir, handoffAddr string, stdout io.Writer, handoffRunner func(context.Context, string, string, io.Writer) error) error {
	input, err := bootInput(runDir, etcDir)
	if err != nil {
		return err
	}
	for _, log := range input.Logs {
		fmt.Fprintf(stdout, "katlos-install input: %s\n", log)
	}
	inputMode := bootInputMode(input)
	fmt.Fprintf(stdout, "katlos-install mode: action=%s installMode=%s manifestPath=%s manifestURL=%s inputMode=%s\n", input.Action, input.InstallMode, input.ManifestPath, input.ManifestURL, inputMode)

	switch input.Action {
	case installer.InstallActionHoldForDebug:
		fmt.Fprintln(stdout, "katlos-install debug hold active")
		<-ctx.Done()
		return ctx.Err()
	case installer.InstallActionWaitForConfig:
		return handoffRunner(ctx, runDir, handoffAddr, stdout)
	case installer.InstallActionRun:
		if input.ManifestURL != "" && input.ManifestPath == "" {
			return fmt.Errorf("manifest URL handoff is not implemented yet: %s", input.ManifestURL)
		}
		return runManifest(ctx, input.ManifestPath, filepath.Join(runDir, "state"), inputMode, input.ManifestPath, stdout)
	default:
		return fmt.Errorf("unsupported install action %q", input.Action)
	}
}

func bootInputMode(input installer.BootInput) string {
	switch input.SelectedSources["manifestPath"] {
	case installer.InputSourceRunKatl, installer.InputSourceEtcKatl, installer.InputSourceEmbeddedMedia, installer.InputSourceLocalFile:
		return installstatus.InputModeOfflineMedia
	default:
		return installstatus.InputModePXEPreseed
	}
}

func bootInput(runDir, etcDir string) (installer.BootInput, error) {
	var request installer.BootInputRequest
	request.KernelCmdline = readText("/proc/cmdline")
	addInputFile(&request, installer.InputSourceEtcKatl, filepath.Join(etcDir, "install-input.json"))
	addManifestFile(&request, installer.InputSourceEtcKatl, filepath.Join(etcDir, "install-manifest.json"))
	addInputFile(&request, installer.InputSourceRunKatl, filepath.Join(runDir, "install-input.json"))
	addManifestFile(&request, installer.InputSourceRunKatl, filepath.Join(runDir, "install-manifest.json"))
	return installer.DiscoverBootInput(request)
}

func addInputFile(request *installer.BootInputRequest, source installer.InputSource, path string) {
	data, ok := readFile(path)
	if !ok {
		return
	}
	request.Files = append(request.Files, installer.BootInputFile{
		Source:  source,
		Path:    path,
		Content: data,
	})
}

func addManifestFile(request *installer.BootInputRequest, source installer.InputSource, path string) {
	data, ok := readFile(path)
	if !ok {
		return
	}
	request.Manifest = data
	request.Files = append(request.Files, installer.BootInputFile{
		Source:  source,
		Path:    path + ".input",
		Content: []byte(fmt.Sprintf(`{"manifestPath":%q}`, path)),
	})
}

func readText(path string) string {
	data, ok := readFile(path)
	if !ok {
		return ""
	}
	return string(data)
}

func readFile(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	return data, err == nil
}

func runHandoff(ctx context.Context, runDir, addr string, stdout io.Writer) error {
	server, err := handoff.NewHandoffServer("", nil)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen for handoff: %w", err)
	}
	defer listener.Close()

	httpServer := &http.Server{Handler: server.Handler()}
	errc := make(chan error, 1)
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			errc <- err
			return
		}
		errc <- nil
	}()
	defer httpServer.Shutdown(context.Background())

	baseURL, err := handoffAnnouncementBaseURL(listener.Addr())
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, server.Announcement(baseURL))
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		if len(server.Manifest()) > 0 {
			manifestPath := filepath.Join(runDir, "install-manifest.json")
			if err := os.MkdirAll(runDir, 0o755); err != nil {
				return fmt.Errorf("create handoff dir: %w", err)
			}
			if err := os.WriteFile(manifestPath, server.Manifest(), 0o600); err != nil {
				return fmt.Errorf("write handoff manifest: %w", err)
			}
			if err := materializeHandoffPayloads(manifestPath, runDir, stdout); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "katlos-install handoff accepted manifest=%s\n", manifestPath)
			return runManifest(ctx, manifestPath, filepath.Join(runDir, "state"), installstatus.InputModeLocalHandoff, manifestPath, stdout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errc:
			return err
		case <-ticker.C:
		}
	}
}

func handoffAnnouncementBaseURL(addr net.Addr) (string, error) {
	return handoffAnnouncementBaseURLWithHost(addr, handoffAnnouncementHost)
}

func handoffAnnouncementBaseURLWithHost(addr net.Addr, detectHost func() (string, error)) (string, error) {
	tcpAddr, ok := addr.(*net.TCPAddr)
	if !ok {
		return "", fmt.Errorf("handoff listener has unexpected address: %s", addr)
	}
	host := tcpAddr.IP.String()
	if tcpAddr.IP == nil || tcpAddr.IP.IsUnspecified() {
		detected, err := detectHost()
		if err != nil {
			return "", err
		}
		host = detected
	}
	return "http://" + net.JoinHostPort(host, fmt.Sprintf("%d", tcpAddr.Port)), nil
}

func handoffAnnouncementHost() (string, error) {
	if ip, ok := outboundIP(); ok {
		return ip.String(), nil
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("discover handoff announcement address: %w", err)
	}
	for _, addr := range addrs {
		ip := interfaceIP(addr)
		if handoffAnnouncementIP(ip) {
			return ip.String(), nil
		}
	}
	return "", errors.New("discover handoff announcement address: no non-loopback interface address found")
}

func outboundIP() (net.IP, bool) {
	conn, err := net.Dial("udp", "192.0.2.1:9")
	if err != nil {
		return nil, false
	}
	defer conn.Close()
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || !handoffAnnouncementIP(addr.IP) {
		return nil, false
	}
	return addr.IP, true
}

func interfaceIP(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		return nil
	}
}

func handoffAnnouncementIP(ip net.IP) bool {
	return ip != nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}

func materializeHandoffPayloads(manifestPath, runDir string, stdout io.Writer) error {
	copied, err := installer.CopyManifestPayloads(manifestPath, filepath.Join(runDir, "preseed"), runDir)
	if err != nil {
		return fmt.Errorf("materialize handoff payloads: %w", err)
	}
	for _, payload := range copied {
		fmt.Fprintf(stdout, "katlos-install handoff copied %s to %s\n", payload.Source, payload.Destination)
	}
	return nil
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("katlos-install", flag.ContinueOnError)
	flags.SetOutput(stderr)

	manifestPath := flags.String("manifest", "", "path to install manifest")
	stateDir := flags.String("state-dir", "/var/lib/katl/install", "installer state directory")
	listStates := flags.Bool("list-states", false, "print the installer state order and exit")
	showVersion := flags.Bool("version", false, "print build metadata and exit")
	applyInput := flags.Bool("apply-input", false, "copy preseeded installer input and exit")
	boot := flags.Bool("boot", false, "run installer boot entrypoint")
	preseedDir := flags.String("preseed-dir", "", "additional installer preseed directory")
	seedWait := flags.Duration("seed-wait", 15*time.Second, "time to wait for installer seed devices")
	runDir := flags.String("run-dir", "/run/katl", "runtime installer input directory")
	etcDir := flags.String("etc-dir", "/etc/katl", "persistent installer input directory")
	handoffAddr := flags.String("handoff-addr", "0.0.0.0:8080", "installer handoff listen address")

	if err := flags.Parse(args); err != nil {
		return err
	}

	if *showVersion {
		fmt.Fprintf(stdout, "katlos-install version=%s commit=%s date=%s\n", version, commit, date)
		return nil
	}

	if *applyInput {
		preseedDirs := installer.DefaultPreseedDirs()
		if strings.TrimSpace(*preseedDir) != "" {
			preseedDirs = append([]string{strings.TrimSpace(*preseedDir)}, preseedDirs...)
		}
		return installer.ApplyInput(installer.InputApplyRequest{
			Context:     ctx,
			PreseedDirs: preseedDirs,
			SeedDevices: installer.DefaultSeedDevices,
			SeedMount:   installer.DefaultSeedMount,
			SeedWait:    *seedWait,
			Commands:    installer.NewExecCommandRunner(),
			RunDir:      *runDir,
			EtcDir:      *etcDir,
			Stdout:      stdout,
		})
	}

	if *boot {
		return runBoot(ctx, *runDir, *etcDir, *handoffAddr, stdout)
	}

	plan := installer.DefaultPlan()
	if *listStates {
		for _, id := range plan.IDs() {
			fmt.Fprintln(stdout, id)
		}
		return nil
	}

	return runManifest(ctx, strings.TrimSpace(*manifestPath), *stateDir, installstatus.InputModePXEPreseed, strings.TrimSpace(*manifestPath), stdout)
}
