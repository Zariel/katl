package vmtest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type VMBoot struct {
	UKI           string
	Image         string
	ImageFormat   DiskFormat
	ImageSnapshot bool
}

type VMConfig struct {
	Boot         VMBoot
	QEMUPath     string
	OVMFCode     string
	OVMFVars     string
	KVM          KVMPolicy
	RAMMiB       int
	CPUs         int
	Phase        string
	Timeout      time.Duration
	HostForwards []HostForward
}

type HostForward struct {
	HostPort  int
	GuestPort int
}

type VMPlan struct {
	QEMUPath       string
	Args           []string
	Accel          string
	SerialLog      string
	CommandFile    string
	OVMFVars       string
	OVMFVarsSource string
	EFITree        string
}

type VMExecutor interface {
	Run(ctx context.Context, name string, args []string, serial io.Writer) error
}

type ExecVMExecutor struct{}

func (ExecVMExecutor) Run(ctx context.Context, name string, args []string, serial io.Writer) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = serial
	cmd.Stderr = serial
	return cmd.Run()
}

type VMRunner struct {
	Executor VMExecutor
	probe    probe
}

func PlanVM(result Result, config VMConfig) (VMPlan, error) {
	return planVM(result, config, systemProbe())
}

func RunVM(ctx context.Context, result Result, config VMConfig) Result {
	return VMRunner{Executor: ExecVMExecutor{}, probe: systemProbe()}.Run(ctx, result, config)
}

func (r VMRunner) Run(ctx context.Context, result Result, config VMConfig) Result {
	started := time.Now().UTC()
	plan, err := planVM(result, config, r.probe)
	if err != nil {
		return finishVM(result, phaseName(config), StatusFailed, err.Error(), started, time.Now().UTC())
	}
	if err := prepareVM(plan, config); err != nil {
		return finishVM(result, phaseName(config), StatusFailed, err.Error(), started, time.Now().UTC())
	}
	if config.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, config.Timeout)
		defer cancel()
	}
	file, err := os.OpenFile(plan.SerialLog, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return finishVM(result, phaseName(config), StatusFailed, err.Error(), started, time.Now().UTC())
	}
	defer file.Close()
	executor := r.Executor
	if executor == nil {
		executor = ExecVMExecutor{}
	}
	if err := executor.Run(ctx, plan.QEMUPath, plan.Args, file); err != nil {
		summary := fmt.Sprintf("qemu exited: %v", err)
		if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			summary = "qemu timed out"
		}
		return finishVM(result, phaseName(config), StatusFailed, summary, started, time.Now().UTC())
	}
	return finishVM(result, phaseName(config), StatusPassed, "", started, time.Now().UTC())
}

func planVM(result Result, config VMConfig, probe probe) (VMPlan, error) {
	probe = probe.withDefaults()
	config = normalizeVM(config)
	qemu := config.QEMUPath
	if qemu == "" {
		found, err := probe.lookPath("qemu-system-x86_64")
		if err != nil {
			return VMPlan{}, PrereqError{Missing: []MissingPrerequisite{{
				Name:   "qemu-system-x86_64",
				Detail: "not found in PATH",
			}}}
		}
		qemu = found
	}
	if config.OVMFCode == "" {
		config.OVMFCode = probe.env("KATL_OVMF_CODE")
	}
	if config.OVMFVars == "" {
		config.OVMFVars = probe.env("KATL_OVMF_VARS")
	}
	if config.OVMFCode == "" || config.OVMFVars == "" {
		return VMPlan{}, errors.New("OVMF firmware is required: set OVMFCode/OVMFVars or KATL_OVMF_CODE/KATL_OVMF_VARS")
	}
	if _, err := probe.stat(config.OVMFCode); err != nil {
		return VMPlan{}, fmt.Errorf("OVMF code not readable: %w", err)
	}
	if _, err := probe.stat(config.OVMFVars); err != nil {
		return VMPlan{}, fmt.Errorf("OVMF vars not readable: %w", err)
	}
	accel, err := qemuAccel(config.KVM, probe)
	if err != nil {
		return VMPlan{}, err
	}
	serial := result.Artifacts.InstallerSerial
	if config.Phase == "runtime" {
		serial = result.Artifacts.RuntimeSerial
	}
	args := []string{
		"-machine", "q35,accel=" + accel,
		"-cpu", "max",
		"-smp", strconv.Itoa(config.CPUs),
		"-m", strconv.Itoa(config.RAMMiB),
		"-display", "none",
		"-monitor", "none",
		"-serial", "file:" + serial,
		"-drive", "if=pflash,format=raw,readonly=on,file=" + config.OVMFCode,
		"-drive", "if=pflash,format=raw,file=" + filepath.Join(result.QEMUDir, "OVMF_VARS.fd"),
	}
	driveArgs, efiTree, err := vmDrives(result, config.Boot)
	if err != nil {
		return VMPlan{}, err
	}
	args = append(args, driveArgs...)
	args = append(args,
		"-device", "virtio-rng-pci",
		"-netdev", netdev(config.HostForwards),
		"-device", "virtio-net-pci,netdev=net0",
	)
	return VMPlan{
		QEMUPath:       qemu,
		Args:           args,
		Accel:          accel,
		SerialLog:      serial,
		CommandFile:    result.Artifacts.QEMUCommand,
		OVMFVars:       filepath.Join(result.QEMUDir, "OVMF_VARS.fd"),
		OVMFVarsSource: config.OVMFVars,
		EFITree:        efiTree,
	}, nil
}

func prepareVM(plan VMPlan, config VMConfig) error {
	if err := os.MkdirAll(filepath.Dir(plan.SerialLog), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plan.CommandFile), 0o755); err != nil {
		return err
	}
	if err := copyFile(plan.OVMFVarsSource, plan.OVMFVars, 0o600); err != nil {
		return err
	}
	if config.Boot.UKI != "" {
		bootPath := filepath.Join(plan.EFITree, "EFI", "BOOT", "BOOTX64.EFI")
		if err := copyFile(config.Boot.UKI, bootPath, 0o644); err != nil {
			return err
		}
	}
	return os.WriteFile(plan.CommandFile, []byte(commandLine(plan.QEMUPath, plan.Args)+"\n"), 0o644)
}

func normalizeVM(config VMConfig) VMConfig {
	if config.KVM == "" {
		config.KVM = KVMAuto
	}
	if config.RAMMiB == 0 {
		config.RAMMiB = 2048
	}
	if config.CPUs == 0 {
		config.CPUs = 2
	}
	if config.Boot.ImageFormat == "" {
		config.Boot.ImageFormat = DiskRaw
	}
	return config
}

func qemuAccel(policy KVMPolicy, probe probe) (string, error) {
	switch policy {
	case KVMOff:
		return "tcg", nil
	case KVMOn:
		if err := probe.access("/dev/kvm"); err != nil {
			return "", fmt.Errorf("/dev/kvm required by KVM policy on: %w", err)
		}
		return "kvm", nil
	default:
		if err := probe.access("/dev/kvm"); err == nil {
			return "kvm", nil
		}
		return "tcg", nil
	}
}

func vmDrives(result Result, boot VMBoot) ([]string, string, error) {
	var args []string
	index := 0
	add := func(spec string) {
		args = append(args, "-drive", fmt.Sprintf("if=virtio,index=%d,%s", index, spec))
		index++
	}
	efiTree := filepath.Join(result.QEMUDir, "efi")
	if boot.UKI != "" {
		add("format=raw,file=fat:rw:" + efiTree)
		if boot.Image != "" {
			add(imageSpec(boot))
		}
	} else {
		if boot.Image == "" {
			return nil, "", errors.New("VM boot requires UKI or disk image")
		}
		add(imageSpec(boot))
	}
	for _, disk := range result.Disks {
		add(fmt.Sprintf("format=%s,file=%s,serial=katl-%s", disk.Format, disk.HostPath, clean(disk.Name)))
	}
	return args, efiTree, nil
}

func imageSpec(boot VMBoot) string {
	spec := fmt.Sprintf("format=%s,file=%s", boot.ImageFormat, boot.Image)
	if boot.ImageSnapshot {
		spec += ",snapshot=on"
	}
	return spec
}

func netdev(forwards []HostForward) string {
	spec := "user,id=net0"
	for _, forward := range forwards {
		spec += fmt.Sprintf(",hostfwd=tcp:127.0.0.1:%d-:%d", forward.HostPort, forward.GuestPort)
	}
	return spec
}

func finishVM(result Result, phase string, status Status, failure string, started, finished time.Time) Result {
	result.Status = status
	result.FailureSummary = failure
	if result.Started.IsZero() {
		result.Started = started
	}
	result.Finished = finished
	result.DurationMS = finished.Sub(result.Started).Milliseconds()
	result.addPhase(phase, status, failure, started, finished)
	return result
}

func phaseName(config VMConfig) string {
	if config.Phase != "" {
		return config.Phase
	}
	return "qemu"
}

func copyFile(src, dst string, mode os.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
}

func commandLine(name string, args []string) string {
	parts := append([]string{name}, args...)
	quoted := make([]string, 0, len(parts))
	for _, part := range parts {
		quoted = append(quoted, quoteArg(part))
	}
	return strings.Join(quoted, " ")
}

func quoteArg(arg string) string {
	if arg == "" {
		return "''"
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return r == '\'' || r == ' ' || r == '\t' || r == '\n'
	}) == -1 {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
}
