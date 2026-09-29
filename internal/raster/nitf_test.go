package raster

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/GerhardOfRivia/aarde/internal/geo"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func nitfInfo(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"driverShortName": "NITF", "size": []int{16, 16}, "bands": []any{map[string]int{"band": 1}, map[string]int{"band": 2}, map[string]int{"band": 3}},
		"coordinateSystem": map[string]any{"wkt": "EPSG:4326"}, "geoTransform": []float64{-106, .0625, 0, 40, 0, -.0625},
		"wgs84Extent": json.RawMessage(`{"type":"Polygon","coordinates":[[[-106,40],[-105,40],[-105,39],[-106,39],[-106,40]]]}`),
		"metadata":    map[string]any{"": map[string]any{"NITF_FHDR": "NITF02.10", "NITF_IID1": "test", "NITF_IC": "NC", "NITF_IREP": "RGB", "NITF_IDATIM": "20260910120000"}},
	}
}
func encodeInfo(t *testing.T, info map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestNITFCountValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
		want   string
	}{
		{"multiband", func(map[string]any) {}, ""},
		{"three images", func(m map[string]any) {
			m["metadata"].(map[string]any)["SUBDATASETS"] = map[string]string{"SUBDATASET_1_NAME": "NITF_IM:0:/test.ntf", "SUBDATASET_1_DESC": "one", "SUBDATASET_2_NAME": "NITF_IM:1:/test.ntf", "SUBDATASET_2_DESC": "two", "SUBDATASET_3_NAME": "NITF_IM:2:/test.ntf", "SUBDATASET_3_DESC": "three"}
		}, "found 3 image segments"},
		{"zero", func(m map[string]any) {
			m["metadata"] = map[string]any{"": map[string]string{"NITF_FHDR": "NITF02.10"}}
		}, "no image segments"},
		{"unknown", func(m map[string]any) { m["metadata"] = map[string]any{} }, "cannot reliably establish"},
		{"partial header", func(m map[string]any) { delete(m["metadata"].(map[string]any)[""].(map[string]any), "NITF_IC") }, "cannot reliably establish"},
		{"bad subdatasets", func(m map[string]any) { m["metadata"].(map[string]any)["SUBDATASETS"] = []string{"x"} }, "cannot reliably establish"},
		{"incomplete list", func(m map[string]any) {
			m["metadata"].(map[string]any)["SUBDATASETS"] = map[string]string{"SUBDATASET_1_NAME": "NITF_IM:0:/test.ntf"}
		}, "cannot reliably establish"},
		{"other driver", func(m map[string]any) { m["driverShortName"] = "PNG" }, "unsupported GDAL driver"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := nitfInfo(t)
			tc.change(m)
			got, err := ParseInfo(encodeInfo(t, m))
			if tc.want == "" {
				if err != nil || got.BandCount != 3 || got.Format != "NITF" || got.Footprint.Type != "MultiPolygon" {
					t.Fatalf("%+v: %v", got, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q got %v", tc.want, err)
			}
		})
	}
}

func TestNITFMetadataShapesAndTime(t *testing.T) {
	m := nitfInfo(t)
	metadata := m["metadata"].(map[string]any)
	metadata["xml:TRE"] = "<tres><test value=\"&lt;script&gt;\"/></tres>"
	metadata["TRE"] = map[string]any{"nested": map[string]any{"n": 1234567890123456789}, "array": []string{"a", "b"}}
	metadata["IMAGE_STRUCTURE"] = map[string]any{"COMPRESSION": "JPEG2000", "CLOUD_COVER": true}
	metadata["RPC"] = []any{1, "unexpected"}
	metadata["TEXT"] = map[string]any{"DATA_0": "do not catalog"}
	metadata["_aarde"] = map[string]any{"format": "spoofed"}
	got, err := ParseInfo(encodeInfo(t, m))
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]json.RawMessage
	_ = json.Unmarshal(got.Metadata, &saved)
	for _, domain := range []string{"xml:TRE", "TRE", "IMAGE_STRUCTURE", "RPC"} {
		want, _ := json.Marshal(metadata[domain])
		if string(want) != string(saved[domain]) {
			t.Fatalf("domain %s changed: %s", domain, saved[domain])
		}
	}
	if _, ok := saved["TEXT"]; ok {
		t.Fatal("unselected payload persisted")
	}
	if got.CloudCover != nil || got.AcquiredAt == nil || got.AcquiredAt.Format(time.RFC3339) != "2026-09-10T12:00:00Z" {
		t.Fatalf("normalized metadata: %+v", got)
	}
	header := metadata[""].(map[string]any)
	header["ACQUISITION_TIME"] = "2025-01-01T00:00:00-06:00"
	got, err = ParseInfo(encodeInfo(t, m))
	if err != nil || got.AcquiredAt.Year() != 2025 || got.AcquiredAt.Hour() != 6 {
		t.Fatal("explicit timestamp priority lost")
	}
	for _, tc := range []struct {
		version, date string
		valid         bool
	}{
		{"NITF02.10", "20240229123456", true}, {"NITF02.10", "20230229123456", false}, {"NITF02.10", "00000000000000", false}, {"NITF02.10", "99999999999999", false}, {"NITF02.10", "2026091012000-", false}, {"NITF02.10", "20260910240000", false}, {"NITF02.10", "20260910120060", false}, {"NITF02.10", "202609101200", false}, {"NITF02.00", "10120000ZSEP26", false}, {"NITF02.00", "20260910120000", false}, {"", "20260910120000", false},
	} {
		if got := nitfAcquisitionTime(map[string]string{"NITF_FHDR": tc.version, "NITF_IDATIM": tc.date, "NITF_FDT": "20260910120000"}); (got != nil) != tc.valid {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}

func TestInvalidFootprintsAndRPCOnly(t *testing.T) {
	for _, shape := range []string{
		`{"type":"Polygon","coordinates":[[[0,0],[1,1],[2,2],[0,0]]]}`,
		`{"type":"Polygon","coordinates":[[[179,0],[-179,0],[-179,1],[179,0]]]}`,
		`{"type":"Polygon","coordinates":[[[0,91],[1,0],[1,1],[0,91]]]}`,
		`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1]]]}`,
		`{"type":"Polygon","coordinates":[]}`,
	} {
		m := nitfInfo(t)
		m["wgs84Extent"] = json.RawMessage(shape)
		if _, err := ParseInfo(encodeInfo(t, m)); err == nil {
			t.Errorf("accepted %s", shape)
		}
	}
	m := nitfInfo(t)
	delete(m, "geoTransform")
	delete(m, "coordinateSystem")
	m["metadata"].(map[string]any)["RPC"] = map[string]string{"LINE_OFF": "10"}
	if _, err := ParseInfo(encodeInfo(t, m)); err == nil || !strings.Contains(err.Error(), "RPC-only footprint estimation") {
		t.Fatalf("RPC-only: %v", err)
	}
}

func fakeTool(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}
func TestInspectionProcessLimits(t *testing.T) {
	source := filepath.Join(t.TempDir(), "odd $name; scene.ntf")
	if err := os.WriteFile(source, []byte("physical bytes"), 0444); err != nil {
		t.Fatal(err)
	}
	t.Run("missing tool", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		_, err := Inspect(context.Background(), source)
		if err == nil || !strings.Contains(err.Error(), source) || !strings.Contains(err.Error(), "gdalinfo is required") {
			t.Fatal(err)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		fakeTool(t, "gdalinfo", "exec /bin/sleep 10\n")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()
		_, err := Inspect(ctx, source)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	for _, stream := range []string{"stdout", "stderr"} {
		t.Run(stream+" limit", func(t *testing.T) {
			redirect := ""
			if stream == "stderr" {
				redirect = " >&2"
			}
			fakeTool(t, "gdalinfo", "/bin/head -c 17825792 /dev/zero"+redirect+"\n")
			_, err := Inspect(context.Background(), source)
			if err == nil || !strings.Contains(err.Error(), "output exceeds size limit") {
				t.Fatal(err)
			}
		})
	}
	t.Run("malformed JSON", func(t *testing.T) {
		fakeTool(t, "gdalinfo", "printf '{bad'\n")
		_, err := Inspect(context.Background(), source)
		if err == nil || !strings.Contains(err.Error(), "invalid gdalinfo JSON") {
			t.Fatal(err)
		}
	})
	t.Run("missing codec", func(t *testing.T) {
		fakeTool(t, "gdalinfo", "printf 'JPEG2000 driver missing' >&2\nexit 1\n")
		_, err := Inspect(context.Background(), source)
		if err == nil || !strings.Contains(err.Error(), "JP2OpenJPEG") || !strings.Contains(err.Error(), source) {
			t.Fatal(err)
		}
	})
	t.Run("source change", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "change.ntf")
		_ = os.WriteFile(path, []byte("before"), 0644)
		data := encodeInfo(t, nitfInfo(t))
		fakeTool(t, "gdalinfo", "for last do :; done\nprintf 'changed' >> \"$last\"\nprintf '%s' '"+string(data)+"'\n")
		_, err := Inspect(context.Background(), path)
		if err == nil || !strings.Contains(err.Error(), "source changed") {
			t.Fatal(err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		link := filepath.Join(t.TempDir(), "link.ntf")
		if err := os.Symlink(source, link); err != nil {
			t.Fatal(err)
		}
		_, err := Inspect(context.Background(), link)
		if err == nil || !strings.Contains(err.Error(), "symlinks") {
			t.Fatal(err)
		}
	})
}

func gcpJSON(t *testing.T) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{"coordinateSystem":{"wkt":"EPSG:4326"},"gcpList":[{"pixel":0,"line":0,"x":-106,"y":40,"z":0},{"pixel":16,"line":0,"x":-105,"y":40,"z":0},{"pixel":16,"line":16,"x":-105,"y":39,"z":0},{"pixel":0,"line":16,"x":-106,"y":39,"z":0}]}`)
}

func TestFootprintSelectionAndGCPFailures(t *testing.T) {
	m := nitfInfo(t)
	delete(m, "coordinateSystem")
	delete(m, "geoTransform")
	m["gcps"] = gcpJSON(t)
	_, err := parseInfo(encodeInfo(t, m), func(info) (geo.Geometry, error) {
		t.Fatal("valid backed extent should take priority")
		return geo.Geometry{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	delete(m, "wgs84Extent")
	data := encodeInfo(t, m)
	source := filepath.Join(t.TempDir(), "gcp.ntf")
	if err := os.WriteFile(source, []byte("source"), 0444); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, body, want string }{
		{"missing", "", "gdaltransform is required"},
		{"failed position", "printf 'transformation failed'\n", "did not return 64"},
		{"nonfinite", "i=0; while [ $i -lt 64 ]; do printf 'NaN 39 0\n'; i=$((i+1)); done\n", "non-finite position"},
		{"degenerate", "i=0; while [ $i -lt 64 ]; do printf '1 1 0\n'; i=$((i+1)); done\n", "zero or non-finite area"},
		{"output limit", "/bin/head -c 65537 /dev/zero\n", "output exceeds size limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := fakeTool(t, "gdalinfo", "printf '%s' '"+string(data)+"'\n")
			if tc.body != "" {
				if err := os.WriteFile(filepath.Join(dir, "gdaltransform"), []byte("#!/bin/sh\n"+tc.body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Inspect(context.Background(), source); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q got %v", tc.want, err)
			}
		})
	}
	t.Run("GCP cancellation", func(t *testing.T) {
		dir := fakeTool(t, "gdalinfo", "printf '%s' '"+string(data)+"'\n")
		_ = os.WriteFile(filepath.Join(dir, "gdaltransform"), []byte("#!/bin/sh\nexec /bin/sleep 10\n"), 0755)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		if _, err := Inspect(ctx, source); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	for _, bad := range []json.RawMessage{json.RawMessage(`{"coordinateSystem":{"wkt":"EPSG:4326"},"gcpList":[{},{},{}]}`), json.RawMessage(`{"gcpList":[]}`)} {
		if _, err := parseGCPs(bad); err == nil {
			t.Fatal("accepted malformed GCPs")
		}
	}
	var gcps gcpInfo
	_ = json.Unmarshal(gcpJSON(t), &gcps)
	gcps.Points = append(gcps.Points[:1], make([]struct{ Pixel, Line, X, Y, Z *float64 }, 256)...)
	tooMany, _ := json.Marshal(gcps)
	if _, err := parseGCPs(tooMany); err == nil || !strings.Contains(err.Error(), "limit of 256") {
		t.Fatalf("GCP limit: %v", err)
	}
}

func TestGDALArgumentsAndPAM(t *testing.T) {
	source := filepath.Join(t.TempDir(), "spaces ; $literal ' file.ntf")
	_ = os.WriteFile(source, []byte("source"), 0444)
	data := encodeInfo(t, nitfInfo(t))
	argsFile := filepath.Join(t.TempDir(), "args")
	t.Setenv("AARDE_TEST_ARGS", argsFile)
	t.Setenv("GDAL_PAM_ENABLED", "YES")
	fakeTool(t, "gdalinfo", "[ \"$GDAL_PAM_ENABLED\" = NO ] || exit 1\nprintf '%s\\n' \"$@\" > \"$AARDE_TEST_ARGS\"\nprintf '%s' '"+string(data)+"'\n")
	if _, err := Inspect(context.Background(), source); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(argsFile)
	lines := strings.Split(strings.TrimSuffix(string(args), "\n"), "\n")
	want := []string{"-json", "-noct", "-norat", "-mdd", "SUBDATASETS", "-mdd", "IMAGE_STRUCTURE", "-mdd", "RPC", "-mdd", "TRE", "-mdd", "xml:TRE", source}
	if !slices.Equal(lines, want) {
		t.Fatalf("unexpected args: %q", lines)
	}
}
