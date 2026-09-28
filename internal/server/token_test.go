package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAccessTokenLifecycle(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	path := filepath.Join(dir, "web.token")
	first, err := writeAccessToken(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(first)
	if err != nil || len(decoded) != 32 {
		t.Fatal("expected a random 256-bit token")
	}
	for file, mode := range map[string]os.FileMode{dir: 0700, path: 0600} {
		info, err := os.Stat(file)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("permissions: %v %v", info, err)
		}
	}
	second, err := writeAccessToken(path)
	if err != nil || second == first {
		t.Fatal("token was not rotated", err)
	}
	removeAccessToken(path, first)
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != second+"\n" {
		t.Fatal("old server removed a newer token", err)
	}
	removeAccessToken(path, second)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("token remains after cleanup: %v", err)
	}
	// Atomic replacement must replace a symlink without overwriting its target.
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("unchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAccessToken(path); err != nil {
		t.Fatal(err)
	}
	contents, _ = os.ReadFile(target)
	if string(contents) != "unchanged" {
		t.Fatal("followed token symlink")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAccessToken(path); err == nil {
		t.Fatal("accepted a non-private token directory")
	}
}

func TestRunTokenStartupAndShutdown(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	path := filepath.Join(t.TempDir(), "private", "web.token")
	if err := Run(context.Background(), address, nil, "dev", path, false, "osm"); err == nil {
		t.Fatal("occupied listener accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed startup created a token")
	}
	_ = listener.Close()
	client := &http.Client{Timeout: time.Second}
	var previousToken string
	for run := 0; run < 2; run++ {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- Run(ctx, address, nil, "dev", path, false, "osm") }()
		// Always release the server if an assertion fails.
		t.Cleanup(cancel)
		deadline := time.Now().Add(5 * time.Second)
		var token string
		for time.Now().Before(deadline) {
			contents, err := os.ReadFile(path)
			if err == nil {
				token = strings.TrimSpace(string(contents))
				break
			}
			time.Sleep(5 * time.Millisecond)
		}
		if token == "" || token == previousToken {
			cancel()
			<-done
			t.Fatal("startup did not rotate token")
		}
		for _, credential := range []string{"", token, previousToken} {
			req, _ := http.NewRequest("GET", "http://"+address+"/api/v1/info", nil)
			if credential != "" {
				req.Header.Set("Authorization", "Bearer "+credential)
			}
			response, err := client.Do(req)
			if err != nil {
				cancel()
				<-done
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			want := 401
			if credential == token {
				want = 200
			}
			if response.StatusCode != want {
				cancel()
				<-done
				t.Fatalf("status %d: %s", response.StatusCode, body)
			}
		}
		cancel()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("token after shutdown: %v", err)
		}
		if strings.Contains(logs.String(), token) {
			t.Fatal("startup log disclosed the token")
		}
		previousToken = token
	}
	if !strings.Contains(logs.String(), "token_file=") || !strings.Contains(logs.String(), "public_read=false") {
		t.Fatal("missing startup access information")
	}
}
