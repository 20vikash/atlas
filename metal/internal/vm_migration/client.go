// Package vmmigration carries VM migration traffic between Metal hosts. The
// target host drives the migration: it makes the control calls and pulls the
// disk from the source over the trusted WireGuard mesh.
package vmmigration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/frappe/atlas/metal/internal/vm"
)

// defaultControlTimeout bounds one target-to-source control call.
const defaultControlTimeout = 60 * time.Second

// SourceClient calls the source host over the mesh. Control calls use HTTP; the
// disk stream uses a plain TCP connection to the source transfer port.
type SourceClient struct {
	client       *http.Client
	transferPort int
}

// NewSourceClient returns a client with the given per-call control timeout and
// the fixed source transfer port used for the disk stream.
func NewSourceClient(timeout time.Duration, transferPort int) *SourceClient {
	if timeout <= 0 {
		timeout = defaultControlTimeout
	}
	return &SourceClient{
		client:       &http.Client{Timeout: timeout},
		transferPort: transferPort,
	}
}

// nextSnapshotRequest asks for the next snapshot.
type nextSnapshotRequest struct {
	ReceivedSequence int `json:"received_sequence,omitempty"`
}

// nextSnapshotResponse is a snapshot reply.
type nextSnapshotResponse struct {
	Sequence  int    `json:"sequence"`
	SizeBytes int64  `json:"size_bytes"`
	GUID      string `json:"guid"`
}

// NextSnapshot acknowledges a sequence and asks for the next snapshot.
func (c *SourceClient) NextSnapshot(ctx context.Context, address, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error) {
	body, err := json.Marshal(nextSnapshotRequest{ReceivedSequence: receivedSequence})
	if err != nil {
		return SourceSnapshot{}, err
	}
	responseBody, err := c.postJSON(ctx, address, sourcePath(migrationID, virtualMachineID, "/snapshot"), body)
	if err != nil {
		return SourceSnapshot{}, err
	}
	var response nextSnapshotResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return SourceSnapshot{}, fmt.Errorf("decode snapshot response: %w", err)
	}
	return SourceSnapshot{Sequence: response.Sequence, SizeBytes: response.SizeBytes, GUID: response.GUID}, nil
}

// StreamSnapshot dials the source transfer port, requests one snapshot, and
// copies the disk stream into w. It returns the byte count.
func (c *SourceClient) StreamSnapshot(ctx context.Context, address, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error) {
	host, err := hostname(address)
	if err != nil {
		return 0, err
	}
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(c.transferPort)))
	if err != nil {
		return 0, fmt.Errorf("dial source transfer port: %w", err)
	}
	defer conn.Close()

	// End the transfer when the context is canceled.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	header := streamHeader{
		MigrationID:      migrationID,
		VirtualMachineID: virtualMachineID,
		Sequence:         sequence,
		ResumeToken:      resumeToken,
		ThroughputMiBps:  throughputMiBps,
	}
	if err := writeStreamHeader(conn, header); err != nil {
		return 0, fmt.Errorf("send stream header: %w", err)
	}

	var status [1]byte
	if _, err := io.ReadFull(conn, status[:]); err != nil {
		return 0, fmt.Errorf("read stream status: %w", err)
	}
	switch status[0] {
	case streamStatusOK:
		return io.Copy(w, conn)
	case streamStatusError:
		message, _ := readFrame(conn, maxFrameBytes)
		return 0, fmt.Errorf("source refused the stream: %s", strings.TrimSpace(string(message)))
	default:
		return 0, fmt.Errorf("unexpected stream status %d", status[0])
	}
}

// hostname returns the host of a source base URL for a mesh dial.
func hostname(address string) (string, error) {
	parsed, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("parse source address %q: %w", address, err)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("source address %q has no host", address)
	}
	return parsed.Hostname(), nil
}

// postJSON sends a JSON control request and returns its body.
func (c *SourceClient) postJSON(ctx context.Context, address, path string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build source request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call source: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read source response: %w", err)
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("source %s: %w", path, vm.ErrNotFound)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source %s returned HTTP %d", path, response.StatusCode)
	}
	return responseBody, nil
}

// sourceHandshakeResponse is a prepare reply.
type sourceHandshakeResponse struct {
	Config        PortableConfig `json:"config"`
	ObservedState vm.State       `json:"observed_state"`
}

// PrepareSource asks the source to lock the VM and return portable state.
func (c *SourceClient) PrepareSource(ctx context.Context, address, migrationID, virtualMachineID string) (PortableConfig, vm.State, error) {
	body, err := c.call(ctx, http.MethodPut, address, sourcePath(migrationID, virtualMachineID, "/source"))
	if err != nil {
		return PortableConfig{}, "", err
	}
	var response sourceHandshakeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return PortableConfig{}, "", fmt.Errorf("decode source handshake: %w", err)
	}
	return response.Config, response.ObservedState, nil
}

// StopSource asks the source to stop the VM, remove its network, and create the
// final snapshot.
func (c *SourceClient) StopSource(ctx context.Context, address, migrationID, virtualMachineID string) (SourceSnapshot, error) {
	responseBody, err := c.postJSON(ctx, address, sourcePath(migrationID, virtualMachineID, "/stop"), nil)
	if err != nil {
		return SourceSnapshot{}, err
	}
	var response nextSnapshotResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return SourceSnapshot{}, fmt.Errorf("decode stop response: %w", err)
	}
	return SourceSnapshot{Sequence: response.Sequence, SizeBytes: response.SizeBytes, GUID: response.GUID}, nil
}

// StartSource asks the source to restore its original desired state.
func (c *SourceClient) StartSource(ctx context.Context, address, migrationID, virtualMachineID string) error {
	_, err := c.call(ctx, http.MethodPost, address, sourcePath(migrationID, virtualMachineID, "/start"))
	return err
}

// FinishSource asks the source to destroy the stopped VM and state.
func (c *SourceClient) FinishSource(ctx context.Context, address, migrationID, virtualMachineID string) error {
	_, err := c.call(ctx, http.MethodPost, address, sourcePath(migrationID, virtualMachineID, "/destroy"))
	return err
}

// RemoveSource asks the source to unlock the VM and drop migration state.
func (c *SourceClient) RemoveSource(ctx context.Context, address, migrationID, virtualMachineID string) error {
	_, err := c.call(ctx, http.MethodDelete, address, sourcePath(migrationID, virtualMachineID, ""))
	return err
}

// call sends a request over the mesh and returns its body.
func (c *SourceClient) call(ctx context.Context, method, address, path string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, address+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build source request: %w", err)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call source: %w", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read source response: %w", err)
	}
	if response.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("source %s %s: %w", method, path, vm.ErrNotFound)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source %s %s returned HTTP %d", method, path, response.StatusCode)
	}
	return body, nil
}

// sourcePath builds a source route with the VM ID query the source needs to
// resolve the migration.
func sourcePath(migrationID, virtualMachineID, suffix string) string {
	path := "/v1/migrations/" + url.PathEscape(migrationID) + suffix
	query := url.Values{"virtual_machine_id": {virtualMachineID}}
	return path + "?" + query.Encode()
}
