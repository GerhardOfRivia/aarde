ALTER TABLE imagery
    ADD COLUMN cloud_cover DOUBLE PRECISION
    CONSTRAINT imagery_cloud_cover_range CHECK (cloud_cover >= 0 AND cloud_cover <= 100);
