package vmtest

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type KVMPolicy string

const (
	KVMAuto KVMPolicy = "auto"
	KVMOn   KVMPolicy = "on"
	KVMOff  KVMPolicy = "off"
)

type KeepPolicy string

const (
	KeepNever  KeepPolicy = "never"
	KeepFailed KeepPolicy = "failed"
	KeepAlways KeepPolicy = "always"
)

type MissingPolicy string

const (
	MissingFails MissingPolicy = "fail"
	MissingSkips MissingPolicy = "skip"
)

type Scenario struct {
	Name      string
	RunID     string
	StateRoot string
	Keep      KeepPolicy
	KVM       KVMPolicy
	Host      HostRequirements
}

type HostRequirements struct {
	QEMU     bool
	QEMUImg  bool
	OVMF     bool
	KVM      KVMPolicy
	OVMFCode string
	OVMFVars string
}

type Options struct {
	Enabled   bool
	StateRoot string
	Keep      KeepPolicy
	KVM       KVMPolicy
	Missing   MissingPolicy
	RunID     string
}

type Runner struct {
	Options Options
	probe   probe
}

type Result struct {
	ScenarioName string
	Status       Status
	RunID        string
	RunDir       string
	QEMUDir      string
	DiskDir      string
	ManifestDir  string
	Keep         KeepPolicy
	KVM          KVMPolicy
	Missing      []MissingPrerequisite
}

type Status string

const (
	StatusPlanned Status = "planned"
	StatusSkipped Status = "skipped"
	StatusFailed  Status = "failed"
)

type MissingPrerequisite struct {
	Name   string
	Detail string
}

type PrereqError struct {
	Missing []MissingPrerequisite
}

func (e PrereqError) Error() string {
	if len(e.Missing) == 0 {
		return "host prerequisites missing"
	}
	parts := make([]string, 0, len(e.Missing))
	for _, missing := range e.Missing {
		if missing.Detail == "" {
			parts = append(parts, missing.Name)
			continue
		}
		parts = append(parts, missing.Name+": "+missing.Detail)
	}
	return "host prerequisites missing: " + strings.Join(parts, "; ")
}

type testTB interface {
	Helper()
	Skipf(format string, args ...any)
	Fatalf(format string, args ...any)
}

var (
	runFlag       = flag.Bool("katl.vmtest.run", false, "run Katl VM scenarios")
	stateRootFlag = flag.String("katl.vmtest.state-root", "", "Katl VM scenario state root")
	keepFlag      = flag.String("katl.vmtest.keep", "", "Katl VM artifact keep policy: never, failed, or always")
	kvmFlag       = flag.String("katl.vmtest.kvm", "", "Katl VM KVM policy: auto, on, or off")
)

func DefaultOptions() Options {
	return Options{
		Enabled:   *runFlag || envBool("KATL_VMTEST_RUN"),
		StateRoot: first(*stateRootFlag, os.Getenv("KATL_VMTEST_STATE_ROOT")),
		Keep:      KeepPolicy(first(*keepFlag, os.Getenv("KATL_VMTEST_KEEP"))),
		KVM:       KVMPolicy(first(*kvmFlag, os.Getenv("KATL_VMTEST_KVM"))),
		Missing:   MissingFails,
	}
}

func NewRunner(options Options) Runner {
	return Runner{Options: options, probe: systemProbe()}
}

func Run(t testing.TB, scenario Scenario) Result {
	return NewRunner(DefaultOptions()).Run(t, scenario)
}

func RequireHost(t testing.TB, requirements HostRequirements) {
	NewRunner(DefaultOptions()).RequireHost(t, requirements)
}

func CheckHost(requirements HostRequirements) error {
	return checkHost(requirements, systemProbe())
}

func (r Runner) Run(t testTB, scenario Scenario) Result {
	t.Helper()
	result, err := r.Plan(scenario)
	if err != nil {
		t.Fatalf("vmtest plan failed: %v", err)
		return result
	}
	if !r.options().Enabled {
		result.Status = StatusSkipped
		t.Skipf("set -katl.vmtest.run or KATL_VMTEST_RUN=1 to run VM scenario %q", scenario.Name)
		return result
	}
	if err := r.check(scenario.Host); err != nil {
		result.Status = StatusFailed
		var prereq PrereqError
		if errors.As(err, &prereq) {
			result.Missing = prereq.Missing
		}
		if r.options().Missing == MissingSkips {
			result.Status = StatusSkipped
			t.Skipf("%v", err)
			return result
		}
		t.Fatalf("%v", err)
		return result
	}
	return result
}

func (r Runner) RequireHost(t testTB, requirements HostRequirements) {
	t.Helper()
	if !r.options().Enabled {
		t.Skipf("set -katl.vmtest.run or KATL_VMTEST_RUN=1 to run VM host checks")
		return
	}
	if err := r.check(requirements); err != nil {
		if r.options().Missing == MissingSkips {
			t.Skipf("%v", err)
			return
		}
		t.Fatalf("%v", err)
	}
}

func (r Runner) Plan(scenario Scenario) (Result, error) {
	options := r.options()
	scenario = normalizeScenario(scenario, options)
	if scenario.Name == "" {
		return Result{}, errors.New("scenario name is required")
	}
	runID := first(scenario.RunID, options.RunID)
	if runID == "" {
		runID = fmt.Sprintf("%s-%d", clean(scenario.Name), time.Now().UTC().Unix())
	}
	runDir := filepath.Join(scenario.StateRoot, runID)
	return Result{
		ScenarioName: scenario.Name,
		Status:       StatusPlanned,
		RunID:        runID,
		RunDir:       runDir,
		QEMUDir:      filepath.Join(runDir, "qemu"),
		DiskDir:      filepath.Join(runDir, "disks"),
		ManifestDir:  filepath.Join(runDir, "manifests"),
		Keep:         scenario.Keep,
		KVM:          scenario.KVM,
	}, nil
}

func (r Runner) check(requirements HostRequirements) error {
	return checkHost(requirements, r.probe.withDefaults())
}

func (r Runner) options() Options {
	return normalizeOptions(r.Options)
}

func normalizeOptions(options Options) Options {
	if options.StateRoot == "" {
		options.StateRoot = filepath.Join("build", "vmtest")
	}
	if options.Keep == "" {
		options.Keep = KeepFailed
	}
	if options.KVM == "" {
		options.KVM = KVMAuto
	}
	if options.Missing == "" {
		options.Missing = MissingFails
	}
	return options
}

func normalizeScenario(scenario Scenario, options Options) Scenario {
	if scenario.StateRoot == "" {
		scenario.StateRoot = options.StateRoot
	}
	if scenario.Keep == "" {
		scenario.Keep = options.Keep
	}
	if scenario.KVM == "" {
		scenario.KVM = options.KVM
	}
	if scenario.Host.KVM == "" {
		scenario.Host.KVM = scenario.KVM
	}
	return scenario
}

func checkHost(requirements HostRequirements, probe probe) error {
	probe = probe.withDefaults()
	var missing []MissingPrerequisite
	if requirements.QEMU {
		missing = appendCommand(missing, probe, "qemu-system-x86_64")
	}
	if requirements.QEMUImg {
		missing = appendCommand(missing, probe, "qemu-img")
	}
	if requirements.OVMF {
		code := first(requirements.OVMFCode, probe.env("KATL_OVMF_CODE"))
		vars := first(requirements.OVMFVars, probe.env("KATL_OVMF_VARS"))
		missing = appendFile(missing, probe, "OVMF code", code, "set KATL_OVMF_CODE or Scenario.Host.OVMFCode")
		missing = appendFile(missing, probe, "OVMF vars", vars, "set KATL_OVMF_VARS or Scenario.Host.OVMFVars")
	}
	if requirements.KVM == KVMOn {
		if err := probe.access("/dev/kvm"); err != nil {
			missing = append(missing, MissingPrerequisite{
				Name:   "/dev/kvm",
				Detail: "required by KVM policy on: " + err.Error(),
			})
		}
	}
	if len(missing) > 0 {
		return PrereqError{Missing: missing}
	}
	return nil
}

func appendCommand(missing []MissingPrerequisite, probe probe, name string) []MissingPrerequisite {
	if _, err := probe.lookPath(name); err != nil {
		return append(missing, MissingPrerequisite{Name: name, Detail: "not found in PATH"})
	}
	return missing
}

func appendFile(missing []MissingPrerequisite, probe probe, name, path, hint string) []MissingPrerequisite {
	if path == "" {
		return append(missing, MissingPrerequisite{Name: name, Detail: hint})
	}
	if _, err := probe.stat(path); err != nil {
		return append(missing, MissingPrerequisite{Name: name, Detail: path + ": " + err.Error()})
	}
	return missing
}

type probe struct {
	lookPath func(string) (string, error)
	stat     func(string) (fs.FileInfo, error)
	access   func(string) error
	env      func(string) string
}

func systemProbe() probe {
	return probe{
		lookPath: exec.LookPath,
		stat:     os.Stat,
		access: func(path string) error {
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			return file.Close()
		},
		env: os.Getenv,
	}
}

func (p probe) withDefaults() probe {
	if p.lookPath == nil {
		p.lookPath = exec.LookPath
	}
	if p.stat == nil {
		p.stat = os.Stat
	}
	if p.access == nil {
		p.access = func(path string) error {
			file, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				return err
			}
			return file.Close()
		}
	}
	if p.env == nil {
		p.env = os.Getenv
	}
	return p
}

func envBool(name string) bool {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return false
	}
	parsed, err := strconv.ParseBool(value)
	return err == nil && parsed
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func clean(name string) string {
	name = strings.ToLower(name)
	var b strings.Builder
	lastDash := false
	for _, r := range name {
		ok := r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
		if ok {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
