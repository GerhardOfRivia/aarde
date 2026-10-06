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

type Segment struct {
	Index      int             `json:"index"`
	SourceCRS  string          `json:"source_crs"`
	Width      int             `json:"width"`
	Height     int             `json:"height"`
	BandCount  int             `json:"band_count"`
	AcquiredAt *time.Time      `json:"acquired_at"`
	CloudCover *float64        `json:"cloud_cover"`
	Footprint  geo.Geometry    `json:"footprint"`
	Metadata   json.RawMessage `json:"metadata"`
}

type Inspection struct {
	Segments      []Segment            `json:"segments"`
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
	Corners   map[string][]float64       `json:"cornerCoordinates"`
	Extent    json.RawMessage            `json:"wgs84Extent"`
	Bands     []json.RawMessage          `json:"bands"`
	Metadata  map[string]json.RawMessage `json:"metadata"`
	Transform []float64                  `json:"geoTransform"`
	GCPs      json.RawMessage            `json:"gcps"`
}

// cappedBuffer drains excess output without retaining it or blocking the child.
// The caller rejects either stream overflowing, even if GDAL exits successfully.
type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	exceeded bool
}

func (b *cappedBuffer) Len() int       { return b.buf.Len() }
func (b *cappedBuffer) Bytes() []byte  { return b.buf.Bytes() }
func (b *cappedBuffer) String() string { return b.buf.String() }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if len(p) > b.max-b.Len() {
		b.exceeded = true
		p = p[:b.max-b.Len()]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

// All subprocesses and the checksum share the inspection's two-minute budget.
func runGDAL(ctx context.Context, name string, input io.Reader, limit int, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), "GDAL_PAM_ENABLED=NO", "NITF_OPEN_UNDERLYING_DS=YES", "GDAL_CACHEMAX=64", "GDAL_NUM_THREADS=1", "PROJ_NETWORK=OFF")
	cmd.WaitDelay = time.Second
	stdout := &cappedBuffer{max: limit}
	stderr := &cappedBuffer{max: 16 << 10}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = input, stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("%s output exceeds size limit", name)
	}
	if errors.Is(err, exec.ErrNotFound) {
		return nil, fmt.Errorf("%s is required; install GDAL (gdal-bin) or use the Aarde Docker image", name)
	}
	if err != nil {
		return nil, fmt.Errorf("%s failed (check file integrity and GDAL driver/codec availability, including JPEG/JP2OpenJPEG for compressed NITF): %w: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func Inspect(ctx context.Context, path string) (result Inspection, err error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return result, err
	}
	defer func() {
		if err != nil {
			err = fmt.Errorf("inspect %q: %w", abs, err)
		}
	}()
	before, err := os.Lstat(abs)
	if err != nil {
		return result, err
	}
	if !before.Mode().IsRegular() {
		return result, errors.New("source must be a regular local file (symlinks are not followed)")
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// Select domains explicitly: never request all domains, TEXT, CGM, DES,
	// or the base64 raw headers in NITF_METADATA.
	data, err := runGDAL(ctx, "gdalinfo", nil, 16<<20, "-json", "-noct", "-norat",
		"-mdd", "SUBDATASETS", "-mdd", "IMAGE_STRUCTURE", "-mdd", "RPC", "-mdd", "TRE", "-mdd", "xml:TRE", abs)
	if err != nil {
		return result, err
	}
	result, err = inspectContainer(ctx, abs, data)
	if err != nil {
		return result, err
	}

	file, err := os.Open(abs)
	if err != nil {
		return result, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return result, err
	}
	if !sameSource(before, opened) {
		return result, errors.New("source changed during inspection; retry when it is no longer being written")
	}
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
	after, err := os.Lstat(abs)
	if err != nil {
		return result, err
	}
	if !sameSource(before, after) {
		return result, errors.New("source changed during inspection; retry when it is no longer being written")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	result.Checksum = hex.EncodeToString(h.Sum(nil))
	result.AssetLocation = abs
	applySidecar(abs, &result)
	return result, nil
}

func sameSource(a, b os.FileInfo) bool {
	return b.Mode().IsRegular() && os.SameFile(a, b) && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

// ParseInfo parses already collected GDAL JSON. GCP fallback requires Inspect,
// which can invoke GDAL's transformer on the physical file.
func ParseInfo(data []byte) (Inspection, error) { return parseInfo(data, nil) }

func parseInfo(data []byte, transform func(info) (geo.Geometry, error)) (Inspection, error) {
	result, err := parseRasterInfo(data, transform)
	if err == nil && result.Format == "NITF" {
		var raw info
		_ = json.Unmarshal(data, &raw)
		result.Segments = []Segment{{Index: 0, Metadata: result.Metadata, CloudCover: result.CloudCover}}
		enrichNITF(&result, raw)
		result.Segments = nil // ParseInfo's existing single-raster contract.
	}
	return result, err
}

func parseRasterInfo(data []byte, transform func(info) (geo.Geometry, error)) (Inspection, error) {
	var raw info
	var result Inspection
	if err := json.Unmarshal(data, &raw); err != nil {
		return result, fmt.Errorf("invalid gdalinfo JSON: %w", err)
	}
	if raw.Driver != "GTiff" && raw.Driver != "NITF" {
		return result, fmt.Errorf("unsupported GDAL driver %q; only GTiff and NITF rasters are supported", raw.Driver)
	}
	if raw.Driver == "NITF" {
		count, err := nitfImageCount(raw)
		if err != nil {
			return result, err
		}
		if count > 1 {
			return result, fmt.Errorf("found %d image segments; use Inspect with the physical source path", count)
		}
	}
	if len(raw.Size) != 2 || raw.Size[0] <= 0 || raw.Size[1] <= 0 || len(raw.Bands) == 0 {
		return result, errors.New("raster has invalid dimensions or no bands")
	}
	gcps, gcpErr := parseGCPs(raw.GCPs)
	affine := validAffine(raw)
	crs, georef, method := raw.CRS.WKT, "affine", "gdal_wgs84_extent"
	footprint, extentErr := catalogFootprint(raw.Extent)
	if extentErr == nil && !affine && gcpErr != nil {
		extentErr = errors.New("WGS84 extent is not backed by usable source georeferencing")
	}
	if !affine && gcpErr == nil {
		crs, georef = gcps.CRS.WKT, "gcp"
	}
	if extentErr != nil || (!affine && gcpErr != nil) {
		if gcpErr != nil {
			extra := ""
			if _, ok := raw.Metadata["RPC"]; ok {
				extra = "; RPC metadata is present but RPC-only footprint estimation is not supported"
			}
			return result, fmt.Errorf("no usable affine WGS84 extent or GCP georeferencing: extent: %v; GCPs: %v%s; provide imagery with a valid CRS and affine extent or GCPs", extentErr, gcpErr, extra)
		}
		if transform == nil {
			return result, errors.New("GCP footprint requires GDAL coordinate transformation; use Inspect with the physical source path")
		}
		var err error
		footprint, err = transform(raw)
		if err != nil {
			return result, fmt.Errorf("GCP footprint: %w", err)
		}
		crs, georef, method = gcps.CRS.WKT, "gcp", "gdal_gcp_tps_perimeter_64"
	}
	bounds := raw.Corners
	if georef == "gcp" {
		// GDAL cornerCoordinates are pixel/line values when no affine transform
		// exists. Do not present those as source georeferenced bounds.
		bounds = map[string][]float64{}
	}
	metadata := make(map[string]json.RawMessage)
	for _, domain := range []string{"", "IMAGE_STRUCTURE", "RPC", "TRE", "xml:TRE"} {
		if value, ok := raw.Metadata[domain]; ok {
			metadata[domain] = value
		}
	}
	annotations := map[string]any{"format": raw.Driver, "georeferencing": georef, "footprint_method": method}
	if affine {
		annotations["geo_transform"] = raw.Transform
	}
	if len(raw.GCPs) > 0 {
		annotations["gcps"] = raw.GCPs
	}
	metadata["_aarde"], _ = json.Marshal(annotations)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return result, err
	}
	scalars := scalarMetadata(metadata)
	acquired := acquisitionTime(scalars)
	if acquired == nil && raw.Driver == "NITF" {
		acquired = nitfAcquisitionTime(scalars[""])
	}
	result = Inspection{Format: raw.Driver, SourceCRS: crs, Bounds: bounds, Width: raw.Size[0], Height: raw.Size[1], BandCount: len(raw.Bands), AcquiredAt: acquired, CloudCover: cloudCover(scalars), Footprint: footprint, Metadata: encoded}
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
