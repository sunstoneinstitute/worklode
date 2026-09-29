package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

func TestOpenAPIRefusesUndocumentedRoute(t *testing.T) {
	t.Parallel()
	_, err := buildOpenAPI(map[string]routeGuard{"GET /api/v1/things": guarded(permTaskRead)}, nil)
	if err == nil || !strings.Contains(err.Error(), "GET /api/v1/things") {
		t.Fatalf("undocumented route: err = %v, want one naming GET /api/v1/things", err)
	}
	_, err = buildOpenAPI(nil, map[string]routeDoc{"GET /api/v1/ghost": {summary: "x"}})
	if err == nil || !strings.Contains(err.Error(), "GET /api/v1/ghost") {
		t.Fatalf("unrouted descriptor: err = %v, want one naming GET /api/v1/ghost", err)
	}
	if _, err := buildOpenAPI(map[string]routeGuard{"POST /hooks/github": open("x")}, nil); err != nil {
		t.Fatalf("non-/api/v1 route needs no descriptor: %v", err)
	}
}

// openAPIDoc is the slice of the rendered document the tests assert on.
type openAPIDoc struct {
	OpenAPI    string `json:"openapi"`
	Paths      map[string]map[string]openAPIOp
	Components struct {
		SecuritySchemes map[string]struct {
			Scheme string `json:"scheme"`
		} `json:"securitySchemes"`
	} `json:"components"`
}

type openAPIOp struct {
	Parameters []struct {
		Name     string `json:"name"`
		In       string `json:"in"`
		Required bool   `json:"required"`
		Schema   struct {
			Type string `json:"type"`
		} `json:"schema"`
	} `json:"parameters"`
	Responses map[string]struct {
		Content map[string]struct {
			Schema struct {
				Ref string `json:"$ref"`
			} `json:"schema"`
		} `json:"content"`
	} `json:"responses"`
	RequestBody *struct {
		Content map[string]struct {
			Schema struct {
				Ref string `json:"$ref"`
			} `json:"schema"`
		} `json:"content"`
	} `json:"requestBody"`
	Security *[]map[string][]string `json:"security"`
}

func renderOpenAPI(t *testing.T, guards map[string]routeGuard, docs map[string]routeDoc) openAPIDoc {
	t.Helper()
	b, err := buildOpenAPI(guards, docs)
	if err != nil {
		t.Fatal(err)
	}
	var d openAPIDoc
	if err := json.Unmarshal(b, &d); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, b)
	}
	return d
}

// probeParams stands in for an internal/model params struct.
type probeParams struct {
	Deleted bool `query:"deleted,omitempty"`
}

func TestOpenAPIDocument(t *testing.T) {
	t.Parallel()
	d := renderOpenAPI(t,
		map[string]routeGuard{
			"GET /api/v1/things/{id}":  guarded(permTaskRead),
			"GET /api/v1/openapi.json": open("x"),
		},
		map[string]routeDoc{
			"GET /api/v1/things/{id}": {
				summary:   "Show a thing",
				responses: map[int]any{http.StatusOK: model.Task{}},
				params:    probeParams{},
			},
			"GET /api/v1/openapi.json": {
				summary:   "Describe",
				responses: map[int]any{http.StatusOK: map[string]any{}},
			},
		})

	if d.OpenAPI != "3.1.0" {
		t.Errorf("openapi = %q, want 3.1.0", d.OpenAPI)
	}
	op := d.Paths["/api/v1/things/{id}"]["get"]
	params := map[string]string{}
	for _, p := range op.Parameters {
		params[p.Name] = p.In
		if p.Name == "id" && !p.Required {
			t.Error("path parameter id is not required")
		}
		if p.Name == "deleted" && p.Schema.Type != "boolean" {
			t.Errorf("deleted schema type = %q, want boolean", p.Schema.Type)
		}
	}
	if params["id"] != "path" || params["deleted"] != "query" {
		t.Errorf("parameters = %v, want id in path and deleted in query", params)
	}
	if ref := op.Responses["200"].Content["application/json"].Schema.Ref; ref != "#/components/schemas/Task" {
		t.Errorf("200 schema $ref = %q, want #/components/schemas/Task", ref)
	}
	if ref := op.Responses["default"].Content["application/json"].Schema.Ref; !strings.HasSuffix(ref, "ErrorResponse") {
		t.Errorf("default schema $ref = %q, want ErrorResponse", ref)
	}
	if op.Security == nil || len(*op.Security) != 1 || (*op.Security)[0]["bearerToken"] == nil {
		t.Errorf("security = %v, want [{bearerToken: []}]", op.Security)
	}
	if pub := d.Paths["/api/v1/openapi.json"]["get"]; pub.Security != nil {
		t.Errorf("open route carries security %v", *pub.Security)
	}
	if s := d.Components.SecuritySchemes["bearerToken"].Scheme; s != "bearer" {
		t.Errorf("bearerToken scheme = %q, want bearer", s)
	}
}

// swaggest drops a DELETE body unless forced; buildOpenAPI must still emit
// it, with the model type's own component $ref.
func TestOpenAPIDeleteBody(t *testing.T) {
	t.Parallel()
	d := renderOpenAPI(t,
		map[string]routeGuard{"DELETE /api/v1/things/{id}": guarded(permTaskRead)},
		map[string]routeDoc{"DELETE /api/v1/things/{id}": {
			summary:   "Remove a thing",
			request:   model.ErrorResponse{},
			responses: map[int]any{http.StatusNoContent: nil},
		}})
	op := d.Paths["/api/v1/things/{id}"]["delete"]
	if op.RequestBody == nil {
		t.Fatal("DELETE has no requestBody")
	}
	if ref := op.RequestBody.Content["application/json"].Schema.Ref; ref != "#/components/schemas/ErrorResponse" {
		t.Errorf("requestBody $ref = %q, want #/components/schemas/ErrorResponse", ref)
	}
	if len(d.Paths) != 1 {
		t.Errorf("paths = %v, want only /api/v1/things/{id}", d.Paths)
	}
}

func TestOpenAPIRejectsPathTagInParams(t *testing.T) {
	t.Parallel()
	type badParams struct {
		ID string `path:"id"`
	}
	_, err := buildOpenAPI(
		map[string]routeGuard{"GET /api/v1/things/{id}": guarded(permTaskRead)},
		map[string]routeDoc{"GET /api/v1/things/{id}": {summary: "x", params: badParams{}}})
	if err == nil || !strings.Contains(err.Error(), "GET /api/v1/things/{id}") {
		t.Fatalf("err = %v, want one naming GET /api/v1/things/{id}", err)
	}
}

func TestOpenAPINonJSONResponse(t *testing.T) {
	t.Parallel()
	d := renderOpenAPI(t,
		map[string]routeGuard{"GET /api/v1/export": guarded(permTaskRead)},
		map[string]routeDoc{"GET /api/v1/export": {
			summary:             "Export",
			responses:           map[int]any{http.StatusOK: nil},
			responseContentType: "application/gzip",
		}})
	if _, ok := d.Paths["/api/v1/export"]["get"].Responses["200"].Content["application/gzip"]; !ok {
		t.Errorf("200 content = %v, want application/gzip", d.Paths["/api/v1/export"]["get"].Responses["200"].Content)
	}
}

// TestOpenAPIWrite is what `make openapi` runs: it writes the real document to
// $LODE_OPENAPI_OUT.
func TestOpenAPIWrite(t *testing.T) {
	out := os.Getenv("LODE_OPENAPI_OUT")
	if out == "" {
		t.Skip("LODE_OPENAPI_OUT not set")
	}
	b, err := buildOpenAPI(routeGuards, routeDocs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(out, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
