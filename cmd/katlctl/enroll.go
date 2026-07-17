package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	"github.com/katl-dev/katl/internal/installer/configbundle"
	agentapi "github.com/katl-dev/katl/internal/katlc/agentapi"
	"github.com/katl-dev/katl/internal/katlctl/workstation"
	"github.com/spf13/cobra"
)

type contextSaveOptions struct {
	configInput string
	contextPath string
	contextName string
}

type contextSaveNodeReport struct {
	Name               string `json:"name"`
	ManagementEndpoint string `json:"managementEndpoint"`
	Connected          bool   `json:"connected"`
}

type contextSaveReport struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Context    string                  `json:"context"`
	ConfigPath string                  `json:"configPath"`
	Nodes      []contextSaveNodeReport `json:"nodes"`
}

func newContextSaveCommand(ctx context.Context, stdout, stderr io.Writer) *cobra.Command {
	opts := contextSaveOptions{}
	cmd := &cobra.Command{
		Use:   "save",
		Short: "Save installed KatlOS nodes as the current workstation context",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return runContextSave(ctx, opts, stdout, stderr)
		},
	}
	cmd.Flags().StringVar(&opts.configInput, "config", "", "ClusterConfig YAML or Katl config bundle")
	cmd.Flags().StringVar(&opts.contextPath, "context-file", "", "workstation context file path")
	cmd.Flags().StringVar(&opts.contextName, "context", "", "context name; defaults to the cluster name")
	return cmd
}

func runContextSave(ctx context.Context, opts contextSaveOptions, stdout, stderr io.Writer) error {
	_ = stderr
	config, err := loadKatlConfig(opts.configInput, "katlctl context save", configbundle.PlanningInputs{})
	if err != nil {
		return err
	}
	bundle := config.Bundle
	inv := bundle.Manifest.Cluster.BootstrapInventory
	configPath := strings.TrimSpace(opts.contextPath)
	if configPath == "" {
		configPath, err = workstation.ConfigPath()
		if err != nil {
			return err
		}
	}
	contextName := strings.TrimSpace(opts.contextName)
	if contextName == "" {
		contextName = bundle.Manifest.ClusterName
	}
	clusterProfile := workstation.Cluster{Name: bundle.Manifest.ClusterName, ControlPlaneEndpoint: inv.ControlPlaneEndpoint}
	report := contextSaveReport{APIVersion: "katl.dev/v1alpha1", Kind: "ContextSaveReport", Context: contextName, ConfigPath: configPath}

	for _, node := range inv.Nodes {
		endpoint := net.JoinHostPort(strings.TrimSpace(node.Address), "9443")
		conn, err := dialKatlcAgent(ctx, endpoint)
		if err != nil {
			return fmt.Errorf("verify node %s management endpoint: %w", node.Name, err)
		}
		status, statusErr := conn.Client.GetNodeStatus(ctx, &agentapi.GetNodeStatusRequest{})
		closeErr := conn.Close()
		if statusErr != nil {
			return fmt.Errorf("verify node %s management endpoint: %w", node.Name, statusErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close node %s management endpoint: %w", node.Name, closeErr)
		}
		if strings.TrimSpace(status.GetMachineId()) == "" {
			return fmt.Errorf("verify node %s management endpoint: agent did not report a machine identity", node.Name)
		}
		clusterProfile.Nodes = append(clusterProfile.Nodes, workstation.Node{
			Name: node.Name, ManagementEndpoint: endpoint, SystemRole: node.SystemRole,
		})
		report.Nodes = append(report.Nodes, contextSaveNodeReport{Name: node.Name, ManagementEndpoint: endpoint, Connected: true})
	}

	cfg := workstation.Config{}
	if existing, loadErr := workstation.Load(configPath); loadErr == nil {
		cfg = existing
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return loadErr
	}
	cfg = cfg.UpsertCluster(contextName, clusterProfile)
	if err := workstation.Save(configPath, cfg); err != nil {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode context save report: %w", err)
	}
	_, err = stdout.Write(append(data, '\n'))
	return err
}
