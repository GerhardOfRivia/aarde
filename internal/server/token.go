package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeAccessToken(filename string) (string, error) {
	if strings.TrimSpace(filename) == "" {
		return "", fmt.Errorf("server: token path is required")
	}
	directory := filepath.Dir(filename)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("server: create token directory: %w", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", fmt.Errorf("server: inspect token directory %s: %w", directory, err)
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("server: token directory %s must be a private directory", directory)
	}

	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("server: generate access token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	temporary, err := os.CreateTemp(directory, ".aarde-web-token-*")
	if err != nil {
		return "", fmt.Errorf("server: create temporary token: %w", err)
	}
	temporaryName := temporary.Name()
	cleanup := true
	defer func() {
		_ = temporary.Close()
		if cleanup {
			_ = os.Remove(temporaryName)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return "", fmt.Errorf("server: protect temporary token: %w", err)
	}
	if _, err := temporary.WriteString(token + "\n"); err != nil {
		return "", fmt.Errorf("server: write temporary token: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return "", fmt.Errorf("server: sync temporary token: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("server: close temporary token: %w", err)
	}
	if err := os.Rename(temporaryName, filename); err != nil {
		return "", fmt.Errorf("server: install token %s: %w", filename, err)
	}
	cleanup = false
	return token, nil
}

func removeAccessToken(filename, token string) {
	contents, err := os.ReadFile(filename)
	if err != nil {
		return
	}
	current := strings.TrimSpace(string(contents))
	if subtle.ConstantTimeCompare([]byte(current), []byte(token)) == 1 {
		_ = os.Remove(filename)
	}
}
