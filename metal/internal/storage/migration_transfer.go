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

// MigrationTransfer runs snapshot, transfer, resume, GUID, and cleanup steps.
type MigrationTransfer struct {
	pool   *ZFSPool
	runner platform.Runner
}

// NewMigrationTransfer returns a transfer helper for one ZFS pool.
func NewMigrationTransfer(pool *ZFSPool) *MigrationTransfer {
	return &MigrationTransfer{pool: pool, runner: platform.HostRunner{}}
}

// CreateSnapshot creates one migration snapshot. A repeat is safe.
func (transfer *MigrationTransfer) CreateSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error {
	// zfs snapshot: atomic read-only point-in-time copy of the VM volume.
	err := transfer.runner.Run(ctx, "zfs", "snapshot", transfer.pool.snapshot(virtualMachineID, snapshotName))
	if err != nil && strings.Contains(err.Error(), "already exists") {
		return nil
	}
	return err
}

// RemoveSnapshot destroys one migration snapshot. Missing snapshots are safe.
func (transfer *MigrationTransfer) RemoveSnapshot(ctx context.Context, virtualMachineID, snapshotName string) error {
	// zfs destroy: remove one snapshot, leaving the volume and other snapshots.
	err := transfer.runner.Run(ctx, "zfs", "destroy", transfer.pool.snapshot(virtualMachineID, snapshotName))
	if err != nil && strings.Contains(err.Error(), "does not exist") {
		return nil
	}
	return err
}

// SnapshotGUID returns a snapshot GUID for post-receive validation.
func (transfer *MigrationTransfer) SnapshotGUID(ctx context.Context, virtualMachineID, snapshotName string) (string, error) {
	// zfs get -Hp -o value guid returns the exact GUID without a header.
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

// EstimateStreamBytes returns the full or incremental send size in bytes.
func (transfer *MigrationTransfer) EstimateStreamBytes(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName string) (int64, error) {
	// zfs send -nP reports the parseable dry-run size.
	output, err := transfer.runner.Output(ctx, "zfs", transfer.sendArguments(virtualMachineID, snapshotName, baseSnapshotName, "", true)...)
	if err != nil {
		return 0, notFoundAware(err)
	}
	return parseSendSizeBytes(output)
}

// TargetDatasetExists reports whether the target dataset exists.
func (transfer *MigrationTransfer) TargetDatasetExists(ctx context.Context, virtualMachineID string) (bool, error) {
	// zfs list succeeds only when the dataset exists.
	err := transfer.runner.Run(ctx, "zfs", "list", transfer.pool.virtualMachineDataset(virtualMachineID))
	if err == nil {
		return true, nil
	}
	if strings.Contains(err.Error(), "does not exist") {
		return false, nil
	}
	return false, fmt.Errorf("check target dataset: %w", err)
}

// ReceiveResumeToken returns an interrupted receive token, or an empty string.
func (transfer *MigrationTransfer) ReceiveResumeToken(ctx context.Context, virtualMachineID string) (string, error) {
	// zfs get receive_resume_token returns the resume token; "-" means none.
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

// SendSnapshot streams a full, incremental, or resumed snapshot to w.
func (transfer *MigrationTransfer) SendSnapshot(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName, resumeToken string, w io.Writer) (int64, error) {
	if resumeToken != "" {
		if err := transfer.verifyResumeToken(ctx, virtualMachineID, snapshotName, resumeToken); err != nil {
			return 0, err
		}
	}
	// zfs send writes the replication stream; -t resumes and -i sends a delta.
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

// ReceiveSnapshot receives a stream and saves a resume token on interruption.
func (transfer *MigrationTransfer) ReceiveSnapshot(ctx context.Context, virtualMachineID string, r io.Reader) error {
	// zfs recv -s receives stdin and saves an interruption token.
	command := exec.CommandContext(ctx, "zfs", "recv", "-s", transfer.pool.virtualMachineDataset(virtualMachineID))
	command.Stdin = r
	var receiveError strings.Builder
	command.Stderr = &receiveError
	if err := command.Run(); err != nil {
		return fmt.Errorf("zfs receive: %w: %s", err, strings.TrimSpace(receiveError.String()))
	}
	return nil
}

// AbortReceive cancels an interrupted receive and removes its dataset.
func (transfer *MigrationTransfer) AbortReceive(ctx context.Context, virtualMachineID string) error {
	dataset := transfer.pool.virtualMachineDataset(virtualMachineID)
	// zfs recv -A deletes saved partial receive state.
	if err := transfer.runner.Run(ctx, "zfs", "recv", "-A", dataset); err != nil &&
		!strings.Contains(err.Error(), "does not exist") &&
		!strings.Contains(err.Error(), "does not have any resumable") {
		return fmt.Errorf("abort partial receive: %w", err)
	}
	// zfs destroy -r removes the target dataset and migration snapshots.
	if err := transfer.runner.Run(ctx, "zfs", "destroy", "-r", dataset); err != nil &&
		!strings.Contains(err.Error(), "does not exist") {
		return fmt.Errorf("remove target dataset: %w", err)
	}
	return nil
}

// verifyResumeToken rejects tokens for another migration snapshot.
func (transfer *MigrationTransfer) verifyResumeToken(ctx context.Context, virtualMachineID, snapshotName, resumeToken string) error {
	// zfs send -nvt reports the snapshot targeted by the resume token.
	output, err := transfer.runner.CombinedOutput(ctx, "zfs", "send", "-nvt", resumeToken)
	if err != nil {
		return fmt.Errorf("validate resume token: %w", err)
	}
	if !strings.Contains(output, transfer.pool.snapshot(virtualMachineID, snapshotName)) {
		return fmt.Errorf("resume token does not match snapshot %s", snapshotName)
	}
	return nil
}

// sendArguments builds full, incremental, resume, and estimate arguments.
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
