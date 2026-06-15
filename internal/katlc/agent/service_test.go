package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseListenAcceptsTCPOnly(t *testing.T) {
	network, address, err := parseListen("tcp://0.0.0.0:9443")
	if err != nil {
		t.Fatal(err)
	}
	if network != "tcp" || address != "0.0.0.0:9443" {
		t.Fatalf("parseListen = %q %q, want tcp 0.0.0.0:9443", network, address)
	}

	for _, value := range []string{"unix:///run/katlc.sock", "localhost:9443", "tcp://"} {
		t.Run(value, func(t *testing.T) {
			if _, _, err := parseListen(value); err == nil {
				t.Fatalf("parseListen(%q) succeeded, want error", value)
			}
		})
	}
}

func TestInitTokenCreatesAndPreservesSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent", "token")
	if err := InitToken(path); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(first)) == "" {
		t.Fatal("token is empty")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("token mode = %v, want 0600", info.Mode().Perm())
	}

	if err := InitToken(path); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("InitToken replaced an existing token")
	}
}

func TestInitTokenRejectsRelativePath(t *testing.T) {
	if err := InitToken("var/lib/katl/agent/token"); err == nil {
		t.Fatal("InitToken accepted a relative path")
	}
}
