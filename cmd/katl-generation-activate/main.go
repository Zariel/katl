package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/katl-dev/katl/internal/installer/generation"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "katl-generation-activate: %v\n", err)
		os.Exit(1)
	}
}

func run(_ context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("katl-generation-activate", flag.ContinueOnError)
	root := flags.String("root", "/", "runtime root containing /var/lib/katl")
	generationID := flags.String("generation", "", "selected generation id; defaults to katl.generation from cmdline")
	cmdline := flags.String("cmdline", "/proc/cmdline", "kernel command line path")
	if err := flags.Parse(args); err != nil {
		return err
	}

	selected := *generationID
	if selected == "" {
		data, err := os.ReadFile(*cmdline)
		if err != nil {
			return fmt.Errorf("read kernel command line: %w", err)
		}
		selected, err = generation.SelectedGenerationFromCommandLine(string(data))
		if err != nil {
			return err
		}
	}
	metadataPath, err := generation.MetadataPath(*root, selected)
	if err != nil {
		return err
	}
	record, err := generation.ReadRecord(metadataPath)
	if err != nil {
		return err
	}
	if record.GenerationID != selected {
		return fmt.Errorf("metadata generation %q does not match selected generation %q", record.GenerationID, selected)
	}
	plan, err := generation.ApplyActivation(*root, record)
	if err != nil {
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "katl-generation-activate generation=%s sysexts=%d confexts=%d\n", plan.GenerationID, len(plan.Sysexts), len(plan.Confexts))
	}
	return nil
}
