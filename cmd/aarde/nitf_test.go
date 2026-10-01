package main

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/database"
	"github.com/GerhardOfRivia/aarde/internal/raster"
	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestNITFCLI(t *testing.T) {
	testutil.RequireGDAL(t)
	t.Setenv("AARDE_DATABASE_URL", "")
	dir := t.TempDir()
	single := filepath.Join(dir, "scene.nitf")
	multi := filepath.Join(dir, "multi.ntf")
	testutil.NITF(t, single)
	testutil.MultiNITF(t, multi)
	var output strings.Builder
	if err := run(context.Background(), []string{"inspect", single}, &output); err != nil {
		t.Fatal(err)
	}
	var inspected raster.Inspection
	if err := json.Unmarshal([]byte(output.String()), &inspected); err != nil || inspected.Format != "NITF" || inspected.AssetLocation != single {
		t.Fatalf("single inspection JSON: %s", output.String())
	}
	output.Reset()
	if err := run(context.Background(), []string{"inspect", multi}, &output); err != nil || !strings.Contains(output.String(), `"segments"`) {
		t.Fatalf("multi inspect: %v", err)
	}
	output.Reset()
	if err := run(context.Background(), []string{"import", dir, "--recursive", "--dry-run", "--catalog", "example"}, &output); err != nil {
		t.Fatal("mixed dry run failed")
	}
	for _, want := range []string{"offline", multi, "Failed: 0", "Would import: 2", "example/scene"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("missing %s: %s", want, output.String())
		}
	}
	if os.Getenv("AARDE_TEST_DATABASE_URL") == "" {
		t.Log("database-assisted CLI dry run covered by make integration")
		return
	}
	ctx := context.Background()
	db, err := database.Open(ctx, os.Getenv("AARDE_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(os.Getenv("AARDE_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("default_transaction_read_only", "on")
	u.RawQuery = q.Encode()
	t.Setenv("AARDE_DATABASE_URL", u.String())
	output.Reset()
	if err := run(ctx, []string{"import", dir, "--recursive", "--dry-run", "--catalog", "nitf-cli-dry"}, &output); err != nil || !strings.Contains(output.String(), "Would import: 2") || !strings.Contains(output.String(), "Failed: 0") {
		t.Fatalf("database dry run: %v %s", err, output.String())
	}
}
