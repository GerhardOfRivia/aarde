# HTTP API guide

[Documentation index](README.md) · [Project README](../README.md)

Open **API docs** in the web header or visit [`/docs/`](http://localhost:8080/docs/) for the interactive Swagger UI. Download the OpenAPI 3.1 document from [`/openapi.json`](http://localhost:8080/openapi.json). Both are accessible without a token and bundled into the Go binary, with no CDN or external validator required.

Use **Authorize** to enter the token from the private file named in the startup log, without the `Bearer` prefix. Swagger keeps it in memory until the page reloads. **Try it out** sends real requests to the same server; all current operations, including polygon search, are read-only. The served spec includes the running build version and reflects `AARDE_WEB_PUBLIC_READ`, including anonymous polygon search when enabled. Explicitly supplied invalid tokens are always rejected.

The [OpenAPI source](../internal/api/openapi.json) is the authoritative endpoint and
schema reference. This guide explains usage and search behavior. See the
[development guide](development.md#maintaining-the-api-specification) for validation.

## Authentication and routes

Routes are under `/api/v1`. Catalog reads require the bearer token unless public
reads are enabled; catalog GET routes also support HEAD. Viewer manifests,
images, and geometry always require the bearer token, including when public
catalog browsing is enabled. See [viewer API details](image-viewer.md#api)
and [web access configuration](configuration.md#web-access-and-read-only-viewing).

Footprints are GeoJSON MultiPolygons in EPSG:4326, with longitude before latitude.
Dates use RFC3339. ID matching is exact and case-sensitive.

## Request examples

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

## Pagination and request limits

Omit `catalog_id` to search all catalogs. List/search responses are `{ "items": [...], "limit": 50, "offset": 0, "has_more": false }`. Pagination is ordered by import time descending, then UUID; concurrent imports can shift offset-based pages. Limits default to 50 and are capped at 200. At most 100 exact IDs, 10,000 geometry positions, a 1 MiB request body, and offset 1,000,000 are accepted. Unknown search body fields, extra JSON values, invalid rings, out-of-range coordinates, and unsupported geometry types are rejected. PostGIS validates polygon topology, including self-intersections.

## Filters

`cloud_cover_lt` is an optional finite percentage from 0 to 100 (not a 0-1 fraction), accepted by GET listing and the spatial-search JSON body. Omission, or JSON `null`, preserves all existing matches including unknown cloud cover unless an explicit `cloud_cover_unknown` policy is supplied. A threshold of `20` includes `0` and `19.9`, but excludes `20`, `100`, and unknown values. `0` is valid and matches no imagery; `100` excludes values equal to 100 and unknown values. Empty GET values, malformed numbers, NaN, infinities, and values outside 0-100 return HTTP 400. The database combines this scene-level metadata filter with catalog, image-ID, and spatial predicates using AND before ordering and pagination; `has_more` reflects only matching imagery. Search never reads raster files or estimates cloud cover within the selected area.

Both search endpoints also accept:

| Parameter | Semantics |
| --- | --- |
| `acquired_from` | Inclusive RFC3339 acquisition instant |
| `acquired_before` | Exclusive RFC3339 acquisition instant; must be after `acquired_from` |
| `cloud_cover_lte` | Inclusive maximum percentage, 0–100; cannot accompany `cloud_cover_lt` |
| `cloud_cover_unknown` | `exclude`, `include`, or `only`; defaults to exclude with a threshold and include otherwise |

`only` cannot accompany either cloud threshold. `exclude` without a threshold returns all known cloud percentages. Unknown acquisition times are excluded when either date bound is set. The [catalog UI](user-guide.md#dates-and-cloud-cover) interprets From and Through as whole UTC days: June 1 through September 29 sends `acquired_from=2026-06-01T00:00:00Z` and `acquired_before=2026-09-30T00:00:00Z`. Cloud **At most 0%** includes recorded zero; **At most 100%** includes fully cloudy scenes. Include unknown is an explicit opt-in with a maximum. Existing CLI `--cloud-cover-lt` behavior remains strict.

Spatial POST additionally accepts `image_ids` as an array of up to 100 IDs and continues to require `geometry`. New JSON metadata fields can be omitted or null to use their defaults; empty GET values are invalid. Filters execute in SQL before pagination. Results remain ordered by import time descending, then UUID; the UI reports a page range and `more available`, not an exact total.

## Spatial matching

Spatial filtering uses indexed `ST_Intersects(footprint, search_geometry)` inside PostGIS. Partial overlap and boundary contact count as matches. Polygon holes are respected. Footprints are never searched using center points or Go-side intersection calculations.

See the [GDAL JSON inspection documentation](https://gdal.org/en/stable/programs/gdalinfo.html)
and [PostGIS ST_Intersects reference](https://postgis.net/docs/ST_Intersects.html).

## Errors and timeouts

Errors are structured, for example:

```json
{"error":{"code":"invalid_geometry","message":"search geometry must be a Polygon or MultiPolygon"}}
```

Database/SQL details are logged server-side and not sent to clients. HTTP header/read/write/idle timeouts are configured; database statements time out after 15 seconds. SIGINT/SIGTERM stop new HTTP requests, drain active requests for up to 30 seconds, and close the database pool.
