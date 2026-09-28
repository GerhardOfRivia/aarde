package importer

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/geo"
	"github.com/GerhardOfRivia/aarde/internal/raster"
)

func TestDiscovery(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"one.tif", "two.TIFF", "readme.txt", "child/three.tif"} {
		if err := os.WriteFile(filepath.Join(dir, p), []byte(p), 0644); err != nil {
			t.Fatal(err)
		}
	}
	files, skipped, err := Discover(context.Background(), dir, false)
	if err != nil || len(files) != 2 || skipped != 1 {
		t.Fatalf("non-recursive: %v %d %v", files, skipped, err)
	}
	files, skipped, err = Discover(context.Background(), dir, true)
	if err != nil || len(files) != 3 || skipped != 1 {
		t.Fatalf("recursive: %v %d %v", files, skipped, err)
	}
	files, _, err = Discover(context.Background(), filepath.Join(dir, "one.tif"), false)
	if err != nil || len(files) != 1 {
		t.Fatal("single file discovery failed")
	}
	if ImageID("/data/WV03_20260910_ABC123.tif") != "WV03_20260910_ABC123" {
		t.Fatal("incorrect ID")
	}
}
func TestDryRunContinuesAndDeduplicates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"one.tif", "two.tif", "bad.tif", "ignored.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	g, _ := geo.Parse([]byte(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`))
	r := Runner{Inspect: func(_ context.Context, path string) (raster.Inspection, error) {
		if filepath.Base(path) == "bad.tif" {
			return raster.Inspection{}, errors.New("corrupt raster")
		}
		return raster.Inspection{Width: 10, Height: 10, BandCount: 1, SourceCRS: "EPSG:4326", Footprint: g, Checksum: strings.Repeat("a", 64), AssetLocation: path, Metadata: json.RawMessage(`{}`)}, nil
	}}
	s, err := r.Run(context.Background(), dir, Options{DryRun: true})
	if err == nil || s.Imported != 0 || s.WouldImport != 1 || s.Existing != 1 || s.Failed != 1 || s.Skipped != 1 {
		t.Fatalf("summary=%+v err=%v", s, err)
	}
}
