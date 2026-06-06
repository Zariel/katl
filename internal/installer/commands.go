package installer

import (
	"context"
	"os/exec"
	"strings"
)

// CommandRunner is the boundary between typed installer state and host tools.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type OutputCommandRunner interface {
	Output(ctx context.Context, name string, args ...string) ([]byte, error)
}

type ExecCommandRunner struct{}

func NewExecCommandRunner() ExecCommandRunner {
	return ExecCommandRunner{}
}

func (ExecCommandRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Run()
}

func (ExecCommandRunner) RunInput(ctx context.Context, input string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	return cmd.Run()
}

func (ExecCommandRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

type NoopCommandRunner struct {
	Calls []CommandCall
}

type CommandCall struct {
	Name string
	Args []string
}

func (r *NoopCommandRunner) Run(_ context.Context, name string, args ...string) error {
	r.Calls = append(r.Calls, CommandCall{Name: name, Args: append([]string(nil), args...)})
	return nil
}
