# Image Viewer

[Documentation index](README.md) · [Project README](../README.md)

Select a catalog row and open **Image Viewer** from its details, or use a copied URL:

```text
/image-viewer?catalog=default&image=scene-001
```

`image` is required exactly once; `catalog` defaults to `default`. Both are catalog IDs,
not paths, download URLs, segment selectors, or credentials. Identical image IDs in
different catalogs remain distinct. The browser retains the requested URL during sign-in.
A normal link supports standard browser navigation; the adjacent ↗ link opens another tab.
**Back to catalog** returns to the catalog. The catalog currently has no persisted search
URL; normal browser history/cache behavior applies to its local search state.

Drag to pan; wheel/trackpad or pinch to zoom. Use **Zoom in**, **Zoom out**, **Fit all**, or
**Fit layer**. Every layer has a visibility checkbox and an independent 0–100% opacity
slider with its current value. Imagery begins at 100%; clouds at 40%. Higher image segment
indexes stack above lower indexes; all clouds stack above imagery. Failed layers have a
retry action while successfully loaded layers remain usable. Unsupported layers explain
why they cannot render. The scene fits once from the manifest; later arrivals never reset
the view.

A loading indicator stays visible while the viewer opens and reads the image manifest.
While image and cloud layers load, it shows a progress bar and the number of loaded
layers. Progress counts completed layer requests, including decoding, rather than bytes
or estimated time. Failed requests are reported separately; unsupported layers are
excluded from progress. The indicator clears when loading finishes and reappears for
resolution changes, viewer reloads, and layer retries. Loaded imagery remains interactive
while other layers finish.

The resolution selector requests complete replacement images only on explicit changes.
**Auto** uses native dimensions if the entire scene fits the configured budget, otherwise
bounded whole-segment images. **Preview** caps each dimension at 1024 and each layer at
one million pixels. **Native dimensions** is enabled only when safe for the complete
scene. Original and display dimensions are shown per layer. Zooming a downsampled image
never fetches extra detail and is not native-resolution inspection.

## Architecture and registration

```text
catalog list → Image Viewer URL → authenticated manifest
            → one authenticated whole-segment PNG per raster layer
            → browser compositing, pan, zoom, visibility, opacity
```

The original local TIFF/NITF is authoritative. GDAL 3.6.2 is the tested baseline. NITF 2.1 / NSIF 1.0
image indexes are zero-based and always opened explicitly as `NITF_IM:index:path`,
including image zero. They are not band numbers or display levels. One catalog record
continues to represent the physical file; TIFF overviews and masks are not scene layers.
No source file is modified, no COG/tile/pyramid is created, no image windows are fetched,
and no queue, preparation action, new backend service, or polling loop is introduced.

Georeferenced imagery uses a north-up Web Mercator plane, the same projection as the
catalog map, so the image retains its footprint's orientation, proportions and shape.
Coordinates are translated to the first registered image's upper-left edge and uniformly
scaled to approximately its pixel size; source rotation and shear are preserved even for
that first image. Original image dimensions and placement are independent of display
downsampling. A row-major placement mesh describes original pixel edges. GDAL transforms
affine georeferencing and GCPs into the map projection using a 16×16 interpolation mesh;
GCP placement uses the same bounded thin-plate spline method as catalog footprint inspection.
This is approximate display registration, not survey-grade orthorectification. The frontend uses
the installed OpenLayers map interactions and one custom non-tiled canvas compositor.
There are no viewer basemap dependencies or external CDN assets.

NITF attachments resolve `IALVL` through unique `IDLVL` values, recursively accumulating
`ILOC_ROW`/`ILOC_COLUMN`. Broken chains, duplicate levels and cycles are rejected as layout
evidence. The supported NCDRD profile uses unit `IMAG`; other magnifications are not
interpreted. Valid independently georeferenced images can still register geographically.
Unregistered independent images/groups remain visible in separated image coordinates,
with explicit labels. A single image without geographic registration uses its original
pixel coordinates. RPC/DEM orthorectification is not implemented.

## Cloud representations

Supported and tested: NCDRD STDI-0006, 18 February 2010, **Byte raster grids** identified
by `ICAT=CLOUD` plus image-scoped CSCCGA. `IREP=NODISPLY` does not exclude these auxiliaries.
CSCCGA provenance/scope is retained using existing TRE inventory reconciliation across
GDAL domains. `REG_SENSOR` resolves an unambiguous PAN/MS synthetic image. One-based
origin fields become zero-based pixel edges; cell sizes are in reference-image pixels,
and grid dimensions must match the raster. The grid spans the complete synthetic image,
including its constituent attachment offsets. It requires no independent geotransform.
Cloud cells use the reference image's map transformation, including nonlinear reprojection
and GCP placement. Ambiguous reference sensors, unsupported metadata/encodings or failed
transformations produce an explicit unsupported layer; imagery still loads.

Cloud-grid values are categorical: **0 clear (transparent)**, **255 cloud (cyan)**,
**1–254 reserved (magenta)**. Reserved values are not probabilities. Both GDAL downsampling
and browser rendering use nearest-neighbor sampling without contrast stretching or
smoothing of classes. A cloud grid is one layer, never a duplicate opaque image.

Also supported and tested: NITF 2.1 **CSSHPA DES v01**, `SHAPE_USE=CLOUD_SHAPES`,
`SHAPE_CLASS=POLYGON`, with three SHP/SHX/DBF component offsets. `IMAGE_SHAPE` footprints
are not clouds. The reader bounds the directory, DES subheaders, payload sizes and
component ranges before temporary extraction. GDAL decodes polygons; WGS84 vertices
are transformed into the viewer plane. Holes and multipart polygons are retained.
When both representations exist, shapes default to outlines to avoid double cloud fill;
without a supported grid they use tinted fill. Shapes have their own opacity control.
Extraction is temporary and never imported into the catalog.

Limits: 8 MiB per CSSHPA payload, 16 MiB total extracted payloads, 8 MiB decoded GeoJSON,
1,000 polygons, 1,000 nodes per polygon, 100,000 vertices per viewer geometry response.
Other NITF versions and DES versions/shape uses and ambiguous metadata are reported without fabricating
clouds. A scene cloud-cover percentage alone is not spatial data. Absence, unsupported
representation, decoding failure, and unavailable registration have separate UI/API
states. NCDRD profile assessment remains distinct from conformance; this implementation
does not claim support for every vendor or historical profile variant.

## Rendering, security and resource configuration

Declared RGB color interpretation is respected; palette and grayscale imagery are
supported. Ambiguous multispectral data defaults to band 1 grayscale. Byte and signed/
unsigned 16/32-bit integer samples use stable declared-range scaling (NBITS when exposed),
not a per-request statistics scan. This can give low contrast when values use only a
small part of their declared range. Floating/complex sample types are explicit unsupported
layers. Alpha, palettes and GDAL validity masks remain separate from intensity scaling;
valid black pixels stay opaque. Missing installed codecs produce per-layer errors.

Rendering uses `exec.CommandContext`, separate arguments, bounded diagnostics, cancellation,
and private temporary directories. A bounded Float32 ENVI buffer holds only the display
plan; its dummy affine is discarded and never used for registration. Source PAM writes
are disabled. GDAL can read existing overviews; it creates none. Temporary storage is
removed on success, failure and cancellation. Every viewer request remains read-only with
respect to catalog/source data, so the existing `read_only` capability is unchanged.

| Variable | Default | Allowed range / meaning |
| --- | --- | --- |
| `AARDE_VIEWER_MAX_DIMENSION` | `4096` | 64–8192, each display dimension |
| `AARDE_VIEWER_LAYER_PIXELS` | `8000000` | 4096–32000000 display pixels per layer |
| `AARDE_VIEWER_SCENE_PIXELS` | `24000000` | At least the layer budget, at most 96000000 |
| `AARDE_VIEWER_CONCURRENT` | `2` | 1–8 request-scoped backend operations |
| `AARDE_VIEWER_TIMEOUT` | `20s` | 1s–20s, within the HTTP server timeout |
| `AARDE_VIEWER_TEMP_DIR` | OS temp directory | Writable scratch directory |
| `AARDE_VIEWER_SOURCE_ROOTS` | unset | Optional OS path-list of absolute source roots; `:` on Linux |

The browser loads at most two layers concurrently and releases old decoded images before
resolution replacement. Budget reporting reserves 16 bytes per display pixel for decoded
copies/texture and replacement overhead, plus a viewport compositing canvas and one
reusable viewport scratch canvas for seamless mesh rendering with layer opacity.
Every segment participates in the budget; later layers are never silently omitted.
The backend keeps at most four bounded metadata plans (no raster cache); all cache hits
still require authorization and a fresh source check. Plan metadata is capped at 16 MiB.
A full pool returns 503 with Retry-After; the user can retry a layer.

Public catalog browsing does **not** authorize viewer manifests or content. Every request
uses the existing bearer-token middleware. Pixels are fetched with an Authorization header
and decoded to ImageBitmap; tokens never enter URLs. Pending requests/queues are aborted
and bitmaps/canvas resources released on navigation, query changes, logout, resolution
replacement or unmount. Physical paths/selectors never appear in viewer manifests.
Catalog-resolved sources must be absolute regular local files, with symlinks rejected in
all path components. Optional roots provide further containment. Fingerprints include
catalog checksum, file identity/size/modification/change time and matching support-file
identities; reads/access times do not change revisions. Changed source content returns
409 before delivery, preventing stale manifest/pixel combinations.

The Compose deployment keeps `/data` read-only, restricts viewer roots to `/data`, and
provides a 768 MiB temporary tmpfs, 2 GiB memory and two CPUs. Runtime GDAL cache defaults
to 64 MiB and one thread. Review these together before raising rendering budgets or
concurrency. A tmpfs counts toward container memory. Large/expensive compressed scenes
can time out even when their display size is small; lower resolution or investigate the
codec/source, rather than adding a tile service.

## API

All endpoints require `Authorization: Bearer <web-token>`:

```text
GET /api/v1/imagery/default/scene-001/viewer?resolution=auto
GET /api/v1/imagery/default/scene-001/viewer/layers/segment-0/image.png?resolution=auto&revision=<manifest-revision>
GET /api/v1/imagery/default/scene-001/viewer/layers/cloud-shapes-0/geometry?resolution=auto&revision=<manifest-revision>
```

Use the manifest's content URLs, including its resolution/revision. Layer IDs resolve
only within that parent file. Geometry is a bounded FeatureCollection in **manifest viewer
coordinates**, not RFC7946 longitude/latitude. The manifest includes original/display sizes,
mesh/extent, source assessment, role/index, opacity/order, rendering notes, legend,
registration status, warnings and unsupported reasons. OpenAPI documents response types
and structured 400/401/404/408/409/415/422/500/503/504 errors. No tile/job APIs exist.

## Verification and smoke test

```sh
# Normal project checks (native tests require gdal-bin; Python GDAL creates tiny fixtures).
source activate
go test ./...
go vet ./...
# Full isolated GDAL/PostGIS + race suite:
make integration
cd web
npm test
npm run build
# Optional browser regression suite, using an installed Playwright + Chromium:
npm run test:browser
# PLAYWRIGHT_MODULE may point to an existing Playwright index.mjs.
```

The browser suite uses deterministic HTTP fixtures against the production bundle and
checks actual composited pixels, direct URL/auth/history/refresh, request counts and cloud
failure/retry. Separate native Go tests generate real TIFF/NITF/CSCCGA/CSSHPA fixtures,
exercise the HTTP contract and inspect actual decoded pixels/placement. No large fixture
binaries are checked in.

1. Open Image Viewer from a catalog image-list entry.
2. Copy its URL and open it directly in another tab; sign in if prompted.
3. Verify every imagery segment and supported cloud layer loads automatically.
4. Pan, zoom, hide layers and adjust independent opacity sliders.
5. Confirm DevTools Network shows no additional image requests for those interactions.
6. Open an NCDRD fixture and check known cloud cells against both halves of its split
   reference image; verify clear cells stay transparent and reserved cells are magenta.
7. Change cloud opacity and imagery opacity independently; exercise Fit layer, resolution
   replacement and a per-layer retry without resetting other successful layers.
8. Compare source/sidecar checksums and directory contents: no COG, tile pyramid,
   persistent derivative or changed source file should exist.

References checked against the GDAL 3.6.2 runtime and installed OpenLayers 10 APIs:
[GDAL NITF](https://gdal.org/en/stable/drivers/raster/nitf.html),
[whole-image translate](https://gdal.org/en/stable/programs/gdal_translate.html),
[OpenLayers static image](https://openlayers.org/en/latest/examples/static-image.html),
[NCDRD 2010](https://csda-maxar-pdfs.s3.amazonaws.com/NCDRD_18February2010.pdf)
(sections 2.1.4.2, 2.2.3, 2.4.1, 2.5.1, 2.6.2, 3.1 and table 4.2-1).
