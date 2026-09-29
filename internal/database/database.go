package database

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/geo"
	"github.com/GerhardOfRivia/aarde/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository struct{ pool *pgxpool.Pool }

func Open(ctx context.Context, url string) (*Repository, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, errors.New("invalid AARDE_DATABASE_URL")
	}
	cfg.MaxConns = 10
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.ConnConfig.RuntimeParams["statement_timeout"] = "15000"
	cfg.ConnConfig.RuntimeParams["application_name"] = "aarde"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, errors.New("could not configure database pool")
	}
	r := &Repository{pool: pool}
	if err := r.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	slog.Info("database connected")
	return r, nil
}
func (r *Repository) Close() { r.pool.Close() }
func (r *Repository) Ping(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return r.pool.Ping(ctx)
}

func (r *Repository) Migrate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(context.Background())
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(728364219)"); err != nil {
		return err
	}
	// Preserve the applied migration history when upgrading a Ruimte catalog.
	// Keep the same advisory lock so old and renamed binaries cannot race here.
	if _, err = tx.Exec(ctx, `DO $$
BEGIN
    IF to_regclass(format('%I.aarde_migrations', current_schema())) IS NULL
       AND to_regclass(format('%I.ruimte_migrations', current_schema())) IS NOT NULL THEN
        ALTER TABLE ruimte_migrations RENAME TO aarde_migrations;
    END IF;
END $$`); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS aarde_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())"); err != nil {
		return err
	}
	entries, err := migrations.Files.ReadDir(".")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		name := entry.Name()
		var exists bool
		if err = tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM aarde_migrations WHERE name=$1)", name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		body, err := migrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
		if _, err = tx.Exec(ctx, "INSERT INTO aarde_migrations(name) VALUES($1)", name); err != nil {
			return err
		}
		slog.Info("migration applied", "name", name)
	}
	return tx.Commit(ctx)
}

const columns = `id, catalog_id, image_id, display_name, acquired_at, imported_at,
ST_AsGeoJSON(footprint, 15), checksum, asset_location, width, height, band_count, source_crs, metadata, created_at, cloud_cover`

func scan(row pgx.Row) (catalog.Imagery, error) {
	var i catalog.Imagery
	var geometry []byte
	err := row.Scan(&i.ID, &i.CatalogID, &i.ImageID, &i.DisplayName, &i.AcquiredAt, &i.ImportedAt, &geometry, &i.Checksum, &i.AssetLocation, &i.Width, &i.Height, &i.BandCount, &i.SourceCRS, &i.Metadata, &i.CreatedAt, &i.CloudCover)
	if errors.Is(err, pgx.ErrNoRows) {
		return i, catalog.ErrNotFound
	}
	if err != nil {
		return i, err
	}
	i.Footprint, err = geo.Parse(geometry)
	return i, err
}
func (r *Repository) Get(ctx context.Context, cat, id string) (catalog.Imagery, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return scan(r.pool.QueryRow(ctx, "SELECT "+columns+" FROM imagery WHERE catalog_id=$1 AND image_id=$2", cat, id))
}
func (r *Repository) ByChecksum(ctx context.Context, cat, sum string) (catalog.Imagery, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return scan(r.pool.QueryRow(ctx, "SELECT "+columns+" FROM imagery WHERE catalog_id=$1 AND checksum=$2", cat, sum))
}
func (r *Repository) ValidateGeometry(ctx context.Context, g geo.Geometry) error {
	if err := g.Validate(); err != nil {
		return fmt.Errorf("%w: %v", catalog.ErrInvalidGeometry, err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var valid bool
	err := r.pool.QueryRow(ctx, `SELECT ST_IsValid(g) AND NOT ST_IsEmpty(g) FROM (SELECT ST_SetSRID(ST_GeomFromGeoJSON($1),4326) g) s`, string(g.JSON())).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("%w: polygon topology is invalid", catalog.ErrInvalidGeometry)
	}
	return nil
}
func (r *Repository) Insert(ctx context.Context, i catalog.Imagery) (catalog.Imagery, bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	row, err := scan(r.pool.QueryRow(ctx, `INSERT INTO imagery
(id,catalog_id,image_id,display_name,acquired_at,imported_at,footprint,checksum,asset_location,width,height,band_count,source_crs,metadata,created_at,cloud_cover)
VALUES ($1,$2,$3,$4,$5,$6,ST_Multi(ST_SetSRID(ST_GeomFromGeoJSON($7),4326)),$8,$9,$10,$11,$12,$13,$14,$15,$16)
ON CONFLICT DO NOTHING RETURNING `+columns, i.ID, i.CatalogID, i.ImageID, i.DisplayName, i.AcquiredAt, i.ImportedAt, string(i.Footprint.JSON()), i.Checksum, i.AssetLocation, i.Width, i.Height, i.BandCount, i.SourceCRS, i.Metadata, i.CreatedAt, i.CloudCover))
	if !errors.Is(err, catalog.ErrNotFound) {
		return row, false, err
	}
	// Resolve a concurrent insert using the same rules as the initial duplicate check.
	existing, err := r.ByChecksum(ctx, i.CatalogID, i.Checksum)
	if err == nil {
		return existing, true, nil
	}
	if !errors.Is(err, catalog.ErrNotFound) {
		return row, false, err
	}
	return row, false, catalog.ErrConflict
}
func (r *Repository) Catalogs(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rows, err := r.pool.Query(ctx, "SELECT DISTINCT catalog_id FROM imagery ORDER BY catalog_id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		items = append(items, s)
	}
	return items, rows.Err()
}
func (r *Repository) Search(ctx context.Context, q catalog.Query) (catalog.Page, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	page := catalog.Page{Items: []catalog.Imagery{}, Limit: q.Limit, Offset: q.Offset}
	args := []any{}
	where := " WHERE true"
	if q.CatalogID != "" {
		args = append(args, q.CatalogID)
		where += fmt.Sprintf(" AND catalog_id=$%d", len(args))
	}
	if len(q.ImageIDs) > 0 {
		args = append(args, q.ImageIDs)
		where += fmt.Sprintf(" AND image_id=ANY($%d::text[])", len(args))
	}
	if q.Geometry != nil {
		args = append(args, string(q.Geometry.JSON()))
		where += fmt.Sprintf(" AND ST_Intersects(footprint, ST_SetSRID(ST_GeomFromGeoJSON($%d),4326))", len(args))
	}
	if q.AcquiredFrom != nil {
		args = append(args, *q.AcquiredFrom)
		where += fmt.Sprintf(" AND acquired_at >= $%d", len(args))
	}
	if q.AcquiredBefore != nil {
		args = append(args, *q.AcquiredBefore)
		where += fmt.Sprintf(" AND acquired_at < $%d", len(args))
	}
	threshold, operator := q.CloudCoverLT, "<"
	if q.CloudCoverLTE != nil {
		threshold, operator = q.CloudCoverLTE, "<="
	}
	if threshold != nil {
		args = append(args, *threshold)
		predicate := fmt.Sprintf("cloud_cover %s $%d", operator, len(args))
		if q.CloudCoverUnknown == "include" {
			predicate += " OR cloud_cover IS NULL"
		}
		where += " AND (" + predicate + ")"
	} else if q.CloudCoverUnknown == "only" {
		where += " AND cloud_cover IS NULL"
	} else if q.CloudCoverUnknown == "exclude" {
		where += " AND cloud_cover IS NOT NULL"
	}
	args = append(args, q.Limit+1, q.Offset)
	query := "SELECT " + columns + " FROM imagery" + where + fmt.Sprintf(" ORDER BY imported_at DESC, id ASC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return page, err
	}
	defer rows.Close()
	for rows.Next() {
		i, err := scan(rows)
		if err != nil {
			return page, err
		}
		page.Items = append(page.Items, i)
	}
	if err := rows.Err(); err != nil {
		return page, err
	}
	if len(page.Items) > q.Limit {
		page.HasMore = true
		page.Items = page.Items[:q.Limit]
	}
	return page, nil
}
