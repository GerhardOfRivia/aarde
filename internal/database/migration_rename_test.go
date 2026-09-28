package database

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestRenamePreservesMigrationHistory(t *testing.T) {
	ctx, admin, _, cat := testDB(t)
	// Isolate legacy bookkeeping from the public schema and other test runs.
	schema := "rename_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.pool.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.pool.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	connection, err := url.Parse(os.Getenv("AARDE_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	query := connection.Query()
	query.Set("search_path", schema+",public")
	connection.RawQuery = query.Encode()
	repo, err := Open(ctx, connection.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(repo.Close)
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	service := catalog.New(repo)
	before, _, err := service.Import(ctx, model(t, cat, "preserved", rectangle))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, "ALTER TABLE "+identifier+".imagery DROP COLUMN cloud_cover"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, "DELETE FROM aarde_migrations WHERE name='002_cloud_cover.sql'"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.pool.Exec(ctx, "ALTER TABLE "+identifier+".aarde_migrations RENAME TO ruimte_migrations"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := repo.Migrate(ctx); err != nil {
			t.Fatalf("upgrade must preserve applied migrations: %v", err)
		}
	}
	after, err := service.Get(ctx, cat, "preserved")
	if err != nil || before.ID != after.ID || after.CloudCover != nil {
		t.Fatalf("catalog record was not preserved: %v", err)
	}
	var oldGone, newExists bool
	if err := repo.pool.QueryRow(ctx, "SELECT to_regclass($1) IS NULL, to_regclass($2) IS NOT NULL", schema+".ruimte_migrations", schema+".aarde_migrations").Scan(&oldGone, &newExists); err != nil {
		t.Fatal(err)
	}
	if !oldGone || !newExists {
		t.Fatal("migration history was not renamed")
	}
}
