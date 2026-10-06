# Aarde

Aarde is an open-source geospatial imagery catalog.
Import local imagery from the CLI, catalog it in PostGIS,
and discover your collection spatially through a web map.

One Go application provides the CLI, HTTP API, and embedded React/OpenLayers UI.
GeoTIFFs and NITFs stay where they are; the catalog stores metadata, SHA-256
checksums, local asset paths, and WGS84 footprints. Licensed under [MIT](LICENSE).

![Aarde imagery catalog and map](screenshot.png)

## Five-minute workflow

Prerequisites: Git and Docker with Docker Compose v2. The first build downloads
Go, Node, GDAL, and PostGIS images/dependencies and may take longer on a slow
connection.

```sh
git clone https://github.com/GerhardOfRivia/aarde.git
cd aarde
# Put georeferenced .tif / .tiff / .ntf / .nitf files into ./data.
docker compose up -d --build

docker compose exec aarde aarde import /data --recursive
# Read the private access token, then paste it at http://localhost:8080.
docker compose exec -T aarde cat /home/aarde/.local/state/aarde/web.token
```

Open [http://localhost:8080](http://localhost:8080), paste the token, and select a
catalog. Set optional area, acquisition-date, or cloud filters, then click
**Search catalog**. Select a result or footprint to inspect its metadata.

To draw a search area, click **Draw Area**, click vertices on the map, and
double-click to finish. **Paste GeoJSON** loads an existing boundary.
Click **Search catalog** to apply changes. See the [user guide](docs/user-guide.md)
for editing areas, saving GeoJSON, and filter behavior.

Choose **Open image viewer** on a result to view imagery and supported cloud
overlays. Pan, zoom, and adjust layer visibility and opacity in the browser.
The viewer always requires the current bearer token, even when public catalog
browsing is enabled. See the [Image Viewer guide](docs/image-viewer.md) for
resolution controls, supported encodings, and resource limits.

### Try it without imagery

Generate a tiny synthetic GeoTIFF, then return to the catalog and search:

```sh
docker compose exec aarde gdal_create -of GTiff -outsize 32 32 \
  -bands 1 -burn 42 -a_srs EPSG:4326 \
  -a_ullr -106.12 39.55 -105.95 39.40 data/example.tif
docker compose exec aarde aarde import /data/example.tif
```

This demo file lives in the application container and disappears when it is
replaced. It is suitable for catalog search; Compose restricts viewer sources to
`/data`. Put real imagery in the persistent `./data` bind mount or another stable
local mount, and configure [viewer source roots](docs/image-viewer.md#rendering-security-and-resource-configuration)
when using a different location.

### Stop and restart

```sh
docker compose down             # keeps the catalog volume
docker compose up -d
```

Each server restart rotates the access token. Read the token file again to sign
in. See [configuration and deployment](docs/configuration.md) for custom settings,
public browsing, offline operation, backups, and upgrades.

## Supported imagery and storage

- GeoTIFF (`.tif`, `.tiff`) and NITF (`.ntf`, `.nitf`) are supported through GDAL.
  See [NITF support](docs/nitf.md) for georeferencing requirements and codec limits.
- One catalog record represents a physical file, including multi-image NITFs.
  Imports read existing files without copying or changing them.
- Imagery is mounted read-only at `/data` in Docker. The application runs as UID
  10001 and needs read access to files and traversal access to their directories.
- The web API is read-only. Imports and catalog removal use the
  [CLI](docs/cli.md); database records and source imagery are stored separately.
- Compose binds the web port to `127.0.0.1:8080`, does not publish PostgreSQL's
  port, and defaults to a bundled offline basemap when `AARDE_WEB_BASEMAP` is unset.

## Documentation

See the [documentation index](docs/README.md) for all guides.

| Guide | Topics |
| --- | --- |
| [User guide](docs/user-guide.md) | Search, areas, filters, themes, and basemaps |
| [Configuration](docs/configuration.md) | Docker, environment, authentication, offline operation, and upgrades |
| [CLI reference](docs/cli.md) | Inspect, import, dry run, search, and remove |
| [Metadata and sidecars](docs/metadata.md) | Cloud cover, acquisition time, and XML precedence |
| [NITF support](docs/nitf.md) | Segments, footprints, GDAL, and format limits |
| [Commercial NITF](docs/commercial-nitf.md) | TRE metadata, provenance, and profile recognition |
| [Image Viewer](docs/image-viewer.md) | Layers, cloud overlays, rendering, and viewer configuration |
| [HTTP API](docs/api.md) | Examples, filters, pagination, and errors |
| [Development](docs/development.md) | Architecture, schema, local setup, builds, and tests |

For interactive API documentation, open **API docs** in the web header or visit
[`/docs/`](http://localhost:8080/docs/) on the running server. The
[OpenAPI specification](internal/api/openapi.json) defines the endpoint schemas.

![Aarde icon](icon.png)
