// Package testutil generates tiny, unclassified GDAL fixtures locally.
package testutil

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func RequireGDAL(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"gdalinfo", "gdal_create", "gdal_translate", "gdaltransform", "ogr2ogr"} {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("AARDE_REQUIRE_GDAL") != "" || os.Getenv("AARDE_TEST_DATABASE_URL") != "" {
				t.Fatalf("%s required; run make integration", tool)
			}
			t.Skipf("%s unavailable; run make integration for real GDAL coverage", tool)
		}
	}
}

func GDAL(t *testing.T, tool string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, tool, args...)
	cmd.Env = append(os.Environ(), "GDAL_PAM_ENABLED=NO")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", tool, args, err, stderr.String())
	}
	return out
}

func GeoTIFF(t *testing.T, path string) {
	t.Helper()
	GDAL(t, "gdal_create", "-of", "GTiff", "-outsize", "16", "16", "-bands", "3", "-burn", "42", "-a_srs", "EPSG:4326", "-a_ullr", "-106", "40", "-105", "39", path)
}

func NITF(t *testing.T, path string, options ...string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "source.tif")
	GeoTIFF(t, source)
	args := []string{"-q", "-of", "NITF", "-co", "ICORDS=G", "-co", "IDATIM=20260910120000", "-co", "FSCLAS=U", "-co", "ISCLAS=U"}
	for _, option := range options {
		args = append(args, "-co", option)
	}
	GDAL(t, "gdal_translate", append(args, source, path)...)
}

// MultiNITF creates a physical container with three image segments.
func MultiNITF(t *testing.T, path string) {
	t.Helper()
	NITF(t, path, "NUMI=3")
	source := filepath.Join(t.TempDir(), "source.tif")
	GeoTIFF(t, source)
	for range 2 {
		GDAL(t, "gdal_translate", "-q", "-of", "NITF", "-co", "APPEND_SUBDATASET=YES", source, path)
	}
}

// DiverseMultiNITF has overlapping first/second images and a separated third
// image. Dimensions, bands, image IDs, and acquisition times differ.
func DiverseMultiNITF(t *testing.T, path string, invalidLast bool) {
	t.Helper()
	for index, spec := range []struct{ w, h, b, left, top, right, bottom string }{
		{"16", "16", "3", "-106", "40", "-105", "39"},
		{"8", "12", "1", "-105.5", "39.5", "-104.5", "38.5"},
		{"20", "10", "2", "-102", "40", "-101", "39"},
	} {
		source := filepath.Join(t.TempDir(), "source.tif")
		args := []string{"-of", "GTiff", "-outsize", spec.w, spec.h, "-bands", spec.b, "-burn", "42"}
		if !invalidLast || index != 2 {
			args = append(args, "-a_srs", "EPSG:4326", "-a_ullr", spec.left, spec.top, spec.right, spec.bottom)
		}
		GDAL(t, "gdal_create", append(args, source)...)
		args = []string{"-q", "-of", "NITF", "-co", "IID1=SEGMENT" + strconv.Itoa(index), "-co", "IDATIM=2026091012000" + strconv.Itoa(index)}
		if !invalidLast || index != 2 {
			args = append(args, "-co", "ICORDS=G")
		}
		if index == 0 {
			args = append(args, "-co", "NUMI=3")
		} else {
			args = append(args, "-co", "APPEND_SUBDATASET=YES")
		}
		GDAL(t, "gdal_translate", append(args, source, path)...)
	}
}
