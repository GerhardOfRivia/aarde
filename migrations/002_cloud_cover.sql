-- Retain the historical migration name for catalogs initialized before cloud
-- cover was folded into 001; new catalogs already have this column.
ALTER TABLE imagery ADD COLUMN IF NOT EXISTS cloud_cover DOUBLE PRECISION
    CONSTRAINT imagery_cloud_cover_range CHECK (cloud_cover >= 0 AND cloud_cover <= 100);
