package vm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// defaultSourceClientTimeout bounds one call from the target to the source.
const defaultSourceClientTimeout = 60 * time.Second

// HTTPSourceClient calls the source host over HTTP with an Atlas-signed token.
type HTTPSourceClient struct {
	client *http.Client
}

// NewHTTPSourceClient returns a source client with a bounded per-call timeout.
func NewHTTPSourceClient(timeout time.Duration) *HTTPSourceClient {
	if timeout <= 0 {
		timeout = defaultSourceClientTimeout
	}
	return &HTTPSourceClient{client: &http.Client{Timeout: timeout}}
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
