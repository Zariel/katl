package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zariel/katl/internal/bootstrap/inventory"
	"github.com/zariel/katl/internal/installer/operation"
	agentapi "github.com/zariel/katl/internal/katlc/agentapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

const (
	APIVersion = operation.APIVersion

	RequestKind = "SubmitOperationRequest"

	DefaultListen = "tcp://0.0.0.0:9443"
)

var bootstrapOperationKinds = []string{
	"bootstrap-init",
	"bootstrap-join-control-plane",
	"bootstrap-join-worker",
}

type Dispatcher interface {
	Dispatch(ctx context.Context, record operation.OperationRecord) error
}

type Server struct {
	agentapi.UnimplementedKatlcAgentServer

	Root                    string
	Store                   operation.Store
	MachineID               string
	AgentStartID            string
	StartedAt               time.Time
	SupportedOperationKinds []string
	Dispatcher              Dispatcher
	Now                     func() time.Time
	OperationID             func(string, time.Time) (string, error)
	submitMu                sync.Mutex
}

func NewServer(root string, store operation.Store) *Server {
	now := time.Now().UTC()
	startID, _ := randomID("agent")
	return &Server{
		Root:                    strings.TrimSpace(root),
		Store:                   store,
		AgentStartID:            startID,
		StartedAt:               now,
		SupportedOperationKinds: append([]string(nil), bootstrapOperationKinds...),
		Now:                     func() time.Time { return time.Now().UTC() },
		OperationID:             defaultOperationID,
	}
}

func (s *Server) GetNodeStatus(ctx context.Context, _ *agentapi.GetNodeStatusRequest) (*agentapi.NodeStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ids, err := s.activeOperationIDs()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "read operation locks: %v", err)
	}
	machineID, err := s.machineID()
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "read machine id: %v", err)
	}
	return &agentapi.NodeStatus{
		ApiVersion:              APIVersion,
		MachineId:               machineID,
		AgentStartId:            s.AgentStartID,
		AgentStartedAt:          formatTime(s.StartedAt),
		SupportedApiVersions:    []string{APIVersion},
		SupportedOperationKinds: append([]string(nil), s.supportedOperationKinds()...),
		OperationLockHeld:       len(ids) > 0,
		ActiveOperationIds:      ids,
	}, nil
}

func (s *Server) SubmitOperation(ctx context.Context, req *agentapi.SubmitOperationRequest) (*agentapi.OperationAccepted, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}
	if err := s.validateSubmit(req); err != nil {
		return nil, err
	}
	digest, err := RequestDigest(req)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "request digest: %v", err)
	}
	if strings.TrimSpace(req.RequestDigest) != "" && req.RequestDigest != digest {
		return nil, status.Error(codes.InvalidArgument, "requestDigest does not match normalized request")
	}
	req.RequestDigest = digest
	plan, err := kubeadmPlanFromSubmit(req)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "operation request: %v", err)
	}
	plan.Timeout = strings.TrimSpace(req.OperationTimeout)
	if !req.DryRun && s.Dispatcher == nil {
		return nil, status.Error(codes.FailedPrecondition, "agent executor is not configured")
	}
	created, dryRun, err := s.acceptOperation(req, digest, plan)
	if err != nil {
		return nil, err
	}
	if dryRun != nil {
		return dryRun, nil
	}
	if err := s.Dispatcher.Dispatch(context.Background(), created); err != nil {
		updated, updateErr := s.markDispatchFailed(created.OperationID, err)
		if updateErr != nil {
			return nil, status.Errorf(codes.Internal, "dispatch failed and status update failed: %v; %v", err, updateErr)
		}
		created = updated
	}
	return s.acceptedFromRecord(created), nil
}

func (s *Server) acceptOperation(req *agentapi.SubmitOperationRequest, digest string, plan toolPlan) (operation.OperationRecord, *agentapi.OperationAccepted, error) {
	s.submitMu.Lock()
	defer s.submitMu.Unlock()
	if existing, ok, err := s.findClientRequest(req.ClientRequestId); err != nil {
		return operation.OperationRecord{}, nil, status.Errorf(codes.Internal, "read idempotency state: %v", err)
	} else if ok {
		if existing.RequestDigest != digest {
			return operation.OperationRecord{}, nil, status.Error(codes.AlreadyExists, "clientRequestID already used with a different requestDigest")
		}
		return operation.OperationRecord{}, s.acceptedFromRecord(existing), nil
	}
	locks := resourceLocks(req.OperationKind)
	if conflict, err := s.conflictingOperation(locks); err != nil {
		return operation.OperationRecord{}, nil, status.Errorf(codes.Internal, "check operation locks: %v", err)
	} else if conflict != "" {
		return operation.OperationRecord{}, nil, status.Errorf(codes.FailedPrecondition, "operation locks conflict with active operation %s", conflict)
	}
	now := s.clock()
	if req.DryRun {
		return operation.OperationRecord{}, &agentapi.OperationAccepted{
			OperationKind: req.OperationKind,
			RequestDigest: digest,
			AcceptedAt:    formatTime(now),
			InitialStatus: &agentapi.OperationStatus{
				OperationKind: req.OperationKind,
				RequestDigest: digest,
				Phase:         "dry-run",
				UpdatedAt:     formatTime(now),
				ResourceLocks: locks,
				NextAction:    "submit with dryRun=false to create an operation record",
			},
		}, nil
	}
	id, err := s.operationID(req.OperationKind, now)
	if err != nil {
		return operation.OperationRecord{}, nil, status.Errorf(codes.Internal, "generate operation id: %v", err)
	}
	record := operation.OperationRecord{
		OperationID:     id,
		OperationKind:   req.OperationKind,
		Scope:           operationScope(req.OperationKind),
		ClientRequestID: req.ClientRequestId,
		Actor:           req.Actor,
		RequestDigest:   digest,
		Phase:           "accepted",
		PhasePlan:       []string{"accepted"},
		ResourceLocks:   locks,
		ExecutorPlan:    &plan,
		NextAction:      "queued for katlc agent executor",
	}
	created, err := s.Store.Create(record, "accepted", now)
	if err != nil {
		return operation.OperationRecord{}, nil, status.Errorf(codes.Internal, "create operation record: %v", err)
	}
	return created, nil, nil
}

func (s *Server) GetOperation(ctx context.Context, req *agentapi.GetOperationRequest) (*agentapi.OperationStatus, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req == nil || strings.TrimSpace(req.OperationId) == "" {
		return nil, status.Error(codes.InvalidArgument, "operationID is required")
	}
	record, err := s.Store.Read(req.OperationId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "read operation: %v", err)
	}
	if strings.TrimSpace(req.ExpectedRequestDigest) != "" && record.RequestDigest != req.ExpectedRequestDigest {
		return nil, status.Error(codes.FailedPrecondition, "operation requestDigest does not match expectedRequestDigest")
	}
	return operationStatus(record), nil
}

func (s *Server) WatchOperation(req *agentapi.WatchOperationRequest, stream agentapi.KatlcAgent_WatchOperationServer) error {
	if req == nil || strings.TrimSpace(req.OperationId) == "" {
		return status.Error(codes.InvalidArgument, "operationID is required")
	}
	record, err := s.Store.Read(req.OperationId)
	if err != nil {
		return status.Errorf(codes.NotFound, "read operation: %v", err)
	}
	if strings.TrimSpace(req.ExpectedRequestDigest) != "" && record.RequestDigest != req.ExpectedRequestDigest {
		return status.Error(codes.FailedPrecondition, "operation requestDigest does not match expectedRequestDigest")
	}
	if int(record.LatestJournalSeq) <= int(req.AfterJournalSeq) {
		return nil
	}
	return stream.Send(&agentapi.OperationEvent{
		OperationId: record.OperationID,
		JournalSeq:  int32(record.LatestJournalSeq),
		EventType:   "snapshot",
		Phase:       record.Phase,
		Terminal:    record.Terminal,
		Status:      operationStatus(record),
	})
}

func (s *Server) validateSubmit(req *agentapi.SubmitOperationRequest) error {
	if req.ApiVersion != APIVersion {
		return status.Errorf(codes.InvalidArgument, "apiVersion must be %q", APIVersion)
	}
	if req.Kind != RequestKind {
		return status.Errorf(codes.InvalidArgument, "kind must be %q", RequestKind)
	}
	if strings.TrimSpace(req.ClientRequestId) == "" {
		return status.Error(codes.InvalidArgument, "clientRequestID is required")
	}
	if !contains(s.supportedOperationKinds(), req.OperationKind) {
		return status.Errorf(codes.InvalidArgument, "operationKind %q is unsupported", req.OperationKind)
	}
	if strings.TrimSpace(req.Actor) == "" {
		return status.Error(codes.InvalidArgument, "actor is required")
	}
	if req.Request == nil || len(req.Request.GetFields()) == 0 {
		return status.Error(codes.InvalidArgument, "request body is required")
	}
	if strings.TrimSpace(req.ExpectedCurrentGenerationId) != "" {
		return status.Error(codes.InvalidArgument, "expectedCurrentGenerationID validation is not implemented")
	}
	if strings.TrimSpace(req.ExpectedClusterIntentDigest) != "" {
		return status.Error(codes.InvalidArgument, "expectedClusterIntentDigest validation is not implemented")
	}
	if strings.TrimSpace(req.OperationTimeout) != "" {
		timeout, err := time.ParseDuration(req.OperationTimeout)
		if err != nil || timeout <= 0 {
			return status.Error(codes.InvalidArgument, "operationTimeout must be a positive Go duration")
		}
		if timeout > maxToolTimeout {
			return status.Errorf(codes.InvalidArgument, "operationTimeout must not exceed %s", maxToolTimeout)
		}
	}
	if strings.TrimSpace(req.ExpectedMachineId) != "" {
		machineID, err := s.machineID()
		if err != nil {
			return status.Errorf(codes.FailedPrecondition, "read machine id: %v", err)
		}
		if req.ExpectedMachineId != machineID {
			return status.Error(codes.FailedPrecondition, "expectedMachineID does not match node machine id")
		}
	}
	return nil
}

func (s *Server) acceptedFromRecord(record operation.OperationRecord) *agentapi.OperationAccepted {
	return &agentapi.OperationAccepted{
		OperationId:   record.OperationID,
		OperationKind: record.OperationKind,
		RequestDigest: record.RequestDigest,
		RecordPath:    filepath.ToSlash(filepath.Join(s.operationStoreRoot(), record.OperationID, "record.json")),
		AcceptedAt:    formatTime(record.CreatedAt),
		InitialStatus: operationStatus(record),
	}
}

func (s *Server) markDispatchFailed(operationID string, err error) (operation.OperationRecord, error) {
	now := s.clock()
	return s.Store.Update(operationID, "dispatch-failed", "dispatch-failed", func(record operation.OperationRecord) (operation.OperationRecord, error) {
		record.Phase = "dispatch-failed"
		record.Result = operation.ResultFailedNeedsRepair
		record.RecoveryRequired = true
		record.NextAction = "agent executor dispatch failed"
		record.FailureReason = inventory.Redact(err.Error())
		record.Terminal = true
		record.UpdatedAt = now
		record.CompletedAt = &now
		return record, nil
	})
}

func (s *Server) findClientRequest(clientRequestID string) (operation.OperationRecord, bool, error) {
	clientRequestID = strings.TrimSpace(clientRequestID)
	if clientRequestID == "" {
		return operation.OperationRecord{}, false, nil
	}
	ids, err := s.Store.OperationIDs()
	if err != nil {
		return operation.OperationRecord{}, false, err
	}
	for _, id := range ids {
		record, err := s.Store.Read(id)
		if err != nil {
			return operation.OperationRecord{}, false, err
		}
		if record.ClientRequestID == clientRequestID {
			return record, true, nil
		}
	}
	return operation.OperationRecord{}, false, nil
}

func (s *Server) conflictingOperation(locks []string) (string, error) {
	if len(locks) == 0 {
		return "", nil
	}
	ids, err := s.Store.OperationIDs()
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		record, err := s.Store.Read(id)
		if err != nil {
			return "", err
		}
		if record.Terminal {
			continue
		}
		for _, held := range record.ResourceLocks {
			if contains(locks, held) {
				return record.OperationID, nil
			}
		}
	}
	return "", nil
}

func (s *Server) activeOperationIDs() ([]string, error) {
	ids, err := s.Store.OperationIDs()
	if err != nil {
		return nil, err
	}
	var active []string
	for _, id := range ids {
		record, err := s.Store.Read(id)
		if err != nil {
			return nil, err
		}
		if !record.Terminal && len(record.ResourceLocks) > 0 {
			active = append(active, record.OperationID)
		}
	}
	sort.Strings(active)
	return active, nil
}

func (s *Server) machineID() (string, error) {
	if strings.TrimSpace(s.MachineID) != "" {
		return strings.TrimSpace(s.MachineID), nil
	}
	root := s.Root
	if strings.TrimSpace(root) == "" {
		root = "/"
	}
	for _, path := range []string{
		filepath.Join(root, "var/lib/katl/identity/machine-id"),
		filepath.Join(root, "etc/machine-id"),
	} {
		data, err := os.ReadFile(path)
		if err == nil {
			value := strings.TrimSpace(string(data))
			if value != "" {
				return value, nil
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("machine identity is not initialized")
}

func (s *Server) operationStoreRoot() string {
	if strings.TrimSpace(s.Store.Root) != "" {
		return s.Store.Root
	}
	root := s.Root
	if strings.TrimSpace(root) == "" {
		root = "/"
	}
	return filepath.Join(root, "var/lib/katl/operations")
}

func (s *Server) supportedOperationKinds() []string {
	if len(s.SupportedOperationKinds) > 0 {
		return append([]string(nil), s.SupportedOperationKinds...)
	}
	return append([]string(nil), bootstrapOperationKinds...)
}

func (s *Server) clock() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s *Server) operationID(kind string, now time.Time) (string, error) {
	if s.OperationID != nil {
		return s.OperationID(kind, now)
	}
	return defaultOperationID(kind, now)
}

func operationStatus(record operation.OperationRecord) *agentapi.OperationStatus {
	diagnostics := make([]*agentapi.DiagnosticArtifact, 0, len(record.DiagnosticArtifacts))
	for _, artifact := range record.DiagnosticArtifacts {
		diagnostics = append(diagnostics, &agentapi.DiagnosticArtifact{
			ArtifactId: artifact.ArtifactID,
			Path:       inventory.Redact(artifact.Path),
			Sha256:     artifact.SHA256,
			Redacted:   artifact.Redacted,
			CreatedAt:  formatTime(artifact.CreatedAt),
		})
	}
	invocations := make([]*agentapi.OperationInvocation, 0, len(record.Invocations))
	for _, invocation := range record.Invocations {
		invocations = append(invocations, &agentapi.OperationInvocation{
			InvocationId:      invocation.InvocationID,
			AgentStartId:      invocation.AgentStartID,
			ExecutorAttemptId: invocation.ExecutorAttemptID,
			ChildProcess:      redactArgv(invocation.ChildProcess),
			Pid:               int32(invocation.PID),
			ExitStatus:        int32(invocation.ExitStatus),
			StartedAt:         formatTime(invocation.StartedAt),
			CompletedAt:       formatTimePtr(invocation.CompletedAt),
			Result:            invocation.Result,
		})
	}
	return &agentapi.OperationStatus{
		OperationId:             record.OperationID,
		OperationKind:           record.OperationKind,
		RequestDigest:           record.RequestDigest,
		Phase:                   record.Phase,
		PhaseIndex:              int32(record.PhaseIndex),
		CompletedPhases:         append([]string(nil), record.CompletedPhases...),
		Terminal:                record.Terminal,
		Result:                  record.Result,
		CandidateGenerationId:   record.CandidateGenerationID,
		ExternalMutationStarted: record.ExternalMutationStarted,
		MutationScopes:          append([]string(nil), record.MutationScopes...),
		ResourceLocks:           append([]string(nil), record.ResourceLocks...),
		LatestJournalSeq:        int32(record.LatestJournalSeq),
		UpdatedAt:               formatTime(record.UpdatedAt),
		NextAction:              record.NextAction,
		Diagnostics:             diagnostics,
		RecoveryRequired:        record.RecoveryRequired,
		FailureReason:           inventory.Redact(record.FailureReason),
		Invocations:             invocations,
	}
}

func RequestDigest(req *agentapi.SubmitOperationRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("request is required")
	}
	clone := *req
	clone.RequestDigest = ""
	data, err := protojson.MarshalOptions{EmitUnpopulated: false}.Marshal(&clone)
	if err != nil {
		return "", err
	}
	var normalized any
	if err := json.Unmarshal(data, &normalized); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func resourceLocks(kind string) []string {
	switch kind {
	case "bootstrap-init", "bootstrap-join-control-plane", "bootstrap-join-worker":
		return []string{"generation-state.lock", "kubeadm-state.lock"}
	default:
		return nil
	}
}

func operationScope(kind string) string {
	switch kind {
	case "bootstrap-init", "bootstrap-join-control-plane", "bootstrap-join-worker":
		return "kubeadm-state"
	default:
		return "host-generation"
	}
}

func kubeadmPlanFromSubmit(req *agentapi.SubmitOperationRequest) (toolPlan, error) {
	if req == nil || req.Request == nil {
		return toolPlan{}, fmt.Errorf("request body is required")
	}
	values := req.Request.AsMap()
	if _, ok := values["toolPlan"]; ok {
		return toolPlan{}, fmt.Errorf("toolPlan is internal executor state and is not accepted over the management API")
	}
	rawPath, ok := values["kubeadmConfigPath"].(string)
	if !ok || strings.TrimSpace(rawPath) == "" {
		return toolPlan{}, fmt.Errorf("kubeadmConfigPath is required")
	}
	configPath := path.Clean(rawPath)
	if !strings.HasPrefix(configPath, "/etc/katl/kubeadm/") {
		return toolPlan{}, fmt.Errorf("kubeadmConfigPath must be under /etc/katl/kubeadm")
	}
	plan := toolPlan{
		MutationScopes: []string{"kubeadm-state", "etc-kubernetes"},
	}
	switch req.OperationKind {
	case "bootstrap-init":
		plan.Phase = "kubeadm-init"
		plan.MarkerID = "kubeadm-init"
		plan.Argv = []string{"/usr/bin/kubeadm", "init", "--config", configPath}
	case "bootstrap-join-control-plane":
		plan.Phase = "kubeadm-join-control-plane"
		plan.MarkerID = "kubeadm-join-control-plane"
		plan.Argv = []string{"/usr/bin/kubeadm", "join", "--config", configPath}
	case "bootstrap-join-worker":
		plan.Phase = "kubeadm-join-worker"
		plan.MarkerID = "kubeadm-join-worker"
		plan.Argv = []string{"/usr/bin/kubeadm", "join", "--config", configPath}
	default:
		return toolPlan{}, fmt.Errorf("operationKind %q has no executor plan", req.OperationKind)
	}
	return plan, nil
}

func defaultOperationID(kind string, now time.Time) (string, error) {
	suffix, err := randomID("")
	if err != nil {
		return "", err
	}
	cleanKind := strings.NewReplacer("_", "-", ".", "-", "/", "-").Replace(strings.TrimSpace(kind))
	if cleanKind == "" {
		cleanKind = "operation"
	}
	return fmt.Sprintf("%s-%s-%s", cleanKind, now.UTC().Format("20060102T150405Z"), suffix), nil
}

func randomID(prefix string) (string, error) {
	var data [6]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(data[:])
	if strings.TrimSpace(prefix) == "" {
		return id, nil
	}
	return prefix + "-" + id, nil
}

func formatTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func formatTimePtr(value *time.Time) string {
	if value == nil {
		return ""
	}
	return formatTime(*value)
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
