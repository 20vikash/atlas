package vm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// defaultSourceClientTimeout bounds one target-to-source control call.
const defaultSourceClientTimeout = 60 * time.Second

// HTTPSourceClient calls the source over the trusted mesh.
type HTTPSourceClient struct {
	client       *http.Client
	streamClient *http.Client
}

// NewHTTPSourceClient returns a client with per-call control timeouts.
func NewHTTPSourceClient(timeout time.Duration) *HTTPSourceClient {
	if timeout <= 0 {
		timeout = defaultSourceClientTimeout
	}
	return &HTTPSourceClient{
		client:       &http.Client{Timeout: timeout},
		streamClient: &http.Client{},
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

// streamRequest asks for one snapshot stream.
type streamRequest struct {
	Sequence        int    `json:"sequence"`
	ResumeToken     string `json:"resume_token,omitempty"`
	ThroughputMiBps int    `json:"throughput_mibps,omitempty"`
}

// NextSnapshot acknowledges a sequence and asks for the next snapshot.
func (c *HTTPSourceClient) NextSnapshot(ctx context.Context, address, migrationID, virtualMachineID string, receivedSequence int) (SourceSnapshot, error) {
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

// StreamSnapshot reads one snapshot into w and returns its byte count.
func (c *HTTPSourceClient) StreamSnapshot(ctx context.Context, address, migrationID, virtualMachineID string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error) {
	body, err := json.Marshal(streamRequest{Sequence: sequence, ResumeToken: resumeToken, ThroughputMiBps: throughputMiBps})
	if err != nil {
		return 0, err
	}
	path := sourcePath(migrationID, virtualMachineID, "/stream")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+path, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build stream request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := c.streamClient.Do(request)
	if err != nil {
		return 0, fmt.Errorf("stream from source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1<<16))
		return 0, fmt.Errorf("source stream returned HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(message)))
	}
	return io.Copy(w, response.Body)
}

// postJSON sends a JSON control request and returns its body.
func (c *HTTPSourceClient) postJSON(ctx context.Context, address, path string, body []byte) ([]byte, error) {
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
		return nil, fmt.Errorf("source %s: %w", path, ErrNotFound)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("source %s returned HTTP %d", path, response.StatusCode)
	}
	return responseBody, nil
}

// sourceHandshakeResponse is a prepare reply.
type sourceHandshakeResponse struct {
	Config        PortableConfig `json:"config"`
	ObservedState State          `json:"observed_state"`
}

// PrepareSource asks the source to lock the VM and return portable state.
func (c *HTTPSourceClient) PrepareSource(ctx context.Context, address, migrationID, virtualMachineID string) (PortableConfig, State, error) {
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
func (c *HTTPSourceClient) StopSource(ctx context.Context, address, migrationID, virtualMachineID string) (SourceSnapshot, error) {
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
func (c *HTTPSourceClient) StartSource(ctx context.Context, address, migrationID, virtualMachineID string) error {
	_, err := c.call(ctx, http.MethodPost, address, sourcePath(migrationID, virtualMachineID, "/start"))
	return err
}

// FinishSource asks the source to destroy the stopped VM and state.
func (c *HTTPSourceClient) FinishSource(ctx context.Context, address, migrationID, virtualMachineID string) error {
	_, err := c.call(ctx, http.MethodPost, address, sourcePath(migrationID, virtualMachineID, "/destroy"))
	return err
}

// RemoveSource asks the source to unlock the VM and drop migration state.
func (c *HTTPSourceClient) RemoveSource(ctx context.Context, address, migrationID, virtualMachineID string) error {
	_, err := c.call(ctx, http.MethodDelete, address, sourcePath(migrationID, virtualMachineID, ""))
	return err
}

// call sends a request over the mesh and returns its body.
func (c *HTTPSourceClient) call(ctx context.Context, method, address, path string) ([]byte, error) {
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
		return nil, fmt.Errorf("source %s %s: %w", method, path, ErrNotFound)
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
