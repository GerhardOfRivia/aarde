package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/geo"
)

// Unimplemented store methods panic, so the access matrix also detects accidental writes.
type readStore struct{ catalog.Store }

func (readStore) Ping(context.Context) error                 { return nil }
func (readStore) Catalogs(context.Context) ([]string, error) { return []string{"default"}, nil }
func (readStore) Get(context.Context, string, string) (catalog.Imagery, error) {
	return catalog.Imagery{Metadata: json.RawMessage(`{}`)}, nil
}
func (readStore) Search(context.Context, catalog.Query) (catalog.Page, error) {
	return catalog.Page{Limit: 50}, nil
}
func (readStore) ValidateGeometry(context.Context, geo.Geometry) error { return nil }

func TestWebAccess(t *testing.T) {
	reads := []struct{ method, path, body string }{
		{"GET", "/api/v1/info", ""}, {"GET", "/api/v1/version", ""}, {"GET", "/api/v1/health", ""},
		{"GET", "/api/v1/catalogs", ""}, {"GET", "/api/v1/imagery?image_id=test", ""},
		{"GET", "/api/v1/imagery/default/test", ""},
		{"POST", "/api/v1/imagery/search", `{"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}`},
	}
	for _, route := range append([]struct{ method, path, body string }{}, reads...) {
		if route.method == "GET" {
			route.method = "HEAD"
			reads = append(reads, route)
		}
	}
	for _, public := range []bool{false, true} {
		h := Handler(catalog.New(readStore{}), "test-version", api.Access{Token: "test-token", PublicRead: public}, "osm")
		for _, route := range reads {
			for _, auth := range []struct {
				name    string
				headers []string
				valid   bool
			}{
				{"anonymous", nil, false}, {"valid", []string{"Bearer test-token"}, true},
				{"wrong", []string{"Bearer wrong"}, false}, {"empty", []string{""}, false},
				{"empty bearer", []string{"Bearer "}, false}, {"basic", []string{"Basic test-token"}, false},
				{"duplicate", []string{"Bearer test-token", "Bearer test-token"}, false},
				{"extra whitespace", []string{"Bearer test-token "}, false},
			} {
				t.Run(fmt.Sprintf("public=%t/%s/%s/%s", public, route.method, route.path, auth.name), func(t *testing.T) {
					req := httptest.NewRequest(route.method, route.path, strings.NewReader(route.body))
					for _, header := range auth.headers {
						req.Header.Add("Authorization", header)
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, req)
					want := http.StatusUnauthorized
					if auth.valid || (public && auth.headers == nil) {
						want = http.StatusOK
					}
					if w.Code != want {
						t.Fatalf("status=%d want=%d: %s", w.Code, want, w.Body.String())
					}
					if w.Header().Get("Cache-Control") != "no-store" {
						t.Fatal("API response may be cached")
					}
					if want == 401 && w.Header().Get("WWW-Authenticate") == "" {
						t.Fatal("missing challenge")
					}
					if w.Code == 200 && route.path == "/api/v1/info" {
						var info api.InfoResponse
						if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
							t.Fatal(err)
						}
						if info.Authenticated != auth.valid || info.PublicRead != public || !info.ReadOnly || info.Version != "test-version" {
							t.Fatalf("incorrect access: %+v", info)
						}
					}
					if strings.Contains(w.Body.String(), "test-token") {
						t.Fatal("token disclosed")
					}
				})
			}
		}
		for _, path := range []string{"/api", "/api/", "/api/v1", "/api/v1/", "/api/v2/imagery", "/api/v1/future", "/api/v1/imagery/default/test/future"} {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 401 {
				t.Errorf("unknown route %s = %d", path, w.Code)
			}
		}
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			for _, path := range []string{"/api/v1/info", "/api/v1/imagery", "/api/v1/imagery/default/test"} {
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, path, nil))
				if w.Code != 401 {
					t.Errorf("%s %s = %d", method, path, w.Code)
				}
			}
		}
	}
	// A missing server token cannot authenticate an empty bearer or a token in the URL/cookie.
	h := Handler(nil, "dev", api.Access{}, "osm")
	for _, path := range []string{"/api/v1/info", "/api/v1/info?token=test-token"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer ")
		req.AddCookie(&http.Cookie{Name: "token", Value: "test-token"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 401 {
			t.Fatalf("empty token accepted: %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code == 401 {
		t.Fatal("login page requires authentication")
	}
}

func TestInfoBasemap(t *testing.T) {
	for _, basemap := range []string{"osm", "none"} {
		for _, public := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/public=%t", basemap, public), func(t *testing.T) {
				h := Handler(nil, "test-version", api.Access{Token: "test-token", PublicRead: public}, basemap)
				req := httptest.NewRequest("GET", "/api/v1/info", nil)
				if !public {
					req.Header.Set("Authorization", "Bearer test-token")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				var info api.InfoResponse
				if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
					t.Fatal(err)
				}
				if w.Code != http.StatusOK || info.Basemap != basemap {
					t.Fatalf("info = %d %s; want basemap %q", w.Code, w.Body.String(), basemap)
				}
			})
		}
	}
}
