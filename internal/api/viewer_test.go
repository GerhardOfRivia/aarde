package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

// Unimplemented mutation methods panic, proving viewing cannot write the catalog.
type viewerStore struct {
	catalog.Store
	items map[string]catalog.Imagery
}

func (s viewerStore) Get(_ context.Context, c, id string) (catalog.Imagery, error) {
	i, ok := s.items[c+"/"+id]
	if !ok {
		return i, catalog.ErrNotFound
	}
	return i, nil
}
func viewerRequest(h http.Handler, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func TestViewerAuthValidationAndOverload(t *testing.T) {
	h := Routes(nil, "test", Access{Token: "secret", PublicRead: true}, "none")
	for _, suffix := range []string{"", "/layers/segment-0/image.png", "/layers/cloud-shapes-0/geometry"} {
		w := viewerRequest(h, "/imagery/default/one/viewer"+suffix, "")
		if w.Code != 401 {
			t.Fatalf("public rendering allowed: %d", w.Code)
		}
	}
	for _, query := range []string{"?resolution=tile", "?size=99999", "?resolution=auto&resolution=native", "?revision=%zz"} {
		w := viewerRequest(h, "/imagery/default/one/viewer"+query, "secret")
		if w.Code != 400 {
			t.Fatalf("invalid parameter accepted: %s %d", query, w.Code)
		}
	}
}
func TestViewerHTTPNativeContractAndRevision(t *testing.T) {
	testutil.RequireGDAL(t)
	path := filepath.Join(t.TempDir(), "scene.ntf")
	testutil.ViewerNITF(t, path, "valid", true)
	in, e := raster.Inspect(context.Background(), path)
	if e != nil {
		t.Fatal(e)
	}
	items := map[string]catalog.Imagery{}
	for _, cat := range []string{"one & two", "other"} {
		items[cat+"/scene ?"] = catalog.Imagery{CatalogID: cat, ImageID: "scene ?", DisplayName: "scene.ntf", AssetLocation: path, Checksum: in.Checksum, Segments: in.Segments, Metadata: in.Metadata}
	}
	h := Routes(catalog.New(viewerStore{items: items}), "test", Access{Token: "secret", PublicRead: true}, "none")
	_, compiler := contractCompiler(t)
	var first raster.ViewerManifest
	for _, cat := range []string{"one & two", "other"} {
		base := "/imagery/" + url.PathEscape(cat) + "/" + url.PathEscape("scene ?") + "/viewer"
		w := viewerRequest(h, base, "secret")
		if w.Code != 200 {
			t.Fatalf("manifest: %d %s", w.Code, w.Body.String())
		}
		validateContract(t, compiler, "#/components/schemas/ViewerManifest", w.Body.Bytes())
		var m raster.ViewerManifest
		if e = json.Unmarshal(w.Body.Bytes(), &m); e != nil {
			t.Fatal(e)
		}
		if m.CatalogID != cat || m.ImageID != "scene ?" || len(m.Layers) != 5 {
			t.Fatal("wrong parent or missing layer")
		}
		if strings.Contains(w.Body.String(), path) || strings.Contains(w.Body.String(), "NITF_IM:") {
			t.Fatal("physical source leaked")
		}
		for _, l := range m.Layers {
			content := strings.TrimPrefix(l.ContentURL, "/api/v1")
			w = viewerRequest(h, content, "secret")
			if w.Code != 200 {
				t.Fatalf("content %s: %d %s", l.ID, w.Code, w.Body.String())
			}
			if l.Role == "cloud_shapes" {
				validateContract(t, compiler, "#/components/schemas/ViewerGeometry", w.Body.Bytes())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cached protected imagery")
			}
			if viewerRequest(h, content, "").Code != 401 {
				t.Fatal("cache bypassed authentication")
			}
		}
		if viewerRequest(h, base+"/layers/segment-999/image.png?revision="+m.Revision, "secret").Code != 404 {
			t.Fatal("unknown layer resolved")
		}
		first = m
	}
	if viewerRequest(h, "/imagery/missing/missing/viewer", "secret").Code != 404 {
		t.Fatal("unknown parent accepted")
	}
	// A source replacement invalidates cached plans before returning any bytes.
	f, e := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal(e)
	}
	_, _ = f.Write([]byte{0})
	f.Close()
	if w := viewerRequest(h, strings.TrimPrefix(first.Layers[0].ContentURL, "/api/v1"), "secret"); w.Code != 409 {
		t.Fatalf("stale source accepted: %d", w.Code)
	}
}
func TestViewerPoolRejectsRatherThanQueues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.tif")
	os.WriteFile(path, []byte("placeholder"), 0600)
	opts := raster.DefaultViewerOptions()
	opts.Concurrent = 1
	h := newViewerHandler(catalog.New(viewerStore{items: map[string]catalog.Imagery{"default/image": {AssetLocation: path, Metadata: json.RawMessage(`{"_aarde":{"format":"GTiff"}}`)}}}), opts)
	h.slots <- struct{}{}
	router, _ := routes(nil, "test", Access{Token: "secret"}, "none")
	// A small router supplies the same parent identifiers without spawning GDAL.
	router.Get("/capacity/{catalogID}/{imageID}", h.serve)
	w := viewerRequest(router, "/capacity/default/image", "secret")
	if w.Code != 503 || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("pool: %d %s", w.Code, w.Body.String())
	}
}
func TestViewerErrorsAreBounded(t *testing.T) {
	for _, err := range []error{raster.ErrViewerLimit, raster.ErrViewerRevision, context.Canceled, context.DeadlineExceeded, fmt.Errorf("/private/path: %s", strings.Repeat("secret", 10000))} {
		w := httptest.NewRecorder()
		viewerFailure(w, err)
		if len(w.Body.Bytes()) > 512 || strings.Contains(w.Body.String(), "private/path") {
			t.Fatal("diagnostics exposed")
		}
	}
}
