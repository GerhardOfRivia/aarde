CREATE EXTENSION IF NOT EXISTS postgis;

CREATE TABLE imagery (
    id UUID PRIMARY KEY,
    catalog_id TEXT NOT NULL,
    image_id TEXT NOT NULL,
    display_name TEXT NOT NULL DEFAULT '',
    acquired_at TIMESTAMPTZ,
    imported_at TIMESTAMPTZ NOT NULL,
    footprint GEOMETRY(MULTIPOLYGON, 4326) NOT NULL,
    checksum TEXT NOT NULL CHECK (checksum ~ '^[0-9a-f]{64}$'),
    asset_location TEXT NOT NULL,
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    band_count INTEGER NOT NULL CHECK (band_count > 0),
    source_crs TEXT NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMPTZ NOT NULL,
    UNIQUE (catalog_id, image_id),
    UNIQUE (catalog_id, checksum),
    CHECK (NOT ST_IsEmpty(footprint) AND ST_IsValid(footprint))
);
CREATE INDEX imagery_footprint_idx ON imagery USING GiST (footprint);
CREATE INDEX imagery_checksum_idx ON imagery (checksum);
CREATE INDEX imagery_acquired_at_idx ON imagery (acquired_at);
CREATE INDEX imagery_imported_at_idx ON imagery (imported_at DESC, id);
