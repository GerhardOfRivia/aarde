package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/config"
	"github.com/GerhardOfRivia/aarde/internal/database"
	"github.com/GerhardOfRivia/aarde/internal/importer"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/GerhardOfRivia/aarde/internal/server"
)

var version = "dev"

const usage = `Aarde — local imagery, spatial discovery

Usage:
  aarde serve
  aarde import <file-or-directory> [--recursive] [--dry-run] [--catalog default] [--cloud-cover percent]
  aarde inspect <image.tif>
  aarde search --id <exact-ID> [--catalog default] [--limit 50] [--offset 0]
  aarde version

Set AARDE_DATABASE_URL for serve, import, and search.
Dry run can work offline; set the database URL to also check existing records.
Web access requires the private token file named in the startup log.
AARDE_WEB_PUBLIC_READ=true allows anonymous catalog browsing and search.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		slog.Error("aarde failed", "error", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, usage)
		return nil
	}
	if args[0] == "version" {
		if len(args) != 1 {
			return errors.New("version takes no arguments")
		}
		fmt.Fprintln(out, "aarde", version)
		return nil
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel})))
	encode := func(v any) error { e := json.NewEncoder(out); e.SetIndent("", "  "); return e.Encode(v) }
	open := func(migrate bool) (*database.Repository, error) {
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
	switch args[0] {
	case "serve":
		if len(args) != 1 {
			return errors.New("serve takes no arguments")
		}
		tokenPath, err := cfg.TokenPath()
		if err != nil {
			return err
		}
		db, err := open(true)
		if err != nil {
			return err
		}
		defer db.Close()
		return server.Run(ctx, cfg.ListenAddress, catalog.New(db), version, tokenPath, cfg.WebPublicRead, cfg.WebBasemap)
	case "inspect":
		if len(args) != 2 {
			return errors.New("usage: aarde inspect <image.tif>")
		}
		i, err := raster.Inspect(ctx, args[1])
		if err != nil {
			return err
		}
		return encode(i)
	case "import":
		flags := flag.NewFlagSet("import", flag.ContinueOnError)
		flags.SetOutput(out)
		opts := importer.Options{}
		flags.StringVar(&opts.Catalog, "catalog", "default", "catalog ID")
		flags.BoolVar(&opts.Recursive, "recursive", false, "walk child directories")
		flags.BoolVar(&opts.DryRun, "dry-run", false, "inspect without database writes")
		flags.Func("cloud-cover", "cloud-cover percentage (0–100); overrides metadata for all imported files", func(raw string) error {
			value, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return errors.New("cloud cover must be a number between 0 and 100")
			}
			if err := catalog.ValidateCloudCover(&value); err != nil {
				return err
			}
			opts.CloudCover = &value
			return nil
		})
		if err := parse(flags, args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 1 {
			return errors.New("usage: aarde import <path> [--recursive] [--dry-run] [--catalog default] [--cloud-cover percent]")
		}
		var service *catalog.Service
		if !opts.DryRun || cfg.DatabaseURL != "" {
			db, err := open(!opts.DryRun)
			if err != nil {
				return err
			}
			defer db.Close()
			service = catalog.New(db)
		} else {
			fmt.Fprintln(out, "Dry run: offline; database duplicates and PostGIS topology are not checked.")
		}
		runner := importer.Runner{Catalog: service, Report: func(e importer.Event) {
			if e.Err != nil {
				fmt.Fprintf(out, "failed: %s: %v\n", e.Path, e.Err)
			} else {
				fmt.Fprintf(out, "%s: %s (%s/%s)\n", e.Status, e.Path, opts.Catalog, e.ImageID)
			}
		}}
		summary, err := runner.Run(ctx, flags.Arg(0), opts)
		fmt.Fprintf(out, "Imported: %d\nExisting: %d\nFailed: %d\nSkipped: %d\n", summary.Imported, summary.Existing, summary.Failed, summary.Skipped)
		if opts.DryRun {
			fmt.Fprintf(out, "Would import: %d\n", summary.WouldImport)
		}
		return err
	case "search":
		flags := flag.NewFlagSet("search", flag.ContinueOnError)
		flags.SetOutput(out)
		var ids string
		q := catalog.Query{}
		flags.StringVar(&ids, "id", "", "exact image ID, or comma-separated IDs")
		flags.StringVar(&q.CatalogID, "catalog", "default", "catalog ID")
		flags.IntVar(&q.Limit, "limit", 50, "page size (1–200)")
		flags.IntVar(&q.Offset, "offset", 0, "pagination offset")
		if err := parse(flags, args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 || ids == "" {
			return errors.New("usage: aarde search --id <exact-ID> [--catalog default]")
		}
		q.ImageIDs = strings.Split(ids, ",")
		for i := range q.ImageIDs {
			q.ImageIDs[i] = strings.TrimSpace(q.ImageIDs[i])
		}
		if err := catalog.NormalizeQuery(&q); err != nil {
			return err
		}
		db, err := open(false)
		if err != nil {
			return err
		}
		defer db.Close()
		p, err := catalog.New(db).Search(ctx, q)
		if err != nil {
			return err
		}
		return encode(api.PageResponse(p))
	default:
		return fmt.Errorf("unknown command %q; run aarde --help", args[0])
	}
}

// Keep standard-library flags while accepting flags after the import path.
func parse(fs *flag.FlagSet, args []string) error {
	options, positionals := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			positionals = append(positionals, arg)
			continue
		}
		options = append(options, arg)
		name := strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-")
		f := fs.Lookup(name)
		if f == nil || strings.Contains(arg, "=") {
			continue
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 < len(args) {
			i++
			options = append(options, args[i])
		}
	}
	return fs.Parse(append(append(options, "--"), positionals...))
}
