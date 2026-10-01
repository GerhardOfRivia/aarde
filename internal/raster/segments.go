package raster

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/GerhardOfRivia/aarde/internal/geo"
)

func inspectContainer(ctx context.Context, path string, data []byte) (Inspection, error) {
	var raw info
	if err := json.Unmarshal(data, &raw); err != nil {
		return Inspection{}, fmt.Errorf("invalid gdalinfo JSON: %w", err)
	}
	if raw.Driver != "NITF" {
		return parseInfo(data, func(i info) (geo.Geometry, error) { return gcpFootprint(ctx, path, i) })
	}
	count, err := nitfImageCount(raw)
	if err != nil {
		return Inspection{}, err
	}
	var result Inspection
	totalMetadata := 0
	for index := 0; index < count; index++ {
		selector := fmt.Sprintf("NITF_IM:%d:%s", index, path)
		segmentData, err := runGDAL(ctx, "gdalinfo", nil, 16<<20, "-json", "-noct", "-norat", "-mdd", "IMAGE_STRUCTURE", "-mdd", "RPC", "-mdd", "TRE", "-mdd", "xml:TRE", selector)
		if err != nil {
			return result, fmt.Errorf("NITF image segment %d: %w", index, err)
		}
		totalMetadata += len(segmentData)
		if totalMetadata > 16<<20 {
			return result, fmt.Errorf("NITF image segment %d: combined segment metadata exceeds size limit", index)
		}
		// Container count is validated above; selected datasets need only raster validation.
		var selected info
		if err = json.Unmarshal(segmentData, &selected); err != nil {
			return result, fmt.Errorf("NITF image segment %d: %w", index, err)
		}
		if selected.Driver != "NITF" {
			return result, fmt.Errorf("NITF image segment %d: unexpected driver %q", index, selected.Driver)
		}
		delete(selected.Metadata, "SUBDATASETS")
		segmentData, _ = json.Marshal(selected)
		part, err := parseInfo(segmentData, func(i info) (geo.Geometry, error) { return gcpFootprint(ctx, selector, i) })
		if err != nil {
			return result, fmt.Errorf("NITF image segment %d: %w", index, err)
		}
		if index == 0 {
			result = part
		}
		result.Segments = append(result.Segments, Segment{Index: index, SourceCRS: part.SourceCRS, Width: part.Width, Height: part.Height, BandCount: part.BandCount, AcquiredAt: part.AcquiredAt, CloudCover: part.CloudCover, Footprint: part.Footprint, Metadata: part.Metadata})
	}
	if count == 1 {
		return result, nil
	}
	result.Width, result.Height, result.BandCount = 0, 0, 0
	result.SourceCRS = ""
	result.Bounds = nil
	for _, s := range result.Segments[1:] {
		if result.AcquiredAt == nil || s.AcquiredAt == nil || !result.AcquiredAt.Equal(*s.AcquiredAt) {
			result.AcquiredAt = nil
		}
		if result.CloudCover == nil || s.CloudCover == nil || *result.CloudCover != *s.CloudCover {
			result.CloudCover = nil
		}
	}
	result.Metadata = json.RawMessage(`{"_aarde":{"format":"NITF","footprint_method":"geometric_union","aggregation":"dimensions/bands/CRS unavailable; time/cloud unanimous or unknown"}}`)
	// Keep file-header metadata without presenting the first image header as
	// container-level metadata. Segment domains are preserved on each segment.
	var metadata map[string]json.RawMessage
	_ = json.Unmarshal(result.Metadata, &metadata)
	header := make(map[string]string)
	for key, value := range scalarMetadata(raw.Metadata)[""] {
		if strings.HasPrefix(key, "NITF_F") || key == "NITF_CLEVEL" || key == "NITF_OSTAID" || key == "NITF_ONAME" || key == "NITF_OPHONE" || key == "NITF_STYPE" {
			header[key] = value
		}
	}
	metadata[""], _ = json.Marshal(header)
	result.Metadata, _ = json.Marshal(metadata)
	features := make([]any, 0, count)
	for _, s := range result.Segments {
		features = append(features, map[string]any{"type": "Feature", "properties": map[string]any{}, "geometry": s.Footprint})
	}
	input, _ := json.Marshal(map[string]any{"type": "FeatureCollection", "name": "footprints", "features": features})
	if len(input) > 16<<20 {
		return result, fmt.Errorf("NITF segment footprint union input exceeds size limit")
	}
	output, err := runGDAL(ctx, "ogr2ogr", strings.NewReader(string(input)), 16<<20, "-f", "GeoJSON", "/vsistdout/", "/vsistdin/", "-dialect", "SQLite", "-sql", "SELECT ST_Union(geometry) AS geometry FROM footprints")
	if err != nil {
		return result, fmt.Errorf("NITF segment footprint union: %w", err)
	}
	var collection struct {
		Features []struct {
			Geometry json.RawMessage `json:"geometry"`
		} `json:"features"`
	}
	if err = json.Unmarshal(output, &collection); err != nil || len(collection.Features) != 1 {
		return result, fmt.Errorf("invalid NITF footprint union output")
	}
	result.Footprint, err = catalogFootprint(collection.Features[0].Geometry)
	return result, err
}
