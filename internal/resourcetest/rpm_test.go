package resourcetest

import (
	"strings"
	"testing"
)

func TestParseRPMPackages(t *testing.T) {
	packages, err := ParseRPMPackages(strings.NewReader("systemd\t0:259.6-1.fc44.x86_64\nbash\t0:5.3.9-3.fc44.x86_64\n"))
	if err != nil {
		t.Fatalf("ParseRPMPackages() error = %v", err)
	}
	if len(packages) != 2 || packages[0].Name != "bash" || packages[1].Name != "systemd" {
		t.Fatalf("packages = %#v", packages)
	}
}

func TestParseRPMPackagesRejectsInvalidLine(t *testing.T) {
	_, err := ParseRPMPackages(strings.NewReader("systemd 0:259.6-1.fc44.x86_64\n"))
	if err == nil || !strings.Contains(err.Error(), "tab") {
		t.Fatalf("ParseRPMPackages() error = %v, want tab rejection", err)
	}
}
