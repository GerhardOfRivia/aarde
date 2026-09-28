package geo

import (
	"encoding/json"
	"testing"
)

func TestValidate(t *testing.T) {
	cases := []struct {
		name, body string
		valid      bool
	}{
		{"polygon", `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`, true},
		{"multi", `{"type":"MultiPolygon","coordinates":[[[[0,0],[1,0],[1,1],[0,0]]]]}`, true},
		{"trailing JSON", `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]} {}`, false},
		{"null position", `{"type":"Polygon","coordinates":[[[null,0],[1,0],[1,1],[0,0]]]}`, false},
		{"malformed", `{`, false},
		{"point", `{"type":"Point","coordinates":[0,0]}`, false},
		{"empty", `{"type":"Polygon","coordinates":[]}`, false},
		{"null", `{"type":"Polygon","coordinates":null}`, false},
		{"open", `{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,1]]]}`, false},
		{"range", `{"type":"Polygon","coordinates":[[[181,0],[1,0],[1,1],[181,0]]]}`, false},
		{"dimension", `{"type":"Polygon","coordinates":[[[0,0,1],[1,0],[1,1],[0,0,1]]]}`, false},
		{"dateline", `{"type":"Polygon","coordinates":[[[179,0],[-179,0],[-179,1],[179,0]]]}`, false},
		{"crs override", `{"type":"Polygon","crs":{},"coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.body))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
		})
	}
	ring := make([][]float64, MaxVertices+1)
	for i := range ring {
		ring[i] = []float64{0, 0}
	}
	coords, _ := json.Marshal([][][]float64{ring})
	if err := (Geometry{Type: "Polygon", Coordinates: coords}).Validate(); err == nil {
		t.Fatal("accepted excessive vertices")
	}
}
