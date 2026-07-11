package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	installstatus "github.com/katl-dev/katl/internal/installer/status"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "katl-runtime-status: %v\n", err)
		os.Exit(1)
	}
}

func run(_ context.Context, args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("katl-runtime-status", flag.ContinueOnError)
	root := flags.String("root", "/", "runtime root containing /var/lib/katl")
	if err := flags.Parse(args); err != nil {
		return err
	}

	path, err := installstatus.RuntimeStatusPath(*root)
	if err != nil {
		return err
	}
	record, err := installstatus.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			record = installstatus.New(installstatus.StateRuntimeFailedNeedsRepair, installstatusClock())
			cause := fmt.Errorf("install status is missing at %s", path)
			if writeErr := installstatus.WriteRuntimeFailure(*root, record, cause); writeErr != nil {
				return errors.Join(cause, writeErr)
			}
			return cause
		}
		return err
	}
	if err := installstatus.WriteRuntimeHandoff(*root, record); err != nil {
		if writeErr := installstatus.WriteRuntimeFailure(*root, record, err); writeErr != nil {
			return errors.Join(err, writeErr)
		}
		return err
	}
	if stdout != nil {
		fmt.Fprintf(stdout, "katl-runtime-status state=%s path=%s\n", installstatus.StateWaitingForClusterBootstrap, path)
	}
	return nil
}

var installstatusClock = func() time.Time {
	return time.Now().UTC()
}
