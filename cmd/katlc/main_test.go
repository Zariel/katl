package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := run(t.Context(), []string{"--help"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage: katlc") || !strings.Contains(stdout.String(), "agent serve") {
		t.Fatalf("help output = %q", stdout.String())
	}
	if strings.Contains(stdout.String(), "operation run-tool") || strings.Contains(stdout.String(), "operation execute") {
		t.Fatalf("help output exposes legacy operation CLI: %q", stdout.String())
	}
}

func TestPublicOperationCommandIsRemoved(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(t.Context(), []string{"operation", "run-tool"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), `unsupported command "operation run-tool"`) {
		t.Fatalf("run() error = %v, want unsupported operation command", err)
	}
}
