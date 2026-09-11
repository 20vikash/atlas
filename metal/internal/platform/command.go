package platform

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Run executes a host command and includes its output when the command fails.
func Run(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)

	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}

	return nil
}

// RunInNetworkNamespace executes a command in a named network namespace.
func RunInNetworkNamespace(ctx context.Context, namespace, name string, args ...string) (string, error) {
	arguments := append([]string{"netns", "exec", namespace, name}, args...)
	return Output(ctx, "ip", arguments...)
}

// CombinedOutput returns combined command output, including failed output.
func CombinedOutput(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

// Output runs a host command and returns stdout. A failure includes stderr.
func Output(ctx context.Context, name string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer

	command := exec.CommandContext(ctx, name, args...)
	command.Stdout, command.Stderr = &stdout, &stderr

	if err := command.Run(); err != nil {
		return "", fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}

	return stdout.String(), nil
}

// Runner runs host commands. Production uses HostRunner. A test injects a fake,
// so a unit test needs no real command.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
	Output(ctx context.Context, name string, args ...string) (string, error)
	CombinedOutput(ctx context.Context, name string, args ...string) (string, error)
}

// HostRunner runs commands on this host through the package functions.
type HostRunner struct{}

// Run executes a host command.
func (HostRunner) Run(ctx context.Context, name string, args ...string) error {
	return Run(ctx, name, args...)
}

// Output runs a host command and returns stdout.
func (HostRunner) Output(ctx context.Context, name string, args ...string) (string, error) {
	return Output(ctx, name, args...)
}

// CombinedOutput runs a host command and returns its combined output.
func (HostRunner) CombinedOutput(ctx context.Context, name string, args ...string) (string, error) {
	return CombinedOutput(ctx, name, args...)
}
