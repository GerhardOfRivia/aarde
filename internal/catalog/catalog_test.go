package catalog

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

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

func TestNormalizeMetadataFilters(t *testing.T) {
	zero, twenty, bad := 0.0, 20.0, math.NaN()
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	for _, tc := range []struct {
		name            string
		query           Query
		policy, invalid string
	}{
		{"default", Query{}, "include", ""},
		{"inclusive zero", Query{CloudCoverLTE: &zero}, "exclude", ""},
		{"legacy default", Query{CloudCoverLT: &twenty}, "exclude", ""},
		{"include legacy unknown", Query{CloudCoverLT: &twenty, CloudCoverUnknown: "include"}, "include", ""},
		{"only unknown", Query{CloudCoverUnknown: "only"}, "only", ""},
		{"known only", Query{CloudCoverUnknown: "exclude"}, "exclude", ""},
		{"both thresholds", Query{CloudCoverLT: &zero, CloudCoverLTE: &twenty}, "", "cloud_cover_lt"},
		{"only and inclusive", Query{CloudCoverUnknown: "only", CloudCoverLTE: &zero}, "", "cloud_cover_unknown"},
		{"only and strict", Query{CloudCoverUnknown: "only", CloudCoverLT: &zero}, "", "cloud_cover_unknown"},
		{"unknown policy", Query{CloudCoverUnknown: "invalid"}, "", "cloud_cover_unknown"},
		{"nonfinite", Query{CloudCoverLTE: &bad}, "", "cloud_cover_lte"},
		{"date range", Query{AcquiredFrom: &start, AcquiredBefore: &end}, "include", ""},
		{"open from", Query{AcquiredFrom: &start}, "include", ""},
		{"open before", Query{AcquiredBefore: &end}, "include", ""},
		{"equal dates", Query{AcquiredFrom: &start, AcquiredBefore: &start}, "", "acquired_before"},
		{"reversed dates", Query{AcquiredFrom: &end, AcquiredBefore: &start}, "", "acquired_before"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := tc.query
			err := NormalizeQuery(&q)
			if tc.invalid != "" {
				if err == nil || !strings.Contains(err.Error(), tc.invalid) {
					t.Fatalf("wanted %s error, got %v", tc.invalid, err)
				}
				return
			}
			if err != nil || q.CloudCoverUnknown != tc.policy {
				t.Fatalf("%+v: %v", q, err)
			}
			if err := NormalizeQuery(&q); err != nil || q.CloudCoverUnknown != tc.policy {
				t.Fatalf("normalization not idempotent: %+v %v", q, err)
			}
		})
	}
}
