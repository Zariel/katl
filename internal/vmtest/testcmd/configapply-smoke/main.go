package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/zariel/katl/internal/installer/configapply"
	"github.com/zariel/katl/internal/installer/generation"
	"github.com/zariel/katl/internal/installer/manifest"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	var root, currentGeneration, nextGeneration, nodeName, manifestPath, requestPath string
	flags := flag.NewFlagSet("configapply-smoke", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	flags.StringVar(&root, "root", "", "runtime root")
	flags.StringVar(&currentGeneration, "current-generation", "", "current generation id")
	flags.StringVar(&nextGeneration, "next-generation", "", "candidate generation id")
	flags.StringVar(&nodeName, "node", "", "node name")
	flags.StringVar(&manifestPath, "manifest", "", "current install manifest JSON")
	flags.StringVar(&requestPath, "request", "", "NodeConfigurationChange YAML")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	if root == "" || currentGeneration == "" || nextGeneration == "" || nodeName == "" || manifestPath == "" || requestPath == "" {
		return fmt.Errorf("root, current-generation, next-generation, node, manifest, and request are required")
	}

	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return fmt.Errorf("open manifest: %w", err)
	}
	defer manifestFile.Close()
	currentManifest, err := manifest.Decode(manifestFile)
	if err != nil {
		return err
	}
	metadataPath, err := generation.MetadataPath(root, currentGeneration)
	if err != nil {
		return err
	}
	currentRecord, err := generation.ReadRecord(metadataPath)
	if err != nil {
		return err
	}
	requestFile, err := os.Open(requestPath)
	if err != nil {
		return fmt.Errorf("open request: %w", err)
	}
	defer requestFile.Close()
	result, err := configapply.ApplyNodeConfigurationChange(context.Background(), requestFile, configapply.TrustedBundleRequest{
		Root:            root,
		NodeName:        nodeName,
		GenerationID:    nextGeneration,
		CurrentManifest: currentManifest,
		CurrentRecord:   currentRecord,
		Chown:           func(string, int, int) error { return nil },
	})
	if err != nil {
		return err
	}
	summary := struct {
		AuditPath    string `json:"auditPath"`
		MetadataPath string `json:"metadataPath,omitempty"`
		StatusPath   string `json:"statusPath,omitempty"`
	}{
		AuditPath:    result.AuditPath,
		MetadataPath: result.MetadataPath,
		StatusPath:   result.StatusPath,
	}
	if err := json.NewEncoder(os.Stdout).Encode(summary); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	return nil
}
