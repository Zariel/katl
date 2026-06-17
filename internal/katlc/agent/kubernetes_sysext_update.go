package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zariel/katl/internal/bootstrap/inventory"
	"github.com/zariel/katl/internal/installer"
	"github.com/zariel/katl/internal/installer/generation"
	"github.com/zariel/katl/internal/installer/operation"
	agentapi "github.com/zariel/katl/internal/katlc/agentapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	OperationKindKubeadmUpgrade = "kubeadm-upgrade"

	kubeadmUpgradeRefusedPhase = "execution-refused-unsupported"
	kubeadmUpgradeNoopPhase    = "planned-current-kubernetes-sysext"
)

func (s *Server) acceptKubernetesSysextUpdateOperation(req *agentapi.SubmitOperationRequest, digest string, id string, locks []string, now time.Time) (operation.OperationRecord, *agentapi.OperationAccepted, error) {
	record, err := s.planKubernetesSysextUpdateOperation(req, digest, id, locks, now)
	if err != nil {
		return operation.OperationRecord{}, nil, err
	}
	created, err := s.Store.Create(record, record.Phase, now)
	if err != nil {
		return operation.OperationRecord{}, nil, status.Errorf(codes.Internal, "create operation record: %v", err)
	}
	return created, nil, nil
}

func (s *Server) dryRunKubernetesSysextUpdateOperation(req *agentapi.SubmitOperationRequest, digest string, locks []string, now time.Time) (*agentapi.OperationAccepted, error) {
	record, err := s.planKubernetesSysextUpdateOperation(req, digest, "", locks, now)
	if err != nil {
		return nil, err
	}
	record.Phase = strings.TrimSpace(record.Phase)
	record.CreatedAt = now.UTC()
	record.UpdatedAt = now.UTC()
	return &agentapi.OperationAccepted{
		OperationKind: req.OperationKind,
		RequestDigest: digest,
		AcceptedAt:    formatTime(now),
		InitialStatus: operationStatus(record, false),
	}, nil
}

func (s *Server) planKubernetesSysextUpdateOperation(req *agentapi.SubmitOperationRequest, digest string, id string, locks []string, now time.Time) (operation.OperationRecord, error) {
	update := kubernetesSysextUpdateFromProto(req.GetKubernetesSysextUpdate())
	currentID, current, currentOK, err := s.currentKubernetesSysext()
	if err != nil {
		return operation.OperationRecord{}, status.Errorf(codes.FailedPrecondition, "read current Kubernetes sysext: %v", err)
	}
	currentFromIntent := false
	if !currentOK {
		state, err := s.kubernetesNodeState()
		if err != nil {
			return operation.OperationRecord{}, status.Errorf(codes.Internal, "inspect Kubernetes node state: %v", err)
		}
		if !state.bootstrapped {
			return operation.OperationRecord{}, status.Error(codes.FailedPrecondition, "Kubernetes sysext selection before kubeadm bootstrap must use the bootstrap operation path")
		}
		intentRef, ok, err := s.currentKubernetesSysextFromIntent()
		if err != nil {
			return operation.OperationRecord{}, status.Errorf(codes.FailedPrecondition, "read installed Kubernetes sysext intent: %v", err)
		}
		if !ok {
			return operation.OperationRecord{}, status.Errorf(codes.FailedPrecondition, "current generation %q has no selected Kubernetes sysext", currentID)
		}
		current = intentRef
		currentFromIntent = true
	}
	record := operation.OperationRecord{
		OperationID:                 id,
		OperationKind:               req.OperationKind,
		Scope:                       operationScope(req.OperationKind),
		ClientRequestID:             req.ClientRequestId,
		Actor:                       req.Actor,
		ExpectedMachineID:           req.ExpectedMachineId,
		ExpectedCurrentGenerationID: req.ExpectedCurrentGenerationId,
		ExpectedClusterIntentDigest: req.ExpectedClusterIntentDigest,
		RequestDigest:               digest,
		PhasePlan:                   []string{"accepted", kubeadmUpgradeRefusedPhase},
		PreviousGenerationID:        currentID,
		KubernetesSysextUpdate:      &update,
		ActivationMode:              operation.ActivationModeNextBoot,
		ActivationState:             operation.ActivationStatePending,
		GenerationCommitState:       operation.GenerationCommitAbandoned,
		PostKubeadmHealthState:      operation.PostKubeadmHealthNotRun,
		ResourceLocks:               locks,
	}
	if !currentFromIntent && sameKubernetesSysext(current, update) {
		completedAt := now.UTC()
		record.Phase = kubeadmUpgradeNoopPhase
		record.PhasePlan = []string{"accepted", kubeadmUpgradeNoopPhase}
		record.CompletedPhases = []string{"accepted", kubeadmUpgradeNoopPhase}
		record.PhaseIndex = len(record.CompletedPhases)
		record.Terminal = true
		record.Result = operation.ResultSucceeded
		record.NextAction = "current Kubernetes sysext already matches requested target; use the KatlOS host update path for root-only updates"
		record.CompletedAt = &completedAt
	} else {
		state, err := s.kubernetesNodeState()
		if err != nil {
			return operation.OperationRecord{}, status.Errorf(codes.Internal, "inspect Kubernetes node state: %v", err)
		}
		if !state.bootstrapped {
			return operation.OperationRecord{}, status.Error(codes.FailedPrecondition, "Kubernetes sysext selection before kubeadm bootstrap must use the bootstrap operation path")
		}
		completedAt := now.UTC()
		record.Phase = kubeadmUpgradeRefusedPhase
		record.CompletedPhases = []string{"accepted", kubeadmUpgradeRefusedPhase}
		record.PhaseIndex = len(record.CompletedPhases)
		record.Terminal = true
		record.Result = kubeadmUpgradeRefusedPhase
		record.RecoveryRequired = false
		record.NextAction = "select a target kubeadm access mode and kubelet activation gate before enabling Kubernetes sysext upgrades"
		record.FailureReason = inventory.Redact(kubernetesSysextUpgradeRefusal(state, current, update))
		record.CompletedAt = &completedAt
	}
	return record, nil
}

func kubernetesSysextUpdateFromProto(req *agentapi.KubernetesSysextUpdateOperationRequest) operation.KubernetesSysextUpdate {
	if req == nil {
		return operation.KubernetesSysextUpdate{}
	}
	return operation.KubernetesSysextUpdate{
		TargetPayloadVersion: strings.TrimSpace(req.TargetPayloadVersion),
		TargetSysextPath:     strings.TrimSpace(req.TargetSysextPath),
		TargetSysextSHA256:   strings.ToLower(strings.TrimSpace(req.TargetSysextSha256)),
		TargetSysextSize:     req.TargetSysextSizeBytes,
		TargetActivationPath: strings.TrimSpace(req.TargetActivationPath),
	}
}

func validateKubernetesSysextUpdateRequest(operationKind string, req *agentapi.KubernetesSysextUpdateOperationRequest) error {
	if operationKind != OperationKindKubeadmUpgrade {
		return fmt.Errorf("operationKind %q does not accept kubernetesSysextUpdate", operationKind)
	}
	if req == nil {
		return fmt.Errorf("kubernetesSysextUpdate is required")
	}
	if strings.TrimSpace(req.TargetPayloadVersion) == "" {
		return fmt.Errorf("targetPayloadVersion is required")
	}
	if strings.TrimSpace(req.TargetSysextPath) == "" {
		return fmt.Errorf("targetSysextPath is required")
	}
	if strings.TrimSpace(req.TargetSysextSha256) == "" {
		return fmt.Errorf("targetSysextSHA256 is required")
	}
	if err := validateLowercaseSHA256("targetSysextSHA256", strings.TrimSpace(req.TargetSysextSha256)); err != nil {
		return err
	}
	if strings.TrimSpace(req.TargetActivationPath) != "" {
		return fmt.Errorf("raw Kubernetes sysext activation paths are unsupported before the kubelet activation gate exists")
	}
	return nil
}

func validateLowercaseSHA256(name string, value string) error {
	if len(value) != sha256.Size*2 {
		return fmt.Errorf("%s must be %d lowercase hex characters", name, sha256.Size*2)
	}
	if value != strings.ToLower(value) {
		return fmt.Errorf("%s must be lowercase hex", name)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("%s is invalid: %w", name, err)
	}
	return nil
}

func (s *Server) currentKubernetesSysext() (string, generation.ExtensionRef, bool, error) {
	currentID, err := currentGenerationID(s.Root)
	if err != nil {
		return "", generation.ExtensionRef{}, false, err
	}
	spec, _, err := generation.ReadGeneration(s.Root, currentID)
	if err != nil {
		return "", generation.ExtensionRef{}, false, err
	}
	for _, ref := range spec.Sysexts {
		if ref.Name == "kubernetes" {
			return currentID, ref, true, nil
		}
	}
	return currentID, generation.ExtensionRef{}, false, nil
}

func (s *Server) currentKubernetesSysextFromIntent() (generation.ExtensionRef, bool, error) {
	intent, _, err := installer.ReadClusterIntent(s.Root)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return generation.ExtensionRef{}, false, nil
		}
		return generation.ExtensionRef{}, false, err
	}
	version := strings.TrimSpace(intent.Kubernetes.PayloadVersion)
	path := strings.TrimSpace(intent.Kubernetes.SysextPath)
	sha := strings.TrimSpace(intent.Kubernetes.SysextSHA256)
	if version == "" || path == "" || sha == "" {
		return generation.ExtensionRef{}, false, nil
	}
	return generation.ExtensionRef{
		Name:           "kubernetes",
		Path:           path,
		ActivationPath: "/run/extensions/" + filepath.Base(path),
		SHA256:         sha,
		PayloadVersion: version,
	}, true, nil
}

func sameKubernetesSysext(current generation.ExtensionRef, update operation.KubernetesSysextUpdate) bool {
	if current.Name != "kubernetes" {
		return false
	}
	return strings.EqualFold(current.SHA256, update.TargetSysextSHA256) && current.PayloadVersion == update.TargetPayloadVersion
}

type kubernetesNodeState struct {
	bootstrapped bool
	evidence     []string
}

func (s *Server) kubernetesNodeState() (kubernetesNodeState, error) {
	state := kubernetesNodeState{}
	ids, err := s.Store.OperationIDs()
	if err != nil {
		return state, err
	}
	for _, id := range ids {
		record, err := s.Store.Read(id)
		if err != nil {
			return state, err
		}
		if !kubeadmStateOperation(record.OperationKind) {
			continue
		}
		if record.ExternalMutationStarted || record.MutatingToolRan || len(record.PreExecMutationMarkers) > 0 || containsKubeadmMutationScope(record.MutationScopes) {
			state.bootstrapped = true
			state.evidence = append(state.evidence, "operation "+record.OperationID+" crossed kubeadm mutation boundary")
			continue
		}
		if record.Terminal && record.Result == operation.ResultSucceeded && record.GenerationCommitState == operation.GenerationCommitCommitted && record.PostKubeadmHealthState == operation.PostKubeadmHealthPassed {
			state.bootstrapped = true
			state.evidence = append(state.evidence, "operation "+record.OperationID+" committed bootstrap generation")
		}
	}
	for _, check := range []struct {
		path    string
		message string
	}{
		{path: "/etc/kubernetes/admin.conf", message: "control-plane admin kubeconfig exists"},
		{path: "/etc/kubernetes/manifests/kube-apiserver.yaml", message: "control-plane static pod manifests exist"},
		{path: "/etc/kubernetes/kubelet.conf", message: "kubelet kubeconfig exists"},
		{path: "/var/lib/kubelet/config.yaml", message: "kubelet config exists"},
		{path: "/var/lib/etcd/member", message: "stacked etcd member data exists"},
	} {
		ok, err := pathExists(s.Root, check.path)
		if err != nil {
			return state, err
		}
		if ok {
			state.bootstrapped = true
			state.evidence = append(state.evidence, check.message)
		}
	}
	return state, nil
}

func kubeadmStateOperation(kind string) bool {
	switch kind {
	case "bootstrap-init", "bootstrap-join-worker", "bootstrap-join-control-plane", OperationKindKubeadmUpgrade:
		return true
	default:
		return false
	}
}

func containsKubeadmMutationScope(scopes []string) bool {
	for _, scope := range scopes {
		switch strings.TrimSpace(scope) {
		case "etc-kubernetes", "kubelet-state", "etcd-state", "cluster-objects", "kubeadm-state":
			return true
		}
	}
	return false
}

func pathExists(root string, absolutePath string) (bool, error) {
	path := filepath.Join(filepath.Clean(root), strings.TrimPrefix(filepath.Clean(absolutePath), string(filepath.Separator)))
	if _, err := os.Lstat(path); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("inspect %s: %w", absolutePath, err)
	}
}

func kubernetesSysextUpgradeRefusal(state kubernetesNodeState, current generation.ExtensionRef, update operation.KubernetesSysextUpdate) string {
	evidence := strings.Join(state.evidence, "; ")
	if evidence == "" {
		evidence = "kubeadm state evidence present"
	}
	return fmt.Sprintf(
		"Kubernetes sysext change from %s/%s to %s/%s is refused on bootstrapped node (%s): target kubeadm access mode is not selected and kubelet activation gate is not implemented",
		current.PayloadVersion,
		current.SHA256,
		update.TargetPayloadVersion,
		update.TargetSysextSHA256,
		evidence,
	)
}
