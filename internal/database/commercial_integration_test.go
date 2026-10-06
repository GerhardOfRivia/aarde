package database

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/importer"
	"github.com/GerhardOfRivia/aarde/internal/testutil"
)

func TestPostGISCommercialNITF(t *testing.T) {
	ctx, _, service, cat := testDB(t)
	testutil.RequireGDAL(t)
	dir := t.TempDir()
	for _, fixture := range []struct {
		name   string
		clouds []string
	}{{"known", []string{"001"}}, {"mixed", []string{"001", "100"}}} {
		testutil.CommercialNITF(t, filepath.Join(dir, fixture.name+".ntf"), fixture.clouds, false)
	}
	runner := importer.Runner{Catalog: service}
	summary, err := runner.Run(ctx, dir, importer.Options{Catalog: cat})
	if err != nil || summary.Imported != 2 {
		t.Fatal(summary, err)
	}
	original, err := service.Get(ctx, cat, "known")
	if err != nil || original.CloudCover == nil || *original.CloudCover != 1 || len(original.Segments) != 1 || !strings.Contains(string(original.Metadata), `"status": "likely"`) && !strings.Contains(string(original.Metadata), `"status":"likely"`) {
		t.Fatal(original, err)
	}
	threshold := 2.0
	page, err := service.Search(ctx, catalog.Query{CatalogID: cat, CloudCoverLT: &threshold})
	if err != nil || len(page.Items) != 1 || page.Items[0].ImageID != "known" {
		t.Fatal(page, err)
	}
	page, err = service.Search(ctx, catalog.Query{CatalogID: cat, CloudCoverUnknown: "only"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ImageID != "mixed" {
		t.Fatal(page, err)
	}
	zero := 0.0
	summary, err = runner.Run(ctx, filepath.Join(dir, "known.ntf"), importer.Options{Catalog: cat, CloudCover: &zero})
	if err != nil || summary.Existing != 1 {
		t.Fatal(summary, err)
	}
	duplicate, _ := service.Get(ctx, cat, "known")
	if string(duplicate.Metadata) != string(original.Metadata) || *duplicate.CloudCover != 1 {
		t.Fatal("duplicate refreshed metadata")
	}
	summary, err = runner.Run(ctx, filepath.Join(dir, "known.ntf"), importer.Options{Catalog: cat + "-override", CloudCover: &zero})
	if err != nil || summary.Imported != 1 {
		t.Fatal(summary, err)
	}
	override, _ := service.Get(ctx, cat+"-override", "known")
	if *override.CloudCover != 0 || !strings.Contains(string(override.Metadata), "explicit_override") {
		t.Fatal(override)
	}
	if err = os.Rename(dir, dir+"-offline"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir + "-offline") })
	h := api.Routes(service, "test", api.Access{PublicRead: true}, "none")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/imagery/"+cat+"/known", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response api.ImageryResponse
	if err = json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Format == nil || *response.Format != "NITF" || len(response.Segments) != 1 || !strings.Contains(string(response.Segments[0].Metadata), "PIAIMC.CLOUDCVR") || !strings.Contains(string(response.Metadata), "commercial_nitf") {
		t.Fatal(response)
	}
}
