package server

import (
	"io/fs"
	"net/http"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/go-chi/chi/v5"
)

func registerDocs(r chi.Router, assets fs.FS, version string, access api.Access) {
	document, err := api.OpenAPIDocument(version, access)
	if err != nil {
		// An invalid embedded specification is a build error, not a request error.
		panic(err)
	}
	files := http.FileServer(http.FS(assets))
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		r.MethodFunc(method, "/openapi.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if r.Method != http.MethodHead {
				_, _ = w.Write(document)
			}
		})
		r.MethodFunc(method, "/docs", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/docs/", http.StatusPermanentRedirect)
		})
		r.MethodFunc(method, "/docs/*", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		})
	}
}
