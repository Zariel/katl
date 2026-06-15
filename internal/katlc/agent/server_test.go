package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zariel/katl/internal/installer/operation"
	agentapi "github.com/zariel/katl/internal/katlc/agentapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/structpb"
)

type dispatchFunc func(context.Context, operation.OperationRecord) error

func (f dispatchFunc) Dispatch(ctx context.Context, record operation.OperationRecord) error {
	return f(ctx, record)
}

func TestSubmitOperationCreatesRecord(t *testing.T) {
	server := newTestServer(t)
	var dispatched atomic.Int32
	server.Dispatcher = dispatchFunc(func(ctx context.Context, record operation.OperationRecord) error {
		dispatched.Add(1)
		return nil
	})

	accepted, err := server.SubmitOperation(context.Background(), submitRequest("req-create"))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.OperationId == "" || accepted.RequestDigest == "" {
		t.Fatalf("accepted response missing identity: %+v", accepted)
	}
	if accepted.InitialStatus.Phase != "accepted" || accepted.InitialStatus.Terminal {
		t.Fatalf("initial status = %+v, want active accepted", accepted.InitialStatus)
	}
	if dispatched.Load() != 1 {
		t.Fatalf("dispatcher calls = %d, want 1", dispatched.Load())
	}
	record, err := server.Store.Read(accepted.OperationId)
	if err != nil {
		t.Fatal(err)
	}
	if record.ClientRequestID != "req-create" || record.Actor != "test-actor" {
		t.Fatalf("record request metadata = %+v", record)
	}
	if len(record.ResourceLocks) != 2 {
		t.Fatalf("resource locks = %v, want bootstrap locks", record.ResourceLocks)
	}
}

func TestSubmitOperationRejectsDigestMismatch(t *testing.T) {
	server := newTestServer(t)
	req := submitRequest("req-digest")
	req.RequestDigest = strings.Repeat("1", 64)

	_, err := server.SubmitOperation(context.Background(), req)
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("SubmitOperation error = %v, want InvalidArgument", err)
	}
}

func TestSubmitOperationIdempotentClientRequest(t *testing.T) {
	server := newTestServer(t)
	server.Dispatcher = dispatchFunc(func(ctx context.Context, record operation.OperationRecord) error {
		return nil
	})
	req := submitRequest("req-idempotent")

	first, err := server.SubmitOperation(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := server.SubmitOperation(context.Background(), submitRequest("req-idempotent"))
	if err != nil {
		t.Fatal(err)
	}
	if first.OperationId != second.OperationId || first.RequestDigest != second.RequestDigest {
		t.Fatalf("idempotent response changed: first=%+v second=%+v", first, second)
	}

	different := submitRequest("req-idempotent")
	different.Actor = "other-actor"
	_, err = server.SubmitOperation(context.Background(), different)
	if status.Code(err) != codes.AlreadyExists {
		t.Fatalf("SubmitOperation with reused client request = %v, want AlreadyExists", err)
	}
}

func TestSubmitOperationRejectsConflictingLocks(t *testing.T) {
	server := newTestServer(t)
	server.Dispatcher = dispatchFunc(func(ctx context.Context, record operation.OperationRecord) error {
		return nil
	})
	if _, err := server.SubmitOperation(context.Background(), submitRequest("req-first")); err != nil {
		t.Fatal(err)
	}

	_, err := server.SubmitOperation(context.Background(), submitRequest("req-second"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SubmitOperation conflict = %v, want FailedPrecondition", err)
	}
}

func TestSubmitOperationSerializesConcurrentConflicts(t *testing.T) {
	server := newTestServer(t)
	server.Dispatcher = dispatchFunc(func(ctx context.Context, record operation.OperationRecord) error {
		return nil
	})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := server.SubmitOperation(context.Background(), submitRequest(fmt.Sprintf("req-race-%d", i)))
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)

	var accepted, conflicted int
	for err := range errs {
		switch status.Code(err) {
		case codes.OK:
			accepted++
		case codes.FailedPrecondition:
			conflicted++
		default:
			t.Fatalf("unexpected SubmitOperation error: %v", err)
		}
	}
	if accepted != 1 || conflicted != 1 {
		t.Fatalf("accepted=%d conflicted=%d, want 1/1", accepted, conflicted)
	}
}

func TestSubmitOperationWithoutDispatcherRejectsBeforeRecord(t *testing.T) {
	server := newTestServer(t)

	_, err := server.SubmitOperation(context.Background(), submitRequest("req-no-dispatcher"))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("SubmitOperation error = %v, want FailedPrecondition", err)
	}
	ids, err := server.Store.OperationIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("operation ids = %v, want none", ids)
	}
}

func TestDryRunDoesNotRequireDispatcher(t *testing.T) {
	server := newTestServer(t)
	req := submitRequest("req-dry-run-no-dispatcher")
	req.DryRun = true

	accepted, err := server.SubmitOperation(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.OperationId != "" || accepted.InitialStatus.Phase != "dry-run" {
		t.Fatalf("dry run response = %+v", accepted)
	}
	nodeStatus, err := server.GetNodeStatus(context.Background(), &agentapi.GetNodeStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if nodeStatus.OperationLockHeld || len(nodeStatus.ActiveOperationIds) != 0 {
		t.Fatalf("node status = %+v, want no active lock after terminal dispatch failure", nodeStatus)
	}
}

func TestSubmitOperationValidatesRequestBodyAndUnsupportedExpectations(t *testing.T) {
	server := newTestServer(t)
	server.Dispatcher = dispatchFunc(func(ctx context.Context, record operation.OperationRecord) error {
		return nil
	})

	tests := []struct {
		name string
		edit func(*agentapi.SubmitOperationRequest)
	}{
		{name: "missing body", edit: func(req *agentapi.SubmitOperationRequest) { req.Request = nil }},
		{name: "empty body", edit: func(req *agentapi.SubmitOperationRequest) { req.Request.Fields = nil }},
		{name: "missing kubeadm config", edit: func(req *agentapi.SubmitOperationRequest) { delete(req.Request.Fields, "kubeadmConfigPath") }},
		{name: "public tool plan", edit: func(req *agentapi.SubmitOperationRequest) {
			plan, err := structpb.NewValue(map[string]any{"argv": []any{"/bin/sh"}})
			if err != nil {
				t.Fatal(err)
			}
			req.Request.Fields["toolPlan"] = plan
		}},
		{name: "expected generation", edit: func(req *agentapi.SubmitOperationRequest) { req.ExpectedCurrentGenerationId = "gen-1" }},
		{name: "expected cluster intent", edit: func(req *agentapi.SubmitOperationRequest) { req.ExpectedClusterIntentDigest = strings.Repeat("0", 64) }},
		{name: "bad timeout", edit: func(req *agentapi.SubmitOperationRequest) { req.OperationTimeout = "-1s" }},
		{name: "too large timeout", edit: func(req *agentapi.SubmitOperationRequest) { req.OperationTimeout = "26m" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := submitRequest("req-" + strings.ReplaceAll(tt.name, " ", "-"))
			tt.edit(req)
			_, err := server.SubmitOperation(context.Background(), req)
			if status.Code(err) != codes.InvalidArgument {
				t.Fatalf("SubmitOperation error = %v, want InvalidArgument", err)
			}
		})
	}
}

func TestSubmitOperationDispatchFailureIsRedactedAndTerminal(t *testing.T) {
	server := newTestServer(t)
	server.Dispatcher = dispatchFunc(func(ctx context.Context, record operation.OperationRecord) error {
		return errors.New("dispatch failed")
	})

	accepted, err := server.SubmitOperation(context.Background(), submitRequest("req-dispatch-fail"))
	if err != nil {
		t.Fatal(err)
	}
	status := accepted.InitialStatus
	if !status.Terminal || status.Phase != "dispatch-failed" || !status.RecoveryRequired {
		t.Fatalf("status = %+v, want terminal recovery-required failure", status)
	}
	if status.FailureReason != "dispatch failed" {
		t.Fatalf("failure reason = %q, want dispatcher error", status.FailureReason)
	}
}

func TestDryRunDoesNotCreateRecord(t *testing.T) {
	server := newTestServer(t)
	req := submitRequest("req-dry-run")
	req.DryRun = true

	accepted, err := server.SubmitOperation(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if accepted.OperationId != "" || accepted.InitialStatus.Phase != "dry-run" {
		t.Fatalf("dry run response = %+v", accepted)
	}
	ids, err := server.Store.OperationIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("operation ids = %v, want none", ids)
	}
}

func TestGetOperationChecksDigest(t *testing.T) {
	server := newTestServer(t)
	server.Dispatcher = dispatchFunc(func(ctx context.Context, record operation.OperationRecord) error {
		return nil
	})
	accepted, err := server.SubmitOperation(context.Background(), submitRequest("req-get"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = server.GetOperation(context.Background(), &agentapi.GetOperationRequest{
		OperationId:           accepted.OperationId,
		ExpectedRequestDigest: strings.Repeat("2", 64),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("GetOperation error = %v, want FailedPrecondition", err)
	}
	got, err := server.GetOperation(context.Background(), &agentapi.GetOperationRequest{
		OperationId:           accepted.OperationId,
		ExpectedRequestDigest: accepted.RequestDigest,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.OperationId != accepted.OperationId {
		t.Fatalf("operation id = %q, want %q", got.OperationId, accepted.OperationId)
	}
}

func newTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "var/lib/katl/identity"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "var/lib/katl/identity/machine-id"), []byte("machine-test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := operation.NewStore(filepath.Join(root, "var/lib/katl/operations"))
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(root, store)
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	var seq atomic.Int64
	server.Now = func() time.Time {
		return now.Add(time.Duration(seq.Load()) * time.Second)
	}
	server.OperationID = func(kind string, t time.Time) (string, error) {
		next := seq.Add(1)
		return fmt.Sprintf("%s-%02d", kind, next), nil
	}
	return server
}

func submitRequest(clientRequestID string) *agentapi.SubmitOperationRequest {
	body, err := structpb.NewStruct(map[string]any{
		"cluster":           "test",
		"kubeadmConfigPath": "/etc/katl/kubeadm/init.yaml",
	})
	if err != nil {
		panic(err)
	}
	return &agentapi.SubmitOperationRequest{
		ApiVersion:        APIVersion,
		Kind:              RequestKind,
		ClientRequestId:   clientRequestID,
		OperationKind:     "bootstrap-init",
		Actor:             "test-actor",
		ExpectedMachineId: "machine-test",
		Request:           body,
	}
}
