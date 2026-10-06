package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/go-chi/chi/v5"
)

type viewerHandler struct {
	catalog *catalog.Service
	options raster.ViewerOptions
	slots   chan struct{}
	mu      sync.Mutex
	plans   map[string]*raster.ViewerPlan
	order   []string
}

func newViewerHandler(service *catalog.Service, o raster.ViewerOptions) *viewerHandler {
	return &viewerHandler{catalog: service, options: o, slots: make(chan struct{}, o.Concurrent), plans: map[string]*raster.ViewerPlan{}}
}
func (h *viewerHandler) serve(w http.ResponseWriter, r *http.Request) {
	cat, id, layer := chi.URLParam(r, "catalogID"), chi.URLParam(r, "imageID"), chi.URLParam(r, "layerID")
	if catalog.ValidateName(cat) != nil || catalog.ValidateName(id) != nil {
		Error(w, 400, "invalid_request", "invalid catalog or image ID")
		return
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		Error(w, 400, "invalid_request", "malformed query parameters")
		return
	}
	for k, v := range values {
		if (k != "revision" && k != "resolution") || len(v) != 1 {
			Error(w, 400, "invalid_request", "only one resolution and revision are supported")
			return
		}
	}
	resolution := values.Get("resolution")
	if resolution == "" {
		resolution = "auto"
	}
	if resolution != "auto" && resolution != "preview" && resolution != "native" {
		Error(w, 400, "invalid_request", "resolution must be auto, preview or native")
		return
	}
	if layer != "" && (len(values.Get("revision")) != 64 || !strings.HasPrefix(layer, "segment-") && !strings.HasPrefix(layer, "cloud-shapes-")) {
		Error(w, 400, "invalid_request", "content requires a manifest revision and layer ID")
		return
	}
	i, err := h.catalog.Get(r.Context(), cat, id)
	if err != nil {
		failure(w, err)
		return
	}
	format := i.Format()
	if format == nil {
		Error(w, 415, "unsupported_encoding", "catalog format is unknown; re-inspect this file")
		return
	}
	revision, err := raster.ViewerRevision(i.AssetLocation, i.Checksum, h.options)
	if err != nil {
		Error(w, 404, "source_unavailable", "catalog source is unavailable or outside configured source roots")
		return
	}
	if requested := values.Get("revision"); requested != "" && requested != revision {
		Error(w, 409, "source_changed", raster.ErrViewerRevision.Error())
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		w.Header().Set("Retry-After", "2")
		Error(w, 503, "viewer_busy", "Viewer render capacity is full; retry this layer shortly")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), h.options.Timeout)
	defer cancel()
	// Authorization middleware ran before record resolution and every cache hit.
	key := cat + "\x00" + id + "\x00" + revision + "\x00" + resolution
	h.mu.Lock()
	p := h.plans[key]
	h.mu.Unlock()
	if p == nil {
		p, err = raster.BuildViewer(ctx, i.AssetLocation, i.Checksum, *format, i.Segments, i.Metadata, resolution, h.options)
		if err != nil {
			viewerFailure(w, err)
			return
		}
		if p.Manifest.Revision != revision {
			viewerFailure(w, raster.ErrViewerRevision)
			return
		}
		p.Manifest.CatalogID = cat
		p.Manifest.ImageID = id
		p.Manifest.Name = i.DisplayName
		base := "/api/v1/imagery/" + url.PathEscape(cat) + "/" + url.PathEscape(id) + "/viewer/layers/"
		for n := range p.Manifest.Layers {
			l := &p.Manifest.Layers[n]
			suffix := "/image.png"
			if l.Role == "cloud_shapes" {
				suffix = "/geometry"
			}
			l.ContentURL = base + l.ID + suffix + "?" + url.Values{"revision": {revision}, "resolution": {resolution}}.Encode()
		}
		h.mu.Lock()
		if _, exists := h.plans[key]; !exists {
			if len(h.order) >= 4 {
				delete(h.plans, h.order[0])
				h.order = h.order[1:]
			}
			h.order = append(h.order, key)
		}
		h.plans[key] = p
		h.mu.Unlock()
	}
	if layer == "" {
		JSON(w, 200, p.Manifest)
		return
	}
	var selected *raster.ViewerLayer
	for n := range p.Manifest.Layers {
		if p.Manifest.Layers[n].ID == layer {
			selected = &p.Manifest.Layers[n]
			break
		}
	}
	if selected == nil || (strings.HasSuffix(r.URL.Path, "/geometry") != (selected.Role == "cloud_shapes")) {
		Error(w, 404, "not_found", "viewer layer not found")
		return
	}
	if selected.Unsupported != "" {
		Error(w, 415, "unsupported_encoding", selected.Unsupported)
		return
	}
	data, kind, err := p.Render(ctx, layer)
	if err != nil {
		viewerFailure(w, err)
		return
	}
	if current, e := raster.ViewerRevision(i.AssetLocation, i.Checksum, h.options); e != nil || current != revision {
		viewerFailure(w, raster.ErrViewerRevision)
		return
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Aarde-Source-Revision", revision)
	if _, err = w.Write(data); err != nil {
		slog.Debug("viewer response disconnected", "error", err)
	}
}
func viewerFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, raster.ErrViewerRevision):
		Error(w, 409, "source_changed", err.Error())
	case errors.Is(err, raster.ErrViewerLimit):
		Error(w, 422, "resolution_limit", err.Error())
	case errors.Is(err, context.DeadlineExceeded):
		Error(w, 504, "viewer_timeout", "Whole-image rendering exceeded the request time limit")
	case errors.Is(err, context.Canceled):
		Error(w, 408, "request_cancelled", "Viewer request cancelled")
	default:
		slog.Warn("viewer rendering failed", "error", err)
		Error(w, 415, "unsupported_encoding", "Layer decoding failed; check the source encoding and installed GDAL codecs")
	}
}
