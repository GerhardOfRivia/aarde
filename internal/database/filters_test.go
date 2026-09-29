package database

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/api"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
)

func TestCombinedCatalogFilters(t *testing.T) {
	ctx, _, service, cat := testDB(t)
	start := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	early, late, outsideTime := start.Add(-time.Microsecond), end.Add(-time.Microsecond), end
	zero, fraction, boundary, full := 0.0, 19.9, 20.0, 100.0
	outside := `{"type":"Polygon","coordinates":[[[5,5],[6,5],[6,6],[5,5]]]}`
	fixtures := []struct {
		id, catalog, shape string
		acquired           *time.Time
		cloud              *float64
	}{
		{"zero", cat, rectangle, &start, &zero},
		{"fraction", cat, rectangle, &late, &fraction},
		{"boundary", cat, rectangle, &start, &boundary},
		{"full", cat, rectangle, &start, &full},
		{"unknown", cat, rectangle, &start, nil},
		{"undated", cat, rectangle, nil, &zero},
		{"before", cat, rectangle, &early, &zero},
		{"after", cat, rectangle, &outsideTime, &zero},
		{"outside", cat, outside, &start, &zero},
		{"other", cat + "-other", rectangle, &start, &zero},
	}
	for _, f := range fixtures {
		item := model(t, f.catalog, cat+"-"+f.id, f.shape)
		item.AcquiredAt, item.CloudCover = f.acquired, f.cloud
		if _, _, err := service.Import(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	h := api.Routes(service, "test", api.Access{PublicRead: true}, "none")
	for _, tc := range []struct {
		name    string
		filters map[string]any
		want    []string
	}{
		{"inclusive zero", map[string]any{"cloud_cover_lte": 0}, []string{"zero"}},
		{"inclusive boundary", map[string]any{"cloud_cover_lte": 20}, []string{"zero", "fraction", "boundary"}},
		{"inclusive full", map[string]any{"cloud_cover_lte": 100}, []string{"zero", "fraction", "boundary", "full"}},
		{"legacy strict", map[string]any{"cloud_cover_lt": 20}, []string{"zero", "fraction"}},
		{"legacy strict zero", map[string]any{"cloud_cover_lt": 0}, []string{}},
		{"include unknown", map[string]any{"cloud_cover_lte": 20, "cloud_cover_unknown": "include"}, []string{"zero", "fraction", "boundary", "unknown"}},
		{"strict include unknown", map[string]any{"cloud_cover_lt": 20, "cloud_cover_unknown": "include"}, []string{"zero", "fraction", "unknown"}},
		{"only unknown", map[string]any{"cloud_cover_unknown": "only"}, []string{"unknown"}},
		{"known only", map[string]any{"cloud_cover_unknown": "exclude"}, []string{"zero", "fraction", "boundary", "full"}},
		{"any", map[string]any{}, []string{"zero", "fraction", "boundary", "full", "unknown"}},
	} {
		for _, method := range []string{"GET", "POST"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				// IDs prevent the nonspatial endpoint from matching the outside fixture;
				// POST also proves the area predicate intersects with the same ID group.
				ids := []string{}
				for _, f := range fixtures {
					if f.id != "outside" {
						ids = append(ids, cat+"-"+f.id)
					}
				}
				seen := []string{}
				for offset := 0; ; offset++ {
					values := url.Values{"catalog_id": {cat}, "image_id": ids, "acquired_from": {start.Format(time.RFC3339Nano)}, "acquired_before": {end.Format(time.RFC3339Nano)}, "limit": {"1"}}
					body := map[string]any{"catalog_id": cat, "image_ids": ids, "geometry": json.RawMessage(rectangle), "acquired_from": start.Format(time.RFC3339Nano), "acquired_before": end.Format(time.RFC3339Nano), "limit": 1, "offset": offset}
					b, _ := json.Marshal(offset)
					values.Set("offset", string(b))
					for k, v := range tc.filters {
						body[k] = v
						if str, ok := v.(string); ok {
							values.Set(k, str)
						} else {
							b, _ := json.Marshal(v)
							values.Set(k, string(b))
						}
					}
					path, raw := "/imagery?"+values.Encode(), ""
					if method == "POST" {
						path = "/imagery/search"
						b, _ := json.Marshal(body)
						raw = string(b)
					}
					w := httptest.NewRecorder()
					h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(raw)))
					if w.Code != 200 {
						t.Fatalf("%d %s", w.Code, w.Body.String())
					}
					var page api.SearchResponse
					if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
						t.Fatal(err)
					}
					if page.Offset != offset || page.Limit != 1 {
						t.Fatal("pagination lost")
					}
					for _, item := range page.Items {
						seen = append(seen, strings.TrimPrefix(item.ImageID, cat+"-"))
					}
					if page.HasMore != (offset+len(page.Items) < len(tc.want)) {
						t.Fatalf("has_more=%v offset=%d want=%v", page.HasMore, offset, tc.want)
					}
					if !page.HasMore {
						break
					}
					if offset > len(fixtures) {
						t.Fatal("unbounded pages")
					}
				}
				slices.Sort(seen)
				want := slices.Clone(tc.want)
				slices.Sort(want)
				if !slices.Equal(seen, want) {
					t.Fatalf("got %v want %v", seen, want)
				}
			})
		}
	}
	// Open date bounds and no bounds must preserve their distinct null semantics.
	for _, tc := range []struct {
		from, before *time.Time
		want         []string
	}{
		{&start, nil, []string{"zero", "after"}},
		{nil, &end, []string{"zero", "before"}},
		{nil, nil, []string{"zero", "before", "after", "undated"}},
	} {
		page, err := service.Search(ctx, catalog.Query{CatalogID: cat, ImageIDs: []string{cat + "-zero", cat + "-before", cat + "-after", cat + "-undated"}, AcquiredFrom: tc.from, AcquiredBefore: tc.before})
		if err != nil {
			t.Fatal(err)
		}
		got := []string{}
		for _, item := range page.Items {
			got = append(got, strings.TrimPrefix(item.ImageID, cat+"-"))
		}
		slices.Sort(got)
		slices.Sort(tc.want)
		if !slices.Equal(got, tc.want) {
			t.Fatalf("open bounds got %v want %v", got, tc.want)
		}
	}
	// Boundary contact is an intersection; the outside shape and other catalog
	// must not leak through the OR branch for unknown cloud cover.
	contact := geometry(t, `{"type":"Polygon","coordinates":[[[2,0],[3,0],[3,2],[2,2],[2,0]]]}`)
	page, err := service.Search(ctx, catalog.Query{CatalogID: cat, Geometry: &contact, AcquiredFrom: &start, AcquiredBefore: &end, CloudCoverLTE: &zero, CloudCoverUnknown: "include"})
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, item := range page.Items {
		got = append(got, strings.TrimPrefix(item.ImageID, cat+"-"))
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{"unknown", "zero"}) {
		t.Fatalf("intersection + cloud grouping: %v", got)
	}
}
