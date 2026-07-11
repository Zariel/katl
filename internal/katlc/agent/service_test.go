package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/katl-dev/katl/internal/installer/operation"
)

type shutdownTestDispatcher struct {
	called chan struct{}
}

func (d *shutdownTestDispatcher) Dispatch(context.Context, operation.OperationRecord) error {
	return nil
}

func (d *shutdownTestDispatcher) Shutdown(context.Context) error {
	close(d.called)
	return nil
}

func TestServeShutsDownDispatcher(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dispatcher := &shutdownTestDispatcher{called: make(chan struct{})}
	err := Serve(ctx, ServeConfig{
		Root:                           t.TempDir(),
		Listen:                         "tcp://127.0.0.1:0",
		AllowUnauthenticatedForTesting: true,
		Dispatcher:                     dispatcher,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Serve() error = %v, want context cancellation", err)
	}
	select {
	case <-dispatcher.called:
	default:
		t.Fatal("Serve did not shut down its dispatcher")
	}
}

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
