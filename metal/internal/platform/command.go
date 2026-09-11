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

// CombinedOutput runs a host command and returns its combined stdout and stderr,
// including the output when the command fails. Use it when a command writes the
// value to read on stderr, such as a verbose dry run.
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
