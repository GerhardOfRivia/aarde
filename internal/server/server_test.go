package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/api"
)

func TestAPIFallbackAndSecurityHeaders(t *testing.T) {
	h := Handler(nil, "dev", api.Access{Token: "test-token"}, "osm")
	for _, path := range []string{"/api/v1/missing", "/api/v2/imagery"} {
		w := httptest.NewRecorder()
		request := httptest.NewRequest("GET", path, nil)
		request.Header.Set("Authorization", "Bearer test-token")
		h.ServeHTTP(w, request)
		if w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
			t.Fatalf("API route fell through to SPA: %d %s", w.Code, w.Body.String())
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Fatal("unexpected security/CORS headers")
		}
	}
}

func TestBuildVersion(t *testing.T) {
	// Version metadata must be available even when no catalog can be reached.
	for _, version := range []string{"dev", "v1.2.3-rc.1+abc123"} {
		w := httptest.NewRecorder()
		Handler(nil, version, api.Access{PublicRead: true}, "osm").ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/version", nil))
		if w.Code != 200 {
			t.Fatalf("unexpected status: %d", w.Code)
		}
		var body struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Version != version {
			t.Fatalf("version = %q, want %q", body.Version, version)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("build version must not be cached across upgrades")
		}
	}
}

func TestOfflineBasemapAssets(t *testing.T) {
	h := Handler(nil, "test", api.Access{PublicRead: true}, "offline")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/basemaps/natural-earth-110m.geojson", nil))
	if w.Code != 200 {
		t.Fatalf("missing embedded basemap: %d", w.Code)
	}
	var data struct {
		Type     string            `json:"type"`
		Features []json.RawMessage `json:"features"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil {
		t.Fatal(err)
	}
	if data.Type != "FeatureCollection" || len(data.Features) == 0 {
		t.Fatal("embedded basemap contains no geography")
	}
	if w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatal("unversioned basemap must revalidate after upgrades")
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/basemaps/NOTICE.txt", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Natural Earth") {
		t.Fatal("missing bundled data provenance")
	}
}
