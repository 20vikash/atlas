package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"
)

var errRetryableDownload = errors.New("retryable download failure")

const (
	downloadAttempts  = 5
	downloadBaseDelay = 2 * time.Second
	downloadTimeout   = 30 * time.Minute
)

func download(ctx context.Context, client *http.Client, directory, source, expectedDigest string, loggers ...*slog.Logger) (string, error) {
	logger := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		logger = loggers[0]
	}
	if _, err := parseImageURL(source); err != nil {
		return "", err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", err
	}

	redactedSource := redactURL(source)
	var lastErr error
	for attempt := 1; attempt <= downloadAttempts; attempt++ {
		if attempt > 1 {
			delay := downloadBaseDelay << (attempt - 2)
			logger.Warn("image download failed, retrying", "source", redactedSource, "attempt", attempt-1, "maximum_attempts", downloadAttempts, "retry_after", delay, "error", lastErr)
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(delay):
			}
		}
		path, err := downloadOnce(ctx, client, directory, source, expectedDigest)
		if err == nil {
			return path, nil
		}
		if !errors.Is(err, errRetryableDownload) {
			return "", fmt.Errorf("download %s: %w", redactedSource, err)
		}
		lastErr = err
	}
	return "", fmt.Errorf("download %s after %d attempts: %w", redactedSource, downloadAttempts, lastErr)
}

func downloadOnce(ctx context.Context, client *http.Client, directory, source, expectedDigest string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return "", fmt.Errorf("create request")
	}
	result, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: request failed", errRetryableDownload)
	}
	defer result.Body.Close()
	if result.StatusCode != http.StatusOK {
		statusError := fmt.Errorf("HTTP status %d", result.StatusCode)
		if result.StatusCode == http.StatusRequestTimeout || result.StatusCode == http.StatusTooManyRequests || result.StatusCode >= 500 {
			return "", fmt.Errorf("%w: %v", errRetryableDownload, statusError)
		}
		return "", statusError
	}

	file, err := os.CreateTemp(directory, "download-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	remove := func() {
		file.Close()
		os.Remove(path)
	}

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(file, hash), result.Body)
	if err == nil && result.ContentLength >= 0 && written != result.ContentLength {
		err = fmt.Errorf("truncated response")
	}
	if err == nil && hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		err = fmt.Errorf("%w: SHA-256 digest mismatch", ErrImageIntegrity)
	}
	if err != nil {
		remove()
		return "", err
	}
	if err := file.Sync(); err != nil {
		remove()
		return "", err
	}
	if err := file.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}

func verifyFileSHA256(path, expectedDigest string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return fmt.Errorf("SHA-256 digest mismatch")
	}
	return nil
}

func parseImageURL(source string) (*url.URL, error) {
	parsed, err := url.ParseRequestURI(source)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("invalid image URL")
	}
	return parsed, nil
}

func redactURL(source string) string {
	parsed, err := url.Parse(source)
	if err != nil {
		return "invalid image URL"
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.User = nil
	return parsed.String()
}
