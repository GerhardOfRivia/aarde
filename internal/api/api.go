package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/geo"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/go-chi/chi/v5"
)

const MaxBodyBytes = 1 << 20

type Handler struct{ catalog *catalog.Service }

// Explicit DTOs keep wire representations out of the catalog model.
type ImageryResponse struct {
	Segments      []raster.Segment `json:"segments,omitempty"`
	Format        *string          `json:"format"`
	ID            string           `json:"id"`
	CatalogID     string           `json:"catalog_id"`
	ImageID       string           `json:"image_id"`
	DisplayName   string           `json:"display_name"`
	AcquiredAt    *time.Time       `json:"acquired_at"`
	CloudCover    *float64         `json:"cloud_cover"`
	ImportedAt    time.Time        `json:"imported_at"`
	CreatedAt     time.Time        `json:"created_at"`
	Footprint     geo.Geometry     `json:"footprint"`
	Checksum      string           `json:"checksum"`
	AssetLocation string           `json:"asset_location"`
	Width         int              `json:"width"`
	Height        int              `json:"height"`
	BandCount     int              `json:"band_count"`
	SourceCRS     string           `json:"source_crs"`
	Metadata      json.RawMessage  `json:"metadata"`
}
type SearchResponse struct {
	Items   []ImageryResponse `json:"items"`
	Limit   int               `json:"limit"`
	Offset  int               `json:"offset"`
	HasMore bool              `json:"has_more"`
}

func Response(i catalog.Imagery) ImageryResponse {
	return ImageryResponse{Format: i.Format(), ID: i.ID.String(), CatalogID: i.CatalogID, ImageID: i.ImageID, DisplayName: i.DisplayName, AcquiredAt: i.AcquiredAt, CloudCover: i.CloudCover, ImportedAt: i.ImportedAt, CreatedAt: i.CreatedAt, Footprint: i.Footprint, Checksum: i.Checksum, AssetLocation: i.AssetLocation, Width: i.Width, Height: i.Height, BandCount: i.BandCount, SourceCRS: i.SourceCRS, Metadata: i.Metadata, Segments: i.Segments}
}
func PageResponse(p catalog.Page) SearchResponse {
	out := SearchResponse{Items: []ImageryResponse{}, Limit: p.Limit, Offset: p.Offset, HasMore: p.HasMore}
	for _, i := range p.Items {
		out.Items = append(out.Items, Response(i))
	}
	return out
}

type VersionResponse struct {
	Version string `json:"version"`
}

func Routes(service *catalog.Service, version string, access Access, basemap string) http.Handler {
	router, _ := routes(service, version, access, basemap)
	return router
}

// Return the same read allowlist used by authentication to render the API docs.
func routes(service *catalog.Service, version string, access Access, basemap string) (chi.Router, map[string]bool) {
	h := Handler{catalog: service}
	r := chi.NewRouter()
	reads := make(map[string]bool)
	r.Use(access.middleware(r, reads))
	read := func(method, path string, handler http.HandlerFunc) {
		r.MethodFunc(method, path, handler)
		reads[method+" "+path] = true
		if method == http.MethodGet {
			r.MethodFunc(http.MethodHead, path, handler)
			reads[http.MethodHead+" "+path] = true
		}
	}
	read(http.MethodGet, "/info", func(w http.ResponseWriter, r *http.Request) {
		authenticated, _ := r.Context().Value(authenticatedKey{}).(bool)
		JSON(w, http.StatusOK, InfoResponse{Version: version, PublicRead: access.PublicRead, Authenticated: authenticated, ReadOnly: true, Basemap: basemap})
	})
	read(http.MethodGet, "/version", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		JSON(w, http.StatusOK, VersionResponse{Version: version})
	})
	read(http.MethodGet, "/health", h.health)
	read(http.MethodGet, "/catalogs", h.catalogs)
	read(http.MethodGet, "/imagery", h.list)
	// The POST body carries search geometry; this endpoint never mutates the catalog.
	read(http.MethodPost, "/imagery/search", h.search)
	read(http.MethodGet, "/imagery/{catalogID}/{imageID}", h.get)
	r.NotFound(func(w http.ResponseWriter, r *http.Request) { Error(w, 404, "not_found", "API route not found") })
	r.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		Error(w, 405, "method_not_allowed", "method not allowed")
	})
	return r, reads
}
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Debug("response write failed", "error", err)
	}
}
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}{Error: struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{code, message}})
}
func failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, catalog.ErrNotFound):
		Error(w, 404, "not_found", "imagery not found")
	case errors.Is(err, catalog.ErrInvalidGeometry):
		Error(w, 400, "invalid_geometry", err.Error())
	default:
		slog.Error("catalog request failed", "error", err)
		Error(w, 500, "internal_error", "catalog request failed")
	}
}
func (h Handler) health(w http.ResponseWriter, r *http.Request) {
	if err := h.catalog.Ping(r.Context()); err != nil {
		slog.Error("database health check failed", "error", err)
		Error(w, 503, "unavailable", "database unavailable")
		return
	}
	JSON(w, 200, struct {
		Status string `json:"status"`
	}{"ok"})
}
func (h Handler) catalogs(w http.ResponseWriter, r *http.Request) {
	items, err := h.catalog.Catalogs(r.Context())
	if err != nil {
		failure(w, err)
		return
	}
	JSON(w, 200, struct {
		Items []string `json:"items"`
	}{items})
}
func (h Handler) get(w http.ResponseWriter, r *http.Request) {
	cat, id := chi.URLParam(r, "catalogID"), chi.URLParam(r, "imageID")
	if catalog.ValidateName(cat) != nil || catalog.ValidateName(id) != nil {
		Error(w, 400, "invalid_request", "invalid catalog or image ID")
		return
	}
	i, err := h.catalog.Get(r.Context(), cat, id)
	if err != nil {
		failure(w, err)
		return
	}
	JSON(w, 200, Response(i))
}
func (h Handler) list(w http.ResponseWriter, r *http.Request) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		Error(w, 400, "invalid_request", "malformed query parameters")
		return
	}
	q := catalog.Query{CatalogID: values.Get("catalog_id"), ImageIDs: values["image_id"]}
	for key, target := range map[string]**float64{"cloud_cover_lt": &q.CloudCoverLT, "cloud_cover_lte": &q.CloudCoverLTE} {
		if values.Has(key) {
			value, err := strconv.ParseFloat(values.Get(key), 64)
			if err != nil {
				Error(w, 400, "invalid_request", key+" must be a finite number between 0 and 100")
				return
			}
			*target = &value
		}
	}
	optional := func(key string) *string {
		if !values.Has(key) {
			return nil
		}
		value := values.Get(key)
		return &value
	}
	if err := searchMetadata(&q, optional("acquired_from"), optional("acquired_before"), optional("cloud_cover_unknown")); err != nil {
		Error(w, 400, "invalid_request", err.Error())
		return
	}
	for key, target := range map[string]*int{"limit": &q.Limit, "offset": &q.Offset} {
		if values.Has(key) {
			n, err := strconv.Atoi(values.Get(key))
			if err != nil || (key == "limit" && n == 0) {
				Error(w, 400, "invalid_request", "invalid "+key)
				return
			}
			*target = n
		}
	}
	h.execute(w, r, q)
}
func (h Handler) search(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CatalogID         string          `json:"catalog_id"`
		ImageIDs          []string        `json:"image_ids"`
		AcquiredFrom      *string         `json:"acquired_from"`
		AcquiredBefore    *string         `json:"acquired_before"`
		CloudCoverUnknown *string         `json:"cloud_cover_unknown"`
		CloudCoverLTE     *float64        `json:"cloud_cover_lte"`
		Geometry          json.RawMessage `json:"geometry"`
		CloudCoverLT      *float64        `json:"cloud_cover_lt"`
		Limit             *int            `json:"limit"`
		Offset            int             `json:"offset"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	if err == nil {
		var extra any
		if next := dec.Decode(&extra); next != io.EOF {
			if next == nil {
				err = errors.New("multiple JSON values")
			} else {
				err = next
			}
		}
	}
	if err != nil {
		var large *http.MaxBytesError
		var field *json.UnmarshalTypeError
		if errors.As(err, &large) {
			Error(w, 413, "body_too_large", "request body exceeds 1 MiB")
		} else if errors.As(err, &field) && (field.Field == "cloud_cover_lt" || field.Field == "cloud_cover_lte") {
			Error(w, 400, "invalid_request", field.Field+" must be a finite number between 0 and 100 or null")
		} else if errors.As(err, &field) {
			Error(w, 400, "invalid_request", "invalid "+field.Field)
		} else {
			Error(w, 400, "invalid_json", "expected one JSON search object with known fields")
		}
		return
	}
	g, err := geo.Parse(body.Geometry)
	if err != nil {
		Error(w, 400, "invalid_geometry", err.Error())
		return
	}
	q := catalog.Query{CatalogID: body.CatalogID, ImageIDs: body.ImageIDs, Geometry: &g, CloudCoverLT: body.CloudCoverLT, CloudCoverLTE: body.CloudCoverLTE, Offset: body.Offset}
	if err := searchMetadata(&q, body.AcquiredFrom, body.AcquiredBefore, body.CloudCoverUnknown); err != nil {
		Error(w, 400, "invalid_request", err.Error())
		return
	}
	if body.Limit != nil {
		q.Limit = *body.Limit
		if q.Limit == 0 {
			Error(w, 400, "invalid_request", "limit must be 1-200")
			return
		}
	}
	h.execute(w, r, q)
}
func (h Handler) execute(w http.ResponseWriter, r *http.Request, q catalog.Query) {
	if err := catalog.NormalizeQuery(&q); err != nil {
		code := "invalid_request"
		if errors.Is(err, catalog.ErrInvalidGeometry) {
			code = "invalid_geometry"
		}
		Error(w, 400, code, err.Error())
		return
	}
	p, err := h.catalog.Search(r.Context(), q)
	if err != nil {
		failure(w, err)
		return
	}
	JSON(w, 200, PageResponse(p))
}

// Shared metadata parsing keeps GET and spatial POST semantics identical.
func searchMetadata(q *catalog.Query, from, before, unknown *string) error {
	for _, bound := range []struct {
		key    string
		raw    *string
		target **time.Time
	}{
		{"acquired_from", from, &q.AcquiredFrom}, {"acquired_before", before, &q.AcquiredBefore},
	} {
		if bound.raw == nil {
			continue
		}
		value, err := time.Parse(time.RFC3339Nano, *bound.raw)
		if err != nil {
			return fmt.Errorf("%s must be an RFC3339 timestamp", bound.key)
		}
		*bound.target = &value
	}
	if unknown != nil {
		if *unknown == "" {
			return errors.New("cloud_cover_unknown must be exclude, include, or only")
		}
		q.CloudCoverUnknown = *unknown
	}
	return nil
}
