package catalog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/GerhardOfRivia/aarde/internal/geo"
)

// Emulate an insert committed between checksum lookup and ID lookup. This
// tests duplicate policy only; spatial correctness uses a real PostGIS store.
type concurrentStore struct {
	Store
	existing Imagery
}

func (s concurrentStore) ByChecksum(context.Context, string, string) (Imagery, error) {
	return Imagery{}, ErrNotFound
}
func (s concurrentStore) Get(context.Context, string, string) (Imagery, error) {
	return s.existing, nil
}

func TestDuplicateCommittedBetweenReads(t *testing.T) {
	g, err := geo.Parse([]byte(`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`))
	if err != nil {
		t.Fatal(err)
	}
	i := Imagery{CatalogID: "default", ImageID: "one", Width: 1, Height: 1, BandCount: 1, SourceCRS: "EPSG:4326", AssetLocation: "/data/one.tif", Checksum: strings.Repeat("a", 64), Metadata: json.RawMessage(`{}`), Footprint: g}
	service := New(concurrentStore{existing: i})
	_, existing, err := service.Import(context.Background(), i)
	if err != nil || !existing {
		t.Fatalf("concurrent duplicate was treated as a conflict: %v %v", existing, err)
	}
}
