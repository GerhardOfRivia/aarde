package api

import (
	"encoding/json"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"net/http/httptest"
	"strings"
	"testing"
)

// These requests must be rejected before any database access.
func TestValidation(t *testing.T) {
	h := Routes(nil, "dev", Access{Token: "test-token"}, "osm")
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/imagery/search", `{`, 400},
		{"POST", "/imagery/search", `{"geometry":{"type":"Point","coordinates":[1,2]}}`, 400},
		{"POST", "/imagery/search", `{"geometry":{"type":"Polygon","coordinates":[]}}`, 400},
		{"POST", "/imagery/search", `{} {}`, 400},
		{"POST", "/imagery/search", `{"surprise":1}`, 400},
		{"POST", "/imagery/search", `{"geometry":` + strings.Repeat(" ", MaxBodyBytes) + `}`, 413},
		{"GET", "/imagery?limit=201", "", 400},
		{"GET", "/imagery?limit=0", "", 400},
		{"GET", "/imagery?offset=-1", "", 400},
		{"GET", "/imagery?image_id=", "", 400},
		{"DELETE", "/imagery", "", 405},
		{"GET", "/missing", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body[:min(20, len(tc.body))], func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatal("expected structured error")
			}
		})
	}
}

func TestCloudCoverResponse(t *testing.T) {
	zero, fraction := 0.0, 12.5
	for _, cover := range []*float64{nil, &zero, &fraction} {
		data, err := json.Marshal(Response(catalog.Imagery{CloudCover: cover}))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		got, present := body["cloud_cover"]
		if !present || cover == nil && got != nil || cover != nil && got != *cover {
			t.Fatalf("cloud_cover serialized incorrectly: %s", data)
		}
	}
}
