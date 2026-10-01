-- Upgrade both original catalogs and catalogs initialized with cloud cover in 001.
ALTER TABLE imagery ADD COLUMN IF NOT EXISTS cloud_cover DOUBLE PRECISION
 CONSTRAINT imagery_cloud_cover_range CHECK (cloud_cover >= 0 AND cloud_cover <= 100);
ALTER TABLE imagery ADD COLUMN segments JSONB NOT NULL DEFAULT '[]';
ALTER TABLE imagery DROP CONSTRAINT imagery_width_check;
ALTER TABLE imagery DROP CONSTRAINT imagery_height_check;
ALTER TABLE imagery DROP CONSTRAINT imagery_band_count_check;
ALTER TABLE imagery ADD CONSTRAINT imagery_dimensions_check CHECK (jsonb_typeof(segments) = 'array' AND (
 (jsonb_array_length(segments) > 1 AND width = 0 AND height = 0 AND band_count = 0 AND source_crs = '') OR
 (jsonb_array_length(segments) <= 1 AND width > 0 AND height > 0 AND band_count > 0)));
