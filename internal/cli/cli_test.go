package cli

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpWithoutConfiguration(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	t.Setenv("AARDE_LOG_LEVEL", "invalid")
	for _, args := range [][]string{
		nil, {"help"}, {"-h"}, {"--help"},
		{"serve", "--help"}, {"inspect", "--help"}, {"import", "--help"},
		{"search", "--help"}, {"remove", "--help"}, {"version", "--help"},
		{"import", "scene.tif", "-h"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := Run(args, &stdout, &stderr); code != 0 || stdout.Len() == 0 || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
		})
	}
}

func TestBuildVersionsArePerInvocation(t *testing.T) {
	t.Setenv("AARDE_LOG_LEVEL", "invalid")
	for _, version := range []string{"v1.5.1-test", "another-build", "dev"} {
		var stdout, stderr bytes.Buffer
		if code := RunVersion([]string{"version"}, &stdout, &stderr, version); code != 0 || stdout.String() != "aarde "+version+"\n" || stderr.Len() != 0 {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
		}
	}
	var stdout bytes.Buffer
	if code := Run([]string{"version"}, &stdout, io.Discard); code != 0 || stdout.String() != "aarde dev\n" {
		t.Fatalf("default version: code=%d stdout=%q", code, &stdout)
	}
}

func TestUsageErrorsBeforeConfiguration(t *testing.T) {
	// Invalid configuration must never mask a malformed command or cause startup.
	t.Setenv("AARDE_DATABASE_URL", "not a database URL")
	t.Setenv("AARDE_LOG_LEVEL", "invalid")
	for _, args := range [][]string{
		{"unknown"}, {"help", "extra"}, {"--help", "extra"}, {"version", "extra"},
		{"serve", "extra"}, {"serve", "--unknown"},
		{"inspect"}, {"inspect", ""}, {"inspect", "one.tif", "two.tif"}, {"inspect", "--unknown"},
		{"import"}, {"import", " "}, {"import", "one.tif", "two.tif"},
		{"import", "one.tif", "--catalog"}, {"import", "one.tif", "--cloud-cover"},
		{"import", "one.tif", "--dry-run=invalid"}, {"import", "one.tif", "--unknown"},
		{"import", "one.tif", "--catalog=bad/name"}, {"import", "one.tif", "--cloud-cover=NaN"},
		{"search"}, {"search", "--id"}, {"search", "--id="}, {"search", "--id=one,"},
		{"search", "--id=one", "extra"}, {"search", "--id=one", "--limit=no"},
		{"search", "--id=one", "--limit=-1"}, {"search", "--id=one", "--limit=201"},
		{"search", "--id=one", "--offset=-1"}, {"search", "--id=one", "--offset=1000001"},
		{"search", "--id=one", "--cloud-cover-lt=Inf"},
		{"remove"}, {"remove", "--image"}, {"remove", "--catalog"}, {"remove", "--image="},
		{"remove", "--image=one", "--catalog=default"}, {"remove", "--catalog=bad/name"},
		{"remove", "--catalog=example", "extra"}, {"remove", "--catalog=example", "--yes"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runWithInput(context.Background(), args, brokenConfirmation{}, &stdout, &stderr, "test")
			if code != 2 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "aarde: ") || strings.Contains(stderr.String(), "AARDE_") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
			if strings.Count(stderr.String(), "aarde: ") != 1 {
				t.Fatalf("duplicate diagnostic: %q", &stderr)
			}
		})
	}
}

func TestOperationalFailures(t *testing.T) {
	t.Setenv("AARDE_LOG_LEVEL", "info")
	t.Setenv("AARDE_WEB_TOKEN_PATH", filepath.Join(t.TempDir(), "token"))
	missing := filepath.Join(t.TempDir(), "missing.tif")
	for _, tc := range []struct {
		args      []string
		url, want string
	}{
		{[]string{"serve"}, "", "AARDE_DATABASE_URL is required"},
		{[]string{"import", missing}, "", "AARDE_DATABASE_URL is required"},
		{[]string{"search", "--id=one"}, "", "AARDE_DATABASE_URL is required"},
		{[]string{"remove", "--image=one"}, "", "AARDE_DATABASE_URL is required"},
		{[]string{"search", "--id=one"}, "://invalid", "invalid AARDE_DATABASE_URL"},
		{[]string{"inspect", missing}, "", "inspect"},
		{[]string{"import", missing, "--dry-run"}, "", "no such file"},
	} {
		t.Run(strings.Join(tc.args, " ")+tc.url, func(t *testing.T) {
			t.Setenv("AARDE_DATABASE_URL", tc.url)
			var stdout, stderr bytes.Buffer
			if code := Run(tc.args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), tc.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
			}
		})
	}
	t.Setenv("AARDE_LOG_LEVEL", "invalid")
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"inspect", missing}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "AARDE_LOG_LEVEL") {
		t.Fatalf("configuration failure: code=%d stderr=%q", code, &stderr)
	}
}

func TestImportStreamsAndLoggerRestoration(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	t.Setenv("AARDE_LOG_LEVEL", "debug")
	// Missing GDAL is a deterministic inspection failure on every machine.
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "broken.tif"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ignored.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	previous := slog.Default()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"import", dir, "--dry-run"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "Failed: 1\nSkipped: 1\nWould import: 0") || !strings.Contains(stdout.String(), "offline") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
	}
	if strings.Contains(stdout.String(), "failed:") || strings.Contains(stdout.String(), "level=") || !strings.Contains(stderr.String(), "failed: "+filepath.Join(dir, "broken.tif")) || !strings.Contains(stderr.String(), "skipping unsupported file") {
		t.Fatalf("incorrect stream routing: stdout=%q stderr=%q", &stdout, &stderr)
	}
	if slog.Default() != previous {
		t.Fatal("CLI replaced the caller's logger")
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"import", t.TempDir(), "--dry-run"}, &stdout, &stderr); code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "Would import: 0") {
		t.Fatalf("successful offline dry run: code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
	}
	if slog.Default() != previous {
		t.Fatal("successful command replaced the caller's logger")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestOutputFailures(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"version"}, {"import", "--help"}, {"remove", "--help"}} {
		var stderr bytes.Buffer
		if code := Run(args, failingWriter{}, &stderr); code != 1 || !strings.Contains(stderr.String(), io.ErrClosedPipe.Error()) {
			t.Fatalf("%v: code=%d stderr=%q", args, code, &stderr)
		}
	}
}

func TestCancellationAndDiscardedStreams(t *testing.T) {
	t.Setenv("AARDE_DATABASE_URL", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	if code := runWithInput(ctx, []string{"remove", "--catalog=example"}, brokenConfirmation{}, &stdout, &stderr, "dev"); code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "context canceled") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, &stdout, &stderr)
	}
	if code := Run([]string{"version"}, nil, nil); code != 0 {
		t.Fatalf("nil outputs: %d", code)
	}
}
