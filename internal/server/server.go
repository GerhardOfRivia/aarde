package server

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/GerhardOfRivia/aarde/web"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func Handler(service *catalog.Service, version string, access api.Access, basemap string, options ...raster.ViewerOptions) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(logging)
	r.Use(recoverer)
	r.Use(middleware.Timeout(25 * time.Second))
	// Same-origin only: no wildcard CORS. Vite proxies /api during development.
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Header().Set("X-Frame-Options", "DENY")
			next.ServeHTTP(w, r)
		})
	})
	r.Mount("/api/v1", api.Routes(service, version, access, basemap, options...))
	r.Handle("/api", access.Protect(http.HandlerFunc(apiNotFound)))
	r.Handle("/api/*", access.Protect(http.HandlerFunc(apiNotFound)))
	assets := web.Assets()
	registerDocs(r, assets, version, access)
	files := http.FileServer(http.FS(assets))
	r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			api.Error(w, 404, "not_found", "API route not found")
			return
		}
		if _, err := fs.Stat(assets, "index.html"); err != nil {
			http.Error(w, "Frontend not built. Run cd web && npm ci && npm run build, then rebuild the Go binary.", http.StatusServiceUnavailable)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(assets, path); err != nil {
			if strings.Contains(path, ".") {
				http.NotFound(w, r)
				return
			}
			r.URL.Path = "/"
		}
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	return r
}
func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		level := slog.LevelDebug
		if ww.Status() >= 500 {
			level = slog.LevelError
		}
		slog.Log(r.Context(), level, "HTTP request", "method", r.Method, "path", r.URL.Path, "status", ww.Status(), "duration", time.Since(start), "request_id", middleware.GetReqID(r.Context()))
	})
}
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if p := recover(); p != nil {
				slog.Error("HTTP panic", "panic", p)
				api.Error(w, 500, "internal_error", "request failed")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
func apiNotFound(w http.ResponseWriter, r *http.Request) {
	api.Error(w, http.StatusNotFound, "not_found", "API route not found")
}

func Run(ctx context.Context, address string, service *catalog.Service, version, tokenPath string, publicRead bool, basemap string, options ...raster.ViewerOptions) error {
	// Bind first: a failed second startup must not rotate a running server's token.
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	defer listener.Close()
	token, err := writeAccessToken(tokenPath)
	if err != nil {
		return err
	}
	defer removeAccessToken(tokenPath, token)
	srv := &http.Server{Addr: address, Handler: Handler(service, version, api.Access{Token: token, PublicRead: publicRead}, basemap, options...), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	slog.Info("aarde started", "address", listener.Addr().String(), "token_file", tokenPath, "public_read", publicRead)
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		slog.Info("shutdown requested")
		shutdown, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdown); err != nil {
			_ = srv.Close()
			return err
		}
		err := <-done
		slog.Info("HTTP server stopped")
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
