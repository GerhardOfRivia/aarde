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
	var selectedImages []info
	auxiliary := map[int]bool{}
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
		selectedImages = append(selectedImages, selected)
		part, err := parseRasterInfo(segmentData, func(i info) (geo.Geometry, error) { return gcpFootprint(ctx, selector, i) })
		if err != nil {
			// Only an explicitly ICAT=CLOUD auxiliary can defer geographic
			// validation to reference coverage; unsupported registration stays explicit. Primary imagery
			// keeps the strict affine/GCP validation above.
			if strings.TrimSpace(scalarMetadata(selected.Metadata)[""]["NITF_ICAT"]) != "CLOUD" || len(selected.Size) != 2 || selected.Size[0] <= 0 || selected.Size[1] <= 0 || len(selected.Bands) == 0 {
				return result, fmt.Errorf("NITF image segment %d: %w", index, err)
			}
			auxiliary[index] = true
			m, _ := json.Marshal(selected.Metadata)
			part = Inspection{Format: "NITF", Width: selected.Size[0], Height: selected.Size[1], BandCount: len(selected.Bands), Metadata: m}
		}
		if index == 0 {
			result = part
		}
		result.Segments = append(result.Segments, Segment{Index: index, SourceCRS: part.SourceCRS, Width: part.Width, Height: part.Height, BandCount: part.BandCount, AcquiredAt: part.AcquiredAt, CloudCover: part.CloudCover, Footprint: part.Footprint, Metadata: part.Metadata})
	}
	if len(auxiliary) > 0 {
		layers := make([]ViewerLayer, count)
		for i, raw := range selectedImages {
			role := "imagery"
			if strings.TrimSpace(scalarMetadata(raw.Metadata)[""]["NITF_ICAT"]) == "CLOUD" {
				role = "cloud_grid"
			}
			layers[i] = ViewerLayer{raw: raw, Width: raw.Size[0], Height: raw.Size[1], Role: role}
		}
		plan := &ViewerPlan{Manifest: ViewerManifest{Format: "NITF", Layers: layers}}
		if err := placeViewer(ctx, plan); err != nil {
			return result, err
		}
		layouts := imageLayouts(layers)
		for i := range auxiliary {
			if plan.Manifest.Layers[i].Unsupported != "" {
				result.Segments[i].Metadata = storeAnnotation(result.Segments[i].Metadata, "cloud_registration_warning", plan.Manifest.Layers[i].Unsupported)
			}
			r, _ := cloudGridRecord(selectedImages[i], i)
			sensor := strings.TrimSpace(r.Fields["REG_SENSOR"])
			reference := -1
			for j, raw := range selectedImages {
				h := scalarMetadata(raw.Metadata)[""]
				if layouts[j].valid && layouts[j].root == j && strings.TrimSpace(h["NITF_ICAT"]) == sensor {
					reference = j
				}
			}
			if reference < 0 || auxiliary[reference] {
				reference = -1
				for j := range selectedImages {
					if !auxiliary[j] {
						reference = j
						break
					}
				}
				if reference < 0 {
					return result, fmt.Errorf("NITF auxiliary cloud segment %d: no primary imagery coverage", i)
				}
			}
			// Catalog coverage for this auxiliary is inherited from its reference
			// image, explicitly labelled; never use mask bounds in the file union.
			result.Segments[i].SourceCRS = result.Segments[reference].SourceCRS
			result.Segments[i].Footprint = result.Segments[reference].Footprint
			result.Segments[i].Metadata = storeAnnotation(result.Segments[i].Metadata, "footprint_method", "csccga_reference_coverage")
			result.Segments[i].Metadata = storeAnnotation(result.Segments[i].Metadata, "format", "NITF")
		}
	}
	if count == 1 {
		enrichNITF(&result, raw)
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
	// These are the unchanged container GDAL exposures. Their actual TRE storage
	// scopes are recorded separately; they are not all file-level evidence.
	for _, domain := range []string{"TRE", "xml:TRE"} {
		if value, ok := raw.Metadata[domain]; ok {
			metadata[domain] = value
		}
	}
	result.Metadata, _ = json.Marshal(metadata)
	features := make([]any, 0, count)
	for _, s := range result.Segments {
		if auxiliary[s.Index] || strings.TrimSpace(scalarMetadata(decodeMetadata(s.Metadata))[""]["NITF_ICAT"]) == "CLOUD" {
			continue
		}
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
	if err == nil {
		enrichNITF(&result, raw)
	}
	return result, err
}
