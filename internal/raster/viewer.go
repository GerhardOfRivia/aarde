package raster

// Whole-image viewing is deliberately independent of catalog writes. Plans are
// bounded metadata; every raster response covers one complete original segment.
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type ViewerOptions struct {
	MaxDimension int
	LayerPixels  int64
	ScenePixels  int64
	Concurrent   int
	Timeout      time.Duration
	TempDir      string
	SourceRoots  []string
}

func DefaultViewerOptions() ViewerOptions {
	return ViewerOptions{4096, 8_000_000, 24_000_000, 2, 20 * time.Second, "", nil}
}
func (o ViewerOptions) Validate() error {
	if o.MaxDimension < 64 || o.MaxDimension > 8192 || o.LayerPixels < 4096 || o.LayerPixels > 32_000_000 || o.ScenePixels < o.LayerPixels || o.ScenePixels > 96_000_000 || o.Concurrent < 1 || o.Concurrent > 8 || o.Timeout < time.Second || o.Timeout > 20*time.Second {
		return errors.New("invalid viewer resource limits")
	}
	return nil
}

type ViewerManifest struct {
	CatalogID        string          `json:"catalog_id"`
	ImageID          string          `json:"image_id"`
	Name             string          `json:"name"`
	Revision         string          `json:"revision"`
	Format           string          `json:"format"`
	Assessment       json.RawMessage `json:"ncdrd_assessment,omitempty"`
	CoordinateSystem string          `json:"coordinate_system"`
	Extent           [4]float64      `json:"extent"`
	Layers           []ViewerLayer   `json:"layers"`
	Resolution       string          `json:"resolution"`
	NativeAvailable  bool            `json:"native_available"`
	DisplayPixels    int64           `json:"display_pixels"`
	// Includes two decoded copies and canvas/texture overhead; PNG bytes are not a memory estimate.
	EstimatedMemoryBytes int64    `json:"estimated_memory_bytes"`
	CloudStatus          string   `json:"cloud_status"`
	Warnings             []string `json:"warnings"`
}
type ViewerLayer struct {
	ID            string     `json:"id"`
	Label         string     `json:"label"`
	Role          string     `json:"role"`
	Segment       *int       `json:"segment_index,omitempty"`
	DES           *int       `json:"des_index,omitempty"`
	Width         int        `json:"width"`
	Height        int        `json:"height"`
	DisplayWidth  int        `json:"display_width"`
	DisplayHeight int        `json:"display_height"`
	Extent        [4]float64 `json:"extent"`
	// Mesh is a row-major lattice of original-pixel edges, mapped to viewer x/y.
	// Affine placement in the map CRS uses a single cell; reprojection/GCPs use a mesh.
	Mesh         [][2]float64      `json:"mesh"`
	MeshSize     int               `json:"mesh_size"`
	Registration string            `json:"registration"`
	Placement    string            `json:"placement"`
	ContentURL   string            `json:"content_url"`
	ContentType  string            `json:"content_type"`
	Opacity      float64           `json:"opacity"`
	Order        int               `json:"order"`
	Rendering    string            `json:"rendering"`
	Legend       map[string]string `json:"legend,omitempty"`
	Unsupported  string            `json:"unsupported,omitempty"`
	Warnings     []string          `json:"warnings"`
	// Internal, never serialized: source metadata/selector and decoded shape geometry.
	raw      info
	selector string
	geometry json.RawMessage
}

type ViewerPlan struct {
	Manifest  ViewerManifest
	Path      string
	Options   ViewerOptions
	reference info
}

var ErrViewerRevision = errors.New("source changed; reload the viewer manifest")
var ErrViewerLimit = errors.New("requested whole-image resolution exceeds viewer limits")

// Only a catalog-resolved absolute local path is accepted. Reject symlinks in
// every component, including configured roots; callers cannot supply selectors.
func ViewerRevision(path, checksum string, opts ViewerOptions) (string, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", errors.New("source unavailable")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved != path {
		return "", errors.New("source unavailable or symlinked")
	}
	if len(opts.SourceRoots) > 0 {
		allowed := false
		for _, root := range opts.SourceRoots {
			rel, e := filepath.Rel(root, path)
			if e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				allowed = true
			}
		}
		if !allowed {
			return "", errors.New("source outside configured roots")
		}
	}
	stat, err := os.Stat(path)
	if err != nil || !stat.Mode().IsRegular() {
		return "", errors.New("source unavailable")
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d|%d|%v", path, checksum, stat.Size(), stat.ModTime().UnixNano(), stableFileIdentity(stat))
	// Sidecars and masks can alter decoding/registration without changing the raster.
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))+"*"))
	if len(matches) > 1024 {
		return "", errors.New("too many source support files")
	}
	for _, p := range matches {
		if p == path {
			continue
		}
		s, e := os.Lstat(p)
		if e != nil {
			return "", e
		}
		if s.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("symlinked source support file")
		}
		fmt.Fprintf(h, "|%s:%d:%d:%v", filepath.Base(p), s.Size(), s.ModTime().UnixNano(), stableFileIdentity(s))
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func viewerInfo(ctx context.Context, selector string) (info, error) {
	var raw info
	data, err := runGDAL(ctx, "gdalinfo", nil, 2<<20, "-json", "-norat", "-mdd", "TRE", "-mdd", "xml:TRE", selector)
	if err != nil {
		return raw, err
	}
	err = json.Unmarshal(data, &raw)
	if err == nil && (len(raw.Size) != 2 || raw.Size[0] <= 0 || raw.Size[1] <= 0 || len(raw.Bands) == 0) {
		err = errors.New("invalid raster dimensions/bands")
	}
	return raw, err
}
func BuildViewer(ctx context.Context, path, checksum, format string, segments []Segment, metadata json.RawMessage, resolution string, opts ViewerOptions) (*ViewerPlan, error) {
	if resolution != "auto" && resolution != "preview" && resolution != "native" {
		return nil, errors.New("invalid resolution")
	}
	rev, err := ViewerRevision(path, checksum, opts)
	if err != nil {
		return nil, err
	}
	p := &ViewerPlan{Path: path, Options: opts, Manifest: ViewerManifest{Revision: rev, Format: format, Resolution: resolution, CoordinateSystem: "Image coordinates; geographic placement unavailable", CloudStatus: "absent", Warnings: []string{}, Layers: []ViewerLayer{}}}
	var annotations struct {
		Aarde struct {
			Commercial commercialNITF `json:"commercial_nitf"`
		} `json:"_aarde"`
	}
	if json.Unmarshal(metadata, &annotations) == nil && annotations.Aarde.Commercial.Profile.Candidate != "" {
		p.Manifest.Assessment, _ = json.Marshal(annotations.Aarde.Commercial.Profile)
	}
	count := 1
	if format == "NITF" {
		count, err = viewerImageCount(path)
		if err != nil {
			return nil, err
		}
	}
	if count > 999 {
		return nil, ErrViewerLimit
	}
	metadataBytes := 0
	for i := 0; i < count; i++ {
		selector := path
		if format == "NITF" {
			selector = fmt.Sprintf("NITF_IM:%d:%s", i, path)
		}
		l := ViewerLayer{ID: fmt.Sprintf("segment-%d", i), Label: fmt.Sprintf("Image segment %d", i), Segment: new(int), Role: "imagery", Opacity: 1, Order: i, ContentType: "image/png", Warnings: []string{}, selector: selector}
		*l.Segment = i
		raw, e := viewerInfo(ctx, selector)
		encoded, _ := json.Marshal(raw)
		metadataBytes += len(encoded)
		if metadataBytes > 16<<20 {
			return nil, errors.New("viewer metadata exceeds 16 MiB limit")
		}
		if e == nil && raw.Driver != format {
			e = errors.New("source driver changed")
		}
		l.raw = raw
		if e != nil {
			l.Unsupported = "Segment decoding failed; check the source and installed GDAL codec"
			if i < len(segments) {
				l.Width = segments[i].Width
				l.Height = segments[i].Height
				raw.Metadata = decodeMetadata(segments[i].Metadata)
			}
		} else {
			l.Width = raw.Size[0]
			l.Height = raw.Size[1]
		}
		h := scalarMetadata(raw.Metadata)[""]
		if name := strings.TrimSpace(h["NITF_IID1"]); name != "" {
			l.Label += " · " + name
		}
		if strings.TrimSpace(h["NITF_ICAT"]) == "CLOUD" {
			l.Role = "cloud_grid"
			l.Label = "Cloud grid · " + l.Label
			l.Opacity = .4
			l.Order = 1000 + i
			l.Legend = map[string]string{"0": "Clear · transparent", "255": "Cloud · cyan", "1–254": "Reserved · magenta (not probability)"}
			p.Manifest.CloudStatus = "present"
		}
		l.Rendering = displayDescription(raw, l.Role)
		if e == nil {
			bands, _, _, bandErr := displayBands(raw)
			if bandErr != nil {
				l.Unsupported = bandErr.Error()
			}
			if l.Role == "cloud_grid" && (len(bands) != 1 || bands[0].Type != "Byte") {
				l.Unsupported = "Unsupported cloud encoding: NCDRD 2010 requires one Byte band"
				p.Manifest.CloudStatus = "unsupported"
			}
		}
		p.Manifest.Layers = append(p.Manifest.Layers, l)
	}
	if err = placeViewer(ctx, p); err != nil {
		return nil, err
	}
	if format == "NITF" {
		addViewerShapes(ctx, p)
		for _, l := range p.Manifest.Layers {
			if l.Role == "cloud_shapes" && l.Unsupported == "" {
				for axis := 0; axis < 2; axis++ {
					p.Manifest.Extent[axis] = math.Min(p.Manifest.Extent[axis], l.Extent[axis])
					p.Manifest.Extent[axis+2] = math.Max(p.Manifest.Extent[axis+2], l.Extent[axis+2])
				}
			}
		}
	}
	clouds, usable := 0, 0
	cloudState := "unsupported"
	for _, l := range p.Manifest.Layers {
		if l.Role == "imagery" {
			continue
		}
		clouds++
		if l.Unsupported == "" {
			usable++
		} else if strings.Contains(l.Unsupported, "decoding failed") {
			cloudState = "decoding_failed"
		} else if l.Registration == "Cloud registration unavailable" && cloudState != "decoding_failed" {
			cloudState = "registration_unavailable"
		}
	}
	if clouds == 0 {
		p.Manifest.CloudStatus = "absent"
	} else if usable > 0 {
		p.Manifest.CloudStatus = "present"
	} else {
		p.Manifest.CloudStatus = cloudState
	}
	if err = planViewerResolution(&p.Manifest, opts); err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if end, e := ViewerRevision(path, checksum, opts); e != nil || end != rev {
		return nil, ErrViewerRevision
	}
	return p, nil
}

func planViewerResolution(m *ViewerManifest, o ViewerOptions) error {
	var total float64
	native := true
	count := 0
	for _, l := range m.Layers {
		if l.Role == "cloud_shapes" {
			continue
		}
		count++
		w, h := float64(max(l.Width, 1)), float64(max(l.Height, 1))
		total += w * h
		if max(w, h) > float64(o.MaxDimension) || w*h > float64(o.LayerPixels) {
			native = false
		}
	}
	native = native && total <= float64(o.ScenePixels)
	m.NativeAvailable = native
	if m.Resolution == "native" && !native {
		return ErrViewerLimit
	}
	// Equal per-layer shares guarantee that no late segment is omitted. Each layer
	// gets its full native size when the complete scene is within limits.
	budget := float64(o.ScenePixels) / float64(max(count, 1))
	if native {
		budget = float64(o.LayerPixels)
	}
	maxDim := o.MaxDimension
	if m.Resolution == "preview" {
		maxDim = min(maxDim, 1024)
		budget = math.Min(budget, 1_000_000)
	}
	m.DisplayPixels = 0
	for i := range m.Layers {
		l := &m.Layers[i]
		if l.Role == "cloud_shapes" {
			continue
		}
		w, h := float64(max(l.Width, 1)), float64(max(l.Height, 1))
		scale := math.Min(1, math.Min(float64(maxDim)/math.Max(w, h), math.Sqrt(math.Min(budget, float64(o.LayerPixels))/(w*h))))
		l.DisplayWidth = max(1, int(math.Floor(w*scale)))
		l.DisplayHeight = max(1, int(math.Floor(h*scale)))
		m.DisplayPixels += int64(l.DisplayWidth) * int64(l.DisplayHeight)
	}
	if m.DisplayPixels > o.ScenePixels {
		return ErrViewerLimit
	}
	m.EstimatedMemoryBytes = m.DisplayPixels * 16
	return nil
}
func displayDescription(raw info, role string) string {
	if role == "cloud_grid" {
		return "NCDRD 2010 categorical Byte grid; nearest-neighbor; no contrast stretch"
	}
	return "Declared RGB or palette; otherwise band 1 grayscale. Integer declared-range scaling (NBITS when available); alpha and GDAL validity masks retained; nearest-neighbor downsampling."
}
func headerInt(h map[string]string, k string) (int, bool) {
	s := strings.TrimSpace(h[k])
	v, e := strconv.Atoi(s)
	return v, e == nil && v >= 0
}

// File access times change during ordinary reads and are not revisions. Retain
// device, inode and change time where the OS exposes them, in addition to size/mtime.
func stableFileIdentity(s os.FileInfo) string {
	v := reflect.ValueOf(s.Sys())
	if !v.IsValid() {
		return ""
	}
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return ""
	}
	var b strings.Builder
	for _, key := range []string{"Dev", "Ino", "Ctim", "Ctimespec"} {
		f := v.FieldByName(key)
		if f.IsValid() && f.CanInterface() {
			fmt.Fprintf(&b, "%s=%v;", key, f.Interface())
		}
	}
	return b.String()
}

// Enumeration does not depend on decoding image zero: a missing codec on one
// image must not hide other segments in a valid NITF 2.1/NSIF container.
func viewerImageCount(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	header := make([]byte, 363)
	if _, err = io.ReadFull(f, header); err != nil {
		return 0, err
	}
	if string(header[:9]) != "NITF02.10" && string(header[:9]) != "NSIF01.00" {
		return 0, errors.New("viewer supports NITF 2.1 and NSIF 1.0 image directories")
	}
	if !digitsTRE.Match(header[360:363]) {
		return 0, errors.New("invalid NITF image count")
	}
	n, err := strconv.Atoi(string(header[360:363]))
	if err != nil || n < 1 || n > 999 {
		return 0, errors.New("invalid NITF image count")
	}
	return n, nil
}
