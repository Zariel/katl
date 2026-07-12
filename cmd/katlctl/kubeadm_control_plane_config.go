package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/katl-dev/katl/internal/bootstrap/cluster"
	"github.com/katl-dev/katl/internal/bootstrap/inventory"
	"github.com/katl-dev/katl/internal/installer/operation"
	agentapi "github.com/katl-dev/katl/internal/katlc/agentapi"
	"github.com/spf13/cobra"
)

type kubeadmControlPlaneConfigOptions struct {
	inventoryPath, coordinator, generationID, configName               string
	desiredDigest, liveDigest, payloadVersion, payloadDigest           string
	rolloutID                                                          string
	fieldDelta                                                         stringList
	snapshotRef, snapshotDigest, snapshotRevision, memberDigest        string
	etcdVersion, snapshotCreatedAt, snapshotLocation, snapshotOperator string
}

func newKubeadmControlPlaneConfigCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	opts := kubeadmControlPlaneConfigOptions{}
	cmd := &cobra.Command{Use: "kubeadm-control-plane-config", Short: "Roll out the bounded kubeadm control-plane configuration change", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return runKubeadmControlPlaneConfig(ctx, opts, stdout) }}
	f := cmd.Flags()
	f.StringVar(&opts.inventoryPath, "inventory", "", "three-control-plane inventory")
	f.StringVar(&opts.coordinator, "coordinator", "", "coordinator control-plane node changed last")
	f.StringVar(&opts.generationID, "generation", "", "active desired generation ID")
	f.StringVar(&opts.configName, "config-name", "", "selected KubeadmConfig name")
	f.StringVar(&opts.desiredDigest, "desired-config-sha256", "", "canonical desired config SHA-256")
	f.StringVar(&opts.liveDigest, "expected-live-sha256", "", "expected live kubeadm ConfigMap SHA-256")
	f.StringVar(&opts.payloadVersion, "kubernetes-version", "", "active Kubernetes payload version")
	f.StringVar(&opts.payloadDigest, "kubernetes-sha256", "", "active Kubernetes sysext SHA-256")
	f.StringVar(&opts.rolloutID, "rollout-id", "", "rollout identity")
	f.Var(&opts.fieldDelta, "field-delta", "supported profiling=false field delta")
	f.StringVar(&opts.snapshotRef, "snapshot-ref", "", "etcd snapshot reference")
	f.StringVar(&opts.snapshotDigest, "snapshot-sha256", "", "etcd snapshot SHA-256")
	f.StringVar(&opts.snapshotRevision, "snapshot-revision", "", "etcd snapshot revision")
	f.StringVar(&opts.memberDigest, "member-list-sha256", "", "captured etcd member-list SHA-256")
	f.StringVar(&opts.etcdVersion, "source-etcd-version", "", "source etcd version")
	f.StringVar(&opts.snapshotCreatedAt, "snapshot-created-at", "", "snapshot RFC3339 creation time")
	f.StringVar(&opts.snapshotLocation, "snapshot-location", "", "snapshot storage location")
	f.StringVar(&opts.snapshotOperator, "snapshot-operator", "", "snapshot operator identity")
	_ = stderr
	return cmd
}

func runKubeadmControlPlaneConfig(ctx context.Context, opts kubeadmControlPlaneConfigOptions, stdout io.Writer) error {
	inv, err := loadInventory(opts.inventoryPath)
	if err != nil {
		return err
	}
	var nodes []inventory.Node
	for _, node := range inv.Nodes {
		if node.SystemRole == inventory.RoleControlPlane {
			nodes = append(nodes, node)
		}
	}
	if len(nodes) != 3 {
		return fmt.Errorf("exactly three control-plane nodes are required, got %d", len(nodes))
	}
	nodes, err = orderControlPlanes(nodes, opts.coordinator)
	if err != nil {
		return err
	}
	if len(opts.fieldDelta.values) == 0 {
		return fmt.Errorf("at least one --field-delta is required")
	}
	type target struct {
		node    inventory.Node
		conn    katlcAgentConnection
		machine string
	}
	targets := make([]target, 0, 3)
	defer func() {
		for _, t := range targets {
			_ = t.conn.Close()
		}
	}()
	for _, node := range nodes {
		token, err := tokenForInventoryNode(node)
		if err != nil {
			return err
		}
		conn, err := dialKatlcAgent(ctx, cluster.AgentEndpoint(node.Address, "9443"), token)
		if err != nil {
			return fmt.Errorf("connect %s: %w", node.Name, err)
		}
		status, err := conn.Client.GetNodeStatus(ctx, &agentapi.GetNodeStatusRequest{})
		if err != nil {
			return fmt.Errorf("status %s: %w", node.Name, err)
		}
		gen, err := conn.Client.GetGeneration(ctx, &agentapi.GetGenerationRequest{GenerationId: opts.generationID, IncludeConfigApply: true})
		if err != nil {
			return fmt.Errorf("generation %s on %s: %w", opts.generationID, node.Name, err)
		}
		if gen.CommitState != "committed" || gen.HealthState != "healthy" {
			return fmt.Errorf("node %s generation %s is not committed and healthy", node.Name, opts.generationID)
		}
		if gen.ConfigApply == nil || !gen.ConfigApply.KubeadmActionRequired || gen.ConfigApply.SelectedKubeadmConfigName != opts.configName {
			return fmt.Errorf("node %s generation %s does not select kubeadm config %q as action-required", node.Name, opts.generationID, opts.configName)
		}
		matched := false
		for _, ref := range gen.Sysexts {
			if ref.Name == "kubernetes" && ref.PayloadVersion == opts.payloadVersion && ref.Sha256 == opts.payloadDigest {
				matched = true
			}
		}
		if !matched {
			return fmt.Errorf("node %s Kubernetes payload does not match rollout", node.Name)
		}
		targets = append(targets, target{node: node, conn: conn, machine: status.MachineId})
	}
	var summary []map[string]string
	for i, t := range targets {
		body := kubeadmControlPlaneConfigBody(opts, t.node, uint32(i+1))
		accepted, err := t.conn.Client.SubmitOperation(ctx, &agentapi.SubmitOperationRequest{ApiVersion: operation.APIVersion, Kind: "SubmitOperationRequest", ClientRequestId: opts.rolloutID + "-dry-run-" + t.node.Name, OperationKind: "kubeadm-control-plane-config", Actor: "katlctl cluster kubeadm-control-plane-config", ExpectedMachineId: t.machine, ExpectedCurrentGenerationId: opts.generationID, DryRun: true, KubeadmControlPlaneConfig: body})
		if err != nil {
			return fmt.Errorf("dry-run %s: %w", t.node.Name, err)
		}
		if accepted.InitialStatus == nil || accepted.InitialStatus.Phase != "dry-run" {
			return fmt.Errorf("node %s did not confirm kubeadm rollout dry-run", t.node.Name)
		}
	}
	for i, t := range targets {
		body := kubeadmControlPlaneConfigBody(opts, t.node, uint32(i+1))
		accepted, err := t.conn.Client.SubmitOperation(ctx, &agentapi.SubmitOperationRequest{ApiVersion: operation.APIVersion, Kind: "SubmitOperationRequest", ClientRequestId: opts.rolloutID + "-" + t.node.Name, OperationKind: "kubeadm-control-plane-config", Actor: "katlctl cluster kubeadm-control-plane-config", ExpectedMachineId: t.machine, ExpectedCurrentGenerationId: opts.generationID, KubeadmControlPlaneConfig: body})
		if err != nil {
			return fmt.Errorf("submit %s: %w", t.node.Name, err)
		}
		terminal, err := waitKubeadmControlPlaneConfig(ctx, t.conn.Client, accepted.OperationId)
		if err != nil {
			return fmt.Errorf("node %s: %w", t.node.Name, err)
		}
		if terminal.Result != operation.ResultSucceeded {
			return fmt.Errorf("node %s stopped rollout: %s: %s", t.node.Name, terminal.Phase, terminal.FailureReason)
		}
		summary = append(summary, map[string]string{"node": t.node.Name, "operationID": accepted.OperationId, "result": terminal.Result})
	}
	return json.NewEncoder(stdout).Encode(map[string]any{"rolloutID": opts.rolloutID, "coordinator": opts.coordinator, "nodes": summary, "automaticRollback": false})
}

func kubeadmControlPlaneConfigBody(opts kubeadmControlPlaneConfigOptions, node inventory.Node, position uint32) *agentapi.KubeadmControlPlaneConfigOperationRequest {
	return &agentapi.KubeadmControlPlaneConfigOperationRequest{RolloutId: opts.rolloutID, NodePosition: position, NodeCount: 3, NodeName: node.Name, CoordinatorNode: opts.coordinator, CoordinatorUpload: node.Name == opts.coordinator, DesiredGenerationId: opts.generationID, ConfigName: opts.configName, DesiredConfigSha256: opts.desiredDigest, ExpectedLiveConfigSha256: opts.liveDigest, KubernetesPayloadVersion: opts.payloadVersion, KubernetesPayloadSha256: opts.payloadDigest, SupportedFieldDelta: append([]string(nil), opts.fieldDelta.values...), SnapshotRef: opts.snapshotRef, SnapshotDigest: opts.snapshotDigest, SnapshotRevision: opts.snapshotRevision, CapturedMemberListDigest: opts.memberDigest, SourceEtcdVersion: opts.etcdVersion, SnapshotCreatedAt: opts.snapshotCreatedAt, SnapshotStorageLocation: opts.snapshotLocation, SnapshotOperatorIdentity: opts.snapshotOperator}
}

func orderControlPlanes(nodes []inventory.Node, coordinator string) ([]inventory.Node, error) {
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	ordered := make([]inventory.Node, 0, len(nodes))
	var coordinatorNode *inventory.Node
	for i := range nodes {
		if nodes[i].Name == coordinator {
			copy := nodes[i]
			coordinatorNode = &copy
		} else {
			ordered = append(ordered, nodes[i])
		}
	}
	if coordinatorNode == nil {
		return nil, fmt.Errorf("coordinator %q is not a control-plane node", coordinator)
	}
	return append(ordered, *coordinatorNode), nil
}

func waitKubeadmControlPlaneConfig(ctx context.Context, client agentapi.KatlcAgentClient, id string) (*agentapi.OperationStatus, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := client.GetOperation(ctx, &agentapi.GetOperationRequest{OperationId: id, IncludeDiagnostics: "normal"})
		if err != nil {
			return nil, err
		}
		if status.Terminal {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func tokenForInventoryNode(node inventory.Node) (string, error) {
	ref := strings.TrimSpace(node.Access.CredentialRef)
	path, ok := strings.CutPrefix(ref, "file:")
	if !ok {
		return "", nil
	}
	return readAgentToken(path)
}
