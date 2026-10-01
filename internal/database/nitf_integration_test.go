package database

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/importer"
	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestPostGISNITFImports(t *testing.T) {
	ctx, repo, service, cat := testDB(t)
	testutil.RequireGDAL(t)
	dir := t.TempDir()
	scene := filepath.Join(dir, "b-scene.NITF")
	multi := filepath.Join(dir, "a-multi.ntf")
	testutil.NITF(t, scene, "IID1=SYNTHETIC", "SDE_TRE=YES")
	testutil.MultiNITF(t, multi)
	testutil.GeoTIFF(t, filepath.Join(dir, "c-tiff.tif"))
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	testutil.NITF(t, filepath.Join(nested, "gcp.ntf"), "IGEOLO=400000N1060000W400000N1050000W390000N1043000W390000N1060000W")
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("ignored"), 0644); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(scene)
	var events []importer.Event
	runner := importer.Runner{Catalog: service, Report: func(e importer.Event) { events = append(events, e) }}
	// Both dry-run modes inspect the entire multi-image container.
	for _, db := range []*catalog.Service{nil, service} {
		r := importer.Runner{Catalog: db}
		summary, err := r.Run(ctx, dir, importer.Options{Catalog: cat, Recursive: true, DryRun: true})
		if err != nil || summary.Failed != 0 || summary.WouldImport != 4 || summary.Imported != 0 || summary.Skipped != 1 {
			t.Fatalf("dry run: %+v %v", summary, err)
		}
		page, err := service.Search(ctx, catalog.Query{CatalogID: cat})
		if err != nil || len(page.Items) != 0 {
			t.Fatal("dry run wrote records")
		}
	}
	summary, err := runner.Run(ctx, dir, importer.Options{Catalog: cat, Recursive: true})
	if err != nil || summary.Failed != 0 || summary.Imported != 4 || summary.Skipped != 1 {
		t.Fatalf("mixed import: %+v %v", summary, err)
	}
	if len(events) != 4 || events[0].Path != multi || events[0].Status != "imported" {
		t.Fatalf("file reporting: %+v", events)
	}
	if got, err := service.Get(ctx, cat, "a-multi"); err != nil || len(got.Segments) != 3 {
		t.Fatalf("multi-image record: %+v %v", got, err)
	}

	original, err := service.Get(ctx, cat, "b-scene")
	if err != nil {
		t.Fatal(err)
	}
	if original.Format() == nil || *original.Format() != "NITF" || original.AssetLocation != scene || original.BandCount != 3 || original.AcquiredAt == nil || original.AcquiredAt.Format(time.RFC3339) != "2026-09-10T12:00:00Z" || original.CloudCover != nil {
		t.Fatalf("stored model: %+v", original)
	}
	if !strings.Contains(string(original.Metadata), "xml:TRE") || original.Footprint.Type != "MultiPolygon" {
		t.Fatal("metadata or footprint lost")
	}
	for _, path := range []string{scene, filepath.Join(dir, "renamed.ntf")} {
		if path != scene {
			if err := os.WriteFile(path, before, 0444); err != nil {
				t.Fatal(err)
			}
		}
		zero := 0.0
		summary, err = runner.Run(ctx, path, importer.Options{Catalog: cat, CloudCover: &zero})
		if err != nil || summary.Existing != 1 || summary.Imported != 0 {
			t.Fatalf("duplicate: %+v %v", summary, err)
		}
		got, err := service.Get(ctx, cat, "b-scene")
		if err != nil || got.ID != original.ID || got.AssetLocation != original.AssetLocation || string(got.Metadata) != string(original.Metadata) || got.CloudCover != nil {
			t.Fatal("duplicate changed original record")
		}
	}
	conflict := filepath.Join(t.TempDir(), "b-scene.ntf")
	testutil.NITF(t, conflict, "IID1=DIFFERENT")
	summary, err = runner.Run(ctx, conflict, importer.Options{Catalog: cat})
	if err == nil || summary.Failed != 1 || !errors.Is(events[len(events)-1].Err, catalog.ErrConflict) {
		t.Fatalf("ID conflict: %+v %v", summary, err)
	}
	zero := 0.0
	summary, err = runner.Run(ctx, scene, importer.Options{Catalog: cat + "-override", CloudCover: &zero})
	if err != nil || summary.Imported != 1 {
		t.Fatalf("override: %+v %v", summary, err)
	}
	override, err := service.Get(ctx, cat+"-override", "b-scene")
	if err != nil || override.CloudCover == nil || *override.CloudCover != 0 {
		t.Fatal("zero override lost")
	}
	g := geometry(t, `{"type":"Polygon","coordinates":[[[-106,39],[-105,39],[-105,40],[-106,40],[-106,39]]]}`)
	page, err := service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &g})
	if err != nil || len(page.Items) != 4 {
		t.Fatalf("NITF/GeoTIFF PostGIS intersection: %+v %v", page, err)
	}
	threshold := 20.0
	page, err = service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &g, CloudCoverLT: &threshold})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("unknown cloud cover passed filter")
	}
	page, err = service.Search(ctx, catalog.Query{CatalogID: cat + "-override", Geometry: &g, CloudCoverLT: &threshold})
	if err != nil || len(page.Items) != 1 {
		t.Fatal("zero cloud cover did not pass filter")
	}
	// Search and HTTP work with the physical source unavailable.
	if err := os.Rename(scene, scene+".offline"); err != nil {
		t.Fatal(err)
	}
	handler := api.Routes(service, "test", api.Access{PublicRead: true}, "none")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("GET", "/imagery/"+cat+"/b-scene", nil))
	var response api.ImageryResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Format == nil || *response.Format != "NITF" || response.AssetLocation != scene {
		t.Fatalf("HTTP detail: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/imagery/search", strings.NewReader(`{"catalog_id":"`+cat+`","geometry":`+string(g.JSON())+`}`)))
	var responsePage api.SearchResponse
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &responsePage) != nil || len(responsePage.Items) != 4 {
		t.Fatalf("HTTP spatial search: %d %s", w.Code, w.Body.String())
	}
	after, _ := os.ReadFile(scene + ".offline")
	if string(before) != string(after) {
		t.Fatal("source changed")
	}
	if err := os.Rename(scene+".offline", scene); err != nil {
		t.Fatal(err)
	}
	t.Run("read only database dry run", func(t *testing.T) {
		u, err := url.Parse(os.Getenv("AARDE_TEST_DATABASE_URL"))
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("default_transaction_read_only", "on")
		u.RawQuery = q.Encode()
		readonly, err := Open(ctx, u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer readonly.Close()
		var migrationsBefore, migrationsAfter int
		if err := repo.pool.QueryRow(ctx, "SELECT count(*) FROM aarde_migrations").Scan(&migrationsBefore); err != nil {
			t.Fatal(err)
		}
		r := importer.Runner{Catalog: catalog.New(readonly)}
		s, err := r.Run(ctx, scene, importer.Options{Catalog: cat, DryRun: true})
		if err != nil || s.Existing != 1 {
			t.Fatalf("readonly existing: %+v %v", s, err)
		}
		s, err = r.Run(ctx, scene, importer.Options{Catalog: cat + "-dry", DryRun: true})
		if err != nil || s.WouldImport != 1 {
			t.Fatalf("readonly new: %+v %v", s, err)
		}
		s, err = r.Run(ctx, multi, importer.Options{Catalog: cat + "-dry", DryRun: true})
		if err != nil || s.WouldImport != 1 {
			t.Fatalf("readonly rejection: %+v %v", s, err)
		}
		if err := repo.pool.QueryRow(ctx, "SELECT count(*) FROM aarde_migrations").Scan(&migrationsAfter); err != nil || migrationsAfter != migrationsBefore {
			t.Fatal("dry run changed migrations")
		}
	})
	t.Run("concurrent physical file imports", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		ready := make(chan struct{}, 2)
		release := make(chan struct{})
		r := importer.Runner{Catalog: catalog.New(insertBarrierStore{Store: repo, ready: ready, release: release})}
		type result struct {
			s   importer.Summary
			err error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() {
				s, err := r.Run(ctx, multi, importer.Options{Catalog: cat + "-concurrent"})
				results <- result{s, err}
			}()
		}
		for range 2 {
			select {
			case <-ready:
			case early := <-results:
				t.Fatalf("early import: %+v", early)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
		close(release)
		imported, existing := 0, 0
		for range 2 {
			result := <-results
			if result.err != nil {
				t.Fatal(result.err)
			}
			imported += result.s.Imported
			existing += result.s.Existing
		}
		if imported != 1 || existing != 1 {
			t.Fatalf("concurrent imported=%d existing=%d", imported, existing)
		}
	})
}
