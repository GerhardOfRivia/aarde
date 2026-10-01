package database

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/importer"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestPostGISXMLSidecarImports(t *testing.T) {
	ctx, _, service, cat := testDB(t)
	testutil.RequireGDAL(t)
	for _, ext := range []string{".tif", ".ntf"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			child := filepath.Join(dir, "child")
			if err := os.Mkdir(child, 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(child, "scene"+ext)
			if ext == ".tif" {
				testutil.GeoTIFF(t, path)
			} else {
				testutil.DiverseMultiNITF(t, path, false)
			}
			embedded, err := raster.Inspect(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			sidecar := filepath.Join(child, "SCENE.XML")
			xml := `<isd><IMD><IMAGE><CLOUDCOVER>3e-3</CLOUDCOVER><FIRSTLINETIME>2021-09-11T05:15:29.005479Z</FIRSTLINETIME></IMAGE></IMD></isd>`
			if err := os.WriteFile(sidecar, []byte(xml), 0600); err != nil {
				t.Fatal(err)
			}
			inspected, err := raster.Inspect(ctx, path)
			if err != nil || inspected.CloudCover == nil || *inspected.CloudCover != .3 || inspected.AcquiredAt.Year() != 2021 || inspected.Checksum != embedded.Checksum {
				t.Fatalf("inspect: %+v %v", inspected, err)
			}
			for index, s := range inspected.Segments {
				if !bytes.Equal(s.Metadata, embedded.Segments[index].Metadata) || !s.AcquiredAt.Equal(*embedded.Segments[index].AcquiredAt) {
					t.Fatal("segment source changed")
				}
			}
			cover := 17.0
			for _, db := range []*catalog.Service{nil, service} {
				for _, source := range []struct {
					path      string
					recursive bool
				}{{path, false}, {child, false}, {dir, true}} {
					summary, err := (importer.Runner{Catalog: db}).Run(ctx, source.path, importer.Options{Catalog: cat, DryRun: true, Recursive: source.recursive, CloudCover: &cover})
					if err != nil || summary.WouldImport != 1 {
						t.Fatalf("dry run: %+v %v", summary, err)
					}
				}
			}
			// Distinct catalogs allow TIFF/NITF to share a basename.

			plainCatalog := cat + "-plain-" + ext[1:]
			plainSummary, err := (importer.Runner{Catalog: service}).Run(ctx, path, importer.Options{Catalog: plainCatalog})
			if err != nil || plainSummary.Imported != 1 {
				t.Fatalf("sidecar import: %+v %v", plainSummary, err)
			}
			plain, err := service.Get(ctx, plainCatalog, "scene")
			if err != nil || plain.CloudCover == nil || *plain.CloudCover != .3 || plain.AcquiredAt == nil || plain.AcquiredAt.Year() != 2021 {
				t.Fatalf("stored sidecar: %+v %v", plain, err)
			}
			target := cat + "-" + ext[1:]
			summary, err := (importer.Runner{Catalog: service}).Run(ctx, dir, importer.Options{Catalog: target, Recursive: true, CloudCover: &cover})
			if err != nil || summary.Imported != 1 {
				t.Fatalf("import: %+v %v", summary, err)
			}
			saved, err := service.Get(ctx, target, "scene")
			if err != nil || saved.CloudCover == nil || *saved.CloudCover != cover || saved.AcquiredAt.Year() != 2021 {
				t.Fatalf("override: %+v %v", saved, err)
			}
			if err := os.WriteFile(sidecar, []byte(`<isd><IMD><IMAGE><CLOUDCOVER>1</CLOUDCOVER></IMAGE></IMD></isd>`), 0600); err != nil {
				t.Fatal(err)
			}
			for _, dry := range []bool{true, false} {
				summary, err = (importer.Runner{Catalog: service}).Run(ctx, path, importer.Options{Catalog: target, DryRun: dry})
				if err != nil || summary.Existing != 1 {
					t.Fatalf("duplicate: %+v %v", summary, err)
				}
			}
			after, err := service.Get(ctx, target, "scene")
			if err != nil || after.ID != saved.ID || !bytes.Equal(after.Metadata, saved.Metadata) || *after.CloudCover != cover || !after.AcquiredAt.Equal(*saved.AcquiredAt) {
				t.Fatal("sidecar change refreshed duplicate")
			}
		})
	}
}
