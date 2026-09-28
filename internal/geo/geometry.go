// Package geo validates bounded, two-dimensional WGS84 GeoJSON structure.
// PostGIS is responsible for topology and spatial operations.
package geo

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
)

const MaxVertices = 10000

type Geometry struct {
	Type        string          `json:"type"`
	Coordinates json.RawMessage `json:"coordinates"`
}

func Parse(data []byte) (Geometry, error) {
	var g Geometry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&g); err != nil {
		return g, errors.New("geometry must be a GeoJSON geometry object")
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return g, errors.New("geometry must contain one JSON object")
	}
	if err := g.Validate(); err != nil {
		return g, err
	}
	return g, nil
}

func (g Geometry) Validate() error {
	if bytes.Contains(g.Coordinates, []byte("null")) {
		return errors.New("coordinates must not contain null")
	}
	var polygons [][][][]float64
	switch g.Type {
	case "Polygon":
		var polygon [][][]float64
		if err := json.Unmarshal(g.Coordinates, &polygon); err != nil {
			return errors.New("invalid polygon coordinates")
		}
		polygons = [][][][]float64{polygon}
	case "MultiPolygon":
		if err := json.Unmarshal(g.Coordinates, &polygons); err != nil {
			return errors.New("invalid multipolygon coordinates")
		}
	default:
		return errors.New("search geometry must be a Polygon or MultiPolygon")
	}
	if len(polygons) == 0 {
		return errors.New("geometry must not be empty")
	}
	count := 0
	for _, polygon := range polygons {
		if len(polygon) == 0 {
			return errors.New("polygon must have an exterior ring")
		}
		for _, ring := range polygon {
			if len(ring) < 4 {
				return errors.New("each ring requires at least four positions")
			}
			count += len(ring)
			if count > MaxVertices {
				return fmt.Errorf("geometry exceeds %d vertices", MaxVertices)
			}
			for i, p := range ring {
				if len(p) != 2 || math.IsNaN(p[0]) || math.IsNaN(p[1]) || math.IsInf(p[0], 0) || math.IsInf(p[1], 0) || p[0] < -180 || p[0] > 180 || p[1] < -90 || p[1] > 90 {
					return errors.New("positions must be [longitude, latitude] in EPSG:4326")
				}
				if i > 0 && math.Abs(p[0]-ring[i-1][0]) > 180 {
					return errors.New("antimeridian-crossing rings are not supported; split them into a MultiPolygon")
				}
			}
			first, last := ring[0], ring[len(ring)-1]
			if first[0] != last[0] || first[1] != last[1] {
				return errors.New("polygon rings must be closed")
			}
		}
	}
	return nil
}

func (g Geometry) JSON() []byte { b, _ := json.Marshal(g); return b }
