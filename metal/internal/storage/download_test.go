package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDownloadVerifiesDigestAndRedactsURL(t *testing.T) {
	content := []byte("verified image")
	digest := sha256.Sum256(content)
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = response.Write(content)
	}))
	defer server.Close()

	path, err := downloadOnce(context.Background(), server.Client(), t.TempDir(), server.URL+"?signature=secret", hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)

	if _, err := downloadOnce(context.Background(), server.Client(), t.TempDir(), server.URL+"?signature=secret", strings.Repeat("0", 64)); !errors.Is(err, ErrImageIntegrity) {
		t.Fatalf("error = %v, want ErrImageIntegrity", err)
	}
	if got := redactURL(server.URL + "?signature=secret"); strings.Contains(got, "secret") {
		t.Fatalf("redacted URL = %q", got)
	}
}

func TestDownloadDoesNotRetryPermanentFailure(t *testing.T) {
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requestCount++
		response.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := download(context.Background(), server.Client(), t.TempDir(), server.URL, strings.Repeat("0", 64))
	if err == nil {
		t.Fatal("download succeeded")
	}
	if requestCount != 1 {
		t.Fatalf("request count = %d, want 1", requestCount)
	}
}

func TestImageHTTPClientHasTimeout(t *testing.T) {
	if timeout := newImageHTTPClient().Timeout; timeout <= 0 {
		t.Fatalf("timeout = %s, want a positive duration", timeout)
	}
}
