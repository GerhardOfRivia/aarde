package api

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
)

func TestCombinedFilterParsing(t *testing.T) {
	for _, policy := range []string{"", "exclude", "include", "only"} {
		for _, method := range []string{"GET", "POST"} {
			var got catalog.Query
			h := Routes(catalog.New(queryStore{query: &got}), "dev", Access{PublicRead: true}, "none")
			values := url.Values{"catalog_id": {"demo"}, "image_id": {"one", "two"}, "acquired_from": {"2026-06-01T02:00:00+02:00"}, "acquired_before": {"2026-09-30T00:00:00Z"}, "limit": {"1"}, "offset": {"2"}}
			body := map[string]any{"catalog_id": "demo", "image_ids": []string{"one", "two"}, "geometry": json.RawMessage(searchPolygon), "acquired_from": values.Get("acquired_from"), "acquired_before": values.Get("acquired_before"), "limit": 1, "offset": 2}
			if policy != "only" {
				values.Set("cloud_cover_lte", "19.9")
				body["cloud_cover_lte"] = 19.9
			}
			if policy != "" {
				values.Set("cloud_cover_unknown", policy)
				body["cloud_cover_unknown"] = policy
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
				t.Fatalf("%s %s: %d %s", method, policy, w.Code, w.Body.String())
			}
			wantPolicy := policy
			if policy == "" {
				wantPolicy = "exclude"
			}
			if got.CatalogID != "demo" || strings.Join(got.ImageIDs, ",") != "one,two" || got.Limit != 1 || got.Offset != 2 || got.CloudCoverUnknown != wantPolicy || (method == "POST" && got.Geometry == nil) {
				t.Fatalf("lost filters: %+v", got)
			}
			if got.AcquiredFrom == nil || got.AcquiredFrom.UTC().Format(time.RFC3339) != "2026-06-01T00:00:00Z" || got.AcquiredBefore == nil || got.AcquiredBefore.UTC().Format(time.RFC3339) != "2026-09-30T00:00:00Z" {
				t.Fatalf("lost dates: %+v", got)
			}
			if policy != "only" && (got.CloudCoverLTE == nil || *got.CloudCoverLTE != 19.9) {
				t.Fatalf("lost threshold: %+v", got)
			}
		}
	}
}

func TestInvalidMetadataFilters(t *testing.T) {
	h := Routes(nil, "dev", Access{PublicRead: true}, "none")
	cases := []struct {
		fields map[string]any
		field  string
	}{
		{map[string]any{"cloud_cover_lte": -1}, "cloud_cover_lte"},
		{map[string]any{"cloud_cover_lte": 100.01}, "cloud_cover_lte"},
		{map[string]any{"cloud_cover_lte": "NaN"}, "cloud_cover_lte"},
		{map[string]any{"cloud_cover_lte": ""}, "cloud_cover_lte"},
		{map[string]any{"cloud_cover_lte": 20, "cloud_cover_lt": 20}, "cloud_cover_lt"},
		{map[string]any{"cloud_cover_lte": 0, "cloud_cover_unknown": "only"}, "cloud_cover_unknown"},
		{map[string]any{"cloud_cover_lt": 0, "cloud_cover_unknown": "only"}, "cloud_cover_unknown"},
		{map[string]any{"cloud_cover_unknown": ""}, "cloud_cover_unknown"},
		{map[string]any{"cloud_cover_unknown": "sometimes"}, "cloud_cover_unknown"},
		{map[string]any{"acquired_from": "2026-02-29T00:00:00Z"}, "acquired_from"},
		{map[string]any{"acquired_before": ""}, "acquired_before"},
		{map[string]any{"acquired_from": "2026-09-29"}, "acquired_from"},
		{map[string]any{"acquired_from": "2026-09-30T00:00:00Z", "acquired_before": "2026-09-29T00:00:00Z"}, "acquired_before"},
	}
	for _, tc := range cases {
		for _, method := range []string{"GET", "POST"} {
			values := url.Values{}
			body := map[string]any{"geometry": json.RawMessage(searchPolygon)}
			for key, value := range tc.fields {
				body[key] = value
				if s, ok := value.(string); ok {
					values.Set(key, s)
				} else {
					b, _ := json.Marshal(value)
					values.Set(key, string(b))
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
			if w.Code != 400 || !strings.Contains(w.Body.String(), tc.field) {
				t.Fatalf("%s %v: %d %s", method, tc.fields, w.Code, w.Body.String())
			}
		}
	}
}

func TestSearchNullableMetadata(t *testing.T) {
	var got catalog.Query
	h := Routes(catalog.New(queryStore{query: &got}), "dev", Access{PublicRead: true}, "none")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/imagery/search", strings.NewReader(`{"geometry":`+searchPolygon+`,"image_ids":null,"cloud_cover_lte":null,"cloud_cover_unknown":null,"acquired_from":null,"acquired_before":null}`)))
	if w.Code != 200 || got.CloudCoverUnknown != "include" || got.CloudCoverLTE != nil || got.AcquiredFrom != nil || got.AcquiredBefore != nil {
		t.Fatalf("%+v: %d %s", got, w.Code, w.Body.String())
	}
}

func TestMetadataFilterContract(t *testing.T) {
	_, c := contractCompiler(t)
	schema, err := c.Compile(contractURL + "#/components/schemas/SearchRequest")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		fields string
		valid  bool
	}{
		{`"cloud_cover_lte":0`, true},
		{`"cloud_cover_lte":100,"cloud_cover_unknown":"include"`, true},
		{`"cloud_cover_lte":null,"cloud_cover_unknown":"only"`, true},
		{`"cloud_cover_lt":null,"cloud_cover_lte":20`, true},
		{`"cloud_cover_lt":20,"cloud_cover_lte":20`, false},
		{`"cloud_cover_lte":0,"cloud_cover_unknown":"only"`, false},
		{`"cloud_cover_lt":0,"cloud_cover_unknown":"only"`, false},
		{`"cloud_cover_unknown":"invalid"`, false},
		{`"acquired_from":"2026-09-29"`, false},
		{`"acquired_from":"2026-09-29T00:00:00Z","image_ids":["one","two"]`, true},
	} {
		var value any
		if err := json.Unmarshal([]byte(`{"geometry":`+searchPolygon+`,`+tc.fields+`}`), &value); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(value); (err == nil) != tc.valid {
			t.Errorf("%s: %v", tc.fields, err)
		}
	}
}
