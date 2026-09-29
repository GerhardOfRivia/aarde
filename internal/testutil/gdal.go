// Package testutil generates tiny, unclassified GDAL fixtures locally.
package testutil

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func RequireGDAL(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"gdalinfo", "gdal_create", "gdal_translate", "gdaltransform"} {
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

// MultiNITF exists only to verify whole-container rejection.
func MultiNITF(t *testing.T, path string) {
	t.Helper()
	NITF(t, path, "NUMI=3")
	source := filepath.Join(t.TempDir(), "source.tif")
	GeoTIFF(t, source)
	for range 2 {
		GDAL(t, "gdal_translate", "-q", "-of", "NITF", "-co", "APPEND_SUBDATASET=YES", source, path)
	}
}
