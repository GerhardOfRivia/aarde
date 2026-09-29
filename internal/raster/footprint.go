package raster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/GerhardOfRivia/aarde/internal/geo"
)

type gcpInfo struct {
	CRS struct {
		WKT string `json:"wkt"`
	} `json:"coordinateSystem"`
	Points []struct{ Pixel, Line, X, Y, Z *float64 } `json:"gcpList"`
}

func parseGCPs(data []byte) (gcpInfo, error) {
	var gcps gcpInfo
	if json.Unmarshal(data, &gcps) != nil || gcps.CRS.WKT == "" || len(gcps.Points) < 3 {
		return gcps, errors.New("at least three GCPs and their source CRS are required")
	}
	// Bound the thin-plate spline solver as well as metadata and output sizes.
	if len(gcps.Points) > 256 {
		return gcps, errors.New("GCP count exceeds limit of 256")
	}
	for _, p := range gcps.Points {
		for _, v := range []*float64{p.Pixel, p.Line, p.X, p.Y, p.Z} {
			if v == nil || math.IsNaN(*v) || math.IsInf(*v, 0) {
				return gcps, errors.New("GCP coordinates must be present and finite")
			}
		}
	}
	return gcps, nil
}

func validAffine(raw info) bool {
	if raw.CRS.WKT == "" || len(raw.Transform) != 6 {
		return false
	}
	for _, v := range raw.Transform {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	determinant := raw.Transform[1]*raw.Transform[5] - raw.Transform[2]*raw.Transform[4]
	return determinant != 0 && !math.IsInf(determinant, 0) && !math.IsNaN(determinant)
}

func catalogFootprint(data []byte) (geo.Geometry, error) {
	g, err := geo.Parse(data)
	if err != nil {
		return g, err
	}
	var polygons [][][][]float64
	if g.Type == "Polygon" {
		var polygon [][][]float64
		_ = json.Unmarshal(g.Coordinates, &polygon)
		polygons = [][][][]float64{polygon}
	} else {
		_ = json.Unmarshal(g.Coordinates, &polygons)
	}
	for _, polygon := range polygons {
		for _, ring := range polygon {
			area := 0.0
			// Translate to the first position to avoid cancellation for small scenes.
			for n := 1; n < len(ring)-1; n++ {
				area += (ring[n][0]-ring[0][0])*(ring[n+1][1]-ring[0][1]) - (ring[n+1][0]-ring[0][0])*(ring[n][1]-ring[0][1])
			}
			if area == 0 || math.IsNaN(area) || math.IsInf(area, 0) {
				return g, errors.New("footprint ring has zero or non-finite area")
			}
			minLon, maxLon := ring[0][0], ring[0][0]
			for _, p := range ring {
				minLon = math.Min(minLon, p[0])
				maxLon = math.Max(maxLon, p[0])
			}
			if maxLon-minLon > 180 {
				return g, errors.New("unsupported footprint geometry: longitude span exceeds 180 degrees; antimeridian footprints must be split")
			}
		}
	}
	g.Type = "MultiPolygon"
	g.Coordinates, _ = json.Marshal(polygons)
	return g, nil
}

func gcpFootprint(ctx context.Context, path string, raw info) (geo.Geometry, error) {
	// TPS explicitly honors non-affine corner GCPs, instead of allowing GDAL to
	// choose an implicit polynomial order. Sample 16 positions along each edge
	// of the outer pixel boundary, for a bounded 64-point catalog approximation.
	const perEdge = 16
	w, h := float64(raw.Size[0]), float64(raw.Size[1])
	var input strings.Builder
	for edge := range 4 {
		for n := range perEdge {
			t := float64(n) / perEdge
			var x, y float64
			switch edge {
			case 0:
				x, y = w*t, 0
			case 1:
				x, y = w, h*t
			case 2:
				x, y = w*(1-t), h
			case 3:
				x, y = 0, h*(1-t)
			}
			fmt.Fprintf(&input, "%.17g %.17g 0\n", x, y)
		}
	}
	output, err := runGDAL(ctx, "gdaltransform", strings.NewReader(input.String()), 64<<10, "-tps", "-t_srs", "EPSG:4326", path)
	if err != nil {
		return geo.Geometry{}, err
	}
	fields := strings.Fields(string(output))
	if len(fields) != 4*perEdge*3 {
		return geo.Geometry{}, errors.New("gdaltransform did not return 64 geographic positions; check GCPs and CRS")
	}
	ring := make([][]float64, 0, 4*perEdge+1)
	for n := 0; n < len(fields); n += 3 {
		xyz := make([]float64, 3)
		for axis := range 3 {
			xyz[axis], err = strconv.ParseFloat(fields[n+axis], 64)
			if err != nil || math.IsNaN(xyz[axis]) || math.IsInf(xyz[axis], 0) {
				return geo.Geometry{}, errors.New("gdaltransform returned a failed or non-finite position")
			}
		}
		ring = append(ring, xyz[:2])
	}
	ring = append(ring, ring[0])
	coords, _ := json.Marshal([][][]float64{ring})
	return catalogFootprint(geo.Geometry{Type: "Polygon", Coordinates: coords}.JSON())
}
