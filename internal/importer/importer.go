package importer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/raster"
)

type Options struct {
	Catalog           string
	CloudCover        *float64
	Recursive, DryRun bool
}
type Summary struct{ Imported, Existing, Failed, Skipped, WouldImport int }
type Event struct {
	Path, ImageID, Status string
	Err                   error
}
type Runner struct {
	Catalog *catalog.Service
	Inspect func(context.Context, string) (raster.Inspection, error)
	Report  func(Event)
}

func ImageID(path string) string { return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) }
func supported(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".tif" || ext == ".tiff"
}

func Discover(ctx context.Context, path string, recursive bool) ([]string, int, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, 0, err
	}
	files := []string{}
	skipped := 0
	consider := func(p string, mode fs.FileMode) {
		if mode.IsRegular() && supported(p) {
			files = append(files, p)
		} else {
			skipped++
			slog.Debug("skipping unsupported file", "path", p)
		}
	}
	if !info.IsDir() {
		consider(path, info.Mode())
		return files, skipped, nil
	}
	err = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if p != path && !recursive {
				return filepath.SkipDir
			}
			return nil
		}
		consider(p, d.Type())
		return nil
	})
	return files, skipped, err
}

func (r Runner) Run(ctx context.Context, path string, opts Options) (Summary, error) {
	var summary Summary
	if err := catalog.ValidateCloudCover(opts.CloudCover); err != nil {
		return summary, err
	}
	if opts.Catalog == "" {
		opts.Catalog = "default"
	}
	if err := catalog.ValidateName(opts.Catalog); err != nil {
		return summary, err
	}
	if !opts.DryRun && r.Catalog == nil {
		return summary, errors.New("database is required for import")
	}
	if r.Inspect == nil {
		r.Inspect = raster.Inspect
	}
	files, skipped, err := Discover(ctx, path, opts.Recursive)
	summary.Skipped = skipped
	if err != nil {
		return summary, err
	}
	seenChecksums := map[string]string{}
	seenIDs := map[string]string{}
	for _, path := range files {
		if err := ctx.Err(); err != nil {
			return summary, err
		}
		event := Event{Path: path, ImageID: ImageID(path)}
		inspection, err := r.Inspect(ctx, path)
		if err == nil {
			i := catalog.Imagery{CatalogID: opts.Catalog, ImageID: event.ImageID, DisplayName: filepath.Base(path), AcquiredAt: inspection.AcquiredAt, CloudCover: inspection.CloudCover, Footprint: inspection.Footprint, Checksum: inspection.Checksum, AssetLocation: inspection.AssetLocation, Width: inspection.Width, Height: inspection.Height, BandCount: inspection.BandCount, SourceCRS: inspection.SourceCRS, Metadata: inspection.Metadata}
			if opts.CloudCover != nil {
				i.CloudCover = opts.CloudCover
			}
			err = catalog.ValidateImagery(i)
			if err == nil {
				existing := false
				if id, ok := seenChecksums[i.Checksum]; ok {
					existing = true
					event.ImageID = id
				} else if sum, ok := seenIDs[i.ImageID]; ok && sum != i.Checksum {
					err = catalog.ErrConflict
				} else if r.Catalog != nil {
					var saved catalog.Imagery
					if opts.DryRun {
						saved, existing, err = r.Catalog.CheckImport(ctx, i)
					} else {
						saved, existing, err = r.Catalog.Import(ctx, i)
					}
					if existing {
						event.ImageID = saved.ImageID
					}
				}
				if err == nil {
					seenChecksums[i.Checksum] = event.ImageID
					seenIDs[event.ImageID] = i.Checksum
					if existing {
						summary.Existing++
						event.Status = "already imported"
					} else if opts.DryRun {
						summary.WouldImport++
						event.Status = "would import"
					} else {
						summary.Imported++
						event.Status = "imported"
					}
				}
			}
		}
		if err != nil {
			summary.Failed++
			event.Status = "failed"
			event.Err = err
			slog.Error("import failed", "path", path, "error", err)
		} else {
			slog.Info(event.Status, "path", path, "catalog", opts.Catalog, "image_id", event.ImageID)
		}
		if r.Report != nil {
			r.Report(event)
		}
	}
	if summary.Failed > 0 {
		return summary, fmt.Errorf("%d file(s) failed to import", summary.Failed)
	}
	return summary, nil
}
