package catalog

import (
	"context"
	"encoding/json"
	"math"
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

func TestValidateCloudCover(t *testing.T) {
	if err := ValidateCloudCover(nil); err != nil {
		t.Fatal(err)
	}
	for _, value := range []float64{0, 0.5, 12.5, 100} {
		if err := ValidateCloudCover(&value); err != nil {
			t.Errorf("rejected %v: %v", value, err)
		}
	}
	for _, value := range []float64{-1, 100.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		if err := ValidateImagery(Imagery{CloudCover: &value}); err == nil || !strings.Contains(err.Error(), "cloud cover") {
			t.Errorf("accepted %v: %v", value, err)
		}
	}
}

func TestNormalizeCloudCoverLT(t *testing.T) {
	if err := NormalizeQuery(&Query{}); err != nil {
		t.Fatal(err)
	}
	for _, value := range []float64{0, 19.9, 20, 100} {
		q := Query{CloudCoverLT: &value}
		if err := NormalizeQuery(&q); err != nil || q.CloudCoverLT == nil || *q.CloudCoverLT != value {
			t.Fatalf("threshold %v was lost: %+v %v", value, q, err)
		}
	}
	for _, value := range []float64{-1, 100.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		// Invalid thresholds must fail before geometry validation or database access.
		_, err := New(nil).Search(context.Background(), Query{CloudCoverLT: &value})
		if err == nil || !strings.Contains(err.Error(), "cloud_cover_lt") {
			t.Errorf("accepted %v: %v", value, err)
		}
	}
}
