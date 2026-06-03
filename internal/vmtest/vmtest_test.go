package vmtest

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalize(t *testing.T) {
	options := normalizeOptions(Options{})
	if options.StateRoot != filepath.Join("build", "vmtest") {
		t.Fatalf("StateRoot = %q", options.StateRoot)
	}
	if options.Keep != KeepFailed {
		t.Fatalf("Keep = %q", options.Keep)
	}
	if options.KVM != KVMAuto {
		t.Fatalf("KVM = %q", options.KVM)
	}
	if options.Missing != MissingFails {
		t.Fatalf("Missing = %q", options.Missing)
	}

	scenario := normalizeScenario(Scenario{Name: "boot"}, Options{
		StateRoot: "/tmp/state",
		Keep:      KeepAlways,
		KVM:       KVMOff,
	})
	if scenario.StateRoot != "/tmp/state" || scenario.Keep != KeepAlways || scenario.KVM != KVMOff {
		t.Fatalf("scenario not normalized: %#v", scenario)
	}
	if scenario.Host.KVM != KVMOff {
		t.Fatalf("Host.KVM = %q", scenario.Host.KVM)
	}
}

func TestOptIn(t *testing.T) {
	tests := []struct {
		name    string
		options Options
		want    Status
		skip    bool
		fail    bool
	}{
		{
			name: "disabled",
			options: Options{
				Enabled: false,
				RunID:   "run-1",
			},
			want: StatusSkipped,
			skip: true,
		},
		{
			name: "fail",
			options: Options{
				Enabled: true,
				RunID:   "run-1",
				Missing: MissingFails,
			},
			want: StatusFailed,
			fail: true,
		},
		{
			name: "skip",
			options: Options{
				Enabled: true,
				RunID:   "run-1",
				Missing: MissingSkips,
			},
			want: StatusSkipped,
			skip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tb := &fakeTB{}
			runner := Runner{
				Options: tt.options,
				probe: probe{
					lookPath: func(string) (string, error) {
						return "", errors.New("missing")
					},
				},
			}
			result := runner.Run(tb, Scenario{
				Name: "boot",
				Host: HostRequirements{QEMU: true},
			})
			if result.Status != tt.want {
				t.Fatalf("Status = %q", result.Status)
			}
			if tb.skipped != tt.skip || tb.failed != tt.fail {
				t.Fatalf("skipped=%v failed=%v", tb.skipped, tb.failed)
			}
		})
	}
}

func TestHostCheck(t *testing.T) {
	err := checkHost(HostRequirements{
		QEMU: true,
		OVMF: true,
		KVM:  KVMOn,
	}, probe{
		lookPath: func(name string) (string, error) {
			if name == "qemu-system-x86_64" {
				return "/usr/bin/" + name, nil
			}
			return "", fmt.Errorf("%s missing", name)
		},
		stat: func(path string) (fs.FileInfo, error) {
			if path == "/ovmf/code.fd" {
				return nil, nil
			}
			return nil, os.ErrNotExist
		},
		env: func(name string) string {
			if name == "KATL_OVMF_CODE" {
				return "/ovmf/code.fd"
			}
			return ""
		},
		access: func(string) error {
			return os.ErrPermission
		},
	})
	if err == nil {
		t.Fatal("CheckHost succeeded")
	}
	var prereq PrereqError
	if !errors.As(err, &prereq) {
		t.Fatalf("error type = %T", err)
	}
	text := err.Error()
	for _, want := range []string{"OVMF vars", "/dev/kvm", "KATL_OVMF_VARS"} {
		if !strings.Contains(text, want) {
			t.Fatalf("error %q missing %q", text, want)
		}
	}
	if len(prereq.Missing) != 2 {
		t.Fatalf("missing = %#v", prereq.Missing)
	}
}

func TestPlanPaths(t *testing.T) {
	result, err := NewRunner(Options{
		StateRoot: "/tmp/katl-vmtest",
		Keep:      KeepAlways,
		KVM:       KVMOff,
	}).Plan(Scenario{
		Name:  "first install",
		RunID: "run-1",
	})
	if err != nil {
		t.Fatalf("Plan() error = %v", err)
	}
	if result.RunDir != "/tmp/katl-vmtest/run-1" {
		t.Fatalf("RunDir = %q", result.RunDir)
	}
	if result.QEMUDir != "/tmp/katl-vmtest/run-1/qemu" {
		t.Fatalf("QEMUDir = %q", result.QEMUDir)
	}
	if result.DiskDir != "/tmp/katl-vmtest/run-1/disks" {
		t.Fatalf("DiskDir = %q", result.DiskDir)
	}
	if result.ManifestDir != "/tmp/katl-vmtest/run-1/manifests" {
		t.Fatalf("ManifestDir = %q", result.ManifestDir)
	}
	if result.Keep != KeepAlways || result.KVM != KVMOff {
		t.Fatalf("result policy = %#v", result)
	}
}

type fakeTB struct {
	skipped bool
	failed  bool
}

func (t *fakeTB) Helper() {}

func (t *fakeTB) Skipf(string, ...any) {
	t.skipped = true
}

func (t *fakeTB) Fatalf(string, ...any) {
	t.failed = true
}
