package workstation

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/katl-dev/katl/internal/bootstrap/inventory"
	"gopkg.in/yaml.v3"
)

type Config struct {
	CurrentContext string    `json:"currentContext" yaml:"currentContext"`
	Contexts       []Context `json:"contexts" yaml:"contexts"`
	Clusters       []Cluster `json:"clusters" yaml:"clusters"`
}

type Context struct {
	Name    string `json:"name" yaml:"name"`
	Cluster string `json:"cluster" yaml:"cluster"`
}

type Cluster struct {
	Name                 string `json:"name" yaml:"name"`
	ControlPlaneEndpoint string `json:"controlPlaneEndpoint,omitempty" yaml:"controlPlaneEndpoint"`
	Nodes                []Node `json:"nodes" yaml:"nodes"`
}

type Node struct {
	Name               string               `json:"name" yaml:"name"`
	ManagementEndpoint string               `json:"managementEndpoint" yaml:"managementEndpoint"`
	SystemRole         inventory.SystemRole `json:"systemRole" yaml:"systemRole"`
	CredentialRef      string               `json:"credentialRef" yaml:"credentialRef"`
}

type Source string

const (
	SourceConfigContext     Source = "config-context"
	SourceExplicitInventory Source = "explicit-inventory"
	SourceExplicitPlan      Source = "explicit-plan"
)

type Topology struct {
	ContextName          string         `json:"contextName,omitempty"`
	ClusterName          string         `json:"clusterName"`
	ControlPlaneEndpoint string         `json:"controlPlaneEndpoint,omitempty"`
	Nodes                []TopologyNode `json:"nodes"`
}

type TopologyNode struct {
	Name               string               `json:"name"`
	ManagementEndpoint string               `json:"managementEndpoint"`
	SystemRole         inventory.SystemRole `json:"systemRole"`
	CredentialRef      string               `json:"credentialRef"`
}

type ResolvedTopology struct {
	Source Source `json:"source"`
	Topology
}

type ResolveRequest struct {
	ConfigPath        string
	ContextName       string
	ExplicitInventory *inventory.Inventory
	ExplicitPlan      *inventory.Plan
}

func ConfigPath() (string, error) {
	return ResolvePath(os.Getenv, os.UserConfigDir)
}

func ResolvePath(getenv func(string) string, userConfigDir func() (string, error)) (string, error) {
	if path := strings.TrimSpace(getenv("KATLCTL_CONFIG")); path != "" {
		return filepath.Clean(path), nil
	}
	if dir := strings.TrimSpace(getenv("KATLCTL_CONFIG_DIR")); dir != "" {
		return filepath.Join(filepath.Clean(dir), "katlctl.yaml"), nil
	}
	dir, err := userConfigDir()
	if err != nil {
		return "", fmt.Errorf("locate katlctl config directory: %w", err)
	}
	return filepath.Join(dir, "katl", "katlctl.yaml"), nil
}

func Load(path string) (Config, error) {
	data, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return Config{}, fmt.Errorf("read katlctl config: %w", err)
	}
	var cfg Config
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode katlctl config: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return Config{}, fmt.Errorf("decode katlctl config: multiple YAML documents are not supported")
	} else if err != io.EOF {
		return Config{}, fmt.Errorf("decode katlctl config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (cfg Config) Validate() error {
	contexts := make(map[string]Context, len(cfg.Contexts))
	for _, ctx := range cfg.Contexts {
		ctx.Name = strings.TrimSpace(ctx.Name)
		ctx.Cluster = strings.TrimSpace(ctx.Cluster)
		if err := validateName("context", ctx.Name); err != nil {
			return err
		}
		if ctx.Cluster == "" {
			return fmt.Errorf("context %q cluster is required", ctx.Name)
		}
		if _, ok := contexts[ctx.Name]; ok {
			return fmt.Errorf("duplicate context name %q", ctx.Name)
		}
		contexts[ctx.Name] = ctx
	}
	clusters := make(map[string]Cluster, len(cfg.Clusters))
	for _, cluster := range cfg.Clusters {
		cluster.Name = strings.TrimSpace(cluster.Name)
		if err := validateName("cluster", cluster.Name); err != nil {
			return err
		}
		if _, ok := clusters[cluster.Name]; ok {
			return fmt.Errorf("duplicate cluster name %q", cluster.Name)
		}
		if err := validateCluster(cluster); err != nil {
			return err
		}
		clusters[cluster.Name] = cluster
	}
	for _, ctx := range contexts {
		if _, ok := clusters[ctx.Cluster]; !ok {
			return fmt.Errorf("context %q references unknown cluster %q", ctx.Name, ctx.Cluster)
		}
	}
	currentContext := strings.TrimSpace(cfg.CurrentContext)
	if currentContext == "" {
		return fmt.Errorf("currentContext is required")
	}
	if _, ok := contexts[currentContext]; !ok {
		return fmt.Errorf("currentContext %q references unknown context", currentContext)
	}
	return nil
}

func (cfg Config) SelectedTopology(contextName string) (Topology, error) {
	if err := cfg.Validate(); err != nil {
		return Topology{}, err
	}
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		contextName = strings.TrimSpace(cfg.CurrentContext)
	}
	contexts := make(map[string]Context, len(cfg.Contexts))
	for _, ctx := range cfg.Contexts {
		ctx.Name = strings.TrimSpace(ctx.Name)
		ctx.Cluster = strings.TrimSpace(ctx.Cluster)
		contexts[ctx.Name] = ctx
	}
	ctx, ok := contexts[contextName]
	if !ok {
		return Topology{}, fmt.Errorf("context %q was not found", contextName)
	}
	clusters := make(map[string]Cluster, len(cfg.Clusters))
	for _, cluster := range cfg.Clusters {
		cluster.Name = strings.TrimSpace(cluster.Name)
		clusters[cluster.Name] = cluster
	}
	cluster := clusters[ctx.Cluster]
	return topologyFromCluster(ctx.Name, cluster)
}

func ResolveTopology(req ResolveRequest) (ResolvedTopology, error) {
	if req.ExplicitPlan != nil {
		topology, err := topologyFromPlan(*req.ExplicitPlan)
		if err != nil {
			return ResolvedTopology{}, err
		}
		return ResolvedTopology{Source: SourceExplicitPlan, Topology: topology}, nil
	}
	if req.ExplicitInventory != nil {
		topology, err := topologyFromInventory(*req.ExplicitInventory)
		if err != nil {
			return ResolvedTopology{}, err
		}
		return ResolvedTopology{Source: SourceExplicitInventory, Topology: topology}, nil
	}
	path := strings.TrimSpace(req.ConfigPath)
	if path == "" {
		resolved, err := ConfigPath()
		if err != nil {
			return ResolvedTopology{}, err
		}
		path = resolved
	}
	cfg, err := Load(path)
	if err != nil {
		return ResolvedTopology{}, err
	}
	topology, err := cfg.SelectedTopology(req.ContextName)
	if err != nil {
		return ResolvedTopology{}, err
	}
	return ResolvedTopology{Source: SourceConfigContext, Topology: topology}, nil
}

func (t Topology) ControlPlaneNodes() []TopologyNode {
	var nodes []TopologyNode
	for _, node := range t.Nodes {
		if node.SystemRole == inventory.RoleControlPlane {
			nodes = append(nodes, node)
		}
	}
	return nodes
}

func topologyFromCluster(contextName string, cluster Cluster) (Topology, error) {
	if err := validateCluster(cluster); err != nil {
		return Topology{}, err
	}
	topology := Topology{
		ContextName:          strings.TrimSpace(contextName),
		ClusterName:          strings.TrimSpace(cluster.Name),
		ControlPlaneEndpoint: strings.TrimSpace(cluster.ControlPlaneEndpoint),
		Nodes:                make([]TopologyNode, 0, len(cluster.Nodes)),
	}
	for _, node := range cluster.Nodes {
		topology.Nodes = append(topology.Nodes, TopologyNode{
			Name:               strings.TrimSpace(node.Name),
			ManagementEndpoint: strings.TrimSpace(node.ManagementEndpoint),
			SystemRole:         inventory.SystemRole(strings.TrimSpace(string(node.SystemRole))),
			CredentialRef:      strings.TrimSpace(node.CredentialRef),
		})
	}
	return topology, nil
}

func topologyFromInventory(inv inventory.Inventory) (Topology, error) {
	cluster := Cluster{
		Name:                 "inventory",
		ControlPlaneEndpoint: inv.ControlPlaneEndpoint,
		Nodes:                make([]Node, 0, len(inv.Nodes)),
	}
	for _, node := range inv.Nodes {
		cluster.Nodes = append(cluster.Nodes, Node{
			Name:               node.Name,
			ManagementEndpoint: managementEndpoint(node.Address),
			SystemRole:         node.SystemRole,
			CredentialRef:      node.Access.CredentialRef,
		})
	}
	return topologyFromCluster("", cluster)
}

func topologyFromPlan(plan inventory.Plan) (Topology, error) {
	cluster := Cluster{
		Name:                 "plan",
		ControlPlaneEndpoint: plan.ControlPlaneEndpoint,
		Nodes:                make([]Node, 0, len(plan.Nodes)),
	}
	for _, node := range plan.Nodes {
		cluster.Nodes = append(cluster.Nodes, Node{
			Name:               node.Name,
			ManagementEndpoint: managementEndpoint(node.Address),
			SystemRole:         node.SystemRole,
			CredentialRef:      node.Access.CredentialRef,
		})
	}
	return topologyFromCluster("", cluster)
}

func validateCluster(cluster Cluster) error {
	if strings.TrimSpace(cluster.ControlPlaneEndpoint) != "" {
		if err := validateEndpoint("cluster "+strings.TrimSpace(cluster.Name)+" controlPlaneEndpoint", cluster.ControlPlaneEndpoint); err != nil {
			return err
		}
	}
	if len(cluster.Nodes) == 0 {
		return fmt.Errorf("cluster %q must contain at least one node", strings.TrimSpace(cluster.Name))
	}
	seen := make(map[string]struct{}, len(cluster.Nodes))
	controlPlanes := 0
	for _, node := range cluster.Nodes {
		name := strings.TrimSpace(node.Name)
		if err := validateName("node", name); err != nil {
			return err
		}
		if _, ok := seen[name]; ok {
			return fmt.Errorf("cluster %q has duplicate node name %q", strings.TrimSpace(cluster.Name), name)
		}
		seen[name] = struct{}{}
		endpoint := strings.TrimSpace(node.ManagementEndpoint)
		if endpoint == "" {
			return fmt.Errorf("node %q managementEndpoint is required", name)
		}
		if err := validateEndpoint("node "+name+" managementEndpoint", endpoint); err != nil {
			return err
		}
		role := inventory.SystemRole(strings.TrimSpace(string(node.SystemRole)))
		switch role {
		case inventory.RoleControlPlane:
			controlPlanes++
		case inventory.RoleWorker:
		default:
			return fmt.Errorf("node %q systemRole %q is unsupported", name, node.SystemRole)
		}
		credentialRef := strings.TrimSpace(node.CredentialRef)
		if credentialRef == "" {
			return fmt.Errorf("node %q credentialRef is required", name)
		}
		if strings.ContainsAny(credentialRef, "\n\r") {
			return fmt.Errorf("node %q credentialRef must be a single line", name)
		}
		if containsInlineSecret(credentialRef) {
			return fmt.Errorf("node %q credentialRef must reference credentials, not inline secret material", name)
		}
	}
	if controlPlanes == 0 {
		return fmt.Errorf("cluster %q must contain at least one control-plane node", strings.TrimSpace(cluster.Name))
	}
	return nil
}

func validateName(kind, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s name is required", kind)
	}
	if len(value) > 63 || !namePattern.MatchString(value) {
		return fmt.Errorf("%s name %q must be a single DNS-style label", kind, value)
	}
	return nil
}

func validateEndpoint(field, endpoint string) error {
	endpoint = strings.TrimSpace(endpoint)
	if strings.Contains(endpoint, "://") {
		return fmt.Errorf("%s must be host:port", field)
	}
	host, portText, err := net.SplitHostPort(endpoint)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(portText) == "" {
		return fmt.Errorf("%s must be host:port", field)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%s port must be a number from 1 to 65535", field)
	}
	return nil
}

func containsInlineSecret(value string) bool {
	return strings.Contains(value, "-----BEGIN ") ||
		bootstrapTokenPattern.MatchString(value) ||
		bearerTokenPattern.MatchString(value) ||
		kubeconfigDataPattern.MatchString(value)
}

func managementEndpoint(address string) string {
	address = strings.TrimSpace(address)
	if address == "" {
		return address
	}
	if _, _, err := net.SplitHostPort(address); err == nil {
		return address
	}
	return net.JoinHostPort(address, defaultAgentPort)
}

var (
	namePattern           = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	bootstrapTokenPattern = regexp.MustCompile(`\b[a-z0-9]{6}\.[a-z0-9]{16}\b`)
	bearerTokenPattern    = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	kubeconfigDataPattern = regexp.MustCompile(`(?i)client-(certificate|key)-data:\s*\S+`)
)

const defaultAgentPort = "9443"
