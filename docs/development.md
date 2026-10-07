# Development and testing

[Documentation index](README.md) · [Project README](../README.md)

Commands below run from the repository root unless noted otherwise.
See [configuration and deployment](configuration.md) for environment variables,
authentication, offline deployments, and migrations.

## Architecture

```mermaid
flowchart TD
    Files[Local GeoTIFFs and NITFs] --> CLI[aarde import / inspect]
    CLI --> GDAL[GDAL JSON inspection + WGS84 footprint]
    GDAL --> Catalog[Shared Go catalog service]
    SearchCLI[aarde search] --> Catalog
    UI[React + OpenLayers + Material UI] --> HTTP[Go net/http + chi API]
    HTTP --> Catalog
    HTTP --> Viewer[Image Viewer rendering]
    Viewer --> GDAL
    Files --> Viewer
    Catalog --> DB[(PostgreSQL + PostGIS)]
    HTTP --> UI
```

`internal/raster` invokes GDAL; `internal/importer` discovers files and runs imports; `internal/geo` validates bounded GeoJSON structure; `internal/catalog` owns models and catalog operations; `internal/database` owns parameterized SQL and migrations; `internal/api` owns validation and response DTOs; `internal/server` owns middleware, embedded assets, and shutdown; `internal/config` reads environment settings. Both CLI and HTTP call the same catalog service. Viewer requests resolve catalog identity through that service, then read the source through `internal/raster`; see [viewer architecture](image-viewer.md#architecture-and-registration).

`cmd/aarde/main.go` only passes arguments, stdout, stderr, and the injected build
version to `internal/cli.RunVersion`, then exits with its return code.
`internal/cli` owns parsing, help, exit status, signal cancellation, configuration,
and command orchestration. Its handlers call the domain packages above; domain
packages do not import the CLI. Tests invoke the CLI directly with supplied
streams and, for confirmation or cancellation, an input reader and context.
The CLI temporarily routes the existing domain logger to its stderr and restores
it on return; invocations that use configuration are serialized for that reason.

## Database schema

The SQL source is in [`migrations/`](../migrations/). Startup and normal imports apply pending migrations automatically; existing rows receive `NULL` for newly added cloud cover.

| Column | Type / meaning |
| --- | --- |
| `id` | UUID primary key |
| `catalog_id`, `image_id` | Catalog-scoped identity |
| `display_name` | Original filename |
| `acquired_at` | Nullable acquisition timestamp |
| `cloud_cover` | Nullable cloud-cover percentage (0-100) |
| `imported_at`, `created_at` | Catalog timestamps |
| `footprint` | Valid, nonempty `geometry(MultiPolygon,4326)` |
| `checksum` | Lowercase SHA-256 |
| `asset_location` | Absolute local path |
| `width`, `height`, `band_count` | Positive for GeoTIFF/single-image records; `0` for multi-image NITFs |
| `source_crs` | Source CRS WKT; empty for multi-image NITFs |
| `segments` | JSONB array of NITF image-segment details in index order; empty for legacy records and GeoTIFFs |
| `metadata` | Selected GDAL metadata domains and reserved `_aarde` annotations as JSONB |

Unique constraints cover `(catalog_id,image_id)` and `(catalog_id,checksum)`. Indexes cover footprint (GiST), checksum, acquisition time, and pagination order. `aarde_migrations` tracks applied SQL files. Back up both the database and your imagery separately.

## Local development

Install Go 1.25+, Node 22.12+ with npm, GDAL 3.6+ (`gdalinfo`, `gdaltransform`, and fixture tools `gdal_create`/`gdal_translate`; Debian/Ubuntu package `gdal-bin`), and PostgreSQL with PostGIS. Docker-only builds require none of these on the host.

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

Browse Vite at `http://localhost:5173`. For production, `make build` creates `bin/aarde` with the compiled UI embedded via `go:embed`. Build the frontend before building Go. The embedded `.gitkeep` lets Go packages compile before the frontend is built, but the full test suite requires built frontend and documentation assets. An unbuilt frontend returns a build instruction instead of the UI. No Node process is needed at runtime. Docker performs both builds in its multi-stage Dockerfile and includes GDAL in its non-root runtime image.

```sh
go build -ldflags '-X main.version=v0.1.0' -o bin/aarde ./cmd/aarde
docker build --build-arg VERSION=v0.1.0 -t aarde:v0.1.0 .
```

## Testing

```sh
cd web && npm ci && npm test && npm run build && cd ..  # Frontend checks and embedded assets
go test -race ./...     # GDAL tests skip without tools; PostGIS tests skip without a test URL

make integration
```

Alternatively, set `AARDE_TEST_DATABASE_URL` to a dedicated PostGIS database and install GDAL locally before `go test -race -count=1 ./...`. Tests migrate that database, use unique catalog names, and remove their own records. Never point tests at a production database.

Integration tests generate tiny unclassified GeoTIFFs in EPSG:4326 and EPSG:32613 and NITFs locally, without imagery downloads. NITF tests cover real zero/single/multiple-image count metadata, multiband images, GCP footprints, RPC-only errors, JPEG/JPEG2000, mixed-directory continuation after rejection, repeated/renamed/concurrent imports, read-only database dry runs, source preservation/no sidecars, and PostGIS/HTTP spatial search. Capability tests explicitly skip if local GDAL/codecs are absent; Docker integration requires them. Three-image fixtures exercise overlapping and disjoint coverage. They cover single/recursive imports, unsupported and corrupt files, missing acquisition time, duplicate checksums, ID conflicts, concurrent imports, source preservation, dry run, exact/unknown IDs, multiple catalogs, strict cloud-cover thresholds/unknown values, filtered pagination, actual polygon intersection (including bounding-box false positives), partial overlap, boundary contact, MultiPolygons, invalid topology, and real HTTP requests. Unit tests cover geometry/request limits, GDAL JSON parsing, discovery, offline dry run, and CLI flags. No PostGIS mocks are used for spatial correctness.

Viewer browser regression checks and the manual smoke test are documented in the
[Image Viewer guide](image-viewer.md#verification-and-smoke-test).

## Maintaining the API specification

The source specification is [`internal/api/openapi.json`](../internal/api/openapi.json). Update it alongside API changes. `cd web && npm test` and `npm run build` validate it; Go tests check route coverage, authentication, response schemas, and embedded documentation assets. Build the frontend before running Go tests.

The application's `/docs/` route serves the bundled Swagger UI. Markdown guides
in this repository's `docs/` directory are separate and are not served by that route.
Update the [documentation index](README.md) when adding a guide.
