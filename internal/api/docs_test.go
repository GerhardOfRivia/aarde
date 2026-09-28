package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/GerhardOfRivia/aarde/internal/catalog"
	"github.com/GerhardOfRivia/aarde/internal/geo"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type documentedResponse struct {
	Ref     string `json:"$ref"`
	Content map[string]struct {
		Schema struct {
			Ref string `json:"$ref"`
		} `json:"schema"`
		Example json.RawMessage `json:"example"`
	} `json:"content"`
}
type documentedOperation struct {
	OperationID string                        `json:"operationId"`
	Security    []map[string][]string         `json:"security"`
	Responses   map[string]documentedResponse `json:"responses"`
}
type documentedAPI struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Version string `json:"version"`
	} `json:"info"`
	Security   []map[string][]string                     `json:"security"`
	Paths      map[string]map[string]documentedOperation `json:"paths"`
	Components struct {
		Responses map[string]documentedResponse `json:"responses"`
		Schemas   map[string]struct {
			Example json.RawMessage `json:"example"`
		} `json:"schemas"`
	} `json:"components"`
}

func TestOpenAPIRoutesAndSecurity(t *testing.T) {
	for _, public := range []bool{false, true} {
		access := Access{Token: "test-token", PublicRead: public}
		data, err := OpenAPIDocument("v1.2.3+docs", access)
		if err != nil {
			t.Fatal(err)
		}
		var doc documentedAPI
		if err = json.Unmarshal(data, &doc); err != nil {
			t.Fatal(err)
		}
		if doc.OpenAPI != "3.1.0" || doc.Info.Version != "v1.2.3+docs" {
			t.Fatal("incorrect specification/build version")
		}
		router, reads := routes(nil, "dev", access, "osm")
		seen := map[string]bool{}
		ids := map[string]bool{}
		err = chi.Walk(router, func(method, path string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			op, ok := doc.Paths["/api/v1"+path][strings.ToLower(method)]
			if !ok {
				t.Errorf("undocumented route: %s %s", method, path)
				return nil
			}
			seen[strings.ToLower(method)+" /api/v1"+path] = true
			if op.OperationID == "" || ids[op.OperationID] {
				t.Errorf("missing/duplicate operation ID: %s %s", method, path)
			}
			ids[op.OperationID] = true
			security := op.Security
			if security == nil {
				security = doc.Security
			}
			anonymous, bearer := len(security) == 0, false
			for _, req := range security {
				anonymous = anonymous || len(req) == 0
				_, b := req["bearerAuth"]
				bearer = bearer || b
			}
			if !bearer || anonymous != (public && reads[method+" "+path]) {
				t.Errorf("incorrect auth for %s %s", method, path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		for path, methods := range doc.Paths {
			for method := range methods {
				if !seen[method+" "+path] {
					t.Errorf("documented unregistered route: %s %s", method, path)
				}
			}
		}
	}
}

const contractURL = "https://aarde.invalid/openapi.json"

func contractCompiler(t *testing.T) (documentedAPI, *jsonschema.Compiler) {
	t.Helper()
	var doc documentedAPI
	if err := json.Unmarshal(openAPISpec, &doc); err != nil {
		t.Fatal(err)
	}
	resource, err := jsonschema.UnmarshalJSON(bytes.NewReader(openAPISpec))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	if err = compiler.AddResource(contractURL, resource); err != nil {
		t.Fatal(err)
	}
	return doc, compiler
}
func validateContract(t *testing.T, c *jsonschema.Compiler, ref string, data []byte) {
	t.Helper()
	if ref == "" {
		t.Fatal("missing schema reference")
	}
	schema, err := c.Compile(contractURL + ref)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		return
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(value); err != nil {
		t.Fatal(err)
	}
}
func TestOpenAPISchemasAndExamples(t *testing.T) {
	doc, c := contractCompiler(t)
	for name, definition := range doc.Components.Schemas {
		t.Run(name, func(t *testing.T) { validateContract(t, c, "#/components/schemas/"+name, definition.Example) })
	}
	for name, response := range doc.Components.Responses {
		content := response.Content["application/json"]
		t.Run(name, func(t *testing.T) { validateContract(t, c, content.Schema.Ref, content.Example) })
	}
}

// Only read methods are implemented: an accidental write fails the contract test.
type contractStore struct {
	catalog.Store
	failure error
}

func (s contractStore) Ping(context.Context) error { return s.failure }
func (s contractStore) Catalogs(context.Context) ([]string, error) {
	return []string{"default"}, s.failure
}
func (s contractStore) ValidateGeometry(context.Context, geo.Geometry) error { return s.failure }
func (s contractStore) Get(context.Context, string, string) (catalog.Imagery, error) {
	return catalog.Imagery{ID: uuid.MustParse("c77f4d82-ef3d-44b9-b027-c6fc30b60c53"), CatalogID: "default", ImageID: "ABC123", DisplayName: "ABC123.tif", ImportedAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), CreatedAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), Footprint: geo.Geometry{Type: "MultiPolygon", Coordinates: json.RawMessage(`[[[[0,0],[1,0],[1,1],[0,0]]]]`)}, Checksum: strings.Repeat("a", 64), AssetLocation: "/data/ABC123.tif", Width: 1024, Height: 1024, BandCount: 3, SourceCRS: "EPSG:4326", Metadata: json.RawMessage(`{}`)}, s.failure
}
func (s contractStore) Search(ctx context.Context, q catalog.Query) (catalog.Page, error) {
	item, _ := s.Get(ctx, "default", "ABC123")
	return catalog.Page{Items: []catalog.Imagery{item}, Limit: q.Limit, Offset: q.Offset}, s.failure
}
func TestAPIResponsesMatchOpenAPI(t *testing.T) {
	doc, c := contractCompiler(t)
	for _, public := range []bool{false, true} {
		token := "test-token"
		if public {
			token = ""
		}
		for _, tc := range []struct {
			method, path, pattern, body string
			status                      int
			failure                     error
			invalidToken                bool
		}{
			{"GET", "/info", "/info", "", 200, nil, false},
			{"GET", "/version", "/version", "", 200, nil, false},
			{"GET", "/health", "/health", "", 200, nil, false},
			{"GET", "/catalogs", "/catalogs", "", 200, nil, false},
			{"GET", "/imagery?limit=1&offset=2&image_id=ABC123", "/imagery", "", 200, nil, false},
			{"GET", "/imagery/default/ABC123", "/imagery/{catalogID}/{imageID}", "", 200, nil, false},
			{"POST", "/imagery/search", "/imagery/search", `{"geometry":{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}}`, 200, nil, false},
			{"GET", "/info", "/info", "", 401, nil, true},
			{"GET", "/imagery?limit=0", "/imagery", "", 400, nil, false},
			{"POST", "/imagery/search", "/imagery/search", `{`, 400, nil, false},
			{"POST", "/imagery/search", "/imagery/search", `{"geometry":{"type":"Point","coordinates":[0,0]}}`, 400, nil, false},
			{"POST", "/imagery/search", "/imagery/search", `{"geometry":` + strings.Repeat(" ", MaxBodyBytes) + `}`, 413, nil, false},
			{"GET", "/imagery/default/missing", "/imagery/{catalogID}/{imageID}", "", 404, catalog.ErrNotFound, false},
			{"GET", "/catalogs", "/catalogs", "", 500, errors.New("database failed"), false},
			{"GET", "/health", "/health", "", 503, errors.New("database failed"), false},
		} {
			t.Run(fmt.Sprintf("public=%t/%s/%s/%d", public, tc.method, tc.path, tc.status), func(t *testing.T) {
				h := Routes(catalog.New(contractStore{failure: tc.failure}), "dev", Access{Token: "test-token", PublicRead: public}, "osm")
				req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				if token != "" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				if tc.invalidToken {
					req.Header.Set("Authorization", "Bearer invalid")
				}
				w := httptest.NewRecorder()
				h.ServeHTTP(w, req)
				if w.Code != tc.status {
					t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body.String())
				}
				response := doc.Paths["/api/v1"+tc.pattern][strings.ToLower(tc.method)].Responses[fmt.Sprint(tc.status)]
				if response.Ref != "" {
					response = doc.Components.Responses[strings.TrimPrefix(response.Ref, "#/components/responses/")]
				}
				validateContract(t, c, response.Content["application/json"].Schema.Ref, w.Body.Bytes())
			})
		}
	}
}
