package raster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestGDALDiverseSegments(t *testing.T) {
	testutil.RequireGDAL(t)
	path := filepath.Join(t.TempDir(), "diverse.ntf")
	testutil.DiverseMultiNITF(t, path, false)
	before, _ := os.ReadFile(path)
	_ = os.Chmod(path, 0444)
	got, err := Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(before)
	if got.AssetLocation != path || got.Checksum != hex.EncodeToString(sum[:]) || len(got.Segments) != 3 || got.Width != 0 || got.Height != 0 || got.BandCount != 0 || got.SourceCRS != "" || got.AcquiredAt != nil || got.CloudCover != nil || got.Bounds != nil {
		t.Fatalf("wrong aggregate: %+v", got)
	}
	for index, w := range []int{16, 8, 20} {
		segment := got.Segments[index]
		if segment.Index != index || segment.Width != w || segment.AcquiredAt == nil || segment.SourceCRS == "" || !strings.Contains(string(segment.Metadata), "SEGMENT"+string(rune('0'+index))) {
			t.Fatalf("segment %d: %+v", index, segment)
		}
	}
	if got.Segments[1].Height != 12 || got.Segments[1].BandCount != 1 || got.Segments[2].BandCount != 2 {
		t.Fatal("dimensions/bands lost")
	}
	var polys [][][][]float64
	_ = json.Unmarshal(got.Footprint.Coordinates, &polys)
	if len(polys) != 2 {
		t.Fatalf("overlaps were not merged or disjoint component lost: %s", got.Footprint.JSON())
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("source changed")
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatal("unexpected sidecars")
	}
	invalid := filepath.Join(t.TempDir(), "invalid.ntf")
	testutil.DiverseMultiNITF(t, invalid, true)
	if _, err = Inspect(context.Background(), invalid); err == nil || !strings.Contains(err.Error(), "image segment 2") || !strings.Contains(err.Error(), "no usable affine") {
		t.Fatalf("invalid segment: %v", err)
	}
}

func TestSegmentMetadataAggregation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cloud string
		time  string
		known bool
	}{
		{"unanimous", "12.5", "20260910120000", true},
		{"differing", "80", "20260911120000", false},
		{"missing", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.ntf")
			_ = os.WriteFile(path, []byte("whole container"), 0444)
			container := nitfInfo(t)
			container["metadata"].(map[string]any)["SUBDATASETS"] = map[string]string{"SUBDATASET_1_NAME": "NITF_IM:0:" + path, "SUBDATASET_1_DESC": "one", "SUBDATASET_2_NAME": "NITF_IM:1:" + path, "SUBDATASET_2_DESC": "two"}
			first := nitfInfo(t)
			first["metadata"].(map[string]any)[""].(map[string]any)["CLOUD_COVER"] = "12.5"
			second := nitfInfo(t)
			header := second["metadata"].(map[string]any)[""].(map[string]any)
			header["CLOUD_COVER"] = tc.cloud
			header["NITF_IDATIM"] = tc.time
			script := "for last do :; done\ncase \"$last\" in\nNITF_IM:0:*) printf '%s' '" + string(encodeInfo(t, first)) + "';;\nNITF_IM:1:*) printf '%s' '" + string(encodeInfo(t, second)) + "';;\n*) printf '%s' '" + string(encodeInfo(t, container)) + "';;\nesac\n"
			dir := fakeTool(t, "gdalinfo", script)
			union := `{"type":"FeatureCollection","features":[{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[[-106,40],[-105,40],[-105,39],[-106,39],[-106,40]]]}}]}`
			_ = os.WriteFile(filepath.Join(dir, "ogr2ogr"), []byte("#!/bin/sh\n/bin/cat >/dev/null\nprintf '%s' '"+union+"'\n"), 0755)
			got, err := Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if (got.CloudCover != nil) != tc.known || (got.AcquiredAt != nil) != tc.known {
				t.Fatalf("consensus: %+v", got)
			}
			if got.Segments[0].CloudCover == nil || *got.Segments[0].CloudCover != 12.5 {
				t.Fatal("segment value lost")
			}
		})
	}
}
