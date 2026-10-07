package api

import (
	"context"
	"encoding/json"
	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/geo"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

// These requests must be rejected before any database access.
func TestValidation(t *testing.T) {
	h := Routes(nil, "dev", Access{Token: "test-token"}, "osm")
	cases := []struct {
		method, path, body string
		status             int
	}{
		{"POST", "/imagery/search", `{`, 400},
		{"POST", "/imagery/search", `{"geometry":{"type":"Point","coordinates":[1,2]}}`, 400},
		{"POST", "/imagery/search", `{"geometry":{"type":"Polygon","coordinates":[]}}`, 400},
		{"POST", "/imagery/search", `{} {}`, 400},
		{"POST", "/imagery/search", `{"surprise":1}`, 400},
		{"POST", "/imagery/search", `{"geometry":` + strings.Repeat(" ", MaxBodyBytes) + `}`, 413},
		{"GET", "/imagery?limit=501", "", 400},
		{"GET", "/imagery?limit=-1", "", 400},
		{"GET", "/imagery?limit=0", "", 400},
		{"POST", "/imagery/search", `{"geometry":` + searchPolygon + `,"limit":501}`, 400},
		{"POST", "/imagery/search", `{"geometry":` + searchPolygon + `,"limit":-1}`, 400},
		{"POST", "/imagery/search", `{"geometry":` + searchPolygon + `,"limit":0}`, 400},
		{"GET", "/imagery?offset=-1", "", 400},
		{"GET", "/imagery?image_id=", "", 400},
		{"DELETE", "/imagery", "", 405},
		{"GET", "/missing", "", 404},
	}
	for _, tc := range cases {
		t.Run(tc.path+tc.body[:min(20, len(tc.body))], func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-token")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("got %d want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), `"error"`) {
				t.Fatal("expected structured error")
			}
		})
	}
}

func TestCloudCoverResponse(t *testing.T) {
	zero, fraction := 0.0, 12.5
	for _, cover := range []*float64{nil, &zero, &fraction} {
		data, err := json.Marshal(Response(catalog.Imagery{CloudCover: cover}))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		got, present := body["cloud_cover"]
		if !present || cover == nil && got != nil || cover != nil && got != *cover {
			t.Fatalf("cloud_cover serialized incorrectly: %s", data)
		}
	}
}

// Capture parsed inputs only; SQL and spatial semantics use real PostGIS tests.
type queryStore struct {
	catalog.Store
	query *catalog.Query
}

func (s queryStore) ValidateGeometry(context.Context, geo.Geometry) error { return nil }
func (s queryStore) Search(_ context.Context, q catalog.Query) (catalog.Page, error) {
	*s.query = q
	return catalog.Page{Limit: q.Limit, Offset: q.Offset}, nil
}

const searchPolygon = `{"type":"Polygon","coordinates":[[[0,0],[2,0],[2,2],[0,2],[0,0]]]}`

func TestPageSizeQueryParsing(t *testing.T) {
	for _, raw := range []string{"", "1", "50", "100", "200", "500"} {
		for _, method := range []string{"GET", "POST"} {
			t.Run(method+"/"+raw, func(t *testing.T) {
				var got catalog.Query
				h := Routes(catalog.New(queryStore{query: &got}), "dev", Access{PublicRead: true}, "none")
				path, body := "/imagery?offset=500", ""
				if method == "GET" && raw != "" {
					path += "&limit=" + raw
				} else if method == "POST" {
					path = "/imagery/search"
					body = `{"geometry":` + searchPolygon + `,"offset":500`
					if raw != "" {
						body += `,"limit":` + raw
					}
					body += "}"
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
				if w.Code != 200 {
					t.Fatalf("%d: %s", w.Code, w.Body.String())
				}
				want := 50
				if raw != "" {
					want, _ = strconv.Atoi(raw)
				}
				var page SearchResponse
				if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
					t.Fatal(err)
				}
				if got.Limit != want || got.Offset != 500 || page.Limit != want || page.Offset != 500 {
					t.Fatalf("pagination lost: query %+v, response %+v", got, page)
				}
			})
		}
	}
}

func TestCloudCoverQueryParsing(t *testing.T) {
	for _, raw := range []string{"", "0", "19.9", "20", "100", "null"} {
		for _, method := range []string{"GET", "POST"} {
			if method == "GET" && raw == "null" {
				continue
			}
			t.Run(method+"/"+raw, func(t *testing.T) {
				var got catalog.Query
				h := Routes(catalog.New(queryStore{query: &got}), "dev", Access{PublicRead: true}, "none")
				path := "/imagery?catalog_id=example&image_id=one&image_id=two&limit=1&offset=2"
				body := ""
				if method == "GET" && raw != "" {
					path += "&cloud_cover_lt=" + raw
				} else if method == "POST" {
					path = "/imagery/search"
					body = `{"catalog_id":"example","geometry":` + searchPolygon + `,"limit":1,"offset":2`
					if raw != "" {
						body += `,"cloud_cover_lt":` + raw
					}
					body += "}"
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
				if w.Code != 200 {
					t.Fatalf("%d: %s", w.Code, w.Body.String())
				}
				if raw == "" || raw == "null" {
					if got.CloudCoverLT != nil {
						t.Fatal("omitted/null filter became a threshold")
					}
				} else {
					var want float64
					if err := json.Unmarshal([]byte(raw), &want); err != nil {
						t.Fatal(err)
					}
					if got.CloudCoverLT == nil || *got.CloudCoverLT != want {
						t.Fatalf("threshold %s lost: %+v", raw, got)
					}
				}
				if got.CatalogID != "example" || got.Limit != 1 || got.Offset != 2 ||
					(method == "GET" && strings.Join(got.ImageIDs, ",") != "one,two") ||
					(method == "POST" && got.Geometry == nil) {
					t.Fatalf("other filters lost: %+v", got)
				}
			})
		}
	}
}

func TestInvalidCloudCoverQueries(t *testing.T) {
	h := Routes(nil, "dev", Access{PublicRead: true}, "none")
	check := func(method, path, body string, fieldMessage bool) {
		t.Helper()
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
		var response struct {
			Error struct{ Code, Message string }
		}
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if w.Code != 400 || response.Error.Code == "" || response.Error.Message == "" ||
			fieldMessage && !strings.Contains(response.Error.Message, "cloud_cover_lt") {
			t.Fatalf("%s %s %s: %d %s", method, path, body, w.Code, w.Body.String())
		}
	}
	for _, raw := range []string{"", " ", "bad", "20%", "null", "-1", "100.01", "NaN", "Inf", "+Inf", "-Inf", "1e400"} {
		check("GET", "/imagery?cloud_cover_lt="+url.QueryEscape(raw), "", true)
	}
	check("GET", "/imagery?cloud_cover_lt", "", true)
	check("GET", "/imagery?cloud_cover_lt=%zz", "", false)
	check("GET", "/imagery?cloud_cover_lt=20;bad", "", false)
	for _, raw := range []string{`""`, `"20"`, `"NaN"`, `true`, `[]`, `{}`, "-1", "100.01", "1e400"} {
		check("POST", "/imagery/search", `{"geometry":`+searchPolygon+`,"cloud_cover_lt":`+raw+`}`, true)
	}
	for _, raw := range []string{"NaN", "Infinity", "-Infinity", "20.1.2", ""} {
		check("POST", "/imagery/search", `{"geometry":`+searchPolygon+`,"cloud_cover_lt":`+raw+`}`, false)
	}
}
