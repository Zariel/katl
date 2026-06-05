package cluster

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/zariel/katl/internal/bootstrap/inventory"
	"github.com/zariel/katl/internal/bootstrap/kubeconfig"
	"github.com/zariel/katl/internal/bootstrap/readiness"
	"gopkg.in/yaml.v3"
)

const (
	adminKubeconfigPath = "/etc/kubernetes/admin.conf"
	defaultAPIPort      = "6443"
)

type Request struct {
	Inventory            inventory.Inventory
	InitNode             string
	AddressOverrides     map[string]string
	ControlPlaneEndpoint string
	KubeconfigOut        string
	OverwriteKubeconfig  bool
	DryRun               bool
	ClusterName          string
	ContextName          string
	UserName             string
}

type Dependencies struct {
	ReadinessChecker inventory.ReadinessChecker
	NodeRunner       NodeRunner
}

type NodeRunner interface {
	RunKubeadmInit(ctx context.Context, node inventory.PlannedNode) (AdminCredentials, error)
	CreateWorkerJoin(ctx context.Context, initNode inventory.PlannedNode) (JoinMaterial, error)
	RunWorkerJoin(ctx context.Context, node inventory.PlannedNode, material JoinMaterial) error
	WaitAPIReady(ctx context.Context, initNode inventory.PlannedNode) error
}

type AdminCredentials struct {
	CertificateAuthorityData string
	ClientCertificateData    string
	ClientKeyData            string
}

type JoinMaterial struct {
	Argv []string
}

type Result struct {
	Plan       inventory.Plan
	Phases     []Phase
	Readiness  inventory.ReadinessReport
	Kubeconfig kubeconfig.Result
	NextStep   string
	DryRun     bool
}

type Phase struct {
	Name   string                    `json:"name"`
	Node   string                    `json:"node,omitempty"`
	Action inventory.BootstrapAction `json:"action,omitempty"`
	Status string                    `json:"status"`
}

func Run(ctx context.Context, request Request, deps Dependencies) (Result, error) {
	inv := request.Inventory
	if strings.TrimSpace(request.ControlPlaneEndpoint) != "" {
		inv.ControlPlaneEndpoint = strings.TrimSpace(request.ControlPlaneEndpoint)
	}
	plan, err := inventory.PlanInventory(inventory.PlanRequest{
		Inventory:       inv,
		InitNode:        request.InitNode,
		AddressOverride: request.AddressOverrides,
	})
	if err != nil {
		return Result{}, err
	}
	result := Result{Plan: plan, DryRun: request.DryRun}
	result.addPhase("plan", "", "", "passed")
	if err := rejectUnsupportedControlPlaneJoin(plan); err != nil {
		result.addPhase("control-plane-join", "", inventory.ActionControlPlaneJoin, "failed")
		return result, err
	}
	if deps.ReadinessChecker == nil {
		return result, errors.New("bootstrap readiness checker is required")
	}
	report, err := inventory.VerifyReadiness(ctx, plan, deps.ReadinessChecker)
	if err != nil {
		result.addPhase("readiness", "", "", "failed")
		return result, err
	}
	result.Readiness = report
	if err := inventory.Error(report); err != nil {
		result.addPhase("readiness", "", "", "failed")
		return result, err
	}
	result.addPhase("readiness", "", "", "passed")
	if request.DryRun {
		result.addPhase("dry-run", "", "", "passed")
		return result, nil
	}
	if deps.NodeRunner == nil {
		return result, errors.New("bootstrap node runner is required")
	}
	initNode, err := findInitNode(plan)
	if err != nil {
		return result, err
	}
	credentials, err := deps.NodeRunner.RunKubeadmInit(ctx, initNode)
	if err != nil {
		result.addPhase("kubeadm-init", initNode.Name, inventory.ActionInit, "failed")
		return result, fmt.Errorf("kubeadm init on %s: %s", initNode.Name, inventory.Redact(err.Error()))
	}
	result.addPhase("kubeadm-init", initNode.Name, inventory.ActionInit, "passed")
	if err := deps.NodeRunner.WaitAPIReady(ctx, initNode); err != nil {
		result.addPhase("api-ready", initNode.Name, "", "failed")
		return result, fmt.Errorf("wait for API readiness on %s: %s", initNode.Name, inventory.Redact(err.Error()))
	}
	result.addPhase("api-ready", initNode.Name, "", "passed")

	workers := workerNodes(plan)
	if len(workers) > 0 {
		material, err := deps.NodeRunner.CreateWorkerJoin(ctx, initNode)
		if err != nil {
			result.addPhase("join-material", initNode.Name, "", "failed")
			return result, fmt.Errorf("create worker join material: %s", inventory.Redact(err.Error()))
		}
		result.addPhase("join-material", initNode.Name, "", "passed")
		for _, node := range workers {
			if err := deps.NodeRunner.RunWorkerJoin(ctx, node, material); err != nil {
				result.addPhase("worker-join", node.Name, inventory.ActionWorkerJoin, "failed")
				return result, fmt.Errorf("worker join on %s: %s", node.Name, inventory.Redact(err.Error()))
			}
			result.addPhase("worker-join", node.Name, inventory.ActionWorkerJoin, "passed")
		}
	}
	if err := deps.NodeRunner.WaitAPIReady(ctx, initNode); err != nil {
		result.addPhase("api-ready-after-join", initNode.Name, "", "failed")
		return result, fmt.Errorf("wait for API readiness after joins on %s: %s", initNode.Name, inventory.Redact(err.Error()))
	}
	result.addPhase("api-ready-after-join", initNode.Name, "", "passed")

	kubeconfigResult, err := kubeconfig.Write(kubeconfig.Request{
		Path:      request.KubeconfigOut,
		Overwrite: request.OverwriteKubeconfig,
		Endpoint: kubeconfig.EndpointSelection{
			InitialEndpoint:      endpointForNode(initNode),
			ControlPlaneEndpoint: plan.ControlPlaneEndpoint,
		},
		ClusterName:              valueOrDefault(request.ClusterName, "katl"),
		ContextName:              valueOrDefault(request.ContextName, "katl"),
		UserName:                 valueOrDefault(request.UserName, "katl-admin"),
		CertificateAuthorityData: credentials.CertificateAuthorityData,
		ClientCertificateData:    credentials.ClientCertificateData,
		ClientKeyData:            credentials.ClientKeyData,
	})
	if err != nil {
		result.addPhase("kubeconfig", "", "", "failed")
		return result, err
	}
	result.Kubeconfig = kubeconfigResult
	result.NextStep = kubeconfigResult.NextStep()
	result.addPhase("kubeconfig", "", "", "passed")
	return result, nil
}

func (r *Result) addPhase(name, node string, action inventory.BootstrapAction, status string) {
	r.Phases = append(r.Phases, Phase{Name: name, Node: node, Action: action, Status: status})
}

func rejectUnsupportedControlPlaneJoin(plan inventory.Plan) error {
	for _, node := range plan.Nodes {
		if node.Action == inventory.ActionControlPlaneJoin {
			return fmt.Errorf("control-plane join for node %q is not implemented yet", node.Name)
		}
	}
	return nil
}

func findInitNode(plan inventory.Plan) (inventory.PlannedNode, error) {
	for _, node := range plan.Nodes {
		if node.Action == inventory.ActionInit {
			return node, nil
		}
	}
	return inventory.PlannedNode{}, fmt.Errorf("plan has no init node")
}

func workerNodes(plan inventory.Plan) []inventory.PlannedNode {
	var nodes []inventory.PlannedNode
	for _, node := range plan.Nodes {
		if node.Action == inventory.ActionWorkerJoin {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func endpointForNode(node inventory.PlannedNode) string {
	if hasPort(node.Address) {
		return node.Address
	}
	return net.JoinHostPort(node.Address, defaultAPIPort)
}

func hasPort(value string) bool {
	_, _, err := net.SplitHostPort(value)
	return err == nil
}

func valueOrDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

type TransportRunner struct {
	Transport       readiness.CommandTransport
	Timeout         time.Duration
	APITimeout      time.Duration
	APIPollInterval time.Duration
	OutputLimit     uint32
	FileLimit       uint32
}

func (r TransportRunner) RunKubeadmInit(ctx context.Context, node inventory.PlannedNode) (AdminCredentials, error) {
	result, err := r.run(ctx, node, []string{"kubeadm", "init", "--config", kubeadmConfigPath(node)}, true)
	if err != nil {
		if result.ExitStatus == 0 || !alreadyInitialized(result) {
			return AdminCredentials{}, err
		}
	}
	transport := r.transport()
	if transport == nil {
		return AdminCredentials{}, errors.New("bootstrap command transport is required")
	}
	file, err := transport.ReadFile(ctx, node, readiness.FileRequest{
		Path:      adminKubeconfigPath,
		Timeout:   r.timeout(),
		MaxBytes:  r.fileLimit(),
		Sensitive: true,
	})
	if err != nil {
		return AdminCredentials{}, err
	}
	return parseAdminCredentials(file.Content)
}

func (r TransportRunner) CreateWorkerJoin(ctx context.Context, initNode inventory.PlannedNode) (JoinMaterial, error) {
	result, err := r.run(ctx, initNode, []string{"kubeadm", "token", "create", "--print-join-command", "--kubeconfig", adminKubeconfigPath}, true)
	if err != nil {
		return JoinMaterial{}, err
	}
	argv := strings.Fields(strings.TrimSpace(result.Stdout))
	if len(argv) < 2 || argv[0] != "kubeadm" || argv[1] != "join" {
		return JoinMaterial{}, errors.New("kubeadm did not print a worker join command")
	}
	return JoinMaterial{Argv: argv}, nil
}

func (r TransportRunner) RunWorkerJoin(ctx context.Context, node inventory.PlannedNode, material JoinMaterial) error {
	if len(material.Argv) == 0 {
		return errors.New("worker join material is required")
	}
	argv := append([]string(nil), material.Argv...)
	argv = append(argv, "--config", kubeadmConfigPath(node))
	result, err := r.run(ctx, node, argv, true)
	if err != nil && (!alreadyJoined(result) || !r.workerJoinComplete(ctx, node)) {
		return err
	}
	return nil
}

func (r TransportRunner) WaitAPIReady(ctx context.Context, initNode inventory.PlannedNode) error {
	timeout := r.apiTimeout()
	interval := r.apiPollInterval()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if err := r.runSensitive(ctx, initNode, []string{"kubectl", "--kubeconfig", adminKubeconfigPath, "get", "--raw=/readyz"}); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("wait for API readyz: %w", lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

func (r TransportRunner) workerJoinComplete(ctx context.Context, node inventory.PlannedNode) bool {
	for _, argv := range [][]string{
		{"test", "-f", "/etc/kubernetes/kubelet.conf"},
		{"systemctl", "is-active", "--quiet", "kubelet.service"},
	} {
		if _, err := r.run(ctx, node, argv, false); err != nil {
			return false
		}
	}
	return true
}

func (r TransportRunner) runSensitive(ctx context.Context, node inventory.PlannedNode, argv []string) error {
	_, err := r.run(ctx, node, argv, true)
	return err
}

func (r TransportRunner) run(ctx context.Context, node inventory.PlannedNode, argv []string, sensitive bool) (readiness.CommandResult, error) {
	transport := r.transport()
	if transport == nil {
		return readiness.CommandResult{}, errors.New("bootstrap command transport is required")
	}
	result, err := transport.RunCommand(ctx, node, readiness.CommandRequest{
		Argv:            argv,
		Timeout:         r.timeout(),
		StdoutLimit:     r.outputLimit(),
		StderrLimit:     r.outputLimit(),
		SensitiveOutput: sensitive,
	})
	if err != nil {
		return result, err
	}
	if result.ExitStatus != 0 {
		return result, commandError(argv, result)
	}
	return result, nil
}

func (r TransportRunner) transport() readiness.CommandTransport {
	return r.Transport
}

func (r TransportRunner) timeout() time.Duration {
	if r.Timeout != 0 {
		return r.Timeout
	}
	return 5 * time.Minute
}

func (r TransportRunner) apiTimeout() time.Duration {
	if r.APITimeout != 0 {
		return r.APITimeout
	}
	return 3 * time.Minute
}

func (r TransportRunner) apiPollInterval() time.Duration {
	if r.APIPollInterval != 0 {
		return r.APIPollInterval
	}
	return 2 * time.Second
}

func (r TransportRunner) outputLimit() uint32 {
	if r.OutputLimit != 0 {
		return r.OutputLimit
	}
	return 256 << 10
}

func (r TransportRunner) fileLimit() uint32 {
	if r.FileLimit != 0 {
		return r.FileLimit
	}
	return 512 << 10
}

func commandError(argv []string, result readiness.CommandResult) error {
	parts := []string{fmt.Sprintf("%q exited %d", inventory.Redact(strings.Join(argv, " ")), result.ExitStatus)}
	if strings.TrimSpace(result.Stdout) != "" {
		parts = append(parts, "stdout: "+inventory.Redact(strings.TrimSpace(result.Stdout)))
	}
	if strings.TrimSpace(result.Stderr) != "" {
		parts = append(parts, "stderr: "+inventory.Redact(strings.TrimSpace(result.Stderr)))
	}
	return errors.New(strings.Join(parts, "; "))
}

func alreadyInitialized(result readiness.CommandResult) bool {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(text, "already initialized")
}

func alreadyJoined(result readiness.CommandResult) bool {
	text := strings.ToLower(result.Stdout + "\n" + result.Stderr)
	return strings.Contains(text, "already joined")
}

func kubeadmConfigPath(node inventory.PlannedNode) string {
	if strings.TrimSpace(node.KubeadmConfig.Path) != "" {
		return node.KubeadmConfig.Path
	}
	return "/etc/katl/kubeadm/" + node.KubeadmConfig.Ref + "/config.yaml"
}

func parseAdminCredentials(data []byte) (AdminCredentials, error) {
	var parsed struct {
		Clusters []struct {
			Cluster struct {
				CertificateAuthorityData string `yaml:"certificate-authority-data"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users []struct {
			User struct {
				ClientCertificateData string `yaml:"client-certificate-data"`
				ClientKeyData         string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return AdminCredentials{}, fmt.Errorf("parse admin kubeconfig: %w", err)
	}
	if len(parsed.Clusters) == 0 || len(parsed.Users) == 0 {
		return AdminCredentials{}, errors.New("admin kubeconfig is missing cluster or user data")
	}
	credentials := AdminCredentials{
		CertificateAuthorityData: strings.TrimSpace(parsed.Clusters[0].Cluster.CertificateAuthorityData),
		ClientCertificateData:    strings.TrimSpace(parsed.Users[0].User.ClientCertificateData),
		ClientKeyData:            strings.TrimSpace(parsed.Users[0].User.ClientKeyData),
	}
	if credentials.CertificateAuthorityData == "" || credentials.ClientCertificateData == "" || credentials.ClientKeyData == "" {
		return AdminCredentials{}, errors.New("admin kubeconfig is missing embedded credential data")
	}
	return credentials, nil
}
