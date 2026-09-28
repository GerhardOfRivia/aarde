# Aarde

Aarde is an open-source geospatial imagery catalog.
Import local imagery from the CLI, catalog it in PostGIS,
and discover your collection spatially through a web map.

One Go application provides the CLI, HTTP API, and embedded React/OpenLayers UI. GeoTIFFs stay where they are; the catalog stores metadata, SHA-256 checksums, local asset paths, and WGS84 footprints. Licensed under [MIT](LICENSE).

![screenshot](screenshot.png)

## Five-minute workflow

Prerequisites: Git and Docker with Docker Compose v2. The first build downloads Go, Node, GDAL, and PostGIS images/dependencies and may take longer on a slow connection.

```sh
git clone https://github.com/GerhardOfRivia/aarde.git
cd aarde
# Put georeferenced .tif / .tiff files into ./data.
docker compose up -d --build

docker compose exec aarde aarde import /data --recursive
# Read the private access token, then paste it at http://localhost:8080.
docker compose exec -T aarde cat /home/aarde/.local/state/aarde/web.token
```

Select a catalog, click **Draw Area**, click vertices on the map, and double-click to finish. Click **Search This Area** to find images whose footprints intersect that polygon. Select a row or a footprint to inspect metadata. **Edit Area** changes vertices; searches run only when explicitly requested. **Clear** removes the drawing and leaves the last results visible. Use **Browse all** to reset the search. Exact image IDs can be entered individually or comma-separated.

To use an existing boundary, choose **Paste GeoJSON**, paste a WGS84 Polygon or MultiPolygon, and click **Load Area**. A polygon Feature or a FeatureCollection containing only polygon Features also works; multiple features become one MultiPolygon search area. The map fits the loaded area, which can be adjusted with **Edit Area**. Click **Search This Area** to search the selected catalog. Loading replaces the current area and keeps existing results until you search. Invalid JSON, unsupported geometries, open rings, out-of-range coordinates, and oversized areas show an error without replacing the existing area. PostGIS checks polygon topology when the search runs.

No imagery yet? Generate a tiny synthetic GeoTIFF for trying the workflow:

```sh
docker compose exec aarde gdal_create -of GTiff -outsize 32 32 \
  -bands 1 -burn 42 -a_srs EPSG:4326 \
  -a_ullr -106.12 39.55 -105.95 39.40 /tmp/example.tif
docker compose exec aarde aarde import /tmp/example.tif
```

This demo file lives in the application container and disappears when it is replaced. Real imagery belongs in the persistent `./data` bind mount or another stable local mount.

The header includes the running server's build version, matching `aarde version`. Use **Theme → Auto / Light / Night** to follow your system preference or choose a mode; the choice is remembered in your browser. Night uses Onderzeeer's charcoal-green palette and lime accents, with a dimmed basemap and distinct imagery/AOI colors.

## Architecture

```mermaid
flowchart TD
    Files[Local GeoTIFFs] --> CLI[aarde import / inspect]
    CLI --> GDAL[GDAL JSON inspection + WGS84 footprint]
    GDAL --> Catalog[Shared Go catalog service]
    SearchCLI[aarde search] --> Catalog
    UI[React + OpenLayers + Material UI] --> HTTP[Go net/http + chi API]
    HTTP --> Catalog
    Catalog --> DB[(PostgreSQL + PostGIS)]
    HTTP --> UI
```

`internal/raster` invokes GDAL; `internal/importer` discovers files and runs imports; `internal/geo` validates bounded GeoJSON structure; `internal/catalog` owns models and catalog operations; `internal/database` owns parameterized SQL and migrations; `internal/api` owns validation and response DTOs; `internal/server` owns middleware, embedded assets, and shutdown; `internal/config` reads environment settings. Both CLI and HTTP call the same catalog service.

## Docker and configuration

Compose starts exactly two services: `aarde` and `postgres`. Postgres is not exposed on the host. The web port binds to `127.0.0.1:8080`. Imagery is mounted read-only at `/data`; the application runs as UID 10001. Give that user read access to imagery and traversal access to its directories.

```sh
cp .env.example .env
docker compose up -d --build
docker compose logs -f aarde
docker compose exec aarde aarde version
docker compose down             # keeps the catalog volume
```

`.env` is read by Compose. The Go binary reads its process environment, so export variables when running locally. Compose supplies its own internal database URL and listen address.

| Variable | Default / purpose |
| --- | --- |
| `AARDE_DATABASE_URL` | Required for server, import, and CLI search; PostgreSQL URL |
| `AARDE_LISTEN_ADDRESS` | `:8080` |
| `AARDE_LOG_LEVEL` | `info`; also `debug`, `warn`, `error` |
| `AARDE_WEB_PUBLIC_READ` | `false`; allow anonymous catalog browsing and search |
| `AARDE_WEB_BASEMAP` | `osm`; set `none` to disable external basemap tiles for all browsers |
| `AARDE_WEB_TOKEN_PATH` | Optional private token file; see below |
| `POSTGRES_DB`, `POSTGRES_USER` | Compose only; both default to `aarde` |
| `POSTGRES_PASSWORD` | Compose only; `aarde-local` for local development |

Change the example database password for shared deployments. URL-encode special characters in connection URLs. The web API requires the startup bearer token by default. Use HTTPS at a reverse proxy or a trusted local connection to protect it in transit. Multi-user identity management belongs at the proxy. CORS is intentionally same-origin; development uses Vite's API proxy.

The OpenStreetMap basemap requires internet access and sends tile requests to OpenStreetMap. Choose **Basemap → No basemap** below the map to remove the tile source and stop new tile requests. The choice is remembered in this browser and applied before the map loads on later visits. Requests already sent before switching may finish. Footprints, selection, drawing/editing, pasted GeoJSON, and catalog searches still work against a plain background; switching basemaps preserves the current view and search area.

For a network without internet, set `AARDE_WEB_BASEMAP=none` in Compose's `.env` and recreate the application with `docker compose up -d aarde` (rebuild first when installing this change). For a native deployment, set the variable in the server process environment before starting `aarde serve`. This forces **No basemap** for every browser, including first visits and browsers with a saved OpenStreetMap preference; the basemap selector is disabled. No external tile requests are made in this mode. Prepare the application/database images and dependencies before moving to the offline network.

Migrations run automatically on `serve` and non-dry `import`, inside a transaction protected by an advisory lock. The database role needs migration privileges, including permission to enable PostGIS if it has not already been enabled. Production operators can provision the extension beforehand. `search`, `inspect`, and dry run never run migrations.

## Web access and read-only viewing

Each `aarde serve` startup generates a fresh random 256-bit bearer token. The startup log reports `token_file` and `public_read`, never the token itself. The token is atomically written with mode `0600` inside a private directory (`0700`), rotated on restart, and removed on clean shutdown. Aarde creates missing token directories and rejects an existing directory that grants group or other access. Use a separate token path for each server instance.

The native default is `$XDG_RUNTIME_DIR/aarde/web.token`, falling back to `$XDG_STATE_HOME/aarde/web.token`, then `$HOME/.local/state/aarde/web.token`. `AARDE_WEB_TOKEN_PATH` overrides it. Docker uses `/home/aarde/.local/state/aarde/web.token`; read it with:

```sh
docker compose exec -T aarde cat /home/aarde/.local/state/aarde/web.token
```

Paste the token into the login screen. It is sent as an `Authorization: Bearer …` header and remembered in this browser tab's session storage. **Sign out** clears it and removes the catalog view. Restarting the server invalidates previous tokens; the next rejected request clears the browser session and asks for the current token.

To enable public viewing, set `AARDE_WEB_PUBLIC_READ=true` in Compose's `.env` and run `docker compose up -d aarde`, or export it before running `aarde serve`. Unset or empty means false; boolean values such as `true`/`false` and `1`/`0` are accepted. Invalid values prevent startup.

Public viewers see **Read only** and can browse catalogs, metadata, local asset paths, and footprints, search exact IDs, draw/edit search areas, and run polygon searches. `POST /api/v1/imagery/search` is explicitly allowed because it only reads the catalog. Unknown API routes and other methods still require a token. An explicitly supplied invalid token is always rejected, even with public reads enabled.

**Sign in** accepts the current token; **Lock** clears it and returns to public viewing. An expired or rejected token also returns to public viewing when enabled. Aarde's entire web API is currently read-only, including authenticated sessions. Imports remain local CLI operations with filesystem/database access; signing in does not add web editing or import controls. API responses use `Cache-Control: no-store`, while the login page and frontend assets remain accessible without a token.

## Upgrading an existing Ruimte catalog

Use `aarde` instead of `ruimte`, and rename `RUIMTE_*` environment variables to `AARDE_*`. The database URL can keep its existing database name and role. On startup, Aarde automatically renames the old migration-history table and preserves the imagery records.

New Compose installations use the `aarde` project name. To reuse an existing Compose catalog, keep its original project name (which determines the volume name) and PostgreSQL credentials. For an installation created with the original defaults, set `POSTGRES_DB=ruimte`, `POSTGRES_USER=ruimte`, and the original `POSTGRES_PASSWORD` in `.env`, then run:

```sh
docker compose -p ruimte up -d --build --remove-orphans
```

Replace `ruimte` in that command with the original Compose project name if it differed. This reuses the existing catalog volume; PostgreSQL does not rename databases or roles when its initialization environment changes. Do not remove that volume during the upgrade.

## CLI

```sh
aarde serve
aarde version
aarde inspect image.tif

aarde import image.tif
aarde import ./imagery
aarde import ./imagery --recursive
aarde import ./imagery --recursive --catalog breckenridge
aarde import ./imagery --recursive --dry-run

aarde search --id ABC123
aarde search --id ABC123,IMG002 --catalog breckenridge
aarde search --id ABC123 --limit 50 --offset 0
```

Flags may precede or follow the import path. `inspect` prints JSON with format, source CRS (WKT), source corners, width, height, band count, acquisition time, calculated EPSG:4326 footprint, SHA-256 checksum, asset path, and metadata. It does not need a database.

Imports catalog existing files without copying or changing them. Absolute paths identify assets; within Docker those are container paths, so preserve the mount layout when moving a deployment. Raster bytes are never put into PostgreSQL. `asset_location` is independent of imagery identity so managed storage can be added later. No `--copy` option is implemented.

Only `.tif` and `.tiff` are discovered, case-insensitively. Directories are scanned one level by default; `--recursive` includes descendants. Symlinks are skipped. Unsupported files appear at debug log level and contribute to `Skipped`. A corrupt GeoTIFF is reported, processing continues, and the command exits nonzero after its summary. A directory traversal failure or cancellation aborts discovery/processing with an error.

```text
Imported: 24
Existing: 3
Failed: 1
Skipped: 5
```

The initial image ID is the filename without its extension, derived in `importer.ImageID`. Catalog defaults to `default`. Names must fit 255 bytes and cannot include slashes, control characters, or surrounding whitespace.

Within a catalog, the same checksum reports **already imported** and keeps the original record and asset path. An existing image ID with different bytes is a conflict, never an overwrite. The same bytes may be cataloged independently in another catalog. SHA-256 covers raster bytes, not external sidecar metadata. Concurrent duplicate imports are also protected by database constraints.

Dry run discovers, inspects, checksums, validates, derives IDs, and reports **would import**, with a separate `Would import` count. With `AARDE_DATABASE_URL`, it checks stored duplicates and PostGIS topology using read-only queries. The schema must already exist. With no URL, it works offline and clearly reports that database duplicates and topology were not checked; in-batch duplicates are still detected. It never migrates or writes to the database.

Acquisition time is separate from import/creation time. The inspector recognizes timezone-qualified RFC3339 values under `ACQUISITION_DATETIME`, `ACQUISITION_TIME`, `SENSING_TIME`, and `TIFFTAG_DATETIME_ORIGINAL` (case-insensitive) in GDAL metadata domains. Missing or ambiguous times remain `NULL`. Generic `TIFFTAG_DATETIME`, filesystem modification time, and import time are not used as acquisition time.

## HTTP API

Routes are under `/api/v1`. All reads require the bearer token unless public reads are enabled; GET routes also support HEAD. Footprints are GeoJSON MultiPolygons in EPSG:4326, with longitude before latitude. Dates use RFC3339. ID matching is exact and case-sensitive.

| Method | Route | Behavior |
| --- | --- | --- |
| GET | `/info` | Build version, `public_read`, `authenticated`, and `read_only` access state |
| GET | `/version` | Running binary build version, independent of database health |
| GET | `/health` | Database readiness; 503 when unavailable |
| GET | `/catalogs` | `{ "items": ["default"] }`; catalogs with imagery |
| GET | `/imagery/{catalogID}/{imageID}` | Exact record; 404 if absent |
| GET | `/imagery` | Filter by `catalog_id`, repeated `image_id`, `limit`, `offset` |
| POST | `/imagery/search` | Polygon or MultiPolygon intersection search |

```sh
# Docker; for a native server, read the token_file path from its startup log.
AARDE_TOKEN=$(docker compose exec -T aarde cat /home/aarde/.local/state/aarde/web.token)
curl -H "Authorization: Bearer $AARDE_TOKEN" http://localhost:8080/api/v1/health
curl -H "Authorization: Bearer $AARDE_TOKEN" http://localhost:8080/api/v1/catalogs
curl -H "Authorization: Bearer $AARDE_TOKEN" http://localhost:8080/api/v1/imagery/default/ABC123
curl -H "Authorization: Bearer $AARDE_TOKEN" 'http://localhost:8080/api/v1/imagery?catalog_id=default&image_id=ABC123&image_id=IMG002&limit=50&offset=0'

curl -X POST http://localhost:8080/api/v1/imagery/search \
  -H "Authorization: Bearer $AARDE_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"catalog_id":"default","geometry":{"type":"Polygon","coordinates":[[[-106.2,39.3],[-105.8,39.3],[-105.8,39.7],[-106.2,39.7],[-106.2,39.3]]]},"limit":100,"offset":0}'
```

Omit `catalog_id` to search all catalogs. List/search responses are `{ "items": [...], "limit": 50, "offset": 0, "has_more": false }`. Pagination is ordered by import time descending, then UUID; concurrent imports can shift offset-based pages. Limits default to 50 and are capped at 200. At most 100 exact IDs, 10,000 geometry positions, a 1 MiB request body, and offset 1,000,000 are accepted. Unknown search body fields, extra JSON values, invalid rings, out-of-range coordinates, and unsupported geometry types are rejected. PostGIS validates polygon topology, including self-intersections.

See the [GDAL JSON inspection documentation](https://gdal.org/en/stable/programs/gdalinfo.html) and [PostGIS ST_Intersects reference](https://postgis.net/docs/ST_Intersects.html).

Spatial filtering uses indexed `ST_Intersects(footprint, search_geometry)` inside PostGIS. Partial overlap and boundary contact count as matches. Polygon holes are respected. Footprints are never searched using center points or Go-side intersection calculations.

Errors are structured, for example:

```json
{"error":{"code":"invalid_geometry","message":"search geometry must be a Polygon or MultiPolygon"}}
```

Database/SQL details are logged server-side and not sent to clients. HTTP header/read/write/idle timeouts are configured; database statements time out after 15 seconds. SIGINT/SIGTERM stop new HTTP requests, drain active requests for up to 30 seconds, and close the database pool.

## Database schema

The SQL source is [`migrations/001_initial.sql`](migrations/001_initial.sql).

| Column | Type / meaning |
| --- | --- |
| `id` | UUID primary key |
| `catalog_id`, `image_id` | Catalog-scoped identity |
| `display_name` | Original filename |
| `acquired_at` | Nullable acquisition timestamp |
| `imported_at`, `created_at` | Catalog timestamps |
| `footprint` | Valid, nonempty `geometry(MultiPolygon,4326)` |
| `checksum` | Lowercase SHA-256 |
| `asset_location` | Absolute local path |
| `width`, `height`, `band_count` | Positive raster dimensions/bands |
| `source_crs` | Source CRS WKT |
| `metadata` | GDAL metadata domains as JSONB |

Unique constraints cover `(catalog_id,image_id)` and `(catalog_id,checksum)`. Indexes cover footprint (GiST), checksum, acquisition time, and pagination order. `aarde_migrations` tracks applied SQL files. Back up both the database and your imagery separately.

## Local development

Install Go 1.25+, Node 22.12+ with npm, GDAL (`gdalinfo` and `gdal_create`; Debian/Ubuntu package `gdal-bin`), and PostgreSQL with PostGIS. Docker-only builds require none of these on the host.

```sh
# Optional dedicated local PostGIS for development:
docker run --name aarde-dev-db -d \
  -e POSTGRES_DB=aarde -e POSTGRES_USER=aarde -e POSTGRES_PASSWORD=aarde-local \
  -p 127.0.0.1:5432:5432 postgis/postgis:17-3.5

export AARDE_DATABASE_URL='postgres://aarde:aarde-local@localhost:5432/aarde?sslmode=disable'
go mod download
cd web && npm ci && npm run build && cd ..
go run ./cmd/aarde serve

# In another terminal; proxies /api to localhost:8080:
cd web && npm run dev
```

Browse Vite at `http://localhost:5173`. For production, `make build` creates `bin/aarde` with the compiled UI embedded via `go:embed`. Build the frontend before building Go. A clean checkout can run Go tests with the embedded `.gitkeep`; an unbuilt frontend returns a build instruction instead of the UI. No Node process is needed at runtime. Docker performs both builds in its multi-stage Dockerfile and includes GDAL in its non-root runtime image.

```sh
go build -ldflags '-X main.version=v0.1.0' -o bin/aarde ./cmd/aarde
docker build --build-arg VERSION=v0.1.0 -t aarde:v0.1.0 .
```

## Testing

```sh
go test -race ./...     # unit tests; integration tests explicitly skip without a test URL
cd web && npm ci && npm test && npm run build && cd ..  # GeoJSON validation, strict TypeScript, production build

make integration
```

Alternatively, set `AARDE_TEST_DATABASE_URL` to a dedicated PostGIS database and install GDAL locally before `go test -race -count=1 ./...`. Tests migrate that database, use unique catalog names, and remove their own records. Never point tests at a production database.

Integration tests generate small real GeoTIFFs in EPSG:4326 and EPSG:32613. They cover single/recursive imports, unsupported and corrupt files, missing acquisition time, duplicate checksums, ID conflicts, concurrent imports, source preservation, dry run, exact/unknown IDs, multiple catalogs, pagination, actual polygon intersection (including bounding-box false positives), partial overlap, boundary contact, MultiPolygons, invalid topology, and real HTTP requests. Unit tests cover geometry/request limits, GDAL JSON parsing, discovery, offline dry run, and CLI flags. No PostGIS mocks are used for spatial correctness.

![icon](icon.png)
