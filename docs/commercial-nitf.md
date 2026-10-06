# Commercial NITF metadata

Aarde enriches the metadata already collected by GDAL. The source format remains
`NITF`. `metadata._aarde.commercial_nitf` version 1 contains TRE occurrences,
normalized values, cloud selection, a file-profile assessment, DES coverage,
and structured diagnostics. The same namespace is used on each image segment.
Inspect and API responses include this stored JSON. Reads never open imagery.
Existing records without annotations display enrichment as unavailable. There
is no migration, reclassification, metadata backfill, or duplicate-import refresh.

A vendor name or a two-image container does not establish NCDRD. Commercial
products can contain multiple sensor images, segmented sub-images, and optional
cloud information. Image order, size and band count do not establish a role.

## References and implementation baseline

* NGA **STDI-0006, 18 February 2010**, including NE344:
  [NCDRD reference](https://csda-maxar-pdfs.s3.amazonaws.com/NCDRD_18February2010.pdf).
  Sections 2.1.1–2.1.4, 2.5 and tables 3.1–3.5 establish the identification,
  storage/semantic scope, fixed widths, units, ranges and reverse-time semantics.
* The Docker runtime/test image was inspected: **GDAL 3.6.2** (Debian Bookworm).
  The shipped `/usr/share/gdal/nitf_spec.xml` SHA-256 is
  `e40502c629a1a5bd75ddd341fa6e7fbc50b028fffa4b138fd905fd9f09285483`.
  Its five supported layouts were compared with the
  [current schema](https://raw.githubusercontent.com/OSGeo/gdal/master/frmts/nitf/data/nitf_spec.xml).
  GDAL 3.6.2 `NITFDataset::InitializeTREMetadata` and
  `NITFGenericMetadataRead` establish occurrence order and merged exposure.
* [OSSIM PIAIMC field documentation](https://raw.githubusercontent.com/ossimlabs/ossim/master/include/ossim/support_data/ossimNitfPiaimcTag.h),
  unversioned upstream header reviewed 2026-10-05, documents CLOUDCVR and
  MEANGSD. Fixed widths come from the GDAL 3.6.2 schema.
* [GDAL NITF driver documentation](https://gdal.org/en/stable/drivers/raster/nitf.html)
  documents selected domains, TREs, overflow, and optional partial validation.

These references are development inputs, never runtime network dependencies.
The dated layout does not establish the specification revision of a product.
Unknown codes and unsupported variants remain in the original metadata.

## Representation, scope and provenance

The selected domains remain default/header, IMAGE_STRUCTURE, RPC, TRE, and
xml:TRE (plus SUBDATASETS for container enumeration). The GDAL JSON values are
retained unchanged; derived data goes only under `_aarde`. Multi-image file
headers retain the existing file-header filter; container TRE/XML exposures
are retained as collected and are **not** all treated as file-scoped evidence.

The inventory supports GDAL XML strings, arrays or objects of documents,
raw `TRE` objects (including `TAG_2`, `TAG_3` occurrences), and flattened
`NITF_<six-character-tag>_<field>` fields. XML is preferred. Fixed-width ASCII
fallbacks are limited to CSDIDA (70 bytes), CSEXRA (132), PIAIMC (362), CSCRNA
(109), and CSCCGA (60). They operate on GDAL's already-exposed payloads, not on
binary NITF headers. Wrong widths, truncated/binary payloads and unknown TREs
remain preserved; unsupported fields are not guessed.

Each record has an ID, tag, occurrence, actual storage scope (`file`,
`image_segment`, `overflow`, `unresolved`), semantic scope (`dataset`,
`sensor_sub_image`, `segment`, `unresolved`), available image index,
observation index, association attributes, and source representations.
GDAL's XML `location` is used, not the expected location from its schema.
In particular the schema's CSCCGA expected location cannot establish an image
role. XML-associated, valid CSCCGA in an image establishes `cloud_grid`.
No metadata requirements are imposed on cloud grids as if they were primary
sensor images. Cloud pixels are never read for percentage estimation.

Identical repeated occurrences stay distinct. Raw/decoded occurrences are
matched one-to-one by decoded fields. For other GDAL-decoded tags, raw/XML
occurrence order is paired only when tag counts agree. A flattened representation
matching multiple occurrences is attached once with an ambiguity warning;
disagreeing representations remain unresolved with a conflict diagnostic.
File/overflow records observed through selectors are stored once at file level,
with segment references. A shared sub-image TRE repeated in different images
remains explicitly `sensor_sub_image`, not independent segment measurements.

Flattened/raw-only records cannot establish storage location. Their values are
normalized with uncertainty, but unresolved PIAIMC is not promoted to segment
cloud cover. This deliberately sacrifices a fallback when GDAL cannot associate
it safely; usual GDAL 3.6.2 output supplies XML location alongside these fields.

Every normalized value retains the original value/units, normalized value/units,
TRE/field/domain/path, record occurrence, semantic scope, available image
association, and warnings. Other decoded fields remain in the inventory. Raw payloads are available through
source paths in the unchanged original domains; supported small payloads also
appear in the inventory. Opaque/ephemeris raw payloads are not duplicated. Codes stay strings; there are no satellite-name
or processing-level display guesses. RPC00A/B, STDIDC, USE00A, CSPROA and other
extensions are inventoried without requiring comprehensive normalization.

## Mappings and unknown values

| TRE | Normalized fields | Units and rules |
| --- | --- | --- |
| CSDIDA | Platform code, vehicle, sensor/product identifiers, pass/operation, software, day/month/year | Strings, including leading zeros. Unknown codes unchanged. |
| CSDIDA | TIME and PROCESS_TIME | Separate dataset collection and processing UTC timestamps. Full valid `YYYYMMDDhhmmss` only; invalid/zero dates are null. Date-only components never fabricate a timestamp. |
| CSEXRA | SENSOR | Reported sensor designation. |
| CSEXRA | MAX_GSD, ALONG_SCAN_GSD, CROSS_SCAN_GSD, GEO_MEAN_GSD, A_S_VERT_GSD, C_S_VERT_GSD, GEO_MEAN_VERT_GSD | Separate measurements, 0–999.9 inches × **0.0254** to meters; blank/N/A/invalid null. Zero preserved. Not output pixel spacing. |
| CSEXRA | ANGLE_TO_NORTH, AZ_OF_OBLIQUITY, SUN_AZIMUTH | 0–360 degrees. |
| CSEXRA | GSD_BETA_ANGLE, OBLIQUITY_ANGLE, SUN_ELEVATION | Respectively 0–180, 0–90, −90–90 degrees. |
| CSEXRA | TIME_FIRST_LINE_IMAGE | 0–86400 seconds from midnight for the synthetic array; date/segment association remains unresolved. No full timestamp is synthesized. |
| CSEXRA | TIME_IMAGE_DURATION | −9999.999999–86400 seconds. Negative values preserved and annotated with reverse-order semantics from table 3.5-1 note 1. |
| PIAIMC | CLOUDCVR | Three integer digits: 000–100 percent, 999 unknown. No fractional heuristic. |
| PIAIMC | SENSNAME, SENSMODE | Reported strings. |
| PIAIMC | MEANGSD | 0–99999.9 inches × 0.0254 to meters. |
| CSCRNA | PREDICT_CORNERS | Y/N becomes true/false; unknown null, raw code retained. |
| CSCRNA | ULCNR/URCNR/LRCNR/LLCNR LAT, LONG, HT | Reported sub-image corners: latitude −90–90°, longitude −179.99999–180°, height −610–10668 m above WGS84 ellipsoid. Unknown/invalid values null. |

All numeric conversions require finite decimal values in the supported range.
Reported corners never become catalog footprints or geotransforms. CSDIDA
collection time can describe multiple images and, for reverse ordering, the
chronological imaging end. It is not substituted for per-segment acquisition.
Existing acquisition fields/NITF_IDATIM, sidecar precedence, georeferencing
validation, RPC-only rejection and footprint union are unchanged.

## Cloud precedence

1. Valid explicit import `--cloud-cover` (file level only).
2. Valid matched Maxar XML `isd/IMD/IMAGE/CLOUDCOVER` (file level only).
3. Existing embedded percentage keys, in existing priority:
   CLOUD_COVER, CLOUD_COVER_PERCENTAGE, EO:CLOUD_COVER; sorted domain/key ties.
4. Associated PIAIMC.CLOUDCVR, in retained occurrence order.

Malformed sources do not block lower-priority valid sources. The cloud selection
lists sources, original values, normalized values, warnings, the selected source,
and conflicts. PIAIMC `001` is **1%**, `000` is **0%**, and `999` is null. Maxar
XML is separately normalized from its documented **0–1 fraction** to percent.

Segments normalize independently. With multiple images, embedded file cloud
cover is known only if **every segment** has the same known percentage; otherwise
it is null. Different percentages are not averaged and image zero is not used as
the aggregate. Sidecar/explicit overrides do not change segment values. An
invalid sidecar preserves the aggregate, including null. Existing cloud filter
behavior operates on the resulting file value.

## Profile recognition rules (`aarde-ncdrd-1`)

The candidate is `NCDRD`; status is `likely`, `possible` or `unknown`.
Conformance is always **`not_evaluated`**. Each segment displays the same **file**
assessment, alongside its own values and role.

`likely` requires all of:

* A file-scoped CSDIDA with the complete supported layout: required widths,
  numeric date/vehicle/pass/operation, two-character alphanumeric identifiers,
  valid collection and processing times, date components consistent with TIME,
  and the documented reserved markers. Vendor codes need not be in a guessed
  satellite/product dictionary.
* Image-associated CSEXRA with a nonblank SENSOR and valid MAX_GSD, on an image
  whose IID1 starts with documented `P1`–`P9` or `M1`–`M9` (table 2.1-3).
* A compatible NITF02.10 header on that image, no contradictory exposed version,
  conflicting file CSDIDA or representation conflicts, and no XML/layout/limit
  diagnostics indicating incomplete enrichment inspection.

`possible` means some CSDIDA/CSEXRA/CSCRNA/CSPROA/CSCCGA evidence exists but the
above combination is incomplete or contradictory. Conflicting and missing
information have separate lists. `unknown` means none of that specific evidence
was established. Generic RPC, STDIDC, USE00A or PIAIMC alone, vendor/filename text,
NITF version or arbitrary `NCDRD` text never suffice. IID2 remains unparsed and
is listed as uninspected; no layout or product revision is inferred from GDAL.
An unknown profile does not suppress useful normalization or prove non-NCDRD.

## Limits and DES coverage

No extra GDAL subprocesses or open options are added. Existing cancellation,
two-minute inspection deadline, 16 MiB GDAL/combined segment metadata limits,
and 16 KiB stderr cap remain. Enrichment additionally limits XML/individual raw
payloads to 1 MiB, XML depth to 32, attributes to 4096 bytes, decoded fields to
256 per record, inventory to 512 occurrences per physical file, diagnostics to
128 per annotation, and profile evidence display to 32 entries. Limit diagnostics
preserve original selected metadata. XML entities are not resolved. Optional
field failures do not reject an otherwise acceptable raster. Structural,
image-count, subprocess-output, and spatial-validation failures retain existing
failure behavior.

DES inventory distinguishes `present`, `absent`, and `not_inspected`. TRE XML
`location="des <identifier>"` inventories exposed identifiers/attributes, including
TRE_OVERFLOW, without claiming a complete inventory or inferred DESITEM mapping.
Only an explicitly available zero NUMDES establishes absence. The usual selected
GDAL 3.6.2 domains do not expose such a count, so absence is not assumed. CSATTA,
CSSHPA and opaque payloads not exposed by these domains remain uninspected.

`xml:DES` is deliberately not requested because identification does not justify
reading/persisting potentially large payloads. GDAL 3.6.2 has no VALIDATE open
option; GDAL 3.7+ validation is optional and partial, and is not enabled here.
There is no sensor-model reconstruction, attitude/ephemeris processing, embedded
shapefile extraction, NCDRD certification, or bypass of image validation.

## Tests

Pure table-driven tests cover representations/scopes/repetition, numeric and
time rules, profile evidence, malformed/oversized XML and raw TREs, cloud
precedence and aggregation. GDAL fixtures cover ordinary NITF, commercial TREs,
multiple image segments, and associated CSCCGA. PostGIS tests cover persistence,
filters, unchanged duplicate imports, explicit overrides, API serialization and
reads with the source unavailable. Frontend tests render the component and
verify escaping and old-record behavior. Fixtures are generated locally without
customer imagery or runtime downloads; they are not certified NCDRD products.

Use `make integration` for the full GDAL/PostGIS suite, and `npm test` plus
`npm run build` in `web` for frontend checks. `go test ./...` skips integration
coverage when the corresponding GDAL tools/database are unavailable.
