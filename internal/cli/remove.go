package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/config"
)

const removeUsage = "usage: aarde remove -image <exact-ID> | aarde remove -catalog <catalog-ID>"

type removeOptions struct {
	imageID, catalogID string
}

func parseRemoveOptions(args []string, out io.Writer) (removeOptions, error) {
	flags := newFlagSet("remove", out, removeUsage)
	flags.Usage = func() {
		out := flags.Output()
		fmt.Fprintln(out, removeUsage)
		fmt.Fprintln(out, "Specify exactly one of -image or -catalog; the flags are mutually exclusive.")
		fmt.Fprintln(out, "Delete database records only; source imagery files are preserved.")
		fmt.Fprintln(out, "Catalog removal requires typing yes at the confirmation prompt.")
		flags.PrintDefaults()
	}
	opts := removeOptions{}
	flags.StringVar(&opts.imageID, "image", "", "exact image ID in the default catalog to remove without confirmation")
	flags.StringVar(&opts.catalogID, "catalog", "", "catalog to remove after confirmation")
	if err := parseFlags(flags, args); err != nil {
		return opts, err
	}
	if flags.NArg() != 0 || flags.NFlag() == 0 {
		return opts, usageError{removeUsage}
	}
	if flags.NFlag() > 1 {
		return opts, usageError{"-image and -catalog are mutually exclusive; " + removeUsage}
	}
	// Validate explicitly supplied empty flags as well, so -image= cannot
	// accidentally turn a single-image removal into a whole-catalog removal.
	var invalid error
	flags.Visit(func(f *flag.Flag) {
		if err := catalog.ValidateName(f.Value.String()); err != nil {
			invalid = fmt.Errorf("%s: %w", f.Name, err)
		}
	})
	if invalid != nil {
		return opts, usageError{invalid.Error()}
	}
	if opts.catalogID == "" {
		opts.catalogID = "default"
	}
	return opts, nil
}

func (c commandLine) removeRecords(ctx context.Context, args []string) error {
	opts, err := parseRemoveOptions(args, c.stdout)
	if err != nil {
		return err
	}
	return c.withConfig(ctx, func(cfg config.Config) error {
		db, err := openDatabase(ctx, cfg, false)
		if err != nil {
			return err
		}
		defer db.Close()
		return remove(ctx, catalog.New(db), opts, c.stdin, c.stdout)
	})
}

type removalService interface {
	RemoveImage(context.Context, string, string) error
	RemoveCatalog(context.Context, string) (int64, error)
}

func remove(ctx context.Context, service removalService, opts removeOptions, in io.Reader, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if opts.imageID != "" {
		if err := service.RemoveImage(ctx, opts.catalogID, opts.imageID); err != nil {
			return fmt.Errorf("remove image %q from catalog %q: %w", opts.imageID, opts.catalogID, err)
		}
		_, err := fmt.Fprintf(out, "Removed image %q from catalog %q. Source imagery files were preserved.\n", opts.imageID, opts.catalogID)
		return err
	}
	if _, err := fmt.Fprintf(out, "Remove catalog %q and all of its image records from the database? Type yes to confirm: ", opts.catalogID); err != nil {
		return err
	}
	confirmed, err := confirmRemoval(ctx, in)
	if err != nil {
		return fmt.Errorf("read confirmation: %w", err)
	}
	if !confirmed {
		_, err := fmt.Fprintln(out, "Removal cancelled; no records were deleted.")
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	count, err := service.RemoveCatalog(ctx, opts.catalogID)
	if err != nil {
		return fmt.Errorf("remove catalog %q: %w", opts.catalogID, err)
	}
	_, err = fmt.Fprintf(out, "Removed catalog %q (%d image records). Source imagery files were preserved.\n", opts.catalogID, count)
	return err
}

func confirmRemoval(ctx context.Context, in io.Reader) (bool, error) {
	type result struct {
		confirmed bool
		err       error
	}
	// Stdin may block indefinitely. Let SIGINT/SIGTERM cancel the command
	// without waiting for another line of input.
	done := make(chan result, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		confirmed := scanner.Scan() && strings.TrimSpace(scanner.Text()) == "yes"
		done <- result{confirmed, scanner.Err()}
	}()
	select {
	case <-ctx.Done():
		return false, ctx.Err()
	case r := <-done:
		return r.confirmed, r.err
	}
}
