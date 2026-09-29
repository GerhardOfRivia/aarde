package database

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/geo"
	"github.com/GerhardOfRivia/aarde/internal/importer"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/google/uuid"
)

func testDB(t *testing.T) (context.Context, *Repository, *catalog.Service, string) {
	t.Helper()
	url := os.Getenv("AARDE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set AARDE_TEST_DATABASE_URL or run make integration for real PostGIS tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	r, err := Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Close)
	if err := r.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := r.Migrate(ctx); err != nil {
		t.Fatalf("migration not idempotent: %v", err)
	}
	cat := "test-" + uuid.NewString()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := r.pool.Exec(ctx, "DELETE FROM imagery WHERE catalog_id LIKE $1", cat+"%"); err != nil {
			t.Error(err)
		}
	})
	return ctx, r, catalog.New(r), cat
}
func geometry(t *testing.T, s string) geo.Geometry {
	t.Helper()
	g, err := geo.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

const rectangle = `{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}`

func model(t *testing.T, cat, id, shape string) catalog.Imagery {
	t.Helper()
	sum := sha256.Sum256([]byte(cat + id))
	return catalog.Imagery{CatalogID: cat, ImageID: id, DisplayName: id + ".tif", Width: 10, Height: 10, BandCount: 1, SourceCRS: "EPSG:4326", AssetLocation: "/data/" + id + ".tif", Checksum: hex.EncodeToString(sum[:]), Metadata: json.RawMessage(`{}`), Footprint: geometry(t, shape)}
}

// Hold inserts after the service's duplicate checks, while retaining the real
// repository for every database operation.
type insertBarrierStore struct {
	catalog.Store
	ready   chan<- struct{}
	release <-chan struct{}
}

func (s insertBarrierStore) Insert(ctx context.Context, i catalog.Imagery) (catalog.Imagery, bool, error) {
	select {
	case s.ready <- struct{}{}:
	case <-ctx.Done():
		return catalog.Imagery{}, false, ctx.Err()
	}
	select {
	case <-s.release:
		return s.Store.Insert(ctx, i)
	case <-ctx.Done():
		return catalog.Imagery{}, false, ctx.Err()
	}
}

func TestPostGISCatalogAndSpatial(t *testing.T) {
	ctx, repo, service, cat := testDB(t)
	first, existing, err := service.Import(ctx, model(t, cat, "ABC123", rectangle))
	if err != nil || existing {
		t.Fatalf("import: %v %v", existing, err)
	}
	if first.Footprint.Type != "MultiPolygon" || first.AcquiredAt != nil || first.ImportedAt.IsZero() {
		t.Fatal("incorrect stored model")
	}
	if got, err := service.Get(ctx, cat, "ABC123"); err != nil || got.ID != first.ID {
		t.Fatalf("exact lookup: %+v %v", got, err)
	}
	if _, err := service.Get(ctx, cat, "ABC"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("partial ID matched: %v", err)
	}
	if _, err := service.Get(ctx, cat, "unknown"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("unknown ID: %v", err)
	}
	for _, id := range []string{"second", "third"} {
		if _, _, err := service.Import(ctx, model(t, cat, id, rectangle)); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := service.Import(ctx, model(t, cat+"-other", "ABC123", rectangle)); err != nil {
		t.Fatal(err)
	}
	p, err := service.Search(ctx, catalog.Query{CatalogID: cat, Limit: 2})
	if err != nil || len(p.Items) != 2 || !p.HasMore {
		t.Fatalf("first page: %+v %v", p, err)
	}
	p2, err := service.Search(ctx, catalog.Query{CatalogID: cat, Limit: 2, Offset: 2})
	if err != nil || len(p2.Items) != 1 || p2.HasMore || p2.Items[0].ID == p.Items[0].ID || p2.Items[0].ID == p.Items[1].ID {
		t.Fatalf("second page: %+v %v", p2, err)
	}
	p, err = service.Search(ctx, catalog.Query{ImageIDs: []string{"ABC123"}, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, i := range p.Items {
		if i.CatalogID == cat || i.CatalogID == cat+"-other" {
			found++
		}
	}
	if found != 2 {
		t.Fatal("cross-catalog exact lookup failed")
	}
	duplicate := model(t, cat, "renamed", rectangle)
	duplicate.Checksum = first.Checksum
	got, exists, err := service.Import(ctx, duplicate)
	if err != nil || !exists || got.ID != first.ID {
		t.Fatalf("duplicate checksum: %v %v", exists, err)
	}
	conflict := model(t, cat, "ABC123", rectangle)
	conflict.Checksum = strings.Repeat("b", 64)
	if _, _, err := service.Import(ctx, conflict); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("filename conflict: %v", err)
	}
	for _, tc := range []struct {
		name, shape string
		matches     int
	}{
		{"inside", `{"type":"Polygon","coordinates":[[[0.1,0.1],[0.2,0.1],[0.2,0.2],[0.1,0.1]]]}`, 3},
		{"none", `{"type":"Polygon","coordinates":[[[5,5],[6,5],[6,6],[5,5]]]}`, 0},
		{"partial", `{"type":"Polygon","coordinates":[[[1,1],[3,1],[3,3],[1,3],[1,1]]]}`, 3},
		{"boundary", `{"type":"Polygon","coordinates":[[[2,0],[3,0],[3,1],[2,1],[2,0]]]}`, 3},
		{"multipolygon", `{"type":"MultiPolygon","coordinates":[[[[0,0],[1,0],[1,1],[0,0]]],[[[8,8],[9,8],[9,9],[8,8]]]]}`, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := geometry(t, tc.shape)
			p, err := service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &g})
			if err != nil || len(p.Items) != tc.matches {
				t.Fatalf("matches=%d want=%d error=%v", len(p.Items), tc.matches, err)
			}
		})
	}
	t.Run("actual polygon not envelope or center", func(t *testing.T) {
		triangle := `{"type":"Polygon","coordinates":[[[10,10],[12,10],[10,12],[10,10]]]}`
		if _, _, err := service.Import(ctx, model(t, cat, "triangle", triangle)); err != nil {
			t.Fatal(err)
		}
		g := geometry(t, `{"type":"Polygon","coordinates":[[[11.5,11.5],[11.9,11.5],[11.9,11.9],[11.5,11.5]]]}`)
		p, err := service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &g})
		if err != nil || len(p.Items) != 0 {
			t.Fatalf("envelope false positive: %+v %v", p, err)
		}
	})
	t.Run("invalid topology", func(t *testing.T) {
		g := geometry(t, `{"type":"Polygon","coordinates":[[[0,0],[2,2],[0,2],[2,0],[0,0]]]}`)
		if _, err := service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &g}); !errors.Is(err, catalog.ErrInvalidGeometry) {
			t.Fatalf("accepted self intersection: %v", err)
		}
	})
	t.Run("concurrent duplicates", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ready := make(chan struct{}, 2)
		release := make(chan struct{})
		concurrentService := catalog.New(insertBarrierStore{Store: repo, ready: ready, release: release})
		i := model(t, cat, "concurrent", rectangle)
		var wg sync.WaitGroup
		defer func() {
			cancel()
			wg.Wait()
		}()
		type result struct {
			item     catalog.Imagery
			existing bool
			err      error
		}
		results := make(chan result, 2)
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				item, existing, err := concurrentService.Import(ctx, i)
				results <- result{item, existing, err}
			}()
		}
		// Both callers must miss the duplicate checks before either can insert.
		for range 2 {
			select {
			case <-ready:
			case early := <-results:
				t.Fatalf("import returned before both inserts were ready: %+v", early)
			case <-ctx.Done():
				t.Fatalf("waiting for both inserts: %v", ctx.Err())
			}
		}
		close(release)
		wg.Wait()
		close(results)
		count := 0
		var storedID uuid.UUID
		for result := range results {
			if result.err != nil {
				t.Fatal(result.err)
			}
			if result.item.ID == uuid.Nil {
				t.Fatal("import returned an empty ID")
			}
			if storedID != uuid.Nil && result.item.ID != storedID {
				t.Fatal("duplicate imports returned different records")
			}
			storedID = result.item.ID
			if result.existing {
				count++
			}
		}
		if count != 1 {
			t.Fatalf("expected one existing, got %d", count)
		}
	})
	t.Run("HTTP real database", func(t *testing.T) {
		h := api.Routes(service, "dev", api.Access{Token: "test-token"}, "osm")
		for _, tc := range []struct {
			method, path, body string
			status             int
		}{
			{"GET", "/health", "", 200}, {"GET", "/catalogs", "", 200}, {"GET", "/imagery/" + cat + "/ABC123", "", 200}, {"GET", "/imagery/" + cat + "/absent", "", 404},
			{"GET", "/imagery?catalog_id=" + cat + "&image_id=ABC123", "", 200},
			{"POST", "/imagery/search", `{"catalog_id":"` + cat + `","geometry":` + rectangle + `}`, 200},
			{"POST", "/imagery/search", `{"geometry":{"type":"Polygon","coordinates":[[[0,0],[2,2],[0,2],[2,0],[0,0]]]}}`, 400},
		} {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("%s: %d: %s", tc.path, w.Code, w.Body.String())
			}
		}
	})
}

func TestGeoTIFFImportIntegration(t *testing.T) {
	ctx, _, service, cat := testDB(t)
	if _, err := exec.LookPath("gdal_create"); err != nil {
		t.Fatal("GDAL is required for integration tests; use make integration")
	}
	dir := t.TempDir()
	scene := filepath.Join(dir, "scene.tif")
	create := func(path, crs string, bounds []string) {
		t.Helper()
		args := []string{"-of", "GTiff", "-outsize", "10", "10", "-bands", "1", "-burn", "42", "-mo", "CLOUD_COVER=12.5", "-a_srs", crs, "-a_ullr"}
		args = append(args, bounds...)
		args = append(args, path)
		if out, err := exec.CommandContext(ctx, "gdal_create", args...).CombinedOutput(); err != nil {
			t.Fatalf("generate raster: %v %s", err, out)
		}
	}
	create(scene, "EPSG:4326", []string{"-106", "40", "-105", "39"})
	before, err := os.ReadFile(scene)
	if err != nil {
		t.Fatal(err)
	}
	runner := importer.Runner{Catalog: service}
	s, err := runner.Run(ctx, scene, importer.Options{Catalog: cat})
	if err != nil || s.Imported != 1 {
		t.Fatalf("single import: %+v %v", s, err)
	}
	i, err := service.Get(ctx, cat, "scene")
	if err != nil || i.AcquiredAt != nil || i.Width != 10 || i.AssetLocation != scene || i.CloudCover == nil || *i.CloudCover != 12.5 {
		t.Fatalf("imported model: %+v %v", i, err)
	}
	zero := 0.0
	s, err = runner.Run(ctx, scene, importer.Options{Catalog: cat + "-override", CloudCover: &zero})
	if err != nil || s.Imported != 1 {
		t.Fatalf("override import: %+v %v", s, err)
	}
	overridden, err := service.Get(ctx, cat+"-override", "scene")
	if err != nil || overridden.CloudCover == nil || *overridden.CloudCover != 0 {
		t.Fatalf("override lost: %+v %v", overridden, err)
	}
	s, err = runner.Run(ctx, scene, importer.Options{Catalog: cat, CloudCover: &zero})
	if err != nil || s.Existing != 1 {
		t.Fatalf("duplicate: %+v %v", s, err)
	}
	original, err := service.Get(ctx, cat, "scene")
	if err != nil || original.CloudCover == nil || *original.CloudCover != 12.5 {
		t.Fatal("duplicate changed cloud cover")
	}
	if err := os.Mkdir(filepath.Join(dir, "nested"), 0755); err != nil {
		t.Fatal(err)
	}
	create(filepath.Join(dir, "nested", "projected.tiff"), "EPSG:32613", []string{"400000", "4400000", "401000", "4399000"})
	for path, data := range map[string][]byte{"copy.tif": before, "corrupt.tif": []byte("not a TIFF"), "readme.txt": []byte("ignored")} {
		if err := os.WriteFile(filepath.Join(dir, path), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	s, err = runner.Run(ctx, dir, importer.Options{Catalog: cat, Recursive: true})
	if err == nil || s.Imported != 1 || s.Existing != 2 || s.Failed != 1 || s.Skipped != 1 {
		t.Fatalf("recursive summary: %+v %v", s, err)
	}
	projected, err := service.Get(ctx, cat, "projected")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(projected.SourceCRS, "32613") {
		t.Fatal("source CRS lost")
	}
	if err := projected.Footprint.Validate(); err != nil {
		t.Fatalf("projected footprint is not WGS84: %v", err)
	}
	g := geometry(t, `{"type":"Polygon","coordinates":[[[-107,38],[-104,38],[-104,41],[-107,41],[-107,38]]]}`)
	p, err := service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &g})
	if err != nil || len(p.Items) != 2 {
		t.Fatalf("projected search: %+v %v", p, err)
	}
	s, err = runner.Run(ctx, scene, importer.Options{Catalog: cat + "-dry", DryRun: true})
	if err != nil || s.WouldImport != 1 || s.Imported != 0 {
		t.Fatalf("dry run: %+v %v", s, err)
	}
	p, err = service.Search(ctx, catalog.Query{CatalogID: cat + "-dry"})
	if err != nil || len(p.Items) != 0 {
		t.Fatal("dry run wrote records")
	}
	s, err = runner.Run(ctx, scene, importer.Options{Catalog: cat, DryRun: true})
	if err != nil || s.Existing != 1 {
		t.Fatal("dry run did not detect duplicate")
	}
	inspection, err := raster.Inspect(ctx, scene)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(scene)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(before)
	if string(before) != string(after) || inspection.Checksum != hex.EncodeToString(sum[:]) {
		t.Fatal("source changed or checksum incorrect")
	}
}

func TestCloudCoverPersistence(t *testing.T) {
	ctx, repo, service, cat := testDB(t)
	zero, fraction, full := 0.0, 12.5, 100.0
	for n, cover := range []*float64{nil, &zero, &fraction, &full} {
		i := model(t, cat, fmt.Sprintf("cloud-%d", n), rectangle)
		i.CloudCover = cover
		saved, _, err := service.Import(ctx, i)
		if err != nil {
			t.Fatal(err)
		}
		check := func(got catalog.Imagery) {
			t.Helper()
			if (got.CloudCover == nil) != (cover == nil) || cover != nil && *got.CloudCover != *cover {
				t.Fatalf("cloud cover lost: %+v", got)
			}
		}
		check(saved)
		got, err := service.Get(ctx, cat, i.ImageID)
		if err != nil {
			t.Fatal(err)
		}
		check(got)
		got, err = repo.ByChecksum(ctx, cat, i.Checksum)
		if err != nil {
			t.Fatal(err)
		}
		check(got)
		page, err := service.Search(ctx, catalog.Query{CatalogID: cat, ImageIDs: []string{i.ImageID}})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("search: %+v %v", page, err)
		}
		check(page.Items[0])
		for _, invalid := range []float64{-1, 100.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
			if _, err := repo.pool.Exec(ctx, "UPDATE imagery SET cloud_cover=$1 WHERE id=$2", invalid, saved.ID); err == nil {
				t.Fatalf("database accepted %v", invalid)
			}
		}
	}
}

func TestCloudCoverFiltering(t *testing.T) {
	ctx, repo, service, cat := testDB(t)
	zero, fraction, threshold, full := 0.0, 19.9, 20.0, 100.0
	outside := `{"type":"Polygon","coordinates":[[[5,5],[6,5],[6,6],[5,5]]]}`
	// Nonmatches are newer than matches, so filtering a fetched page fails.
	for n, fixture := range []struct {
		name, catalog, shape string
		cover                *float64
	}{
		{"zero", cat, rectangle, &zero},
		{"fraction", cat, rectangle, &fraction},
		{"boundary", cat, rectangle, &threshold},
		{"full", cat, rectangle, &full},
		{"unknown", cat, rectangle, nil},
		{"outside", cat, outside, &zero},
		{"zero", cat + "-other", rectangle, &zero},
	} {
		i := model(t, fixture.catalog, cat+"-"+fixture.name, fixture.shape)
		i.CloudCover = fixture.cover
		saved, _, err := service.Import(ctx, i)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repo.pool.Exec(ctx, "UPDATE imagery SET imported_at=$1 WHERE id=$2", time.Date(2026, 1, 1, 0, 0, n, 0, time.UTC), saved.ID); err != nil {
			t.Fatal(err)
		}
	}
	g := geometry(t, rectangle)
	assertPage := func(q catalog.Query, want []string, more bool) {
		t.Helper()
		page, err := service.Search(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, strings.TrimPrefix(item.ImageID, cat+"-"))
		}
		if !slices.Equal(got, want) || page.HasMore != more || page.Offset != q.Offset {
			t.Fatalf("query %+v: got %v, has_more=%v, offset=%d; want %v, has_more=%v", q, got, page.HasMore, page.Offset, want, more)
		}
	}
	for _, tc := range []struct {
		name  string
		cover *float64
		want  []string
	}{
		{"omitted", nil, []string{"unknown", "full", "boundary", "fraction", "zero"}},
		{"twenty", &threshold, []string{"fraction", "zero"}},
		{"zero", &zero, nil},
		{"hundred", &full, []string{"boundary", "fraction", "zero"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertPage(catalog.Query{CatalogID: cat, Geometry: &g, CloudCoverLT: tc.cover}, tc.want, false)
		})
	}
	assertPage(catalog.Query{CatalogID: cat}, []string{"outside", "unknown", "full", "boundary", "fraction", "zero"}, false)
	assertPage(catalog.Query{CatalogID: cat, CloudCoverLT: &threshold}, []string{"outside", "fraction", "zero"}, false)
	assertPage(catalog.Query{CatalogID: cat, Geometry: &g, CloudCoverLT: &threshold, ImageIDs: []string{cat + "-fraction", cat + "-boundary", cat + "-unknown"}}, []string{"fraction"}, false)
	assertPage(catalog.Query{Geometry: &g, CloudCoverLT: &threshold, ImageIDs: []string{cat + "-zero"}}, []string{"zero", "zero"}, false)
	for _, spatial := range []bool{false, true} {
		q := catalog.Query{CatalogID: cat, CloudCoverLT: &threshold, Limit: 1}
		want := []string{"outside", "fraction", "zero"}
		if spatial {
			q.Geometry = &g
			want = want[1:]
		}
		for offset, id := range want {
			q.Offset = offset
			assertPage(q, []string{id}, offset < len(want)-1)
		}
		q.Offset = len(want)
		assertPage(q, nil, false)
		q.Offset, q.Limit = 0, len(want)
		assertPage(q, want, false)
	}

	h := api.Routes(service, "dev", api.Access{PublicRead: true}, "none")
	for _, tc := range []struct {
		method, path, body string
		want               []string
		more               bool
	}{
		{"GET", "/imagery?catalog_id=" + cat + "&cloud_cover_lt=20&limit=1&offset=1", "", []string{"fraction"}, true},
		{"GET", "/imagery?catalog_id=" + cat + "&cloud_cover_lt=0", "", nil, false},
		{"POST", "/imagery/search", `{"catalog_id":"` + cat + `","geometry":` + rectangle + `,"cloud_cover_lt":20,"limit":1,"offset":1}`, []string{"zero"}, false},
		{"POST", "/imagery/search", `{"catalog_id":"` + cat + `","geometry":` + rectangle + `,"cloud_cover_lt":null}`, []string{"unknown", "full", "boundary", "fraction", "zero"}, false},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)))
		if w.Code != 200 {
			t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
		}
		var page api.SearchResponse
		if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, strings.TrimPrefix(item.ImageID, cat+"-"))
		}
		if !slices.Equal(got, tc.want) || page.HasMore != tc.more {
			t.Fatalf("HTTP %s %s: %s", tc.method, tc.path, w.Body.String())
		}
	}
}
