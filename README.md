# Aarde

Aarde is an open-source geospatial imagery catalog.
Import local imagery from the CLI, catalog it in PostGIS,
and discover your collection spatially through a web map.

One Go application provides the CLI, HTTP API, and embedded React/OpenLayers UI. GeoTIFFs and NITFs stay where they are; the catalog stores metadata, SHA-256 checksums, local asset paths, and WGS84 footprints. Licensed under [MIT](LICENSE).

![screenshot](screenshot.png)

## Five-minute workflow

Prerequisites: Git and Docker with Docker Compose v2. The first build downloads Go, Node, GDAL, and PostGIS images/dependencies and may take longer on a slow connection.

```sh
git clone https://github.com/GerhardOfRivia/aarde.git
cd aarde
# Put georeferenced .tif / .tiff / .ntf / .nitf files into ./data.
docker compose up -d --build

docker compose exec aarde aarde import /data --recursive
# Read the private access token, then paste it at http://localhost:8080.
docker compose exec -T aarde cat /home/aarde/.local/state/aarde/web.token
```

Select a catalog and set optional area, acquisition-date, or cloud filters, then click **Search catalog**. To draw an area, click **Draw Area**, click vertices on the map, and double-click to finish. Select a result or footprint to inspect metadata. **Edit Area** changes vertices; searches run only when explicitly requested. **Clear area** removes only the spatial condition. **Reset filters** clears metadata and catalog while preserving the drawn area and spatial scope. Exact image IDs are available under **More filters**.

To use an existing boundary, choose **Paste GeoJSON**, paste a WGS84 Polygon or MultiPolygon, and click **Load Area**. A polygon Feature or a FeatureCollection containing only polygon Features also works; multiple features become one MultiPolygon search area. The map fits the loaded area, which can be adjusted with **Edit Area**. Click **Search catalog** to apply the area and other filters. Loading replaces the current area and keeps existing results until you search. Invalid JSON, unsupported geometries, open rings, out-of-range coordinates, and oversized areas show an error without replacing the existing area. PostGIS checks polygon topology when the search runs.

Choose **Save GeoJSON** to download the current search area to your device as `aarde-search-area.geojson`. The file contains the drawn or loaded Polygon/MultiPolygon in WGS84 longitude/latitude coordinates, including holes and any edits, and can be reused with **Paste GeoJSON**. Saving is available once an area is loaded or drawing is finished; no search is required.

**Scene cloud cover** offers **Any cloud cover**, **At most**, and **Unknown only**. At most accepts decimal percentages from 0 to 100 and includes the selected boundary. **Include unknown cloud cover** broadens a maximum filter. Acquisition dates include whole UTC days; unknown dates are excluded while date bounds are set. Unapplied edits preserve the previous results and disable pagination. Each successful search starts on page one. Cloud cover describes the whole scene, not just the selected area.

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
    Files[Local GeoTIFFs and NITFs] --> CLI[aarde import / inspect]
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
| `AARDE_WEB_BASEMAP` | Native default `osm`; Compose default `offline`. Use `offline` for the bundled map or `none` for a plain background; both prevent external tiles for all browsers |
| `AARDE_WEB_TOKEN_PATH` | Optional private token file; see below |
| `POSTGRES_DB`, `POSTGRES_USER` | Compose only; both default to `aarde` |
| `POSTGRES_PASSWORD` | Compose only; `aarde-local` for local development |

Change the example database password for shared deployments. URL-encode special characters in connection URLs. The web API requires the startup bearer token by default. Use HTTPS at a reverse proxy or a trusted local connection to protect it in transit. Multi-user identity management belongs at the proxy. CORS is intentionally same-origin; development uses Vite's API proxy.

The OpenStreetMap basemap requires internet access and sends tile requests to OpenStreetMap. Choose **Basemap → Offline map** for the bundled overview map, or **No basemap** for a plain background. Either choice removes the external tile source and stops new tile requests. The choice is remembered in this browser and applied before the map loads on later visits. Requests already sent before switching may finish. Footprints, selection, drawing/editing, pasted GeoJSON, and catalog searches work in all modes; switching basemaps preserves the current view and search area.

For a network without internet, set `AARDE_WEB_BASEMAP=offline` in Compose's `.env` and rebuild/recreate the application with `docker compose up -d --build aarde`. This is also Compose's default when the variable is unset. For a native deployment, rebuild the frontend and Go binary, then set the variable in the server process environment before starting `aarde serve`. This forces **Offline map** for every browser, including first visits and browsers with a saved OpenStreetMap preference; the basemap selector is disabled. `AARDE_WEB_BASEMAP=none` similarly forces **No basemap**. Neither mode makes external tile requests. Prepare the application/database images and dependencies before moving to the offline network.

The offline basemap ships inside the binary/container: roughly 300 KB of [Natural Earth](https://www.naturalearthdata.com/) 1:110 million vector data, served by Aarde itself. It shows land, country boundaries and names, U.S. state boundaries, and major cities in Light and Night themes. Empty maps start at a world view; imagery and loaded search areas still zoom to their footprints. This is a generalized overview map without roads, street-level detail, or imagery. The data is public domain; source versions and regeneration instructions are in [`web/public/basemaps/NOTICE.txt`](web/public/basemaps/NOTICE.txt). Normal builds use the checked-in data without fetching it from the internet.

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
aarde inspect /data/scene.ntf
aarde import /data/scene.ntf
aarde import /data/scene.nitf --catalog example
aarde import image.tif --cloud-cover 12.5
aarde import ./imagery
aarde import ./imagery --recursive
aarde import ./imagery --recursive --catalog breckenridge
aarde import ./imagery --recursive --dry-run

aarde search --id ABC123
aarde search --id ABC123,IMG002 --catalog breckenridge
aarde search --id ABC123 --limit 50 --offset 0
aarde search --id ABC123,IMG002 --catalog breckenridge --cloud-cover-lt 20
```

`aarde search --cloud-cover-lt 20` (with the required `--id`) returns only imagery whose reported scene cloud cover is strictly below 20%. Omit the flag for any cloud cover; unknown values are excluded when the flag is supplied. The threshold accepts finite decimal percentages from 0 through 100, including explicit zero. It describes the scene, not a selected area.

Flags may precede or follow the import path. `inspect` prints JSON with format, source CRS (WKT), source corners, width, height, band count, acquisition time, cloud-cover percentage, calculated EPSG:4326 footprint, SHA-256 checksum, asset path, and metadata. It does not need a database.

Imports catalog existing files without copying or changing them. Absolute paths identify assets; within Docker those are container paths, so preserve the mount layout when moving a deployment. Raster bytes are never put into PostgreSQL. `asset_location` is independent of imagery identity so managed storage can be added later. No `--copy` option is implemented.

The extensions `.tif`, `.tiff`, `.ntf`, and `.nitf` are discovered case-insensitively. GDAL must detect `GTiff` or `NITF`; renaming an arbitrary format does not make it supported. Directories are scanned one level by default; `--recursive` includes descendants. Symlinks are skipped. Unsupported files appear at debug log level and contribute to `Skipped`. A corrupt or unsupported raster counts as `Failed`, its physical path and reason are reported, processing continues, and the command exits nonzero after its summary. A directory traversal failure or cancellation aborts discovery/processing with an error.

```text
Imported: 24
Existing: 3
Failed: 1
Skipped: 5
```

The initial image ID is the filename without its extension, derived in `importer.ImageID`, for both GeoTIFF and NITF. There are no segment suffixes or segment selectors. Catalog defaults to `default`. Names must fit 255 bytes and cannot include slashes, control characters, or surrounding whitespace.

For either supported format, including a renamed copy, the same physical-file checksum within a catalog reports **already imported** and keeps the original record and asset path. An existing image ID with different bytes is a conflict, never an overwrite. The same bytes may be cataloged independently in another catalog. SHA-256 reads and covers the entire physical source file, not external sidecar metadata. Concurrent duplicate imports are also protected by database constraints.

Dry run discovers, inspects, checksums, validates, derives IDs, and reports **would import**, with a separate `Would import` count. With `AARDE_DATABASE_URL`, it checks stored duplicates and PostGIS topology using read-only queries. The schema must already exist. With no URL, it works offline and clearly reports that database duplicates and topology were not checked; in-batch duplicates are still detected. It never migrates, writes to the database or source, or creates derivatives. All-image NITF validation also applies to both dry-run modes.

Cloud cover is an optional percentage from 0 to 100; `0` means clear and `NULL` means unknown. The inspector reads numeric values from `CLOUD_COVER`, `CLOUD_COVER_PERCENTAGE`, and `EO:CLOUD_COVER` (case-insensitive, in that priority order) in GDAL metadata domains. Missing, non-finite, or out-of-range metadata values stay unknown. `aarde import <path> --cloud-cover 12.5` overrides metadata for every file in that import, including during dry runs. Duplicate imports keep the original record and cloud cover. Existing rows remain unknown after migration; cloud cover is not calculated from pixels.

Acquisition time is separate from import/creation time. For embedded GDAL metadata, timezone-qualified RFC3339 values under `ACQUISITION_DATETIME`, `ACQUISITION_TIME`, `SENSING_TIME`, and `TIFFTAG_DATETIME_ORIGINAL` take precedence, in that order (case-insensitive keys; domains and matching keys sorted lexically). If none is valid, NITF 2.1 (`NITF_FHDR=NITF02.10`) uses a strictly valid, 14-digit `NITF_IDATIM` in `CCYYMMDDhhmmss` UTC format. Unknown, invalid, placeholder, and unsupported legacy encodings stay `NULL`; no host-local timezone is assumed. `NITF_FDT`, generic `TIFFTAG_DATETIME`, filesystem modification time, and import time are never acquisition-time fallbacks.

## XML metadata sidecars

Automatic XML metadata sidecars are read by `inspect` and every import mode, including individual files, directories, recursive imports, and dry runs. For `.tif`, `.tiff`, `.ntf`, and `.nitf`, Aarde looks in the same directory for the raster basename with an XML extension (extension matching is case-insensitive). An exact basename takes priority; otherwise a unique case-insensitive basename is accepted. Multiple matches at the selected priority are ambiguous and ignored with a warning. Nonregular files, including symlinks, are skipped.

The supported schema is Maxar/DigitalGlobe `isd/IMD/IMAGE`, using namespace-local element names with complete path context. `CLOUDCOVER` is a **fraction from 0 to 1**, converted to percentage by multiplying by 100: `3.000000000000000e-03` becomes **0.3%**. This rule comes from the vendor's [ISD specification v1.1.2, printed page 34](https://csda-maxar-pdfs.s3.amazonaws.com/ISD_External.pdf#page=34); `-999` means unassessed. Zero is known; nonfinite, invalid, and out-of-range values are ignored with a warning. Values below 1 are never interpreted using a heuristic.

Acquisition time uses `isd/IMD/IMAGE/FIRSTLINETIME` (first-line exposure time), then valid `isd/IMD/IMAGE/TLCTIME` (first time-tagged line-count record) as an approximate acquisition-time fallback. Both require RFC3339 timestamps with a timezone and are normalized to UTC. `GENERATIONTIME` is processing metadata and is never used for acquisition. Cloud precedence is explicit `--cloud-cover` > valid sidecar > embedded GDAL > unknown. Acquisition precedence is valid sidecar > embedded GDAL > unknown. Missing or invalid sidecar fields leave embedded values intact. For multi-image NITFs, sidecar scene values populate file-level searchable fields even when segment values differ; each segment retains its original source values. No XML-to-segment association is inferred. Repeated IMAGE groups are unsupported.

Missing XML is normal. Unreadable, malformed, unsupported, ambiguous, or oversized XML produces a visible log warning without failing a valid raster. Reads are bounded to **4 MiB** (recognized scalar fields to 1024 bytes and nesting to 64 elements), accommodating large ISD files such as the approximately 552 KB example. Parsing uses Go's `encoding/xml`, does not resolve external entities or fetch resources, and ignores unrelated paths. Recognized sidecars add compact provenance under `metadata._aarde.xml_sidecar`: absolute path, schema (`maxar-isd-imd`), SHA-256 of the XML bytes, source fields with full paths, normalized values, and the selected acquisition source. Original GDAL domains and annotations remain intact. The XML document and attitude/ephemeris arrays are not stored.

Raster checksums remain SHA-256 of the physical raster alone. Duplicate imports retain the original record and metadata: adding or changing a sidecar (or supplying a new override) **does not refresh an existing record**, including during dry runs. A metadata-refresh command is outside this implementation.

## NITF support

NITFs containing one or more image segments are supported, with one record per physical file. Every image must have supported georeferencing; one invalid segment rejects the whole file. Zero-image files and containers whose image count cannot be established are rejected. Text, graphics, and data-extension segments do not count as images. Original files are preserved, including on read-only imagery mounts.

GDAL's [NITF container contract](https://gdal.org/en/stable/drivers/raster/nitf_advanced.html#multi-image-nitf-files) is checked using an explicit `SUBDATASETS` request on the physical filename, plus default file/image-header metadata. GDAL 3.6.2 enumerates every image when there are multiple images, omits the list for a single image, and exposes only file headers for zero images. Aarde validates complete indexed subdataset entries and image-header presence; it never derives segment count from band count. Every image is inspected with a zero-based `NITF_IM:<index>:<physical path>` selector. Locally generated zero-, one-, and three-image fixtures test this behavior. Inconsistent or unrecognized count metadata fails validation.

### Multi-image NITF records

Each physical NITF produces one catalog record, retaining its filename-based ID, original absolute asset path, and SHA-256 of the entire file. No images are extracted and no source or sidecar is written. Repeated, renamed, and concurrent imports use the existing catalog/checksum uniqueness rules.

`inspect` JSON and HTTP records expose `segments`, ordered by zero-based image index, with dimensions, band count, source CRS, WGS84 footprint, acquisition time, cloud cover, and selected GDAL metadata for each image. The UI exposes these in the Image segments disclosure. Legacy records and GeoTIFFs have no segment details; existing records are not re-inspected by the migration.

For multiple images, file-level width, height, and band count are `0` (unavailable), source CRS is empty, and inspect bounds are null. Without valid XML scene metadata, file-level acquisition time and cloud cover are populated only when every image has the same known value; differing or missing values produce unknown. Date/cloud filters operate on the file-level values, with valid XML scene metadata taking precedence over these unanimous aggregates. Segment metadata remains available even when aggregates are unknown. Single-image NITF and GeoTIFF retain their existing scalar behavior. `--cloud-cover` overrides only the file-level searchable cloud cover, including in dry runs; segment values and metadata retain the source values, and duplicate imports retain the original record.

The searchable footprint is the geometric union of every image footprint, preserving holes, gaps, and disjoint regions. Multi-image inspection additionally requires `ogr2ogr` with SQLite spatial functions (included in the Docker GDAL installation). All inspection/union subprocesses and whole-file hashing share the two-minute inspection budget and existing output limits. A failed image rejects the entire file with its zero-based index; zero-image and ambiguous containers remain rejected. RPC-only imagery and unsplit antimeridian footprints remain unsupported. GCP footprints retain the existing sampled-perimeter approximation.

Footprints use this selection order:

1. A valid GDAL WGS84 extent backed by an affine transform and source CRS, or by usable GCPs and their CRS.
2. GDAL's [`gdaltransform -tps -t_srs EPSG:4326`](https://gdal.org/en/stable/programs/gdaltransform.html) on the image selector for NITF (physical source for GeoTIFF). The explicit thin-plate-spline method honors non-affine GCPs. Aarde samples 16 positions on each outer pixel edge (64 total), closes the ring, and limits the input to 256 GCPs.

The result is a catalog approximation, not evidence of orthorectification. Inspection validates longitude/latitude order and ranges, finite coordinates, closure, nonzero area, and a longitude span no greater than 180 degrees per ring. Antimeridian-crossing rings are explicitly rejected; existing split MultiPolygons can be accepted. PostGIS additionally enforces valid topology at import and in database-assisted dry runs. Offline dry runs do not check PostGIS topology. Files without usable affine/GCP georeferencing fail with an actionable error, including RPC-only files. GCP-only inspection reports the GCP CRS as `source_crs` and empty `bounds`, since GDAL's ordinary corners in that case are pixel coordinates.

Aarde deliberately requests the default/header, `IMAGE_STRUCTURE`, `RPC`, `TRE`, and `xml:TRE` metadata domains and preserves their JSON values, including objects, arrays, and XML strings. It does not request all domains, raw-header `NITF_METADATA`, text, graphics, or DES payload domains. Identifiers, source descriptions, compression, geolocation, and handling markings available in those selected domains are retained. Markings are metadata, **not implemented access controls**. The UI displays metadata as escaped text.

The reserved `metadata._aarde` namespace records the detected `format`, affine/GCP `georeferencing`, `footprint_method`, and available affine transform/GCP metadata separately from original GDAL domains. The API's additive nullable `format` field and detail view use that stored annotation. Older records report unknown format without reopening their sources; duplicates retain their original metadata. The backward-compatible segment migration preserves existing rows and uniqueness constraints. HTTP/search handlers use catalog metadata only.

The Docker runtime and tests use Debian Bookworm GDAL 3.6.2 with the NITF, JPEG, and JP2OpenJPEG drivers; the build checks their availability. No proprietary codecs are required. Uncompressed NITF, JPEG (`IC=C3`), and JPEG2000 (`IC=C8`, using JP2OpenJPEG) have synthetic fixture coverage. Other compression variants depend on the installed GDAL build. For local installations, check `gdalinfo --version`, `gdalinfo --format NITF`, `gdalinfo --format JPEG`, and `gdalinfo --format JP2OpenJPEG`. See the official [NITF driver and codec documentation](https://gdal.org/en/stable/drivers/raster/nitf.html).

Inspection invokes GDAL with separate arguments and `GDAL_PAM_ENABLED=NO`. Metadata collection, GCP transformation, and whole-file SHA-256 share a two-minute timeout and cancellation. Metadata stdout and combined segment JSON are capped at 16 MiB, union input/output at 16 MiB, transformer stdout at 64 KiB, and each stderr at 16 KiB. Excess output is rejected. Inspection checks source identity, size, and modification time before persistence. It requests no statistics, histograms, per-band checksums, or full pixel decoding. Successful metadata inspection is **not a full-raster integrity check**; unreadable codecs or corrupt metadata may fail before cataloging, and pixel corruption may remain undetected.

Preview generation, format conversion, COGs, tiles, orthorectification, and RPC-only footprint estimation are outside this milestone. Ingestion never converts NITF to GeoTIFF.

Generate a tiny unclassified NITF locally (GDAL tools are included in the Docker image), then inspect/import it:

```sh
export GDAL_PAM_ENABLED=NO
gdal_create -of GTiff -outsize 16 16 -bands 3 -burn 42 \
  -a_srs EPSG:4326 -a_ullr -106 40 -105 39 /tmp/tiny-source.tif
gdal_translate -of NITF -co ICORDS=G -co IC=NC \
  -co FSCLAS=U -co ISCLAS=U -co IDATIM=20260910120000 \
  /tmp/tiny-source.tif /tmp/tiny.ntf
gdalinfo -json -mdd SUBDATASETS -mdd TRE -mdd xml:TRE /tmp/tiny.ntf
aarde inspect /tmp/tiny.ntf
aarde import /tmp/tiny.ntf --dry-run
# Set AARDE_DATABASE_URL for the actual import:
aarde import /tmp/tiny.ntf --catalog example
```

This conversion creates a test fixture only; the ingestion path preserves the resulting NITF unchanged. To run these commands inside Compose, prefix each with `docker compose exec -e GDAL_PAM_ENABLED=NO aarde` (omit the host `export`).

## HTTP API

Open **API docs** in the web header or visit [`/docs/`](http://localhost:8080/docs/) for the interactive Swagger UI. Download the OpenAPI 3.1 document from [`/openapi.json`](http://localhost:8080/openapi.json). Both are accessible without a token and bundled into the Go binary, with no CDN or external validator required.

Use **Authorize** to enter the token from the private file named in the startup log, without the `Bearer` prefix. Swagger keeps it in memory until the page reloads. **Try it out** sends real requests to the same server; all current operations, including polygon search, are read-only. The served spec includes the running build version and reflects `AARDE_WEB_PUBLIC_READ`, including anonymous polygon search when enabled. Explicitly supplied invalid tokens are always rejected.

The source specification is [`internal/api/openapi.json`](internal/api/openapi.json). Update it alongside API changes. `cd web && npm test` and `npm run build` validate it; Go tests check route coverage, authentication, response schemas, and embedded documentation assets. Build the frontend before running Go tests.

Routes are under `/api/v1`. All reads require the bearer token unless public reads are enabled; GET routes also support HEAD. Footprints are GeoJSON MultiPolygons in EPSG:4326, with longitude before latitude. Dates use RFC3339. ID matching is exact and case-sensitive.

| Method | Route | Behavior |
| --- | --- | --- |
| GET | `/info` | Build version, `public_read`, `authenticated`, and `read_only` access state |
| GET | `/version` | Running binary build version, independent of database health |
| GET | `/health` | Database readiness; 503 when unavailable |
| GET | `/catalogs` | `{ "items": ["default"] }`; catalogs with imagery |
| GET | `/imagery/{catalogID}/{imageID}` | Exact record; 404 if absent |
| GET | `/imagery` | Filter by catalog, repeated image IDs, acquisition dates, scene clouds, and pagination |
| POST | `/imagery/search` | Polygon or MultiPolygon intersection search with the same metadata filters and optional `image_ids` |

```sh
# Docker; for a native server, read the token_file path from its startup log.
AARDE_TOKEN=$(docker compose exec -T aarde cat /home/aarde/.local/state/aarde/web.token)
curl -H "Authorization: Bearer $AARDE_TOKEN" http://localhost:8080/api/v1/health
curl -H "Authorization: Bearer $AARDE_TOKEN" http://localhost:8080/api/v1/catalogs
curl -H "Authorization: Bearer $AARDE_TOKEN" http://localhost:8080/api/v1/imagery/default/ABC123
curl -H "Authorization: Bearer $AARDE_TOKEN" 'http://localhost:8080/api/v1/imagery?catalog_id=default&image_id=ABC123&image_id=IMG002&cloud_cover_lt=20&limit=50&offset=0'

curl -X POST http://localhost:8080/api/v1/imagery/search \
  -H "Authorization: Bearer $AARDE_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"catalog_id":"default","geometry":{"type":"Polygon","coordinates":[[[-106.2,39.3],[-105.8,39.3],[-105.8,39.7],[-106.2,39.7],[-106.2,39.3]]]},"cloud_cover_lt":20,"limit":100,"offset":0}'
```

Omit `catalog_id` to search all catalogs. List/search responses are `{ "items": [...], "limit": 50, "offset": 0, "has_more": false }`. Pagination is ordered by import time descending, then UUID; concurrent imports can shift offset-based pages. Limits default to 50 and are capped at 200. At most 100 exact IDs, 10,000 geometry positions, a 1 MiB request body, and offset 1,000,000 are accepted. Unknown search body fields, extra JSON values, invalid rings, out-of-range coordinates, and unsupported geometry types are rejected. PostGIS validates polygon topology, including self-intersections.

`cloud_cover_lt` is an optional finite percentage from 0 to 100 (not a 0-1 fraction), accepted by GET listing and the spatial-search JSON body. Omission, or JSON `null`, preserves all existing matches including unknown cloud cover unless an explicit `cloud_cover_unknown` policy is supplied. A threshold of `20` includes `0` and `19.9`, but excludes `20`, `100`, and unknown values. `0` is valid and matches no imagery; `100` excludes values equal to 100 and unknown values. Empty GET values, malformed numbers, NaN, infinities, and values outside 0-100 return HTTP 400. The database combines this scene-level metadata filter with catalog, image-ID, and spatial predicates using AND before ordering and pagination; `has_more` reflects only matching imagery. Search never reads raster files or estimates cloud cover within the selected area.

The catalog editor combines Catalog, Area, Acquired date, and Scene cloud cover in one explicit **Search catalog** action. **More filters** retains exact image IDs (OR between IDs, AND with other groups). Drawing, presets, resets, and chip removal only change the draft. Applied chips, rows, and footprints stay on the last successful search; pagination is disabled while changes are pending. **Clear area** preserves metadata filters, while **Reset filters** preserves the polygon and chosen spatial scope. Cloud percentage and acquisition date (UTC) appear on each result.

Both search endpoints also accept:

| Parameter | Semantics |
| --- | --- |
| `acquired_from` | Inclusive RFC3339 acquisition instant |
| `acquired_before` | Exclusive RFC3339 acquisition instant; must be after `acquired_from` |
| `cloud_cover_lte` | Inclusive maximum percentage, 0–100; cannot accompany `cloud_cover_lt` |
| `cloud_cover_unknown` | `exclude`, `include`, or `only`; defaults to exclude with a threshold and include otherwise |

`only` cannot accompany either cloud threshold. `exclude` without a threshold returns all known cloud percentages. Unknown acquisition times are excluded when either date bound is set. The UI interprets From and Through as whole UTC days: June 1 through September 29 sends `acquired_from=2026-06-01T00:00:00Z` and `acquired_before=2026-09-30T00:00:00Z`. Cloud **At most 0%** includes recorded zero; **At most 100%** includes fully cloudy scenes. Include unknown is an explicit opt-in with a maximum. Existing CLI `--cloud-cover-lt` behavior remains strict.

Spatial POST additionally accepts `image_ids` as an array of up to 100 IDs and continues to require `geometry`. New JSON metadata fields can be omitted or null to use their defaults; empty GET values are invalid. Filters execute in SQL before pagination. Results remain ordered by import time descending, then UUID; the UI reports a page range and `more available`, not an exact total.


See the [GDAL JSON inspection documentation](https://gdal.org/en/stable/programs/gdalinfo.html) and [PostGIS ST_Intersects reference](https://postgis.net/docs/ST_Intersects.html).

Spatial filtering uses indexed `ST_Intersects(footprint, search_geometry)` inside PostGIS. Partial overlap and boundary contact count as matches. Polygon holes are respected. Footprints are never searched using center points or Go-side intersection calculations.

Errors are structured, for example:

```json
{"error":{"code":"invalid_geometry","message":"search geometry must be a Polygon or MultiPolygon"}}
```

Database/SQL details are logged server-side and not sent to clients. HTTP header/read/write/idle timeouts are configured; database statements time out after 15 seconds. SIGINT/SIGTERM stop new HTTP requests, drain active requests for up to 30 seconds, and close the database pool.

## Database schema

The SQL source is in [`migrations/`](migrations/). Startup and normal imports apply pending migrations automatically; existing rows receive `NULL` for newly added cloud cover.

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
| `width`, `height`, `band_count` | Positive raster dimensions/bands |
| `source_crs` | Source CRS WKT |
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

Browse Vite at `http://localhost:5173`. For production, `make build` creates `bin/aarde` with the compiled UI embedded via `go:embed`. Build the frontend before building Go. A clean checkout can run Go tests with the embedded `.gitkeep`; an unbuilt frontend returns a build instruction instead of the UI. No Node process is needed at runtime. Docker performs both builds in its multi-stage Dockerfile and includes GDAL in its non-root runtime image.

```sh
go build -ldflags '-X main.version=v0.1.0' -o bin/aarde ./cmd/aarde
docker build --build-arg VERSION=v0.1.0 -t aarde:v0.1.0 .
```

## Testing

```sh
go test -race ./...     # GDAL tests skip without tools; PostGIS tests skip without a test URL
cd web && npm ci && npm test && npm run build && cd ..  # GeoJSON/search request tests, strict TypeScript, production build

make integration
```

Alternatively, set `AARDE_TEST_DATABASE_URL` to a dedicated PostGIS database and install GDAL locally before `go test -race -count=1 ./...`. Tests migrate that database, use unique catalog names, and remove their own records. Never point tests at a production database.

Integration tests generate tiny unclassified GeoTIFFs in EPSG:4326 and EPSG:32613 and NITFs locally, without imagery downloads. NITF tests cover real zero/single/multiple-image count metadata, multiband images, GCP footprints, RPC-only errors, JPEG/JPEG2000, mixed-directory continuation after rejection, repeated/renamed/concurrent imports, read-only database dry runs, source preservation/no sidecars, and PostGIS/HTTP spatial search. Capability tests explicitly skip if local GDAL/codecs are absent; Docker integration requires them. Three-image fixtures exercise overlapping and disjoint coverage. They cover single/recursive imports, unsupported and corrupt files, missing acquisition time, duplicate checksums, ID conflicts, concurrent imports, source preservation, dry run, exact/unknown IDs, multiple catalogs, strict cloud-cover thresholds/unknown values, filtered pagination, actual polygon intersection (including bounding-box false positives), partial overlap, boundary contact, MultiPolygons, invalid topology, and real HTTP requests. Unit tests cover geometry/request limits, GDAL JSON parsing, discovery, offline dry run, and CLI flags. No PostGIS mocks are used for spatial correctness.

![icon](icon.png)
