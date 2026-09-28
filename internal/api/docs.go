package api

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

//go:embed openapi.json
var openAPISpec []byte

// OpenAPIDocument renders the build version and authentication requirements for
// this listener. Only registered read operations can become public.
func OpenAPIDocument(version string, access Access) ([]byte, error) {
	var document map[string]any
	if err := json.Unmarshal(openAPISpec, &document); err != nil {
		return nil, fmt.Errorf("decode OpenAPI document: %w", err)
	}
	document["info"].(map[string]any)["version"] = version
	paths := document["paths"].(map[string]any)
	_, reads := routes(nil, version, access, "")
	for pattern := range reads {
		method, path, _ := strings.Cut(pattern, " ")
		item, _ := paths["/api/v1"+path].(map[string]any)
		operation, ok := item[strings.ToLower(method)].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("OpenAPI document is missing operation %q", pattern)
		}
		if access.PublicRead {
			operation["security"] = []any{map[string]any{}, map[string]any{"bearerAuth": []string{}}}
		}
	}
	return json.MarshalIndent(document, "", "  ")
}
