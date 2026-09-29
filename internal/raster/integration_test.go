package raster

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestGDALNITFInspection(t *testing.T) {
	testutil.RequireGDAL(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "scene.NiTf")
	testutil.NITF(t, path, "IID1=SYNTHETIC", "ISORCE=local test", "TEXT=DATA_0=unclassified test text", "SDE_TRE=YES")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("fixture created unexpected files: %v", entries)
	}
	if err := os.Chmod(path, 0444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	got, err := Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(before)
	if got.Format != "NITF" || got.BandCount != 3 || got.Width != 16 || got.Height != 16 || got.Checksum != hex.EncodeToString(sum[:]) || got.AssetLocation != path || got.CloudCover != nil || got.AcquiredAt == nil {
		t.Fatalf("inspection: %+v", got)
	}
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(got.Metadata, &metadata)
	scalars := scalarMetadata(metadata)
	if scalars[""]["NITF_IID1"] != "SYNTHETIC" || scalars[""]["NITF_ISORCE"] != "local test" || scalars[""]["NITF_FSCLAS"] != "U" || scalars[""]["NITF_IC"] != "NC" {
		t.Fatal("header metadata lost")
	}
	for _, key := range []string{"TRE", "xml:TRE", "_aarde"} {
		if len(metadata[key]) == 0 {
			t.Fatalf("missing %s", key)
		}
	}
	for _, key := range []string{"TEXT", "CGM", "NITF_METADATA", "xml:DES"} {
		if _, ok := metadata[key]; ok {
			t.Fatalf("unselected domain %s", key)
		}
	}
	after, _ := os.ReadFile(path)
	entries, _ = os.ReadDir(dir)
	if string(before) != string(after) || len(entries) != 1 {
		t.Fatal("inspection changed source or created sidecars")
	}
}

func TestGDALNITFContainerContract(t *testing.T) {
	testutil.RequireGDAL(t)
	for _, tc := range []struct {
		name   string
		images int
	}{{"single", 1}, {"multiple", 3}, {"empty", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name+".ntf")
			switch tc.images {
			case 1:
				testutil.NITF(t, path)
			case 3:
				testutil.MultiNITF(t, path)
			case 0:
				testutil.GDAL(t, "gdal_create", "-of", "NITF", "-outsize", "1", "1", "-bands", "1", "-co", "NUMI=0", path)
			}
			data, err := runGDAL(context.Background(), "gdalinfo", nil, 16<<20, "-json", "-mdd", "SUBDATASETS", path)
			if err != nil {
				t.Fatal(err)
			}
			var raw info
			if err := json.Unmarshal(data, &raw); err != nil {
				t.Fatal(err)
			}
			sub := scalarMetadata(raw.Metadata)["SUBDATASETS"]
			header := scalarMetadata(raw.Metadata)[""]
			if tc.images == 3 && len(sub) != 6 {
				t.Fatalf("GDAL contract changed: expected all 3 image entries: %v", sub)
			}
			if tc.images < 2 && len(sub) != 0 {
				t.Fatal("GDAL contract changed: unexpected subdatasets")
			}
			_, hasImage := header["NITF_IID1"]
			if hasImage != (tc.images > 0) {
				t.Fatal("GDAL image-header presence contract changed")
			}
			_, err = Inspect(context.Background(), path)
			if tc.images == 1 {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				want := "found 3 image segments"
				if tc.images == 0 {
					want = "no image segments"
				}
				if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), path) {
					t.Fatalf("expected %s: %v", want, err)
				}
			}
		})
	}
}

func TestGDALGCPFootprint(t *testing.T) {
	testutil.RequireGDAL(t)
	path := filepath.Join(t.TempDir(), "gcp.ntf")
	testutil.NITF(t, path, "IGEOLO=400000N1060000W400000N1050000W390000N1043000W390000N1060000W")
	rawJSON := testutil.GDAL(t, "gdalinfo", "-json", path)
	var raw info
	if err := json.Unmarshal(rawJSON, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.CRS.WKT != "" || len(raw.Extent) > 0 {
		t.Fatal("fixture must exercise absent dataset CRS and WGS84 extent")
	}
	got, err := Inspect(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(got.Metadata, &metadata)
	if !strings.Contains(string(metadata["_aarde"]), "gdal_gcp_tps_perimeter_64") || !strings.Contains(got.SourceCRS, "4326") || len(got.Bounds) != 0 {
		t.Fatalf("GCP provenance: %+v", got)
	}
	var coordinates [][][][]float64
	_ = json.Unmarshal(got.Footprint.Coordinates, &coordinates)
	if len(coordinates) != 1 || len(coordinates[0][0]) != 65 {
		t.Fatal("wrong perimeter sampling")
	}
	for _, point := range coordinates[0][0] {
		if point[0] < -107 || point[0] > -104 || point[1] < 38 || point[1] > 41 {
			t.Fatalf("incorrect transform or axis ordering: %v", point)
		}
	}
}

func TestGDALDriverAndGeoreferencingErrors(t *testing.T) {
	testutil.RequireGDAL(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source.tif")
	testutil.GeoTIFF(t, source)
	// Content detection wins over extensions in both directions.
	renamed := filepath.Join(dir, "renamed.ntf")
	bytes, _ := os.ReadFile(source)
	_ = os.WriteFile(renamed, bytes, 0644)
	if got, err := Inspect(context.Background(), renamed); err != nil || got.Format != "GTiff" {
		t.Fatalf("renamed TIFF: %v", err)
	}
	png := filepath.Join(dir, "png.ntf")
	testutil.GDAL(t, "gdal_translate", "-q", "-of", "PNG", source, png)
	if _, err := Inspect(context.Background(), png); err == nil || !strings.Contains(err.Error(), `driver "PNG"`) {
		t.Fatalf("renamed PNG: %v", err)
	}
	for _, tc := range []struct {
		name   string
		create func(string)
		want   string
	}{
		{"unreferenced", func(path string) { testutil.GDAL(t, "gdal_create", "-of", "NITF", "-outsize", "16", "16", path) }, "no usable affine"},
		{"corrupt", func(path string) { _ = os.WriteFile(path, []byte("NITF02.10 truncated"), 0644) }, "gdalinfo failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), tc.name+".ntf")
			tc.create(path)
			if _, err := Inspect(context.Background(), path); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: %v", tc.name, err)
			}
		})
	}
}

func TestGDALNITFCodecs(t *testing.T) {
	testutil.RequireGDAL(t)
	for _, tc := range []struct{ codec, ic string }{{"JPEG", "C3"}, {"JP2OpenJPEG", "C8"}} {
		t.Run(tc.codec, func(t *testing.T) {
			cmd := exec.Command("gdalinfo", "--format", tc.codec)
			if out, err := cmd.CombinedOutput(); err != nil {
				if os.Getenv("AARDE_REQUIRE_GDAL") != "" {
					t.Fatalf("required Docker codec %s: %v %s", tc.codec, err, out)
				}
				t.Skipf("codec %s unavailable: %s", tc.codec, out)
			}
			path := filepath.Join(t.TempDir(), "compressed.ntf")
			options := []string{"IC=" + tc.ic}
			if tc.ic == "C8" {
				options = append(options, "JPEG2000_DRIVER=JP2OpenJPEG")
			}
			testutil.NITF(t, path, options...)
			got, err := Inspect(context.Background(), path)
			if err != nil || got.Format != "NITF" {
				t.Fatalf("compressed inspection: %v", err)
			}
			var md map[string]json.RawMessage
			_ = json.Unmarshal(got.Metadata, &md)
			if scalarMetadata(md)[""]["NITF_IC"] != tc.ic {
				t.Fatal("compression metadata lost")
			}
			if tc.ic == "C8" {
				t.Setenv("GDAL_SKIP", "JP2OpenJPEG")
				if _, err := Inspect(context.Background(), path); err == nil || !strings.Contains(err.Error(), "JP2OpenJPEG") {
					t.Fatalf("missing JPEG2000 capability: %v", err)
				}
			}
		})
	}
}

func TestGDALRPCOnly(t *testing.T) {
	testutil.RequireGDAL(t)
	dir := t.TempDir()
	vrt := filepath.Join(dir, "rpc.vrt")
	path := filepath.Join(dir, "rpc.ntf")
	var xml strings.Builder
	xml.WriteString(`<VRTDataset rasterXSize="16" rasterYSize="16"><Metadata domain="RPC">`)
	for key, value := range map[string]string{"LINE_OFF": "8", "SAMP_OFF": "8", "LAT_OFF": "39", "LONG_OFF": "-105", "HEIGHT_OFF": "0", "LINE_SCALE": "8", "SAMP_SCALE": "8", "LAT_SCALE": "1", "LONG_SCALE": "1", "HEIGHT_SCALE": "1", "LINE_NUM_COEFF": "0 0 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0", "SAMP_NUM_COEFF": "0 1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0", "LINE_DEN_COEFF": "1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0", "SAMP_DEN_COEFF": "1 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"} {
		xml.WriteString(`<MDI key="` + key + `">` + value + `</MDI>`)
	}
	xml.WriteString(`</Metadata><VRTRasterBand dataType="Byte" band="1"/></VRTDataset>`)
	if err := os.WriteFile(vrt, []byte(xml.String()), 0644); err != nil {
		t.Fatal(err)
	}
	testutil.GDAL(t, "gdal_translate", "-q", "-of", "NITF", vrt, path)
	if _, err := Inspect(context.Background(), path); err == nil || !strings.Contains(err.Error(), "RPC-only footprint estimation") {
		t.Fatalf("RPC-only error: %v", err)
	}
}
