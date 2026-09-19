package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	platform "github.com/frappe/atlas/metal/internal/platform"
)

// MigrationTransfer runs snapshot, transfer, resume, GUID, and cleanup steps.
type MigrationTransfer struct {
	pool   *ZFSPool
	runner platform.Runner
	tls    MigrationTLSConfig
}

// snapshotServerReadyTimeout bounds the readiness probe on the source.
const snapshotServerReadyTimeout = 30 * time.Second

// MigrationTLSConfig configures the one-shot OpenSSL snapshot transport.
type MigrationTLSConfig struct {
	CAFile          string
	CertificateFile string
	PrivateKeyFile  string
	ListenAddress   string
	TransferPort    int
}

// NewMigrationTransfer returns a transfer helper for one ZFS pool.
func NewMigrationTransfer(pool *ZFSPool, configuration MigrationTLSConfig) *MigrationTransfer {
	return &MigrationTransfer{pool: pool, runner: platform.HostRunner{}, tls: configuration}
}

// SnapshotServer owns one source-side ZFS and OpenSSL pipeline.
type SnapshotServer struct {
	done chan error
}

// SourceStream is one running source-side snapshot transfer.
type SourceStream interface {
	Wait() error
}

// Wait waits until the source pipeline exits.
func (server *SnapshotServer) Wait() error {
	return <-server.done
}

// StartSnapshotServer starts one mutual-TLS source stream without a Go copy loop.
func (transfer *MigrationTransfer) StartSnapshotServer(ctx context.Context, virtualMachineID, snapshotName, baseSnapshotName, resumeToken string) (SourceStream, error) {
	if err := transfer.validateTLS(); err != nil {
		return nil, err
	}
	if resumeToken != "" {
		if err := transfer.verifyResumeToken(ctx, virtualMachineID, snapshotName, resumeToken); err != nil {
			return nil, err
		}
	}

	streamContext, cancel := context.WithCancel(ctx)
	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create source transfer pipe: %w", err)
	}
	serverError := &strings.Builder{}
	serverCommand := exec.CommandContext(streamContext, "openssl", transfer.serverArguments()...)
	serverCommand.Stdin = pipeReader
	serverCommand.Stdout = io.Discard
	serverCommand.Stderr = serverError
	if err := serverCommand.Start(); err != nil {
		pipeReader.Close()
		pipeWriter.Close()
		cancel()
		return nil, fmt.Errorf("start OpenSSL migration server: %w", err)
	}
	pipeReader.Close()
	if err := transfer.waitForSnapshotServer(streamContext); err != nil {
		pipeWriter.Close()
		cancel()
		_ = serverCommand.Wait()
		return nil, err
	}

	sendError := &strings.Builder{}
	sendCommand := exec.CommandContext(streamContext, "zfs", transfer.sendArguments(virtualMachineID, snapshotName, baseSnapshotName, resumeToken, false)...)
	sendCommand.Stdout = pipeWriter
	sendCommand.Stderr = sendError
	if err := sendCommand.Start(); err != nil {
		pipeWriter.Close()
		cancel()
		_ = serverCommand.Wait()
		return nil, fmt.Errorf("start zfs send: %w", err)
	}
	pipeWriter.Close()

	server := &SnapshotServer{done: make(chan error, 1)}
	go func() {
		sendWaitError := sendCommand.Wait()
		serverWaitError := serverCommand.Wait()
		cancel()
		server.done <- errors.Join(
			commandError("zfs send", sendWaitError, sendError.String()),
			commandError("OpenSSL migration server", serverWaitError, serverError.String()),
		)
	}()
	return server, nil
}

// ReceiveSnapshotTLS receives a mutual-TLS ZFS stream without a Go copy loop.
func (transfer *MigrationTransfer) ReceiveSnapshotTLS(ctx context.Context, virtualMachineID, sourceAddress string) error {
	if err := transfer.validateTLS(); err != nil {
		return err
	}
	host, err := migrationHost(sourceAddress)
	if err != nil {
		return err
	}

	pipeReader, pipeWriter, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("create target transfer pipe: %w", err)
	}
	clientError := &strings.Builder{}
	clientCommand := exec.CommandContext(ctx, "openssl", transfer.clientArguments(host, false)...)
	clientCommand.Stdout = pipeWriter
	clientCommand.Stderr = clientError
	receiveError := &strings.Builder{}
	receiveCommand := exec.CommandContext(ctx, "zfs", "recv", "-s", transfer.pool.virtualMachineDataset(virtualMachineID))
	receiveCommand.Stdin = pipeReader
	receiveCommand.Stderr = receiveError

	if err := receiveCommand.Start(); err != nil {
		pipeReader.Close()
		pipeWriter.Close()
		return fmt.Errorf("start zfs receive: %w", err)
	}
	pipeReader.Close()
	if err := clientCommand.Start(); err != nil {
		pipeWriter.Close()
		_ = receiveCommand.Process.Kill()
		_ = receiveCommand.Wait()
		return fmt.Errorf("start OpenSSL migration client: %w", err)
	}
	pipeWriter.Close()

	clientWaitError := clientCommand.Wait()
	receiveWaitError := receiveCommand.Wait()
	return errors.Join(
		commandError("OpenSSL migration client", clientWaitError, clientError.String()),
		commandError("zfs receive", receiveWaitError, receiveError.String()),
	)
}

func (transfer *MigrationTransfer) validateTLS() error {
	if transfer.tls.CAFile == "" || transfer.tls.CertificateFile == "" || transfer.tls.PrivateKeyFile == "" || transfer.tls.ListenAddress == "" || transfer.tls.TransferPort <= 0 {
		return fmt.Errorf("migration TLS configuration is required")
	}
	return nil
}

func (transfer *MigrationTransfer) serverArguments() []string {
	return []string{
		"s_server", "-quiet", "-no_ign_eof", "-naccept", "2",
		"-accept", transfer.tls.ListenAddress,
		"-cert", transfer.tls.CertificateFile, "-key", transfer.tls.PrivateKeyFile,
		"-verifyCAfile", transfer.tls.CAFile, "-Verify", "1", "-verify_return_error", "-tls1_3",
	}
}

func (transfer *MigrationTransfer) waitForSnapshotServer(ctx context.Context) error {
	host, _, err := net.SplitHostPort(transfer.tls.ListenAddress)
	if err != nil {
		return fmt.Errorf("parse migration transfer listen address %q: %w", transfer.tls.ListenAddress, err)
	}

	readyContext, cancel := context.WithTimeout(ctx, snapshotServerReadyTimeout)
	defer cancel()

	output := &strings.Builder{}
	command := exec.CommandContext(readyContext, "openssl", transfer.clientArguments(host, true)...)
	command.Stdout = io.Discard
	command.Stderr = output
	if err := command.Run(); err != nil {
		return commandError("check OpenSSL migration server", err, output.String())
	}
	return nil
}

// clientArguments builds the OpenSSL client flags. The readiness probe stops at
// its own stdin EOF. The data client reads until the source closes the stream.
func (transfer *MigrationTransfer) clientArguments(host string, stopAtInputEOF bool) []string {
	endOfFile := "-ign_eof"
	if stopAtInputEOF {
		endOfFile = "-no_ign_eof"
	}
	return []string{
		"s_client", "-quiet", endOfFile,
		"-connect", net.JoinHostPort(host, strconv.Itoa(transfer.tls.TransferPort)),
		"-cert", transfer.tls.CertificateFile, "-key", transfer.tls.PrivateKeyFile,
		"-verifyCAfile", transfer.tls.CAFile, "-verify_return_error", "-verify_ip", host, "-tls1_3",
	}
}

func migrationHost(address string) (string, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Hostname() == "" {
		return "", fmt.Errorf("invalid migration source address %q", address)
	}
	return parsed.Hostname(), nil
}

func commandError(name string, err error, output string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(output))
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
