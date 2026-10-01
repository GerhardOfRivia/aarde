package database

import (
	"encoding/json"
	"math"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/importer"
	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestPostGISSegmentUnion(t *testing.T) {
	ctx, repo, service, cat := testDB(t)
	testutil.RequireGDAL(t)
	path := filepath.Join(t.TempDir(), "diverse.ntf")
	testutil.DiverseMultiNITF(t, path, false)
	runner := importer.Runner{Catalog: service}
	cover := 23.0
	for _, db := range []*catalog.Service{nil, service} {
		summary, err := (importer.Runner{Catalog: db}).Run(ctx, path, importer.Options{Catalog: cat, DryRun: true, CloudCover: &cover})
		if err != nil || summary.WouldImport != 1 {
			t.Fatalf("dry run: %+v %v", summary, err)
		}
	}
	summary, err := runner.Run(ctx, path, importer.Options{Catalog: cat, CloudCover: &cover})
	if err != nil || summary.Imported != 1 {
		t.Fatalf("import: %+v %v", summary, err)
	}
	saved, err := service.Get(ctx, cat, "diverse")
	if err != nil || len(saved.Segments) != 3 || saved.AcquiredAt != nil || saved.CloudCover == nil || *saved.CloudCover != cover || saved.Segments[0].CloudCover != nil {
		t.Fatalf("stored segments/override: %v", err)
	}
	var area float64
	var components int
	if err = repo.pool.QueryRow(ctx, "SELECT ST_Area(footprint), ST_NumGeometries(footprint) FROM imagery WHERE id=$1", saved.ID).Scan(&area, &components); err != nil || math.Abs(area-2.75) > .005 || components != 2 {
		t.Fatalf("geometric union area=%g components=%d: %v", area, components, err)
	}
	for _, tc := range []struct {
		name, polygon string
		count         int
	}{
		{"first image", `[[[-105.9,39.8],[-105.8,39.8],[-105.8,39.9],[-105.9,39.9],[-105.9,39.8]]]`, 1},
		{"second image", `[[[-104.8,38.7],[-104.7,38.7],[-104.7,38.8],[-104.8,38.8],[-104.8,38.7]]]`, 1},
		{"third image", `[[[-101.8,39.4],[-101.7,39.4],[-101.7,39.5],[-101.8,39.5],[-101.8,39.4]]]`, 1},
		{"disjoint gap", `[[[-103.8,39.4],[-103.7,39.4],[-103.7,39.5],[-103.8,39.5],[-103.8,39.4]]]`, 0},
		{"concave gap", `[[[-104.8,39.8],[-104.7,39.8],[-104.7,39.9],[-104.8,39.9],[-104.8,39.8]]]`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := geometry(t, `{"type":"Polygon","coordinates":`+tc.polygon+`}`)
			page, err := service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &g})
			if err != nil || len(page.Items) != tc.count {
				t.Fatalf("spatial search: count=%d %v", len(page.Items), err)
			}
			w := httptest.NewRecorder()
			api.Routes(service, "test", api.Access{PublicRead: true}, "none").ServeHTTP(w, httptest.NewRequest("POST", "/imagery/search", strings.NewReader(`{"catalog_id":"`+cat+`","geometry":`+string(g.JSON())+`}`)))
			var response api.SearchResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || len(response.Items) != tc.count {
				t.Fatalf("HTTP: %d %s", w.Code, w.Body.String())
			}
			if tc.count == 1 && len(response.Items[0].Segments) != 3 {
				t.Fatal("API omitted segments")
			}
		})
	}
	bytes, _ := os.ReadFile(path)
	renamed := filepath.Join(t.TempDir(), "renamed.ntf")
	_ = os.WriteFile(renamed, bytes, 0444)
	for _, source := range []string{path, renamed} {
		summary, err = runner.Run(ctx, source, importer.Options{Catalog: cat})
		if err != nil || summary.Existing != 1 {
			t.Fatalf("duplicate: %+v %v", summary, err)
		}
	}
	after, _ := service.Get(ctx, cat, "diverse")
	if after.ID != saved.ID || after.AssetLocation != path || after.Checksum != saved.Checksum || string(after.Metadata) != string(saved.Metadata) {
		t.Fatal("duplicate changed record")
	}
	invalid := filepath.Join(t.TempDir(), "invalid.ntf")
	testutil.DiverseMultiNITF(t, invalid, true)
	for _, opts := range []importer.Options{{Catalog: cat}, {Catalog: cat, DryRun: true}} {
		summary, err = runner.Run(ctx, invalid, opts)
		if err == nil || summary.Failed != 1 {
			t.Fatalf("invalid file accepted: %+v %v", summary, err)
		}
	}
	if _, err = service.Get(ctx, cat, "invalid"); err != catalog.ErrNotFound {
		t.Fatalf("partial record created: %v", err)
	}
}
