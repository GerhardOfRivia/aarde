# NITF support

[Documentation index](README.md) · [Project README](../README.md)

NITFs containing one or more image segments are supported, with one record per physical file. Primary imagery must have supported georeferencing; invalid primary imagery rejects the whole file. Cloud auxiliaries can inherit catalog coverage as described in [Native Image Viewer support](commercial-nitf.md#native-image-viewer). Zero-image files and containers whose image count cannot be established are rejected. Text, graphics, and data-extension segments do not count as images. Original files are preserved, including on read-only imagery mounts.

GDAL's [NITF container contract](https://gdal.org/en/stable/drivers/raster/nitf_advanced.html#multi-image-nitf-files) is checked using an explicit `SUBDATASETS` request on the physical filename, plus default file/image-header metadata. GDAL 3.6.2 enumerates every image when there are multiple images, omits the list for a single image, and exposes only file headers for zero images. Aarde validates complete indexed subdataset entries and image-header presence; it never derives segment count from band count. Every image is inspected with a zero-based `NITF_IM:<index>:<physical path>` selector. Locally generated zero-, one-, and three-image fixtures test this behavior. Inconsistent or unrecognized count metadata fails validation.

## Multi-image NITF records

Each physical NITF produces one catalog record, retaining its filename-based ID, original absolute asset path, and SHA-256 of the entire file. No images are extracted and no source or sidecar is written. Repeated, renamed, and concurrent imports use the existing catalog/checksum uniqueness rules.

`inspect` JSON and HTTP records expose `segments`, ordered by zero-based image index, with dimensions, band count, source CRS, WGS84 footprint, acquisition time, cloud cover, and selected GDAL metadata for each image. The UI exposes these in the Image segments disclosure. Legacy records and GeoTIFFs have no segment details; existing records are not re-inspected by the migration.

For multiple images, file-level width, height, and band count are `0` (unavailable), source CRS is empty, and inspect bounds are null. Without valid XML scene metadata, file-level acquisition time and cloud cover are populated only when every image has the same known value; differing or missing values produce unknown. Date/cloud filters operate on the file-level values, with valid XML scene metadata taking precedence over these unanimous aggregates. Segment metadata remains available even when aggregates are unknown. Single-image NITF and GeoTIFF retain their existing scalar behavior. `--cloud-cover` overrides only the file-level searchable cloud cover, including in dry runs; segment values and metadata retain the source values, and duplicate imports retain the original record.

The searchable footprint is the geometric union of primary imagery footprints, preserving holes, gaps, and disjoint regions. Cloud auxiliaries are excluded from this union; inherited auxiliary coverage is not measured cloud geometry. Multi-image inspection additionally requires `ogr2ogr` with SQLite spatial functions (included in the Docker GDAL installation). All inspection/union subprocesses and whole-file hashing share the two-minute inspection budget and existing output limits. Outside the cloud auxiliary exception above, a failed image rejects the entire file with its zero-based index; zero-image and ambiguous containers remain rejected. RPC-only imagery and unsplit antimeridian footprints remain unsupported. GCP footprints retain the existing sampled-perimeter approximation.

## Footprints and georeferencing

Footprints use this selection order:

1. A valid GDAL WGS84 extent backed by an affine transform and source CRS, or by usable GCPs and their CRS.
2. GDAL's [`gdaltransform -tps -t_srs EPSG:4326`](https://gdal.org/en/stable/programs/gdaltransform.html) on the image selector for NITF (physical source for GeoTIFF). The explicit thin-plate-spline method honors non-affine GCPs. Aarde samples 16 positions on each outer pixel edge (64 total), closes the ring, and limits the input to 256 GCPs.

The result is a catalog approximation, not evidence of orthorectification. Inspection validates longitude/latitude order and ranges, finite coordinates, closure, nonzero area, and a longitude span no greater than 180 degrees per ring. Antimeridian-crossing rings are explicitly rejected; existing split MultiPolygons can be accepted. PostGIS additionally enforces valid topology at import and in database-assisted dry runs. Offline dry runs do not check PostGIS topology. Files without usable affine/GCP georeferencing fail with an actionable error, including RPC-only files. GCP-only inspection reports the GCP CRS as `source_crs` and empty `bounds`, since GDAL's ordinary corners in that case are pixel coordinates.

## Stored metadata

Aarde deliberately requests the default/header, `IMAGE_STRUCTURE`, `RPC`, `TRE`, and `xml:TRE` metadata domains and preserves their JSON values, including objects, arrays, and XML strings. It does not request all domains, raw-header `NITF_METADATA`, text, graphics, or DES payload domains. Identifiers, source descriptions, compression, geolocation, and handling markings available in those selected domains are retained. Markings are metadata, **not implemented access controls**. The UI displays metadata as escaped text.

Commercial NITF inspection adds versioned metadata under `_aarde.commercial_nitf`: scoped TRE occurrences, commercial identifiers, reported GSD/angles/timing/corners, cloud provenance, conservative NCDRD profile evidence, and bounded diagnostics. The detail UI shows a **Commercial NITF** section at file and segment levels. Profile recognition is explicitly `likely`, `possible`, or `unknown`, never conformance certification. Segment count/order does not identify cloud grids; reported sub-image corners do not change footprints. Older records remain unavailable until explicitly inspected/imported under existing duplicate rules. See [supported mappings, detection rules, GDAL requirements, and coverage limits](commercial-nitf.md).

The reserved `metadata._aarde` namespace records the detected `format`, affine/GCP `georeferencing`, `footprint_method`, and available affine transform/GCP metadata separately from original GDAL domains. The API's additive nullable `format` field and detail view use that stored annotation. Older records report unknown format without reopening their sources; duplicates retain their original metadata. The backward-compatible segment migration preserves existing rows and uniqueness constraints. Catalog detail and search handlers use stored metadata only; [viewer endpoints](image-viewer.md#api) reopen sources to render imagery.

## GDAL requirements and inspection limits

The Docker runtime and tests use Debian Bookworm GDAL 3.6.2 with the NITF, JPEG, and JP2OpenJPEG drivers; the build checks their availability. No proprietary codecs are required. Uncompressed NITF, JPEG (`IC=C3`), and JPEG2000 (`IC=C8`, using JP2OpenJPEG) have synthetic fixture coverage. Other compression variants depend on the installed GDAL build. For local installations, check `gdalinfo --version`, `gdalinfo --format NITF`, `gdalinfo --format JPEG`, and `gdalinfo --format JP2OpenJPEG`. See the official [NITF driver and codec documentation](https://gdal.org/en/stable/drivers/raster/nitf.html).

Inspection invokes GDAL with separate arguments and `GDAL_PAM_ENABLED=NO`. Metadata collection, GCP transformation, and whole-file SHA-256 share a two-minute timeout and cancellation. Metadata stdout and combined segment JSON are capped at 16 MiB, union input/output at 16 MiB, transformer stdout at 64 KiB, and each stderr at 16 KiB. Excess output is rejected. Inspection checks source identity, size, and modification time before persistence. It requests no statistics, histograms, per-band checksums, or full pixel decoding. Successful metadata inspection is **not a full-raster integrity check**; unreadable codecs or corrupt metadata may fail before cataloging, and pixel corruption may remain undetected.

Ingestion never converts NITF to GeoTIFF or generates previews, COGs, or tiles. Orthorectification and RPC-only footprint estimation are unsupported. The separate [Image Viewer](image-viewer.md) renders temporary whole-segment display images on request.

## Generate a test fixture

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

See [metadata and XML sidecars](metadata.md) for scene-level metadata precedence and [CLI import behavior](cli.md#inspect-and-import) for discovery and duplicate handling.
