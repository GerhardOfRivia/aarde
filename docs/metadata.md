# Metadata and XML sidecars

[Documentation index](README.md) · [Project README](../README.md)

These rules apply to `aarde inspect` and [imports](cli.md#inspect-and-import).
[NITF records](nitf.md#multi-image-nitf-records) also expose per-segment metadata;
[commercial NITF metadata](commercial-nitf.md) documents TRE mappings and provenance.

## Cloud cover

Cloud cover is an optional percentage from 0 to 100; `0` means clear and `NULL` means unknown. The inspector reads numeric values from `CLOUD_COVER`, `CLOUD_COVER_PERCENTAGE`, and `EO:CLOUD_COVER` (case-insensitive, in that priority order) in GDAL metadata domains. Associated NITF `PIAIMC.CLOUDCVR` is a fallback after those fields: `000`–`100` are percentages and `999` is unknown. Missing, non-finite, or out-of-range metadata values stay unknown. `aarde import <path> --cloud-cover 12.5` overrides metadata for every file in that import, including during dry runs. Duplicate imports keep the original record and cloud cover. Existing rows remain unknown after migration; cloud cover is not calculated from pixels.

## Acquisition time

Acquisition time is separate from import/creation time. For embedded GDAL metadata, timezone-qualified RFC3339 values under `ACQUISITION_DATETIME`, `ACQUISITION_TIME`, `SENSING_TIME`, and `TIFFTAG_DATETIME_ORIGINAL` take precedence, in that order (case-insensitive keys; domains and matching keys sorted lexically). If none is valid, NITF 2.1 (`NITF_FHDR=NITF02.10`) uses a strictly valid, 14-digit `NITF_IDATIM` in `CCYYMMDDhhmmss` UTC format. Unknown, invalid, placeholder, and unsupported legacy encodings stay `NULL`; no host-local timezone is assumed. `NITF_FDT`, generic `TIFFTAG_DATETIME`, filesystem modification time, and import time are never acquisition-time fallbacks.

## XML metadata sidecars

Automatic XML metadata sidecars are read by `inspect` and every import mode, including individual files, directories, recursive imports, and dry runs. For `.tif`, `.tiff`, `.ntf`, and `.nitf`, Aarde looks in the same directory for the raster basename with an XML extension (extension matching is case-insensitive). An exact basename takes priority; otherwise a unique case-insensitive basename is accepted. Multiple matches at the selected priority are ambiguous and ignored with a warning. Nonregular files, including symlinks, are skipped.

The supported schema is Maxar/DigitalGlobe `isd/IMD/IMAGE`, using namespace-local element names with complete path context. `CLOUDCOVER` is a **fraction from 0 to 1**, converted to percentage by multiplying by 100: `3.000000000000000e-03` becomes **0.3%**. This rule comes from the vendor's [ISD specification v1.1.2, printed page 34](https://csda-maxar-pdfs.s3.amazonaws.com/ISD_External.pdf#page=34); `-999` means unassessed. Zero is known; nonfinite, invalid, and out-of-range values are ignored with a warning. Values below 1 are never interpreted using a heuristic.

Acquisition time uses `isd/IMD/IMAGE/FIRSTLINETIME` (first-line exposure time), then valid `isd/IMD/IMAGE/TLCTIME` (first time-tagged line-count record) as an approximate acquisition-time fallback. Both require RFC3339 timestamps with a timezone and are normalized to UTC. `GENERATIONTIME` is processing metadata and is never used for acquisition. Cloud precedence is explicit `--cloud-cover` > valid sidecar > embedded GDAL > unknown. Acquisition precedence is valid sidecar > embedded GDAL > unknown. Missing or invalid sidecar fields leave embedded values intact. For multi-image NITFs, sidecar scene values populate file-level searchable fields even when segment values differ; each segment retains its original source values. No XML-to-segment association is inferred. Repeated IMAGE groups are unsupported.

Missing XML is normal. Unreadable, malformed, unsupported, ambiguous, or oversized XML produces a visible log warning without failing a valid raster. Reads are bounded to **4 MiB** (recognized scalar fields to 1024 bytes and nesting to 64 elements), accommodating large ISD files such as the approximately 552 KB example. Parsing uses Go's `encoding/xml`, does not resolve external entities or fetch resources, and ignores unrelated paths. Recognized sidecars add compact provenance under `metadata._aarde.xml_sidecar`: absolute path, schema (`maxar-isd-imd`), SHA-256 of the XML bytes, source fields with full paths, normalized values, and the selected acquisition source. Original GDAL domains and annotations remain intact. The XML document and attitude/ephemeris arrays are not stored.

Raster checksums remain SHA-256 of the physical raster alone. Duplicate imports retain the original record and metadata: adding or changing a sidecar (or supplying a new override) **does not refresh an existing record**, including during dry runs. A metadata-refresh command is outside this implementation.
