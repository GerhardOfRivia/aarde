# CLI reference

[Documentation index](README.md) · [Project README](../README.md)

Run these commands with the native `aarde` binary, or prefix them with
`docker compose exec aarde` inside Docker. Configure the database through
[environment variables](configuration.md#docker-and-configuration).

## Command examples

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

aarde remove -image xyz
aarde remove -catalog project-123
```

## Docker Compose

Compose mounts the host's `./data` directory at `/data` inside the container.
To import imagery from another location, replace the existing mount under
`services.aarde.volumes` in [`docker-compose.yml`](../docker-compose.yml):

```yaml
volumes:
  - /absolute/path/to/imagery:/data:ro
```

Replace `/absolute/path/to/imagery` with your host directory. The `:ro` suffix
keeps the imagery mount read-only. The container user (UID 10001) needs read
access to the files and traversal access to their directories.

From the project directory, apply the mount change and run the import inside
the container using its `/data` path:

```sh
docker compose up -d aarde
docker compose exec aarde aarde import /data/
```

Add `--recursive` to include subdirectories, or `--catalog <name>` to import
into a named catalog.

## Help, output, and exit status

Run `aarde --help` for the command list or `aarde <command> --help` for command
options. Help and version work without valid environment configuration. Running
`aarde` without arguments prints help and succeeds.

Help, version, JSON results, import progress and summaries, and removal prompts
go to stdout. Errors, per-file import failures, and diagnostic logs go to stderr.
Exit status is `0` for success (including help and cancelled removal), `1` for
operational failures, and `2` for invalid command syntax or flag values. Command
arguments are validated before configuration or database access.

Flags may precede or follow positional arguments. Use `--` to end flag parsing
for a path beginning with a dash, for example `aarde inspect -- -scene.tif`.

## Inspect and import

Flags may precede or follow the import path. `inspect` prints JSON with format, source CRS (WKT), source corners, width, height, band count, acquisition time, cloud-cover percentage, calculated EPSG:4326 footprint, SHA-256 checksum, asset path, and metadata. It does not need a database.

Imports catalog existing files without copying or changing them. Absolute paths identify assets; within Docker those are container paths, so preserve the mount layout when moving a deployment. Raster bytes are never put into PostgreSQL. `asset_location` is independent of imagery identity so managed storage can be added later. No `--copy` option is implemented.

The extensions `.tif`, `.tiff`, `.ntf`, and `.nitf` are discovered case-insensitively. GDAL must detect `GTiff` or `NITF`; renaming an arbitrary format does not make it supported. Directories are scanned one level by default; `--recursive` includes descendants. Symlinks are skipped. Unsupported files appear at debug log level and contribute to `Skipped`. A corrupt or unsupported raster counts as `Failed`, its physical path and reason are reported, processing continues, and the command exits nonzero after its summary. A directory traversal failure or cancellation aborts discovery/processing with an error.

```text
Imported: 24
Existing: 3
Failed: 1
Skipped: 5
```

## Identity and duplicate imports

The initial image ID is the filename without its extension, derived in `importer.ImageID`, for both GeoTIFF and NITF. There are no segment suffixes or segment selectors. Catalog defaults to `default`. Names must fit 255 bytes and cannot include slashes, control characters, or surrounding whitespace.

For either supported format, including a renamed copy, the same physical-file checksum within a catalog reports **already imported** and keeps the original record and asset path. An existing image ID with different bytes is a conflict, never an overwrite. The same bytes may be cataloged independently in another catalog. SHA-256 reads and covers the entire physical source file, not external sidecar metadata. Concurrent duplicate imports are also protected by database constraints.

## Dry runs

Dry run discovers, inspects, checksums, validates, derives IDs, and reports **would import**, with a separate `Would import` count. With `AARDE_DATABASE_URL`, it checks stored duplicates and PostGIS topology using read-only queries. The schema must already exist. With no URL, it works offline and clearly reports that database duplicates and topology were not checked; in-batch duplicates are still detected. It never migrates, writes to the database or source, or creates derivatives. All-image NITF validation also applies to both dry-run modes.

See [metadata and sidecars](metadata.md) for cloud-cover overrides, acquisition
timestamps, and precedence. The [NITF guide](nitf.md) covers segment validation,
georeferencing, and supported codecs.

## Search

`aarde search --cloud-cover-lt 20` (with the required `--id`) returns only imagery whose reported scene cloud cover is strictly below 20%. Omit the flag for any cloud cover; unknown values are excluded when the flag is supplied. The threshold accepts finite decimal percentages from 0 through 100, including explicit zero. It describes the scene, not a selected area.

For spatial searches, inclusive cloud thresholds, and acquisition-date filters,
use the [web catalog](user-guide.md#search-the-catalog) or [HTTP API](api.md#filters).

## Remove catalog records

`aarde remove` requires exactly one of `-image` or `-catalog`. The flags are mutually exclusive; supplying both reports an error before opening the database or deleting any records.

`aarde remove -image xyz` deletes the exact image ID from the `default` catalog without prompting. Image removal is limited to the `default` catalog. IDs are case-sensitive; matching IDs in other catalogs are preserved.

`aarde remove -catalog project-123` prompts before deleting all image records in that catalog. Type `yes` to confirm; any other response or empty input cancels. Ctrl+C also aborts the prompt. There is no confirmation-bypass flag. Catalogs exist only while they contain imagery, so deleting their last image removes them from catalog listings. A missing image or catalog reports an error. Successful catalog removal reports the number of deleted image records.

Removal requires `AARDE_DATABASE_URL` and an existing database schema. It deletes database records only, preserving source imagery and sidecar files. Removed imagery can be imported again. In Docker, use `docker compose exec aarde aarde remove -catalog project-123` to answer the prompt interactively.
