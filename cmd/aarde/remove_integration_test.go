package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/database"
	"github.com/GerhardOfRivia/aarde/internal/geo"
	"github.com/google/uuid"
)

func TestRemoveCLIWithPostGIS(t *testing.T) {
	url := os.Getenv("AARDE_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("set AARDE_TEST_DATABASE_URL or run make integration for real PostGIS tests")
	}
	t.Setenv("AARDE_DATABASE_URL", url)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := database.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	service := catalog.New(db)
	cat := "remove-test-" + uuid.NewString() + "%'"
	other := cat + "-other"
	id := "image-" + uuid.NewString()
	sum := sha256.Sum256([]byte(id))
	checksum := hex.EncodeToString(sum[:])
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, name := range []string{cat, other} {
			if _, err := service.RemoveCatalog(cleanup, name); err != nil && !errors.Is(err, catalog.ErrCatalogNotFound) {
				t.Error(err)
			}
		}
		if err := service.RemoveImage(cleanup, "default", id); err != nil && !errors.Is(err, catalog.ErrNotFound) {
			t.Error(err)
		}
	}()
	asset := filepath.Join(t.TempDir(), "source.tif")
	if err := os.WriteFile(asset, []byte("source imagery bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	footprint, err := geo.Parse([]byte(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	importImage := func(cat, id, checksum string) {
		t.Helper()
		_, exists, err := service.Import(ctx, catalog.Imagery{
			CatalogID: cat, ImageID: id, Width: 1, Height: 1, BandCount: 1,
			SourceCRS: "EPSG:4326", AssetLocation: asset, Checksum: checksum,
			Metadata: []byte(`{}`), Footprint: footprint,
		})
		if err != nil || exists {
			t.Fatalf("import %s/%s: existing=%v error=%v", cat, id, exists, err)
		}
	}
	for _, name := range []string{"default", cat, other} {
		importImage(name, id, checksum)
	}
	importImage(cat, "second", strings.Repeat("b", 64))
	invoke := func(input string, args ...string) (string, error) {
		t.Helper()
		var out strings.Builder
		err := runWithInput(ctx, append([]string{"remove"}, args...), strings.NewReader(input), &out)
		return out.String(), err
	}
	assertPresent := func(cat, id string, present bool) {
		t.Helper()
		_, err := service.Get(ctx, cat, id)
		if present && err != nil || !present && !errors.Is(err, catalog.ErrNotFound) {
			t.Fatalf("%s/%s: present=%v error=%v", cat, id, present, err)
		}
	}
	for _, args := range [][]string{
		{"-image", id, "-catalog", cat},
		{"-catalog", cat, "-image", id},
		{"--image=" + id, "--catalog=default"},
	} {
		if out, err := invoke("yes\n", args...); err == nil || !strings.Contains(err.Error(), "mutually exclusive") || out != "" {
			t.Fatalf("combined flags %v: error=%v output=%q", args, err, out)
		}
		assertPresent("default", id, true)
		assertPresent(cat, id, true)
		assertPresent(cat, "second", true)
		assertPresent(other, id, true)
	}
	if out, err := invoke("", "-image", id); err != nil || !strings.Contains(out, `catalog "default"`) {
		t.Fatalf("default image removal: %v %s", err, out)
	}
	assertPresent("default", id, false)
	assertPresent(cat, id, true)
	assertPresent(other, id, true)
	if _, err := invoke("", "-image", id); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("missing image: %v", err)
	}
	// Removal releases both the image ID and checksum for a fresh import.
	importImage("default", id, checksum)
	for _, input := range []string{"", "\n", "no\n"} {
		if out, err := invoke(input, "-catalog", cat); err != nil || !strings.Contains(out, "cancelled") {
			t.Fatalf("cancel removal: %v %s", err, out)
		}
		assertPresent(cat, id, true)
		assertPresent(cat, "second", true)
	}
	if out, err := invoke("yes\n", "-catalog", cat); err != nil || !strings.Contains(out, "(2 image records)") {
		t.Fatalf("catalog removal: %v %s", err, out)
	}
	assertPresent(cat, id, false)
	assertPresent(cat, "second", false)
	assertPresent(other, id, true)
	assertPresent("default", id, true)
	catalogs, err := service.Catalogs(ctx)
	if err != nil || slices.Contains(catalogs, cat) || !slices.Contains(catalogs, other) {
		t.Fatalf("catalog listing after removal: %v %v", catalogs, err)
	}
	if _, err := invoke("yes\n", "-catalog", cat); !errors.Is(err, catalog.ErrCatalogNotFound) {
		t.Fatalf("missing catalog: %v", err)
	}
	contents, err := os.ReadFile(asset)
	if err != nil || string(contents) != "source imagery bytes" {
		t.Fatalf("source asset was changed: %q %v", contents, err)
	}
}
