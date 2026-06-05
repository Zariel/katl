package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zariel/katl/internal/bootstrap/cluster"
	"github.com/zariel/katl/internal/bootstrap/inventory"
	"github.com/zariel/katl/internal/bootstrap/readiness"
	"github.com/zariel/katl/internal/vmtest"
	vmtestpb "github.com/zariel/katl/internal/vmtest/proto"
)

func TestVersion(t *testing.T) {
	oldVersion, oldCommit, oldDate := version, commit, date
	version, commit, date = "dev", "abc123", "2026-06-05T00:00:00Z"
	t.Cleanup(func() {
		version, commit, date = oldVersion, oldCommit, oldDate
	})

	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if got, want := stdout.String(), "katlctl version=dev commit=abc123 date=2026-06-05T00:00:00Z\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestClusterBootstrapParsesFlagsAndPrintsNextStep(t *testing.T) {
	inventoryPath := writeInventory(t)
	var got cluster.Request
	var gotDeps cluster.Dependencies
	old := runBootstrap
	runBootstrap = func(_ context.Context, request cluster.Request, deps cluster.Dependencies) (cluster.Result, error) {
		got = request
		gotDeps = deps
		return cluster.Result{
			Plan: inventory.Plan{
				InitNode: "cp-1",
				AddressOverrides: []inventory.AddressOverride{{
					Node:    "worker-1",
					Before:  "10.0.0.21",
					Address: "10.0.0.22",
				}},
				Nodes: []inventory.PlannedNode{{Name: "cp-1"}},
			},
			Phases: []cluster.Phase{
				{Name: "plan", Status: "passed"},
				{Name: "dry-run", Status: "passed"},
			},
			NextStep: "kubectl --kubeconfig out.conf get nodes",
		}, nil
	}
	t.Cleanup(func() { runBootstrap = old })

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{
		"cluster", "bootstrap",
		"--inventory", inventoryPath,
		"--init-node", "cp-1",
		"--node-address", "worker-1=10.0.0.22",
		"--control-plane-endpoint", "api.override.test:6443",
		"--kubeconfig-out", "out.conf",
		"--overwrite-kubeconfig",
		"--dry-run",
		"--vmtest-transcript-dir", "artifacts/transcripts",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("run() error = %v, stderr = %s", err, stderr.String())
	}
	if got.InitNode != "cp-1" || got.ControlPlaneEndpoint != "api.override.test:6443" || got.KubeconfigOut != "out.conf" || !got.OverwriteKubeconfig || !got.DryRun {
		t.Fatalf("request = %#v", got)
	}
	if got.Inventory.Nodes[1].Access.CredentialRef != "agent/worker-1" {
		t.Fatalf("inventory = %#v", got.Inventory)
	}
	if got.AddressOverrides["worker-1"] != "10.0.0.22" {
		t.Fatalf("address overrides = %#v", got.AddressOverrides)
	}
	runner, ok := gotDeps.NodeRunner.(cluster.TransportRunner)
	if !ok {
		t.Fatalf("NodeRunner = %T", gotDeps.NodeRunner)
	}
	transport, ok := runner.Transport.(vmtestAgentTransport)
	if !ok {
		t.Fatalf("Transport = %T", runner.Transport)
	}
	if transport.TranscriptDir != "artifacts/transcripts" {
		t.Fatalf("TranscriptDir = %q", transport.TranscriptDir)
	}
	out := stdout.String()
	for _, want := range []string{
		"init-node=cp-1",
		"address-override node=worker-1 before=10.0.0.21 after=10.0.0.22",
		"phase=plan status=passed",
		"next: kubectl --kubeconfig out.conf get nodes",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout = %q, missing %q", out, want)
		}
	}
}

func TestClusterBootstrapRequiresInventory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"cluster", "bootstrap"}, &stdout, &stderr)
	if err == nil || !strings.Contains(err.Error(), "--inventory is required") {
		t.Fatalf("run() error = %v, want inventory error", err)
	}
}

func TestAddressOverrideValidation(t *testing.T) {
	var overrides addressOverrides
	if err := overrides.Set("bad"); err == nil {
		t.Fatal("Set() error = nil, want node=address validation")
	}
	if err := overrides.Set("node=10.0.0.10"); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if overrides.values["node"] != "10.0.0.10" {
		t.Fatalf("values = %#v", overrides.values)
	}
}

func TestParseVSockCredentialRef(t *testing.T) {
	cid, port, err := parseVSockCredentialRef("vsock:1234:10240")
	if err != nil {
		t.Fatalf("parseVSockCredentialRef() error = %v", err)
	}
	if cid != 1234 || port != 10240 {
		t.Fatalf("cid=%d port=%d", cid, port)
	}
	for _, value := range []string{"agent/cp-1", "vsock:0:10240", "vsock:abc:10240"} {
		if _, _, err := parseVSockCredentialRef(value); err == nil {
			t.Fatalf("parseVSockCredentialRef(%q) error = nil, want validation", value)
		}
	}
}

func TestVMTestAgentTransportWritesPerNodeTranscript(t *testing.T) {
	transcriptDir := t.TempDir()
	guestDir := t.TempDir()
	secretPath := filepath.Join(guestDir, "admin.conf")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write secret fixture: %v", err)
	}
	oldDial := dialVMTestAgent
	dialVMTestAgent = func(_ context.Context, cid, port uint32, transcript string) (*vmtest.AgentClient, error) {
		nameByCID := map[uint32]string{
			1234: "cp-1",
			5678: "worker-1",
		}
		nodeName, ok := nameByCID[cid]
		if !ok || port != 10240 {
			t.Fatalf("dial cid=%d port=%d", cid, port)
		}
		if transcript != filepath.Join(transcriptDir, nodeName+".jsonl") {
			t.Fatalf("transcript = %q", transcript)
		}
		serverConn, clientConn := net.Pipe()
		server := vmtest.NewAgentServer("test")
		server.AllowedFilePaths = []string{guestDir + string(os.PathSeparator)}
		server.CommandRunner = commandRunnerFunc(func(context.Context, *vmtestpb.RunCommandRequest) (*vmtestpb.CommandResult, error) {
			return &vmtestpb.CommandResult{ExitStatus: 0, Stdout: []byte("ok"), StdoutBytes: 2}, nil
		})
		done := make(chan error, 1)
		go func() { done <- server.Serve(context.Background(), serverConn) }()
		client := vmtest.NewAgentClient(clientConn, transcript)
		t.Cleanup(func() {
			_ = client.Close()
			if err := <-done; err != nil {
				t.Fatalf("agent server: %v", err)
			}
		})
		return client, nil
	}
	t.Cleanup(func() { dialVMTestAgent = oldDial })

	transport := vmtestAgentTransport{TranscriptDir: transcriptDir}
	_, err := transport.RunCommand(context.Background(), inventory.PlannedNode{
		Name:   "cp-1",
		Access: inventory.Access{Method: "agent", CredentialRef: "vsock:1234:10240"},
	}, readiness.CommandRequest{
		Argv:            []string{"kubeadm", "init"},
		SensitiveOutput: true,
	})
	if err != nil {
		t.Fatalf("RunCommand() error = %v", err)
	}
	_, err = transport.ReadFile(context.Background(), inventory.PlannedNode{
		Name:   "cp-1",
		Access: inventory.Access{Method: "agent", CredentialRef: "vsock:1234:10240"},
	}, readiness.FileRequest{
		Path:      secretPath,
		Sensitive: true,
	})
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	_, err = transport.RunCommand(context.Background(), inventory.PlannedNode{
		Name:   "worker-1",
		Access: inventory.Access{Method: "agent", CredentialRef: "vsock:5678:10240"},
	}, readiness.CommandRequest{
		Argv:            []string{"kubeadm", "join"},
		SensitiveOutput: true,
	})
	if err != nil {
		t.Fatalf("worker RunCommand() error = %v", err)
	}
	entries := readTranscript(t, filepath.Join(transcriptDir, "cp-1.jsonl"))
	if len(entries) != 2 {
		t.Fatalf("transcript entries = %#v", entries)
	}
	if entries[0].Method != "RunCommand" || entries[0].Redaction != "output" || entries[0].StdoutBytes != 2 {
		t.Fatalf("transcript entry = %#v", entries[0])
	}
	if entries[1].Method != "ReadFile" || entries[1].Redaction != "sensitive" || !entries[1].SensitiveOutput {
		t.Fatalf("file transcript entry = %#v", entries[1])
	}
	workerEntries := readTranscript(t, filepath.Join(transcriptDir, "worker-1.jsonl"))
	if len(workerEntries) != 1 {
		t.Fatalf("worker transcript entries = %#v", workerEntries)
	}
	if workerEntries[0].Method != "RunCommand" || workerEntries[0].Redaction != "output" || !workerEntries[0].SensitiveOutput {
		t.Fatalf("worker transcript entry = %#v", workerEntries[0])
	}
}

type commandRunnerFunc func(context.Context, *vmtestpb.RunCommandRequest) (*vmtestpb.CommandResult, error)

func (f commandRunnerFunc) Run(ctx context.Context, req *vmtestpb.RunCommandRequest) (*vmtestpb.CommandResult, error) {
	return f(ctx, req)
}

type transcriptEntry struct {
	Method          string `json:"method"`
	Redaction       string `json:"redaction,omitempty"`
	StdoutBytes     uint32 `json:"stdoutBytes,omitempty"`
	SensitiveOutput bool   `json:"sensitiveOutput,omitempty"`
}

func readTranscript(t *testing.T, path string) []transcriptEntry {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer file.Close()
	var entries []transcriptEntry
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var entry transcriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("decode transcript: %v", err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan transcript: %v", err)
	}
	return entries
}

func writeInventory(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cluster.yaml")
	data := `controlPlaneEndpoint: api.katl.test:6443
kubernetesVersion: v1.36.1
nodes:
- name: cp-1
  address: 10.0.0.11
  systemRole: control-plane
  access:
    method: agent
    credentialRef: agent/cp-1
  kubeadmConfig:
    ref: control-plane
    path: /etc/katl/kubeadm/control-plane/config.yaml
    intent: control-plane
  kubernetesVersion: v1.36.1
- name: worker-1
  address: 10.0.0.21
  systemRole: worker
  access:
    method: agent
    credentialRef: agent/worker-1
  kubeadmConfig:
    ref: worker
    path: /etc/katl/kubeadm/worker/config.yaml
    intent: worker
  kubernetesVersion: v1.36.1
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
