package raster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type nitfDES struct {
	index                          int
	headerOffset, dataOffset, size int64
	headerSize                     int
}

// Read only the bounded NITF 2.1 directory and DES subheaders. Image data is
// always decoded by GDAL; opaque DES payloads are never returned to clients.
func viewerDES(path string) ([]nitfDES, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	prefix := make([]byte, 360)
	if _, err = io.ReadFull(f, prefix); err != nil {
		return nil, err
	}
	if string(prefix[:9]) != "NITF02.10" {
		return nil, errors.New("CSSHPA directory supports NITF 2.1 only")
	}
	hl, err := strconv.Atoi(string(prefix[354:360]))
	if err != nil || hl < 360 || hl > 1<<20 || int64(hl) > st.Size() {
		return nil, errors.New("invalid NITF header size")
	}
	header := make([]byte, hl)
	if _, err = f.ReadAt(header, 0); err != nil {
		return nil, err
	}
	pos := 360
	offset := int64(hl)
	var result []nitfDES
	number := func(width int) (int64, error) {
		if pos+width > len(header) {
			return 0, errors.New("truncated NITF directory")
		}
		s := string(header[pos : pos+width])
		pos += width
		if !digitsTRE.MatchString(s) {
			return 0, fmt.Errorf("invalid NITF directory number at %d (width %d)", pos-width, width)
		}
		return strconv.ParseInt(s, 10, 64)
	}
	for section, widths := range [][2]int{{6, 10}, {4, 6}, {0, 0}, {4, 5}, {4, 9}, {4, 7}} {
		count, e := number(3)
		if e != nil {
			return nil, e
		}
		if section == 2 && count != 0 {
			return nil, errors.New("unsupported NITF reserved segments")
		}
		for i := 0; i < int(count); i++ {
			sh, e := number(widths[0])
			if e != nil {
				return nil, e
			}
			data, e := number(widths[1])
			if e != nil {
				return nil, e
			}
			if sh <= 0 || offset+sh+data > st.Size() {
				return nil, errors.New("NITF segment range outside file")
			}
			if section == 4 {
				result = append(result, nitfDES{i, offset, offset + sh, data, int(sh)})
			}
			offset += sh + data
		}
	}
	return result, nil
}
func addViewerShapes(ctx context.Context, p *ViewerPlan) {
	entries, err := viewerDES(p.Path)
	if err != nil {
		p.Manifest.Warnings = append(p.Manifest.Warnings, "Embedded cloud-shape discovery unavailable: "+err.Error())
		return
	}
	f, err := os.Open(p.Path)
	if err != nil {
		return
	}
	defer f.Close()
	var consumed int64
	grid := false
	for _, l := range p.Manifest.Layers {
		grid = grid || (l.Role == "cloud_grid" && l.Unsupported == "")
	}
	for _, entry := range entries {
		header := make([]byte, entry.headerSize)
		if _, err = f.ReadAt(header, entry.headerOffset); err != nil {
			continue
		}
		if len(header) < 200 || string(header[:2]) != "DE" || strings.TrimSpace(string(header[2:27])) != "CSSHPA DES" {
			continue
		}
		if len(header) < 225 {
			p.Manifest.Warnings = append(p.Manifest.Warnings, "Malformed CSSHPA subheader")
			continue
		}
		use := strings.TrimSpace(string(header[200:225]))
		if use == "IMAGE_SHAPE" {
			continue
		}
		if use != "CLOUD_SHAPES" {
			p.Manifest.Warnings = append(p.Manifest.Warnings, "Unsupported CSSHPA SHAPE_USE; no cloud geometry inferred")
			continue
		}
		l := ViewerLayer{ID: fmt.Sprintf("cloud-shapes-%d", entry.index), Label: fmt.Sprintf("Cloud shapes · DES %d", entry.index), Role: "cloud_shapes", DES: new(int), Opacity: .4, Order: 2000 + entry.index, ContentType: "application/geo+json", Mesh: [][2]float64{}, Warnings: []string{}, Registration: "Registered", Placement: "CSSHPA v01 WGS84 polygons transformed to viewer coordinates", Rendering: "Polygon holes and multipart geometry retained; outline only when a cloud grid is present"}
		*l.DES = entry.index
		if grid {
			l.Rendering = "outline"
		} else {
			l.Rendering = "fill"
		}
		p.Manifest.CloudStatus = "present"
		fail := func(message string) {
			l.Unsupported = message
			l.Registration = "Cloud registration unavailable"
			p.Manifest.CloudStatus = "unsupported"
		}
		length, e := strconv.Atoi(string(header[196:200]))
		if e != nil || string(header[27:29]) != "01" || length != 80 || len(header) != 280 || strings.TrimSpace(string(header[225:235])) != "POLYGON" {
			fail("Unsupported CSSHPA version or polygon subheader")
		} else if entry.size > 8<<20 || entry.size < 100 || consumed+entry.size > 16<<20 {
			fail("CSSHPA payload exceeds bounded extraction limit")
		} else if !validAffine(p.reference) {
			fail("Cloud-shape geographic registration unavailable")
		} else {
			consumed += entry.size
			payload := make([]byte, entry.size)
			if _, err = f.ReadAt(payload, entry.dataOffset); err != nil {
				fail("Cloud-shape decoding failed")
			} else {
				geometry, extent, e := decodeViewerShapes(ctx, header[253:280], payload, p)
				if e != nil {
					fail("Cloud-shape decoding failed: " + e.Error())
					p.Manifest.CloudStatus = "decoding_failed"
				} else {
					l.geometry = geometry
					l.Extent = extent
				}
			}
		}
		p.Manifest.Layers = append(p.Manifest.Layers, l)
	}
}
func decodeViewerShapes(ctx context.Context, components, payload []byte, p *ViewerPlan) (json.RawMessage, [4]float64, error) {
	var empty [4]float64
	type component struct {
		name  string
		start int
	}
	var parts []component
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		name := string(components[i*9 : i*9+3])
		start, e := strconv.Atoi(string(components[i*9+3 : i*9+9]))
		if e != nil || start < 0 || start >= len(payload) || (name != "SHP" && name != "SHX" && name != "DBF") || seen[name] {
			return nil, empty, errors.New("invalid component offsets/names")
		}
		seen[name] = true
		parts = append(parts, component{name, start})
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].start < parts[j].start })
	if parts[0].start != 0 {
		return nil, empty, errors.New("invalid first component offset")
	}
	dir, err := os.MkdirTemp(p.Options.TempDir, "aarde-shapes-")
	if err != nil {
		return nil, empty, errors.New("temporary extraction unavailable")
	}
	defer os.RemoveAll(dir)
	for i, c := range parts {
		end := len(payload)
		if i < 2 {
			end = parts[i+1].start
		}
		if end <= c.start {
			return nil, empty, errors.New("overlapping components")
		}
		if err = os.WriteFile(filepath.Join(dir, "cloud."+strings.ToLower(c.name)), payload[c.start:end], 0600); err != nil {
			return nil, empty, errors.New("temporary extraction failed")
		}
	}
	data, err := runGDAL(ctx, "ogr2ogr", nil, 8<<20, "-f", "GeoJSON", "/vsistdout/", filepath.Join(dir, "cloud.shp"), "-select", "")
	if err != nil {
		return nil, empty, errors.New("invalid embedded shapefile")
	}
	var fc struct {
		Features []struct {
			Geometry struct {
				Type        string          `json:"type"`
				Coordinates json.RawMessage `json:"coordinates"`
			} `json:"geometry"`
		} `json:"features"`
	}
	if json.Unmarshal(data, &fc) != nil || len(fc.Features) > 1000 {
		return nil, empty, errors.New("invalid/excessive cloud geometry")
	}
	var points [][2]float64
	var polygons [][][][]float64
	types := []string{}
	counts := []int{}
	for _, feature := range fc.Features {
		g := feature.Geometry
		var multi [][][][]float64
		if g.Type == "Polygon" {
			var poly [][][]float64
			if json.Unmarshal(g.Coordinates, &poly) != nil {
				return nil, empty, errors.New("invalid polygon")
			}
			multi = [][][][]float64{poly}
		} else if g.Type == "MultiPolygon" {
			if json.Unmarshal(g.Coordinates, &multi) != nil {
				return nil, empty, errors.New("invalid multipolygon")
			}
		} else {
			return nil, empty, errors.New("only polygon cloud shapes are supported")
		}
		types = append(types, g.Type)
		counts = append(counts, len(multi))
		for _, poly := range multi {
			nodes := 0
			for _, ring := range poly {
				if len(ring) < 4 {
					return nil, empty, errors.New("invalid polygon ring")
				}
				for _, v := range ring {
					nodes++
					if len(v) < 2 || math.IsNaN(v[0]) || math.IsNaN(v[1]) || math.Abs(v[0]) > 180 || math.Abs(v[1]) > 90 {
						return nil, empty, errors.New("invalid WGS84 coordinate")
					}
					points = append(points, [2]float64{v[0], v[1]})
				}
				if ring[0][0] != ring[len(ring)-1][0] || ring[0][1] != ring[len(ring)-1][1] {
					return nil, empty, errors.New("unclosed polygon ring")
				}
			}
			if nodes > 1000 {
				return nil, empty, errors.New("polygon exceeds 1000 nodes")
			}
			polygons = append(polygons, poly)
		}
	}
	if len(polygons) > 1000 || len(points) > 100_000 {
		return nil, empty, errors.New("cloud geometry exceeds viewer vertex limit")
	}
	points, err = transformPoints(ctx, points, "EPSG:4326", p.reference.CRS.WKT)
	if err != nil {
		return nil, empty, errors.New("coordinate transformation failed")
	}
	extent := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	at := 0
	for _, poly := range polygons {
		for _, ring := range poly {
			for _, v := range ring {
				xy := viewerPoint(p.reference, points[at])
				at++
				v[0], v[1] = xy[0], xy[1]
				extent[0] = math.Min(extent[0], xy[0])
				extent[1] = math.Min(extent[1], xy[1])
				extent[2] = math.Max(extent[2], xy[0])
				extent[3] = math.Max(extent[3], xy[1])
			}
		}
	}
	features := []any{}
	at = 0
	for i, kind := range types {
		var coords any = polygons[at : at+counts[i]]
		if kind == "Polygon" {
			coords = polygons[at]
		}
		at += counts[i]
		features = append(features, map[string]any{"type": "Feature", "properties": map[string]any{}, "geometry": map[string]any{"type": kind, "coordinates": coords}})
	}
	if len(points) == 0 {
		extent = p.Manifest.Extent
	}
	result, err := json.Marshal(map[string]any{"type": "FeatureCollection", "features": features})
	return result, extent, err
}
