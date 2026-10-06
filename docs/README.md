# Aarde documentation

[Project README](../README.md)

Start with the [project quick start](../README.md#five-minute-workflow) to run
Aarde with Docker and import your first imagery.

| Guide | What it covers |
| --- | --- |
| [Catalog user guide](user-guide.md) | Search filters, drawing and loading areas, GeoJSON export, themes, and basemaps |
| [Configuration and deployment](configuration.md) | Docker, environment variables, offline operation, access tokens, public browsing, migrations, and Ruimte upgrades |
| [CLI reference](cli.md) | Inspect, import, dry runs, duplicates, exact-ID search, and removal |
| [Metadata and XML sidecars](metadata.md) | Cloud cover, acquisition timestamps, metadata precedence, sidecar discovery, and provenance |
| [NITF support](nitf.md) | Multi-image records, footprints, georeferencing, GDAL requirements, limits, and test fixtures |
| [Commercial NITF metadata](commercial-nitf.md) | TRE mappings, cloud provenance, profile recognition, and coverage limits |
| [Image Viewer](image-viewer.md) | Layers, cloud overlays, registration, rendering limits, viewer API, and smoke tests |
| [HTTP API guide](api.md) | Authentication, request examples, filters, pagination, spatial matching, and errors |
| [Development and testing](development.md) | Architecture, database schema, local setup, builds, tests, and OpenAPI maintenance |

The running application's [`/docs/`](http://localhost:8080/docs/) route serves
interactive Swagger documentation backed by [OpenAPI](../internal/api/openapi.json).
These Markdown guides are repository documentation and are not served by that route.
