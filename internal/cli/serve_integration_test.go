package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServeCLIWithPostGIS(t *testing.T) {
	url := os.Getenv("AARDE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set AARDE_TEST_DATABASE_URL or run make integration for real PostGIS tests")
	}
	t.Setenv("AARDE_DATABASE_URL", url)
	t.Setenv("AARDE_WEB_PUBLIC_READ", "true")
	t.Setenv("AARDE_LOG_LEVEL", "info")
	tokenPath := filepath.Join(t.TempDir(), "private", "web.token")
	t.Setenv("AARDE_WEB_TOKEN_PATH", tokenPath)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	address := listener.Addr().String()
	t.Setenv("AARDE_LISTEN_ADDRESS", address)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"serve"}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "address already in use") {
		t.Fatalf("startup failure: code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
	}
	if _, err := os.Stat(tokenPath); !os.IsNotExist(err) {
		t.Fatalf("failed startup created a token: %v", err)
	}
	listener.Close()
	stdout.Reset()
	stderr.Reset()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() { done <- runWithInput(ctx, []string{"serve"}, nil, &stdout, &stderr, "cli-build-test") }()
	// Join the server before reading buffers, including on assertion failures.
	defer func() {
		cancel()
		select {
		case code := <-done:
			if code != 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "HTTP server stopped") {
				t.Errorf("shutdown: code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
		case <-time.After(35 * time.Second):
			t.Error("server did not stop after cancellation")
		}
	}()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + address + "/api/v1/version")
		if err != nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		var got struct {
			Version string `json:"version"`
		}
		if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(body, &got) != nil || got.Version != "cli-build-test" {
			t.Fatalf("server build version: status=%d body=%s error=%v", response.StatusCode, body, err)
		}
		return
	}
	t.Fatal("CLI server did not become ready")
}
