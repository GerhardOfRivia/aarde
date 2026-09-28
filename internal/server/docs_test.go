package server

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/api"
)

func TestDocsRoutesAndAssets(t *testing.T) {
	h := Handler(nil, "docs-test", api.Access{Token: "secret-test-token"}, "osm")
	for _, tc := range []struct {
		method, path, contentType string
		status                    int
	}{
		{"GET", "/docs", "text/html", 308},
		{"GET", "/docs/", "text/html", 200},
		{"HEAD", "/docs/", "text/html", 200},
		{"GET", "/openapi.json", "application/json", 200},
		{"HEAD", "/openapi.json", "application/json", 200},
		{"GET", "/docs/missing.js", "text/plain", 404},
		{"GET", "/docs/missing", "text/plain", 404},
		{"POST", "/docs/", "", 405},
		{"POST", "/openapi.json", "", 405},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(tc.method, tc.path, nil))
			if w.Code != tc.status || !strings.HasPrefix(w.Header().Get("Content-Type"), tc.contentType) {
				t.Fatalf("response: %d %v %s", w.Code, w.Header(), w.Body.String())
			}
			if tc.method == "HEAD" && w.Body.Len() != 0 {
				t.Error("HEAD returned a body")
			}
			if tc.status == 308 && w.Header().Get("Location") != "/docs/" {
				t.Error("missing canonical redirect")
			}
			if tc.status == 200 && w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Error("missing security headers")
			}
			if tc.path == "/openapi.json" && tc.status == 200 && w.Header().Get("Cache-Control") != "no-store" {
				t.Error("spec must not be cached")
			}
			if strings.Contains(w.Body.String(), "secret-test-token") {
				t.Error("token disclosed")
			}
		})
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/docs/", nil))
	if !strings.Contains(w.Body.String(), `id="swagger-ui"`) || !strings.Contains(w.Body.String(), `href="/openapi.json"`) {
		t.Fatal("docs returned SPA instead of reference")
	}
	assets := regexp.MustCompile(`(?:src|href)="(/assets/[^"?]+)"`).FindAllStringSubmatch(w.Body.String(), -1)
	if len(assets) < 2 {
		t.Fatal("docs must reference bundled scripts and styles")
	}
	for _, asset := range assets {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", asset[1], nil))
		if w.Code != 200 || strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
			t.Errorf("missing docs asset %s", asset[1])
		}
	}
	for _, name := range []string{"LICENSE", "NOTICE", "swagger-ui-bundle.js.LICENSE.txt"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/docs/"+name, nil))
		if w.Code != 200 {
			t.Errorf("missing license %s", name)
		}
	}
}
