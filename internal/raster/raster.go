// Package raster invokes GDAL without shell interpolation or Go GDAL bindings.
package raster

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/geo"
)

type Inspection struct {
	Format        string               `json:"format"`
	SourceCRS     string               `json:"source_crs"`
	Bounds        map[string][]float64 `json:"bounds"`
	Width         int                  `json:"width"`
	Height        int                  `json:"height"`
	BandCount     int                  `json:"band_count"`
	AcquiredAt    *time.Time           `json:"acquired_at"`
	CloudCover    *float64             `json:"cloud_cover"`
	Footprint     geo.Geometry         `json:"footprint"`
	Checksum      string               `json:"checksum"`
	AssetLocation string               `json:"asset_location"`
	Metadata      json.RawMessage      `json:"metadata"`
}

type info struct {
	Driver string `json:"driverShortName"`
	Size   []int  `json:"size"`
	CRS    struct {
		WKT string `json:"wkt"`
	} `json:"coordinateSystem"`
	Corners  map[string][]float64         `json:"cornerCoordinates"`
	Extent   json.RawMessage              `json:"wgs84Extent"`
	Bands    []json.RawMessage            `json:"bands"`
	Metadata map[string]map[string]string `json:"metadata"`
}

// cappedBuffer prevents corrupt or unusually large metadata exhausting memory.
type cappedBuffer struct {
	bytes.Buffer
	max int
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if len(p) > b.max-b.Len() {
		return 0, errors.New("GDAL output exceeds size limit")
	}
	return b.Buffer.Write(p)
}

func Inspect(ctx context.Context, path string) (Inspection, error) {
	var result Inspection
	abs, err := filepath.Abs(path)
	if err != nil {
		return result, err
	}
	before, err := os.Lstat(abs)
	if err != nil {
		return result, err
	}
	if !before.Mode().IsRegular() {
		return result, errors.New("source must be a regular local file (symlinks are not followed)")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gdalinfo", "-json", abs)
	cmd.Env = append(os.Environ(), "GDAL_PAM_ENABLED=NO")
	stdout := &cappedBuffer{max: 16 << 20}
	stderr := &cappedBuffer{max: 16 << 10}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return result, errors.New("gdalinfo is required; install GDAL or use the Aarde Docker image")
		}
		return result, fmt.Errorf("gdalinfo failed: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	result, err = ParseInfo(stdout.Bytes())
	if err != nil {
		return result, err
	}
	file, err := os.Open(abs)
	if err != nil {
		return result, err
	}
	defer file.Close()
	h := sha256.New()
	buf := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		n, err := file.Read(buf)
		if n > 0 {
			_, _ = h.Write(buf[:n])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return result, err
		}
	}
	after, err := os.Stat(abs)
	if err != nil {
		return result, err
	}
	if !os.SameFile(before, after) || before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return result, errors.New("source changed during inspection; retry when it is no longer being written")
	}
	result.Checksum = hex.EncodeToString(h.Sum(nil))
	result.AssetLocation = abs
	return result, nil
}

func ParseInfo(data []byte) (Inspection, error) {
	var raw info
	var result Inspection
	if err := json.Unmarshal(data, &raw); err != nil {
		return result, fmt.Errorf("invalid gdalinfo JSON: %w", err)
	}
	if raw.Driver != "GTiff" {
		return result, errors.New("only GeoTIFF rasters are supported")
	}
	if len(raw.Size) != 2 || raw.Size[0] <= 0 || raw.Size[1] <= 0 || len(raw.Bands) == 0 {
		return result, errors.New("raster has invalid dimensions or no bands")
	}
	if raw.CRS.WKT == "" {
		return result, errors.New("raster has no source CRS")
	}
	if len(raw.Corners) < 4 {
		return result, errors.New("raster has no georeferenced corners")
	}
	footprint, err := geo.Parse(raw.Extent)
	if err != nil {
		return result, fmt.Errorf("GDAL did not produce a supported EPSG:4326 footprint: %w", err)
	}
	metadata, err := json.Marshal(raw.Metadata)
	if err != nil {
		return result, err
	}
	if raw.Metadata == nil {
		metadata = []byte("{}")
	}
	result = Inspection{Format: raw.Driver, SourceCRS: raw.CRS.WKT, Bounds: raw.Corners, Width: raw.Size[0], Height: raw.Size[1], BandCount: len(raw.Bands), AcquiredAt: acquisitionTime(raw.Metadata), CloudCover: cloudCover(raw.Metadata), Footprint: footprint, Metadata: metadata}
	return result, nil
}

// Deliberately exclude TIFFTAG_DATETIME: it often describes processing/export.
// Ambiguous timestamps without an offset are left unknown.
func acquisitionTime(metadata map[string]map[string]string) *time.Time {
	domains := make([]string, 0, len(metadata))
	for domain := range metadata {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, key := range []string{"ACQUISITION_DATETIME", "ACQUISITION_TIME", "SENSING_TIME", "TIFFTAG_DATETIME_ORIGINAL"} {
		for _, domain := range domains {
			keys := make([]string, 0, len(metadata[domain]))
			for k := range metadata[domain] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if strings.EqualFold(k, key) {
					if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(metadata[domain][k])); err == nil {
						utc := t.UTC()
						return &utc
					}
				}
			}
		}
	}
	return nil
}

// Read explicitly named percentage fields only; missing or invalid values stay unknown.
// Key priority and sorted domains/keys keep conflicting metadata deterministic.
func cloudCover(metadata map[string]map[string]string) *float64 {
	domains := make([]string, 0, len(metadata))
	for domain := range metadata {
		domains = append(domains, domain)
	}
	sort.Strings(domains)
	for _, key := range []string{"CLOUD_COVER", "CLOUD_COVER_PERCENTAGE", "EO:CLOUD_COVER"} {
		for _, domain := range domains {
			keys := make([]string, 0, len(metadata[domain]))
			for k := range metadata[domain] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				if strings.EqualFold(k, key) {
					value, err := strconv.ParseFloat(strings.TrimSpace(metadata[domain][k]), 64)
					if err == nil && !math.IsNaN(value) && value >= 0 && value <= 100 {
						return &value
					}
				}
			}
		}
	}
	return nil
}
