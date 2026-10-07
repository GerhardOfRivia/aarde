// Package cli implements Aarde's command line interface.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/GerhardOfRivia/aarde/internal/config"
	"github.com/GerhardOfRivia/aarde/internal/database"
)

const usage = `Aarde — local imagery, spatial discovery

Usage:
  aarde serve
  aarde import <file-or-directory> [--recursive] [--dry-run] [--catalog default] [--cloud-cover percent]
  aarde inspect <image.tif|image.ntf|image.nitf>
  aarde search --id <exact-ID> [--catalog default] [--cloud-cover-lt percent] [--limit 50] [--offset 0]
  aarde remove -image <exact-ID> (default catalog, no confirmation)
  aarde remove -catalog <catalog-ID> (requires confirmation)
  aarde version

Set AARDE_DATABASE_URL for serve, import, search, and remove.
Remove requires exactly one of -image or -catalog; the flags are mutually exclusive.
Remove deletes database records only; source imagery files are preserved.
Dry run can work offline; set the database URL to also check existing records.
Web access requires the private token file named in the startup log.
AARDE_WEB_PUBLIC_READ=true allows anonymous catalog browsing and search.
`

// Run executes a CLI invocation with the development version.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunVersion(args, stdout, stderr, "dev")
}

// RunVersion executes a CLI invocation and returns 0 on success, 1 for an
// operational failure, or 2 for invalid command line usage.
func RunVersion(args []string, stdout, stderr io.Writer, version string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithInput(ctx, args, os.Stdin, stdout, stderr, version)
}

// Tests can supply both cancellation and confirmation input without changing
// process streams or sending a signal to the test process.
func runWithInput(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) int {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	err := execute(ctx, args, stdin, stdout, stderr, version)
	if err == nil || errors.Is(err, flag.ErrHelp) {
		return 0
	}
	fmt.Fprintln(stderr, "aarde:", err)
	var usage usageError
	if errors.As(err, &usage) {
		return 2
	}
	return 1
}

type commandLine struct {
	stdin          io.Reader
	stdout, stderr io.Writer
	version        string
}

func execute(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, version string) error {
	c := commandLine{stdin: stdin, stdout: stdout, stderr: stderr, version: version}
	if len(args) == 0 {
		_, err := fmt.Fprint(stdout, usage)
		return err
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) != 1 {
			return usageError{"help takes no arguments"}
		}
		_, err := fmt.Fprint(stdout, usage)
		return err
	case "version":
		flags := newFlagSet("version", stdout, "usage: aarde version")
		if err := parseFlags(flags, args[1:]); err != nil {
			return err
		}
		if err := requireArguments(flags); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "aarde", version)
		return err
	case "serve":
		return c.serve(ctx, args[1:])
	case "inspect":
		return c.inspect(ctx, args[1:])
	case "import":
		return c.importImages(ctx, args[1:])
	case "search":
		return c.search(ctx, args[1:])
	case "remove":
		return c.removeRecords(ctx, args[1:])
	default:
		return usageError{fmt.Sprintf("unknown command %q; run aarde --help", args[0])}
	}
}

// Domain packages currently use the process-wide slog logger. Serialize the
// configured portion of CLI invocations so their diagnostics cannot cross
// streams, and restore the caller's logger on every return path.
var loggingMu sync.Mutex

func (c commandLine) withConfig(ctx context.Context, run func(config.Config) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	loggingMu.Lock()
	defer loggingMu.Unlock()
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(c.stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))
	defer slog.SetDefault(previous)
	if err := ctx.Err(); err != nil {
		return err
	}
	return run(cfg)
}

func openDatabase(ctx context.Context, cfg config.Config, migrate bool) (*database.Repository, error) {
	if cfg.DatabaseURL == "" {
		return nil, errors.New("AARDE_DATABASE_URL is required")
	}
	db, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if migrate {
		if err := db.Migrate(ctx); err != nil {
			db.Close()
			return nil, err
		}
	}
	return db, nil
}
