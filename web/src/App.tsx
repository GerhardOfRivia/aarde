import { useEffect, useRef, useState } from "react";
import {
  Alert,
  Button,
  Chip,
  CircularProgress,
  MenuItem,
  TextField,
} from "@mui/material";
import { AccessGate, ThemeControl } from "./Access";
import type { Session } from "./Access";
import { MapCanvas } from "./MapCanvas";
import { GeoJSONInput } from "./GeoJSONInput";
import type { MapHandle } from "./MapCanvas";
import { APIError, request, search } from "./types";
import type { Area, Basemap, Imagery, Page, Search } from "./types";

const basemapKey = "aarde.web.basemap";
function storedBasemap(): Basemap {
  try {
    if (localStorage.getItem(basemapKey) === "none") return "none";
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
  const basemap = info.basemap === "none" ? "none" : basemapPreference;
  const map = useRef<MapHandle>(null),
    active = useRef<AbortController | null>(null);
  const [catalogs, setCatalogs] = useState<string[]>([]),
    [catalog, setCatalog] = useState("");
  const [ids, setIDs] = useState(""),
    [page, setPage] = useState<Page>(empty);
  const [selected, setSelected] = useState<Imagery | null>(null),
    [criteria, setCriteria] = useState<Search>({ catalog: "" });
  const [loading, setLoading] = useState(false),
    [error, setError] = useState("");
  const [hasArea, setHasArea] = useState(false),
    [mode, setMode] = useState<"draw" | "edit" | "">("");
  const [areaChanged, setAreaChanged] = useState(false),
    [searched, setSearched] = useState(false);

  async function run(next: Search, offset = 0) {
    active.current?.abort();
    const controller = new AbortController();
    active.current = controller;
    map.current?.stop();
    setLoading(true);
    setError("");
    setMode("");
    try {
      const result = await search(token, next, offset, controller.signal);
      if (controller.signal.aborted) return;
      setPage(result);
      setSelected(null);
      setCriteria(next);
      setSearched(true);
      setAreaChanged(false);
    } catch (e) {
      if (controller.signal.aborted) return;
      if (e instanceof APIError && e.status === 401) {
        unauthorized();
        return;
      }
      setError(e instanceof Error ? e.message : "Could not reach the catalog.");
    } finally {
      if (!controller.signal.aborted) setLoading(false);
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
    void run({ catalog: "" });
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
    // A pending search must not refit the map or mark this new area as searched.
    active.current?.abort();
    setLoading(false);
    setSelected(null);
    setError("");
  }
  function areaSearch() {
    const geometry = map.current?.area();
    if (geometry) void run({ catalog, geometry });
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
        <span className="header-note">Local imagery. Spatial discovery.</span>
        <div className="header-actions">
          <Button className="access-button" size="small" href="/docs/">API docs</Button>
          <ThemeControl />
          <Chip className="access-badge" label={info.authenticated ? "Authenticated · Read only" : "Read only"} size="small" variant="outlined" />
          {info.authenticated
            ? <Button className="access-button" size="small" onClick={() => session.signOut()}>{info.public_read ? "Lock" : "Sign out"}</Button>
            : <Button className="access-button" size="small" onClick={session.signIn}>Sign in</Button>}
        </div>
      </header>
      {session.message && <Alert severity="warning" onClose={session.dismissMessage}>{session.message}</Alert>}
      <div className="search-bar">
        <div className="workspace-title">
          <span className="eyebrow">YOUR COLLECTION</span>
          <h1>Explore imagery</h1>
        </div>
        <form
          className="id-search"
          onSubmit={(event) => {
            event.preventDefault();
            const imageIDs = ids
              .split(/[\n,]+/)
              .map((id) => id.trim())
              .filter(Boolean);
            if (imageIDs.length) void run({ catalog, ids: imageIDs });
          }}
        >
          <TextField
            select
            label="Catalog"
            size="small"
            value={catalog}
            onChange={(e) => setCatalog(e.target.value)}
            className="catalog-input"
          >
            <MenuItem value="">All catalogs</MenuItem>
            {catalogs.map((c) => (
              <MenuItem key={c} value={c}>
                {c}
              </MenuItem>
            ))}
          </TextField>
          <TextField
            label="Exact image ID"
            placeholder="ABC123, IMG002"
            size="small"
            value={ids}
            onChange={(e) => setIDs(e.target.value)}
            className="id-input"
            helperText="Separate multiple IDs with commas"
          />
          <Button
            type="submit"
            variant="contained"
            disabled={loading || !ids.trim()}
          >
            Search ID
          </Button>
          <Button onClick={() => void run({ catalog })} disabled={loading}>
            Browse all
          </Button>
        </form>
      </div>
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
                  map.current?.draw();
                  setMode("draw");
                }}
              >
                ＋ Draw Area
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
                onClick={() => {
                  map.current?.clear();
                  setMode("");
                  setAreaChanged(true);
                }}
              >
                Clear
              </Button>
            </div>
            <Button
              size="small"
              variant="contained"
              disabled={!hasArea || loading || mode === "draw"}
              onClick={areaSearch}
            >
              Search This Area
            </Button>
          </div>
          <div className="map-wrap">
            <MapCanvas
              ref={map}
              basemap={basemap}
              items={page.items}
              selected={selected}
              onSelect={select}
              onArea={(exists, editing) => {
                setHasArea(exists);
                setMode(editing ? (exists ? "edit" : "draw") : "");
                setAreaChanged(true);
              }}
            />
            <div className="map-hint">
              {mode === "draw"
                ? "Click to add vertices. Double-click to finish."
                : mode === "edit"
                  ? "Drag vertices to edit. Click Search This Area when ready."
                  : areaChanged && hasArea
                    ? "Area changed · click Search This Area to update results"
                    : "Draw an area or paste GeoJSON to find intersecting imagery"}
            </div>
            <div className="map-legend">
              <span>
                <i className="swatch aoi" />
                Search area
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
                disabled={info.basemap === "none"}
                title={info.basemap === "none" ? "Basemap disabled by server" : undefined}
                onChange={(event) => {
                  const value = event.target.value as Basemap;
                  try { localStorage.setItem(basemapKey, value); }
                  catch { /* Keep the choice for this visit when storage is unavailable. */ }
                  setBasemapPreference(value);
                }}
              >
                <option value="osm">OpenStreetMap</option>
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
              <h2>
                Results{" "}
                <span className="count">
                  {page.items.length}
                  {page.has_more ? "+" : ""}
                </span>
              </h2>
            </div>
            {loading && <CircularProgress size={22} aria-label="Searching" />}
          </div>
          <div className="result-context">
            {criteria.geometry
              ? "Intersecting the searched area"
              : criteria.ids
                ? "Exact ID matches"
                : "Browsing imagery"}
            {criteria.catalog ? ` · ${criteria.catalog}` : " · All catalogs"}
          </div>
          <div className="results-scroll" aria-busy={loading}>
            {!page.items.length ? (
              <div className="empty-state">
                <span className="empty-icon">▱</span>
                <h3>
                  {loading
                    ? "Searching the catalog…"
                    : criteria.geometry || criteria.ids
                      ? "No matching imagery"
                      : "Your map starts here"}
                </h3>
                <p>
                  {criteria.geometry || criteria.ids
                    ? "Try another area, image ID, or catalog."
                    : "Import local GeoTIFFs to discover your collection on the map."}
                </p>
                {!criteria.geometry && !criteria.ids && (
                  <code>aarde import ./data --recursive</code>
                )}
              </div>
            ) : (
              <table className="results-table">
                <thead>
                  <tr>
                    <th>Image / catalog</th>
                    <th>Acquired</th>
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
                          new Date(item.acquired_at).toLocaleDateString()
                        ) : (
                          <span className="muted">Unknown</span>
                        )}
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
                ? `${page.offset + 1}–${page.offset + page.items.length}${page.has_more ? " · more available" : ""}`
                : "0 images"}
            </span>
            <div>
              <Button
                size="small"
                disabled={loading || !page.offset}
                onClick={() =>
                  void run(criteria, Math.max(0, page.offset - page.limit))
                }
              >
                Previous
              </Button>
              <Button
                size="small"
                disabled={loading || !page.has_more}
                onClick={() => void run(criteria, page.offset + page.limit)}
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
                <dt>Imported</dt>
                <dd>{date(selected.imported_at)}</dd>
              </div>
              <div>
                <dt>Raster dimensions</dt>
                <dd>
                  {selected.width.toLocaleString()} ×{" "}
                  {selected.height.toLocaleString()} px
                </dd>
              </div>
              <div>
                <dt>Bands</dt>
                <dd>{selected.band_count}</dd>
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
              <details>
                <summary>Source CRS</summary>
                <pre>{selected.source_crs}</pre>
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
