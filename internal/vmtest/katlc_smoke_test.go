package vmtest

import (
	"context"
	"strings"
	"testing"

	vmtestpb "github.com/katl-dev/katl/internal/vmtest/proto"
)

func TestRunKatlcSmoke(t *testing.T) {
	result := guestResult(t)
	client := newScriptedGuestClient()
	client.commandResults = map[string][]*vmtestpb.CommandResult{
		commandKey("test", "-x", "/usr/bin/katlc"): {okCommand()},
		commandKey("/usr/bin/katlc", "--help"):     {stdoutCommand("Usage: katlc <command> [args]\nagent serve\n")},
	}
	guest := NewGuestControl(result, client)

	if err := RunKatlcSmoke(context.Background(), guest); err != nil {
		t.Fatalf("RunKatlcSmoke() error = %v", err)
	}
	if client.commandCount(commandKey("/usr/bin/katlc", "--help")) != 1 {
		t.Fatalf("katlc --help command was not recorded: %#v", client.commandRequests)
	}
}

func TestRunKatlcSmokeRejectsUnexpectedHelp(t *testing.T) {
	result := guestResult(t)
	client := newScriptedGuestClient()
	client.commandResults = map[string][]*vmtestpb.CommandResult{
		commandKey("test", "-x", "/usr/bin/katlc"): {okCommand()},
		commandKey("/usr/bin/katlc", "--help"):     {stdoutCommand("other command\n")},
	}
	guest := NewGuestControl(result, client)

	err := RunKatlcSmoke(context.Background(), guest)
	if err == nil || !strings.Contains(err.Error(), "expected command summary") {
		t.Fatalf("RunKatlcSmoke() error = %v, want expected command summary", err)
	}
}
