package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/config"
)

const searchUsage = "usage: aarde search --id <exact-ID> [--catalog default] [--cloud-cover-lt percent] [--limit 50] [--offset 0]"

func (c commandLine) search(ctx context.Context, args []string) error {
	q, err := parseSearchQuery(args, c.stdout)
	if err != nil {
		return err
	}
	return c.withConfig(ctx, func(cfg config.Config) error {
		db, err := openDatabase(ctx, cfg, false)
		if err != nil {
			return err
		}
		defer db.Close()
		page, err := catalog.New(db).Search(ctx, q)
		if err != nil {
			return err
		}
		return encodeJSON(c.stdout, api.PageResponse(page))
	})
}

func parseSearchQuery(args []string, out io.Writer) (catalog.Query, error) {
	flags := newFlagSet("search", out, searchUsage)
	var ids string
	q := catalog.Query{}
	flags.StringVar(&ids, "id", "", "exact image ID, or comma-separated IDs")
	flags.StringVar(&q.CatalogID, "catalog", "default", "catalog ID")
	flags.IntVar(&q.Limit, "limit", 50, "page size (1-200)")
	flags.IntVar(&q.Offset, "offset", 0, "pagination offset")
	flags.Func("cloud-cover-lt", "scene cloud cover strictly less than this percentage (0-100); excludes unknown values; not selected-area cloud cover", func(raw string) error {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return errors.New("cloud_cover_lt must be a finite number between 0 and 100")
		}
		if err := catalog.ValidateCloudCover(&value); err != nil {
			return fmt.Errorf("cloud_cover_lt: %w", err)
		}
		q.CloudCoverLT = &value
		return nil
	})
	if err := parseFlags(flags, args); err != nil {
		return q, err
	}
	if flags.NArg() != 0 || ids == "" {
		return q, usageError{searchUsage}
	}
	q.ImageIDs = strings.Split(ids, ",")
	for i := range q.ImageIDs {
		q.ImageIDs[i] = strings.TrimSpace(q.ImageIDs[i])
	}
	if err := catalog.NormalizeQuery(&q); err != nil {
		return q, usageError{err.Error()}
	}
	return q, nil
}
