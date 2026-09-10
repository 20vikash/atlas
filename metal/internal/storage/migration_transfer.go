package storage

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// commandRunner runs the non-streaming ZFS commands. A test injects a fake, so a
// focused transfer test needs no host ZFS. The streaming send and receive use
// exec directly and are covered by the integration test.
type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) error
	Output(ctx context.Context, name string, args ...string) (string, error)
}

// platformRunner runs commands on the host.
type platformRunner struct{}

func (platformRunner) Run(ctx context.Context, name string, args ...string) error {
	return platform.Run(ctx, name, args...)
}

func (platformRunner) Output(ctx context.Context, name string, args ...string) (string, error) {
	return platform.Output(ctx, name, args...)
}

// MigrationTransfer runs the ZFS operations of one VM disk migration: snapshot,
// estimate, resumable send and receive, resume token, GUID, and cleanup.
type MigrationTransfer struct {
	pool   *ZFSPool
	runner commandRunner
}

// NewMigrationTransfer returns a transfer helper for one ZFS pool.
func NewMigrationTransfer(pool *ZFSPool) *MigrationTransfer {
	return &MigrationTransfer{pool: pool, runner: platformRunner{}}
}

// CreateSnapshot creates one migration snapshot. A repeat is safe.
func (transfer *MigrationTransfer) CreateSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error {
	err := transfer.runner.Run(ctx, "zfs", "snapshot", transfer.pool.snapshot(virtualMachineID, snapshotName))
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// RemoveSnapshot destroys one migration snapshot. A missing snapshot is not an error.
func (transfer *MigrationTransfer) RemoveSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error {
	err := transfer.runner.Run(ctx, "zfs", "destroy", transfer.pool.snapshot(virtualMachineID, snapshotName))
	if err != nil && strings.Contains(err.Error(), "does not exist") {
		return nil
	}
	return err
}

// SnapshotGUID returns the GUID of one snapshot. The target compares it after a
// receive, so a silent corruption cannot pass as a complete interval.
func (transfer *MigrationTransfer) SnapshotGUID(ctx context.Context, virtualMachineID, snapshotName string) (string, error) {
	output, err := transfer.runner.Output(ctx, "zfs", "get", "-Hp", "-o", "value", "guid", transfer.pool.snapshot(virtualMachineID, snapshotName))
	if err != nil {
		return "", notFoundAware(err)
	}
	guid := strings.TrimSpace(output)
	if guid == "" || guid == "-" {
		return "", fmt.Errorf("snapshot %s has no GUID", snapshotName)
	}
	return guid, nil
}

// EstimateStreamBytes returns the send size in bytes. An empty base estimates a
// full stream, and a base name estimates the incremental stream after it.
func (transfer *MigrationTransfer) EstimateStreamBytes(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName string) (int64, error) {
	output, err := transfer.runner.Output(ctx, "zfs", transfer.sendArguments(virtualMachineID, snapshotName, baseSnapshotName, "", true)...)
	if err != nil {
		return 0, notFoundAware(err)
	}
	return parseSendSizeBytes(output)
}

// TargetDatasetExists reports whether the target VM dataset is already present.
// The first receive uses this to refuse an unrelated existing dataset.
func (transfer *MigrationTransfer) TargetDatasetExists(ctx context.Context, virtualMachineID string) (bool, error) {
	err := transfer.runner.Run(ctx, "zfs", "list", transfer.pool.virtualMachineDataset(virtualMachineID))
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "does not exist") {
		return false, nil
	}
	return false, fmt.Errorf("check target dataset: %w", err)
}

// ReceiveResumeToken returns the token that resumes an interrupted receive. It
// returns an empty string when the dataset is absent or holds no token.
func (transfer *MigrationTransfer) ReceiveResumeToken(ctx context.Context, virtualMachineID string) (string, error) {
	output, err := transfer.runner.Output(ctx, "zfs", "get", "-Hp", "-o", "value", "receive_resume_token", transfer.pool.virtualMachineDataset(virtualMachineID))
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return "", nil
		}
		return "", err
	}
	token := strings.TrimSpace(output)
	if token == "-" {
		return "", nil
	}
	return token, nil
}

// SendSnapshot streams one snapshot to w and returns the byte count. With a
// resume token it continues an interrupted send. With a base name it sends the
// incremental stream. Otherwise it sends the full stream.
func (transfer *MigrationTransfer) SendSnapshot(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName, resumeToken string, w io.Writer) (int64, error) {
	command := exec.CommandContext(ctx, "zfs", transfer.sendArguments(virtualMachineID, snapshotName, baseSnapshotName, resumeToken, false)...)
	var sendError strings.Builder
	command.Stderr = &sendError
	stdout, err := command.StdoutPipe()
	if err != nil {
		return 0, err
	}
	if err := command.Start(); err != nil {
		return 0, err
	}
	written, copyError := io.Copy(w, stdout)
	if waitError := command.Wait(); waitError != nil {
		return written, fmt.Errorf("zfs send: %w: %s", waitError, strings.TrimSpace(sendError.String()))
	}
	if copyError != nil {
		return written, copyError
	}
	return written, nil
}

// ReceiveSnapshot receives one stream into the target VM dataset. The -s flag
// saves a resume token if the receive is interrupted.
func (transfer *MigrationTransfer) ReceiveSnapshot(ctx context.Context, virtualMachineID string, r io.Reader) error {
	command := exec.CommandContext(ctx, "zfs", "recv", "-s", transfer.pool.virtualMachineDataset(virtualMachineID))
	command.Stdin = r
	var receiveError strings.Builder
	command.Stderr = &receiveError
	if err := command.Run(); err != nil {
		return fmt.Errorf("zfs receive: %w: %s", err, strings.TrimSpace(receiveError.String()))
	}
	return nil
}

// sendArguments builds the zfs send arguments. The estimate flag adds the dry
// run flags, so one place decides the full, incremental, and resume forms.
func (transfer *MigrationTransfer) sendArguments(virtualMachineID, snapshotName, baseSnapshotName, resumeToken string, estimate bool) []string {
	arguments := []string{"send"}
	if estimate {
		arguments = append(arguments, "-nP")
	}
	if resumeToken != "" {
		return append(arguments, "-t", resumeToken)
	}
	if baseSnapshotName != "" {
		arguments = append(arguments, "-i", transfer.pool.snapshot(virtualMachineID, baseSnapshotName))
	}
	return append(arguments, transfer.pool.snapshot(virtualMachineID, snapshotName))
}

// parseSendSizeBytes reads the size line of `zfs send -nP` output.
func parseSendSizeBytes(output string) (int64, error) {
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "size" {
			return strconv.ParseInt(fields[1], 10, 64)
		}
	}
	return 0, fmt.Errorf("zfs send estimate has no size")
}
