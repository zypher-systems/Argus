package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAPISpecMatchesTheServer keeps the published description honest: it
// has to parse, and every path it claims has to exist on the router.
func TestOpenAPISpecMatchesTheServer(t *testing.T) {
	var spec struct {
		OpenAPI string                           `json:"openapi"`
		Info    struct{ Title, Version string }  `json:"info"`
		Paths   map[string]map[string]any        `json:"paths"`
		Comp    struct{ Schemas map[string]any } `json:"components"`
	}
	if err := json.Unmarshal(openAPISpec, &spec); err != nil {
		t.Fatalf("openapi.json does not parse: %v", err)
	}
	if !strings.HasPrefix(spec.OpenAPI, "3.") || spec.Info.Title == "" {
		t.Fatalf("spec header %+v", spec)
	}
	if len(spec.Paths) < 15 {
		t.Fatalf("only %d paths described", len(spec.Paths))
	}

	srv := &Server{}
	router := srv.Handler()
	for path, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "post", "put", "delete", "patch":
			default:
				continue
			}
			// Swap the templated segments for values the router will match.
			real := strings.NewReplacer("{id}", "1", "{slug}", "demo", "{token}", "abc").Replace(path)
			req := httptest.NewRequest(strings.ToUpper(method), "/api"+real, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "404 page not found") {
				t.Errorf("%s %s is described but not routed", strings.ToUpper(method), "/api"+real)
			}
		}
	}
}

func TestOpenAPIEndpointNeedsNoAuth(t *testing.T) {
	srv := &Server{}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/openapi.json", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("served spec does not parse: %v", err)
	}
}
