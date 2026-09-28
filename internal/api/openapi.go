package api

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"github.com/swaggest/jsonschema-go"
	"github.com/swaggest/openapi-go"
	"github.com/swaggest/openapi-go/openapi31"

	"github.com/sunstoneinstitute/worklode/internal/buildinfo"
	"github.com/sunstoneinstitute/worklode/internal/model"
)

// routeDoc is what the OpenAPI document says about one /api/v1 route, next
// to the routeGuard that says who may call it. Both tables key on the same
// ServeMux pattern; buildOpenAPI refuses a route that is in one and not the
// other, and registerRoutes runs it, so the document cannot drift from the
// router.
type routeDoc struct {
	// summary is one imperative line: "List the tasks of a project".
	summary string
	// request is the zero value of the JSON body type the handler passes to
	// readJSON or readOptionalJSON (model.CreateTaskInput{}); nil when the
	// route takes no body.
	request any
	// requestContentType overrides application/json for a raw body
	// (uploadBlob: application/octet-stream). request is nil then.
	requestContentType string
	// responses maps each status the handler writes to the zero value it
	// passes to writeJSON: the success status (model.Task{}, []model.Task{})
	// and any error status that has its own body type (409
	// ClaimConflictResponse on POST .../claim); nil for an empty body (204).
	responses map[int]any
	// responseContentType overrides application/json for every response
	// (text/event-stream, application/gzip); the structure is nil then.
	responseContentType string
	// query lists the query parameter names the handler reads, in the order
	// it reads them. Every parameter is documented as an optional string.
	query []string
}

// routeDocs is every /api/v1 route's descriptor, merged from the feature
// files. mergeRouteDocs panics on a pattern two maps both claim.
var routeDocs = mergeRouteDocs(
	taskRouteDocs, leaseRouteDocs, docRouteDocs, ruleRouteDocs,
	adminRouteDocs, projectRouteDocs, overviewRouteDocs,
	map[string]routeDoc{
		"GET /api/v1/openapi.json": {
			summary:   "Describe this API as an OpenAPI 3.1 document",
			responses: map[int]any{http.StatusOK: map[string]any{}},
		},
	},
)

func mergeRouteDocs(parts ...map[string]routeDoc) map[string]routeDoc {
	out := map[string]routeDoc{}
	for _, p := range parts {
		for pattern, d := range p {
			if _, dup := out[pattern]; dup {
				panic(fmt.Sprintf("routeDocs: %q is documented twice", pattern))
			}
			out[pattern] = d
		}
	}
	return out
}

const openAPISecurity = "bearerToken"

// buildOpenAPI renders the OpenAPI 3.1 document for every /api/v1 route in
// guards. It errors on a /api/v1 guard with no descriptor and on a descriptor
// with no guard, naming each pattern.
func buildOpenAPI(guards map[string]routeGuard, docs map[string]routeDoc) ([]byte, error) {
	var undocumented, unrouted []string
	for pattern := range guards {
		if _, ok := docs[pattern]; isAPIPattern(pattern) && !ok {
			undocumented = append(undocumented, pattern)
		}
	}
	for pattern := range docs {
		if _, ok := guards[pattern]; !ok || !isAPIPattern(pattern) {
			unrouted = append(unrouted, pattern)
		}
	}
	if len(undocumented) > 0 || len(unrouted) > 0 {
		slices.Sort(undocumented)
		slices.Sort(unrouted)
		var msg []string
		if len(undocumented) > 0 {
			msg = append(msg, fmt.Sprintf("%d /api/v1 routes have no routeDocs entry: %s",
				len(undocumented), strings.Join(undocumented, ", ")))
		}
		if len(unrouted) > 0 {
			msg = append(msg, fmt.Sprintf("%d routeDocs entries name no /api/v1 route: %s",
				len(unrouted), strings.Join(unrouted, ", ")))
		}
		return nil, fmt.Errorf("openapi: %s", strings.Join(msg, "; "))
	}

	r := openapi31.NewReflector()
	// Every schema is an internal/model type; drop the package prefix
	// swaggest adds so model.Task is #/components/schemas/Task.
	r.DefaultOptions = append(r.DefaultOptions, jsonschema.StripDefinitionNamePrefix("Model"))
	r.Spec.Info.Title = "Worklode API"
	r.Spec.Info.Version = buildinfo.Version
	r.Spec.SetHTTPBearerTokenSecurity(openAPISecurity, "wl_ + 40 hex",
		"A worklode token (bootstrap, minted, or task-scoped).")

	for _, pattern := range slices.Sorted(maps.Keys(docs)) {
		doc := docs[pattern]
		method, path, _ := strings.Cut(pattern, " ")
		oc, err := r.NewOperationContext(method, path)
		if err != nil {
			return nil, fmt.Errorf("openapi: %s: %w", pattern, err)
		}
		oc.SetSummary(doc.summary)
		if p := paramStruct(path, doc.query); p != nil {
			oc.AddReqStructure(p)
		}
		if doc.request != nil {
			oc.AddReqStructure(doc.request)
		} else if doc.requestContentType != "" {
			oc.AddReqStructure(nil, openapi.WithContentType(doc.requestContentType))
		}
		for _, code := range slices.Sorted(maps.Keys(doc.responses)) {
			opts := []openapi.ContentOption{openapi.WithHTTPStatus(code)}
			if doc.responseContentType != "" {
				opts = append(opts, openapi.WithContentType(doc.responseContentType))
			}
			oc.AddRespStructure(doc.responses[code], opts...)
		}
		oc.AddRespStructure(model.ErrorResponse{}, func(cu *openapi.ContentUnit) {
			cu.IsDefault = true
			cu.Description = "Any non-2xx answer"
		})
		if guards[pattern].perm != permPublic {
			oc.AddSecurity(openAPISecurity)
		}
		if err := r.AddOperation(oc); err != nil {
			return nil, fmt.Errorf("openapi: %s: %w", pattern, err)
		}
	}
	return r.Spec.MarshalJSON()
}

// paramStruct builds the struct swaggest reads path and query parameters
// from: one exported string field per {segment} of path (tag path:"name")
// and per query name (tag query:"name"). The fields are named P0.. and Q0..;
// only the tags matter. It returns nil when there are no parameters.
func paramStruct(path string, query []string) any {
	var fields []reflect.StructField
	str := reflect.TypeFor[string]()
	for _, seg := range strings.Split(path, "/") {
		if name, ok := strings.CutPrefix(seg, "{"); ok {
			name = strings.TrimSuffix(name, "}")
			fields = append(fields, reflect.StructField{
				Name: fmt.Sprintf("P%d", len(fields)), Type: str,
				Tag: reflect.StructTag(fmt.Sprintf(`path:%q`, name)),
			})
		}
	}
	for i, name := range query {
		fields = append(fields, reflect.StructField{
			Name: fmt.Sprintf("Q%d", i), Type: str,
			Tag: reflect.StructTag(fmt.Sprintf(`query:%q`, name)),
		})
	}
	if len(fields) == 0 {
		return nil
	}
	return reflect.New(reflect.StructOf(fields)).Elem().Interface()
}

// isAPIPattern reports whether pattern is a /api/v1 route: method, space,
// then a path under /api/v1/.
func isAPIPattern(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	return strings.HasPrefix(path, "/api/v1/")
}

// serveOpenAPI answers GET /api/v1/openapi.json with the document
// registerRoutes built at boot.
func (s *server) serveOpenAPI(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(s.openapiJSON)
}
