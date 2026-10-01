import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Chip,
  CircularProgress,
} from "@mui/material";
import { AccessGate, ThemeControl } from "./Access";
import type { Session } from "./Access";
import { MapCanvas } from "./MapCanvas";
import { GeoJSONInput } from "./GeoJSONInput";
import type { MapHandle } from "./MapCanvas";
import { APIError, request, search } from "./types";
import type { Area, Basemap, Imagery, Page } from "./types";
import { CatalogFilters } from "./CatalogFilters";
import { appliedFilters, buildSearch, defaultFilters, filterKey, removeFilter } from "./filters";
import type { FilterDraft, FilterErrors } from "./filters";
import { parseSearchArea } from "./geojson";

const basemapKey = "aarde.web.basemap";
function storedBasemap(): Basemap {
  try {
    const saved = localStorage.getItem(basemapKey);
    if (saved === "none" || saved === "offline") return saved;
  } catch {
    // Basemap switching still works when browser storage is unavailable.
  }
  return "osm";
}

const date = (value: string | null) =>
  value ? new Date(value).toLocaleString() : "Unknown";
const empty: Page = { items: [], limit: 50, offset: 0, has_more: false };

export default function App() {
  return <AccessGate>{(session) => <CatalogApp session={session} />}</AccessGate>;
}

function CatalogApp({ session }: { session: Session }) {
  const { token, info, unauthorized } = session;
  const [basemapPreference, setBasemapPreference] = useState(storedBasemap);
  // A server configured for offline use takes precedence over saved preferences.
  const basemap = info.basemap === "osm" ? basemapPreference : info.basemap;
  const map = useRef<MapHandle>(null),
    active = useRef<AbortController | null>(null);
  const [catalogs, setCatalogs] = useState<string[]>([]);
  const [draft, setDraft] = useState<FilterDraft>(defaultFilters);
  const [applied, setApplied] = useState<FilterDraft>(defaultFilters);
  const [fieldErrors, setFieldErrors] = useState<FilterErrors>({});
  const [page, setPage] = useState<Page>(empty);
  const [selected, setSelected] = useState<Imagery | null>(null);
  const [loading, setLoading] = useState(false), [error, setError] = useState("");
  const [mode, setMode] = useState<"draw" | "edit" | "">("");
  const [searched, setSearched] = useState(false);
  const dirty = filterKey(draft) !== filterKey(applied);
  const hasArea = !!draft.geometry;
  const chips = appliedFilters(applied);
  const draftGroups = new Set(appliedFilters(draft).map((chip) => chip.group));

  function cancelSearch() {
    active.current?.abort();
    setLoading(false);
  }
  function changeDraft(next: FilterDraft | ((previous: FilterDraft) => FilterDraft)) {
    cancelSearch();
    setDraft(next);
    setFieldErrors({});
    setError("");
  }
  async function run(next: FilterDraft, offset = 0) {
    cancelSearch();
    const snapshot = structuredClone(next);
    const { criteria, errors } = buildSearch(snapshot);
    if (criteria.geometry) {
      try { criteria.geometry = parseSearchArea(JSON.stringify(criteria.geometry)); }
      catch (e) { errors.area = e instanceof Error ? e.message : "Invalid search area."; }
    }
    setFieldErrors(errors);
    if (Object.keys(errors).length) return;
    const controller = new AbortController();
    active.current = controller;
    map.current?.stop();
    setLoading(true);
    setError("");
    setMode("");
    try {
      const result = await search(token, criteria, offset, controller.signal);
      if (controller.signal.aborted || active.current !== controller) return;
      setPage(result);
      setSelected(null);
      setApplied(snapshot);
      setSearched(true);
    } catch (e) {
      if (controller.signal.aborted || active.current !== controller) return;
      if (e instanceof APIError && e.status === 401) {
        unauthorized();
        return;
      }
      setError(e instanceof Error ? e.message : "Could not reach the catalog.");
    } finally {
      if (!controller.signal.aborted && active.current === controller) setLoading(false);
    }
  }
  useEffect(() => {
    const controller = new AbortController();
    request<{ items: string[] }>("/api/v1/catalogs", token, {
      signal: controller.signal,
    })
      .then((result) => {
        if (!controller.signal.aborted) setCatalogs(result.items);
      })
      .catch((e) => {
        if (controller.signal.aborted) return;
        if (e instanceof APIError && e.status === 401) {
          unauthorized();
          return;
        }
        setError(e.message);
      });
    void run(defaultFilters);
    return () => {
      controller.abort();
      active.current?.abort();
    };
  }, [token, unauthorized]);
  useEffect(() => {
    if (selected)
      document
        .getElementById(`row-${selected.id}`)
        ?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [selected]);
  function loadArea(area: Area) {
    if (!map.current) throw new Error("The map is still loading. Try again in a moment.");
    map.current.loadArea(area);
  }
  function clearArea() {
    map.current?.clear();
    setMode("");
  }
  function saveArea() {
    const geometry = map.current?.area();
    if (!geometry) return;
    const url = URL.createObjectURL(new Blob(
      [JSON.stringify(geometry, null, 2) + "\n"],
      { type: "application/geo+json" },
    ));
    const link = document.createElement("a");
    link.href = url;
    link.download = "aarde-search-area.geojson";
    document.body.appendChild(link);
    try {
      link.click();
    } finally {
      link.remove();
      // Give the browser time to start the download before releasing its data.
      window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
    }
  }
  function select(id: string) {
    setSelected(page.items.find((item) => item.id === id) ?? null);
  }

  return (
    <div className="app">
      <header className="header">
        <a className="brand" href="/" aria-label="Aarde home">
          <span className="brand-mark">◈</span>
          <span className="brand-lockup">
            <span>aarde</span>
            <span className="build-version" aria-label="Server version">
              Version {info.version || "unavailable"}
            </span>
          </span>
          <span className="brand-caption">IMAGERY CATALOG</span>
        </a>
        <div className="header-actions">
          <Button className="access-button" size="small" href="/docs/">API docs</Button>
          <ThemeControl />
          <Chip className="access-badge" label={info.authenticated ? "Authenticated" : "Read only"} size="small" variant="outlined" />
          {info.authenticated
            ? <Button className="access-button" size="small" onClick={() => session.signOut()}>{info.public_read ? "Lock" : "Sign out"}</Button>
            : <Button className="access-button" size="small" onClick={session.signIn}>Sign in</Button>}
        </div>
      </header>
      {session.message && <Alert severity="warning" onClose={session.dismissMessage}>{session.message}</Alert>}
      <CatalogFilters draft={draft} catalogs={catalogs} errors={fieldErrors} dirty={dirty}
        loading={loading} drawing={mode === "draw"} onChange={changeDraft}
        onSearch={() => void run(draft)} onClearArea={clearArea} />
      {error && (
        <Alert severity="error" onClose={() => setError("")}>
          {error}
        </Alert>
      )}
      <main className="workspace">
        <section className="map-panel" aria-label="Spatial search">
          <div className="map-toolbar">
            <div className="drawing-tools">
              <Button
                size="small"
                variant={mode === "draw" ? "contained" : "outlined"}
                onClick={() => {
                  cancelSearch();
                  map.current?.draw();
                  setMode("draw");
                }}
              >
                + Draw Area
              </Button>
              <GeoJSONInput onLoad={loadArea} />
              <Button
                size="small"
                variant={mode === "edit" ? "contained" : "outlined"}
                disabled={!hasArea}
                onClick={() => map.current?.edit()}
              >
                Edit Area
              </Button>
              <Button
                size="small"
                variant="outlined"
                disabled={!hasArea || mode === "draw"}
                onClick={saveArea}
              >
                Save GeoJSON
              </Button>
              <Button size="small" disabled={!hasArea && mode !== "draw"} onClick={clearArea}>Clear area</Button>
              {mode && <Button size="small" onClick={() => { map.current?.stop(); setMode(""); }}>Done</Button>}
            </div>
          </div>
          <div className="map-wrap">
            <MapCanvas
              ref={map}
              basemap={basemap}
              items={page.items}
              selected={selected}
              onSelect={select}
              onArea={(geometry, editing) => {
                setMode(editing ? (geometry ? "edit" : "draw") : "");
                changeDraft((previous) => ({
                  ...previous, geometry,
                  scope: geometry ? (JSON.stringify(geometry) === JSON.stringify(previous.geometry) ? previous.scope : "area") : "anywhere",
                }));
              }}
            />
            <div className="map-hint">
              {mode === "draw"
                ? "Click to add vertices. Double-click to finish."
                : mode === "edit"
                  ? "Drag vertices to edit. Click Search catalog when ready."
                  : dirty && draft.scope === "area"
                    ? "Area ready · click Search catalog to apply your filters"
                    : hasArea && draft.scope === "anywhere"
                      ? "Polygon retained · choose Use drawn or loaded area to filter"
                      : "Draw an area or load GeoJSON to find intersecting imagery"}
            </div>
            <div className="map-legend">
              <span>
                <i className="swatch aoi" />
                Draft area
              </span>
              <span>
                <i className="swatch result" />
                Imagery
              </span>
              <span>
                <i className="swatch selected" />
                Selected
              </span>
            </div>
          </div>
          <div className="map-footer">
            <label className="basemap-control">
              <span>Basemap</span>
              <select
                aria-label="Basemap"
                value={basemap}
                disabled={info.basemap !== "osm"}
                title={info.basemap !== "osm" ? "Basemap set by server" : undefined}
                onChange={(event) => {
                  const value = event.target.value as Basemap;
                  try { localStorage.setItem(basemapKey, value); }
                  catch { /* Keep the choice for this visit when storage is unavailable. */ }
                  setBasemapPreference(value);
                }}
              >
                <option value="osm">OpenStreetMap</option>
                <option value="offline">Offline map</option>
                <option value="none">No basemap</option>
              </select>
            </label>
            <span>
              Spatial match: <strong>footprint intersects area</strong>
            </span>
            <Button
              size="small"
              disabled={!page.items.length}
              onClick={() => map.current?.fit()}
            >
              Fit results ↗
            </Button>
          </div>
        </section>
        <aside className="results-panel">
          <div className="results-heading">
            <div>
              <span className="eyebrow">CATALOG MATCHES</span>
              <h2>Results</h2>
            </div>
            {loading && <CircularProgress size={22} aria-label="Searching" />}
          </div>
          <div className="result-context">
            <span>{searched ? "Applied filters" : "Loading catalog"}</span>
            <div className="applied-filters" aria-label="Applied filters">
              {chips.length ? chips.map(({ group, label }) => {
                const pending = !draftGroups.has(group);
                return <button key={group} type="button" className={pending ? "filter-chip pending-removal" : "filter-chip"}
                  aria-label={pending ? `Removal pending: ${label}` : `Remove ${label}`}
                  aria-disabled={pending} title={pending ? "Removal pending · search to apply" : label}
                  onClick={() => { if (!pending) changeDraft(removeFilter(draft, group)); }}>
                  {label}<span aria-hidden="true">{pending ? " · pending" : " ×"}</span>
                </button>;
              }) : <span>All imagery · no filters</span>}
            </div>
            {dirty && <span className="filter-pending">Changes not applied · results show the previous search</span>}
          </div>
          <div className="results-scroll" aria-busy={loading}>
            {!page.items.length ? (
              <div className="empty-state">
                <span className="empty-icon">▱</span>
                <h3>
                  {loading
                    ? "Searching the catalog…"
                    : chips.length > 0
                      ? "No matching imagery"
                      : "Your map starts here"}
                </h3>
                <p>
                  {chips.length > 0
                    ? "Try a wider date range, another area or ID, a higher cloud limit, or include unknown cloud cover."
                    : "Import local GeoTIFFs to discover your collection on the map."}
                </p>
                {!chips.length && (
                  <code>aarde import ./data --recursive</code>
                )}
              </div>
            ) : (
              <table className="results-table">
                <thead>
                  <tr>
                    <th>Image / catalog</th>
                    <th>Acquired · UTC / clouds</th>
                  </tr>
                </thead>
                <tbody>
                  {page.items.map((item) => (
                    <tr
                      key={item.id}
                      id={`row-${item.id}`}
                      aria-selected={selected?.id === item.id}
                      className={selected?.id === item.id ? "is-selected" : ""}
                    >
                      <td>
                        <button
                          className="result-button"
                          onClick={() => setSelected(item)}
                        >
                          <strong>{item.image_id}</strong>
                          <span>{item.catalog_id}</span>
                          <small>{item.display_name}</small>
                        </button>
                      </td>
                      <td>
                        {item.acquired_at ? (
                          new Date(item.acquired_at).toLocaleDateString(undefined, { timeZone: "UTC" })
                        ) : (
                          <span className="muted">Unknown</span>
                        )}
                        <span className="result-cloud">Clouds: {item.cloud_cover == null ? "Unknown" : `${item.cloud_cover}%`}</span>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
          <div className="pagination">
            <span aria-live="polite">
              {searched && page.items.length
                ? `Showing ${page.offset + 1}–${page.offset + page.items.length}${page.has_more ? " · more available" : ""}`
                : "0 images"}
            </span>
            <div>
              <Button
                size="small"
                disabled={loading || dirty || mode === "draw" || !page.offset}
                onClick={() =>
                  void run(applied, Math.max(0, page.offset - page.limit))
                }
              >
                Previous
              </Button>
              <Button
                size="small"
                disabled={loading || dirty || mode === "draw" || !page.has_more}
                onClick={() => void run(applied, page.offset + page.limit)}
              >
                Next
              </Button>
            </div>
          </div>
        </aside>
      </main>
      <section className="metadata-panel">
        <div className="metadata-heading">
          <span className="eyebrow">IMAGERY DETAILS</span>
          <h2>
            {selected
              ? selected.image_id
              : "Select an image to inspect its metadata"}
          </h2>
        </div>
        {selected ? (
          <>
            <dl className="metadata-grid">
              <div>
                <dt>Catalog</dt>
                <dd>{selected.catalog_id}</dd>
              </div>
              <div>
                <dt>Display name</dt>
                <dd>{selected.display_name}</dd>
              </div>
              <div>
                <dt>Acquisition time</dt>
                <dd>{date(selected.acquired_at)}</dd>
              </div>
              <div>
                <dt>Cloud cover</dt>
                <dd>{selected.cloud_cover == null ? "Unknown" : `${selected.cloud_cover}%`}</dd>
              </div>
              <div>
                <dt>Imported</dt>
                <dd>{date(selected.imported_at)}</dd>
              </div>
              <div>
                <dt>Raster dimensions</dt>
                <dd>
                  {selected.width > 0 ? `${selected.width.toLocaleString()} × ${selected.height.toLocaleString()} px` : "See image segments"}
                </dd>
              </div>
              <div>
                <dt>Format</dt>
                <dd>{selected.format ?? "Unknown"}</dd>
              </div>
              <div>
                <dt>Bands</dt>
                <dd>{selected.band_count || "See image segments"}</dd>
              </div>
              <div className="wide">
                <dt>Asset location</dt>
                <dd>
                  <code>{selected.asset_location}</code>
                </dd>
              </div>
              <div className="wide">
                <dt>SHA-256</dt>
                <dd>
                  <code>{selected.checksum}</code>
                </dd>
              </div>
            </dl>
            <div className="metadata-disclosures">
              {selected.segments && selected.segments.length > 0 && (
                <details>
                  <summary>Image segments ({selected.segments.length})</summary>
                  {selected.segments.map((segment) => (
                    <details key={segment.index}>
                      <summary>
                        Segment {segment.index}: {segment.width} × {segment.height}, {segment.band_count} bands
                      </summary>
                      <pre>{JSON.stringify(segment, null, 2)}</pre>
                    </details>
                  ))}
                </details>
              )}
              <details>
                <summary>Source CRS</summary>
                <pre>{selected.source_crs || "See image segments"}</pre>
              </details>
              <details>
                <summary>Additional metadata</summary>
                <pre>{JSON.stringify(selected.metadata, null, 2)}</pre>
              </details>
            </div>
          </>
        ) : (
          <p className="metadata-empty">
            Choose a result or click a footprint on the map. Source rasters stay
            on your filesystem.
          </p>
        )}
      </section>
      <footer className="app-footer">
        <span>AARDE / OPEN SOURCE</span>
      </footer>
    </div>
  );
}
