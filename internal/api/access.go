package api

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// Access is private by default. PublicRead only opens explicitly registered reads.
type Access struct {
	Token      string
	PublicRead bool
}

type authenticatedKey struct{}

type InfoResponse struct {
	Version       string `json:"version"`
	PublicRead    bool   `json:"public_read"`
	Authenticated bool   `json:"authenticated"`
	ReadOnly      bool   `json:"read_only"`
	Basemap       string `json:"basemap"`
}

func (a Access) authorize(w http.ResponseWriter, r *http.Request, read bool) (*http.Request, bool) {
	w.Header().Set("Cache-Control", "no-store")
	headers := r.Header.Values("Authorization")
	provided, bearer := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	authenticated := len(headers) == 1 && bearer && a.Token != "" &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(a.Token)) == 1
	// An invalid credential must never silently fall back to public access.
	if !authenticated && !(len(headers) == 0 && a.PublicRead && read) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="aarde-web"`)
		Error(w, http.StatusUnauthorized, "unauthorized", "A valid aarde web token is required")
		return r, false
	}
	return r.WithContext(context.WithValue(r.Context(), authenticatedKey{}, authenticated)), true
}

// Protect requires a token, including for unknown API paths outside the versioned router.
func (a Access) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if request, ok := a.authorize(w, r, false); ok {
			next.ServeHTTP(w, request)
		}
	})
}

func (a Access) middleware(routes chi.Router, reads map[string]bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Match separately so authentication cannot change the mounted route context.
			path := chi.RouteContext(r.Context()).RoutePath
			if path == "" {
				path = r.URL.RawPath
			}
			if path == "" {
				path = r.URL.Path
			}
			match := chi.NewRouteContext()
			read := routes.Match(match, r.Method, path) && reads[r.Method+" "+match.RoutePattern()]
			if request, ok := a.authorize(w, r, read); ok {
				next.ServeHTTP(w, request)
			}
		})
	}
}
