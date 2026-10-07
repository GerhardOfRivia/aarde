package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/config"
	"github.com/GerhardOfRivia/aarde/internal/importer"
)

const importUsage = "usage: aarde import <path> [--recursive] [--dry-run] [--catalog default] [--cloud-cover percent]"

func parseImportOptions(args []string, out io.Writer) (string, importer.Options, error) {
	flags := newFlagSet("import", out, importUsage)
	opts := importer.Options{}
	flags.StringVar(&opts.Catalog, "catalog", "default", "catalog ID")
	flags.BoolVar(&opts.Recursive, "recursive", false, "walk child directories")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "inspect without database writes")
	flags.Func("cloud-cover", "file-level cloud-cover percentage (0-100); overrides aggregate metadata, preserving segment values", func(raw string) error {
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
	if err := parseFlags(flags, args); err != nil {
		return "", opts, err
	}
	if err := requireArguments(flags, "import path"); err != nil {
		return "", opts, err
	}
	// Preserve the importer's empty-catalog default while validating before DB access.
	if opts.Catalog == "" {
		opts.Catalog = "default"
	}
	if err := catalog.ValidateName(opts.Catalog); err != nil {
		return "", opts, usageError{fmt.Sprintf("catalog: %v", err)}
	}
	return flags.Arg(0), opts, nil
}

func (c commandLine) importImages(ctx context.Context, args []string) error {
	path, opts, err := parseImportOptions(args, c.stdout)
	if err != nil {
		return err
	}
	return c.withConfig(ctx, func(cfg config.Config) error {
		var service *catalog.Service
		if !opts.DryRun || cfg.DatabaseURL != "" {
			db, err := openDatabase(ctx, cfg, !opts.DryRun)
			if err != nil {
				return err
			}
			defer db.Close()
			service = catalog.New(db)
		} else {
			if _, err := fmt.Fprintln(c.stdout, "Dry run: offline; database duplicates and PostGIS topology are not checked."); err != nil {
				return err
			}
		}
		var reportErr error
		runner := importer.Runner{Catalog: service, Report: func(e importer.Event) {
			var err error
			if e.Err != nil {
				_, err = fmt.Fprintf(c.stderr, "failed: %s: %v\n", e.Path, e.Err)
			} else {
				_, err = fmt.Fprintf(c.stdout, "%s: %s (%s/%s)\n", e.Status, e.Path, opts.Catalog, e.ImageID)
			}
			if reportErr == nil {
				reportErr = err
			}
		}}
		summary, runErr := runner.Run(ctx, path, opts)
		_, summaryErr := fmt.Fprintf(c.stdout, "Imported: %d\nExisting: %d\nFailed: %d\nSkipped: %d\n", summary.Imported, summary.Existing, summary.Failed, summary.Skipped)
		var dryRunErr error
		if opts.DryRun {
			_, dryRunErr = fmt.Fprintf(c.stdout, "Would import: %d\n", summary.WouldImport)
		}
		return errors.Join(runErr, reportErr, summaryErr, dryRunErr)
	})
}
