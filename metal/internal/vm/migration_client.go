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

// defaultSourceClientTimeout bounds one control call from the target to the source.
const defaultSourceClientTimeout = 60 * time.Second

// HTTPSourceClient calls the source host over HTTP with an Atlas-signed token.
type HTTPSourceClient struct {
	client       *http.Client
	streamClient *http.Client
}

// NewHTTPSourceClient returns a source client. Control calls use a per-call
// timeout. A stream has no whole-request timeout and stops with the context.
func NewHTTPSourceClient(timeout time.Duration) *HTTPSourceClient {
	if timeout <= 0 {
		timeout = defaultSourceClientTimeout
	}
	return &HTTPSourceClient{
		client:       &http.Client{Timeout: timeout},
		streamClient: &http.Client{},
	}
}

// nextSnapshotRequest asks the source for the next snapshot.
type nextSnapshotRequest struct {
	ReceivedSequence int `json:"received_sequence,omitempty"`
}

// nextSnapshotResponse is the source reply to a snapshot request.
type nextSnapshotResponse struct {
	Sequence  int    `json:"sequence"`
	SizeBytes int64  `json:"size_bytes"`
	GUID      string `json:"guid"`
}

// streamRequest asks the source for one snapshot stream.
type streamRequest struct {
	Sequence        int    `json:"sequence"`
	ResumeToken     string `json:"resume_token,omitempty"`
	ThroughputMiBps int    `json:"throughput_mibps,omitempty"`
}

// NextSnapshot acknowledges the received sequence and asks for the next snapshot.
func (c *HTTPSourceClient) NextSnapshot(ctx context.Context, address, migrationID, token string, receivedSequence int) (SourceSnapshot, error) {
	body, err := json.Marshal(nextSnapshotRequest{ReceivedSequence: receivedSequence})
	if err != nil {
		return SourceSnapshot{}, err
	}
	path := fmt.Sprintf("/v1/migrations/%s/snapshot", url.PathEscape(migrationID))
	responseBody, err := c.postJSON(ctx, address, path, token, body)
	if err != nil {
		return SourceSnapshot{}, err
	}
	var response nextSnapshotResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return SourceSnapshot{}, fmt.Errorf("decode snapshot response: %w", err)
	}
	return SourceSnapshot{Sequence: response.Sequence, SizeBytes: response.SizeBytes, GUID: response.GUID}, nil
}

// StreamSnapshot reads one snapshot stream into w and returns the byte count.
func (c *HTTPSourceClient) StreamSnapshot(ctx context.Context, address, migrationID, token string, sequence int, resumeToken string, throughputMiBps int, w io.Writer) (int64, error) {
	body, err := json.Marshal(streamRequest{Sequence: sequence, ResumeToken: resumeToken, ThroughputMiBps: throughputMiBps})
	if err != nil {
		return 0, err
	}
	path := fmt.Sprintf("/v1/migrations/%s/stream", url.PathEscape(migrationID))
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+path, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("build stream request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
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

// postJSON sends one JSON control request and returns the response body.
func (c *HTTPSourceClient) postJSON(ctx context.Context, address, path, token string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address+path, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build source request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
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

// sourceHandshakeResponse is the source reply to a prepare call.
type sourceHandshakeResponse struct {
	Config        PortableConfig `json:"config"`
	ObservedState State          `json:"observed_state"`
}

// PrepareSource asks the source host to lock the VM and return its portable state.
func (c *HTTPSourceClient) PrepareSource(ctx context.Context, address, migrationID, _, token string) (PortableConfig, State, error) {
	path := fmt.Sprintf("/v1/migrations/%s/source", url.PathEscape(migrationID))
	body, err := c.call(ctx, http.MethodPut, address, path, token)
	if err != nil {
		return PortableConfig{}, "", err
	}
	var response sourceHandshakeResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return PortableConfig{}, "", fmt.Errorf("decode source handshake: %w", err)
	}
	return response.Config, response.ObservedState, nil
}

// StopSource asks the source host to stop the VM, remove its network, and create
// the final snapshot. It returns that snapshot.
func (c *HTTPSourceClient) StopSource(ctx context.Context, address, migrationID, token string) (SourceSnapshot, error) {
	path := fmt.Sprintf("/v1/migrations/%s/stop", url.PathEscape(migrationID))
	responseBody, err := c.postJSON(ctx, address, path, token, nil)
	if err != nil {
		return SourceSnapshot{}, err
	}
	var response nextSnapshotResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return SourceSnapshot{}, fmt.Errorf("decode stop response: %w", err)
	}
	return SourceSnapshot{Sequence: response.Sequence, SizeBytes: response.SizeBytes, GUID: response.GUID}, nil
}

// RemoveSource asks the source host to unlock the VM and drop its migration state.
func (c *HTTPSourceClient) RemoveSource(ctx context.Context, address, migrationID, token string) error {
	path := fmt.Sprintf("/v1/migrations/%s", url.PathEscape(migrationID))
	_, err := c.call(ctx, http.MethodDelete, address, path, token)
	return err
}

// call sends one authorized request and returns the response body.
func (c *HTTPSourceClient) call(ctx context.Context, method, address, path, token string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, address+path, nil)
	if err != nil {
		return nil, fmt.Errorf("build source request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)

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
