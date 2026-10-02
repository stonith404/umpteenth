package mcpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stonith404/umpteenth/backend/internal/httpserver"
)

// toolOptions say what the operation itself doesn't, for one tool
type toolOptions struct {
	// Destructive marks a tool that removes or replaces data, which its method alone doesn't tell for a POST or PUT
	Destructive bool
	// OpenWorld marks a tool that reaches beyond Umpteenth, such as a model call or a sandbox
	OpenWorld bool
	// Defaults replace the REST defaults of query parameters with ones that fit an agent's context window
	Defaults map[string]any
}

// tools lists the operations served as MCP tools, so a new route only becomes one by a deliberate entry here
var tools = map[string]toolOptions{
	// What the workspace offers a new job
	"list-models":      {},
	"list-mcp-servers": {},
	"list-secrets":     {},
	"list-skills":      {},

	// Jobs
	"list-jobs":   {},
	"get-job":     {},
	"compile-job": {OpenWorld: true},
	"create-job":  {},
	"update-job":  {OpenWorld: true},
	"run-job":     {OpenWorld: true},

	// What a job is given, where a PUT replaces the whole list
	"get-job-mcp-servers": {},
	"set-job-mcp-servers": {Destructive: true},
	"get-job-secrets":     {},
	"set-job-secrets":     {Destructive: true},
	"get-job-skills":      {},
	"set-job-skills":      {Destructive: true},

	// Runs
	"list-runs":       {},
	"get-run":         {},
	"cancel-run":      {Destructive: true},
	"list-run-events": {Defaults: map[string]any{"limit": 50}},
}

// bodyName is the argument that carries a request body that isn't an object, such as the list a set_job_* tool replaces
const bodyName = "body"

// route is how a tool's arguments map back onto its REST request
type route struct {
	method string
	path   string
	// pathParams and queryParams are the arguments that go into the URL
	pathParams  []string
	queryParams []string
	// bodyFields are the arguments that make up an object body, which is sent even when empty
	bodyFields []string
	// hasBody is set for every operation that takes a body, flattened into bodyFields or passed as the body argument
	hasBody  bool
	defaults map[string]any
}

type builtTool struct {
	tool  *mcp.Tool
	route route
}

// buildTools turns every listed operation of the spec into a tool, and fails on one that can't be served over MCP
func buildTools(spec *huma.OpenAPI) ([]builtTool, error) {
	ops := operations(spec)
	schemas, err := componentSchemas(spec)
	if err != nil {
		return nil, err
	}

	ids := slices.Sorted(maps.Keys(tools))
	built := make([]builtTool, 0, len(ids))
	var errs []error
	for _, id := range ids {
		op, ok := ops[id]
		if !ok {
			errs = append(errs, fmt.Errorf("operation %q does not exist", id))
			continue
		}
		t, err := buildTool(op, tools[id], schemas)
		if err != nil {
			errs = append(errs, fmt.Errorf("operation %q: %w", id, err))
			continue
		}
		built = append(built, t)
	}
	return built, errors.Join(errs...)
}

// operations indexes the spec's operations by ID
func operations(spec *huma.OpenAPI) map[string]*huma.Operation {
	ops := map[string]*huma.Operation{}
	for _, item := range spec.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op != nil {
				ops[op.OperationID] = op
			}
		}
	}
	return ops
}

func buildTool(op *huma.Operation, opts toolOptions, schemas map[string]any) (builtTool, error) {
	err := checkServable(op)
	if err != nil {
		return builtTool{}, err
	}

	r := route{method: op.Method, path: op.Path, defaults: opts.Defaults}
	properties := map[string]any{}
	var required []string

	// Path and query parameters become arguments of their own, and only the path ones are always required
	for _, p := range op.Parameters {
		schema, err := toMap(p.Schema)
		if err != nil {
			return builtTool{}, err
		}
		if _, ok := schema["description"]; !ok && p.Description != "" {
			schema["description"] = p.Description
		}
		if v, ok := opts.Defaults[p.Name]; ok {
			schema["default"] = v
		}
		properties[p.Name] = schema
		if p.In == "path" {
			r.pathParams = append(r.pathParams, p.Name)
		} else {
			r.queryParams = append(r.queryParams, p.Name)
		}
		if p.In == "path" || p.Required {
			required = append(required, p.Name)
		}
	}
	for name := range opts.Defaults {
		if !slices.Contains(r.queryParams, name) {
			return builtTool{}, fmt.Errorf("default for %q, which is not a query parameter", name)
		}
	}

	// An object body is flattened into top-level arguments, which reads more naturally than a nested body, and anything else is passed as the body argument
	if op.RequestBody != nil {
		r.hasBody = true
		body, err := resolve(op.RequestBody.Content["application/json"].Schema, schemas)
		if err != nil {
			return builtTool{}, err
		}
		bodyProps, isObject := body["properties"].(map[string]any)
		if isObject {
			bodyRequired, _ := body["required"].([]any)
			for _, name := range slices.Sorted(maps.Keys(bodyProps)) {
				if _, clash := properties[name]; clash {
					return builtTool{}, fmt.Errorf("body field %q has the name of a parameter", name)
				}
				properties[name] = bodyProps[name]
				r.bodyFields = append(r.bodyFields, name)
				if op.RequestBody.Required && slices.Contains(bodyRequired, any(name)) {
					required = append(required, name)
				}
			}
		} else {
			if _, clash := properties[bodyName]; clash {
				return builtTool{}, fmt.Errorf("parameter %q clashes with the body argument", bodyName)
			}
			properties[bodyName] = body
			if op.RequestBody.Required {
				required = append(required, bodyName)
			}
		}
	}

	input := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		input["required"] = required
	}

	return builtTool{
		tool: &mcp.Tool{
			Name:        strings.ReplaceAll(op.OperationID, "-", "_"),
			Title:       title(op),
			Description: strings.TrimSpace(op.Description),
			InputSchema: input,
			Annotations: annotations(op, opts),
		},
		route: r,
	}, nil
}

// checkServable refuses operations a tool call can't reach or answer, such as session-only routes and streams
func checkServable(op *huma.Operation) error {
	access := httpserver.AccessOf(op)
	switch {
	case access.SessionOnly:
		return errors.New("it needs a signed-in session, which an MCP client doesn't have")
	case access.InstanceAdmin:
		return errors.New("it is limited to instance admins, which tokens never are")
	}
	for _, p := range op.Parameters {
		if p.In != "path" && p.In != "query" {
			return fmt.Errorf("parameter %q is passed in the %s, which a tool can't set", p.Name, p.In)
		}
	}
	if op.RequestBody != nil {
		if len(op.RequestBody.Content) != 1 || op.RequestBody.Content["application/json"] == nil {
			return errors.New("its request body isn't JSON")
		}
	}
	for status, resp := range op.Responses {
		if !strings.HasPrefix(status, "2") {
			continue
		}
		if len(resp.Content) == 0 && (status == "202" || status == "204") {
			continue
		}
		if len(resp.Content) != 1 || resp.Content["application/json"] == nil {
			return fmt.Errorf("its %s response isn't JSON", status)
		}
	}
	return nil
}

// title names a tool after its operation, since the operations carry no summaries
func title(op *huma.Operation) string {
	if op.Summary != "" {
		return op.Summary
	}
	words := strings.Split(op.OperationID, "-")
	for i, w := range words {
		if w == "mcp" {
			words[i] = "MCP"
		}
	}
	words[0] = strings.ToUpper(words[0][:1]) + words[0][1:]
	return strings.Join(words, " ")
}

// annotations are set in full, since the SDK assumes a tool without them is destructive and reaches the open world
func annotations(op *huma.Operation, opts toolOptions) *mcp.ToolAnnotations {
	a := &mcp.ToolAnnotations{OpenWorldHint: new(opts.OpenWorld)}
	switch op.Method {
	case http.MethodGet:
		a.ReadOnlyHint = true
		return a
	case http.MethodPut, http.MethodDelete:
		a.IdempotentHint = true
	}
	a.DestructiveHint = new(opts.Destructive || op.Method == http.MethodDelete)
	return a
}

// componentSchemas returns the spec's shared schemas as plain JSON values, the form a tool's input schema takes
func componentSchemas(spec *huma.OpenAPI) (map[string]any, error) {
	schemas := map[string]any{}
	for name, s := range spec.Components.Schemas.Map() {
		m, err := toMap(s)
		if err != nil {
			return nil, fmt.Errorf("schema %q: %w", name, err)
		}
		schemas[name] = m
	}
	return schemas, nil
}

// toMap converts a Huma schema to a plain JSON object through its own marshaling, which renders nullable types and drops hidden fields
func toMap(s *huma.Schema) (map[string]any, error) {
	if s == nil {
		return map[string]any{}, nil
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	err = json.Unmarshal(raw, &m)
	return m, err
}

// resolve converts a schema and inlines every reference into the shared schemas, since a tool's input schema has to stand on its own
func resolve(s *huma.Schema, schemas map[string]any) (map[string]any, error) {
	m, err := toMap(s)
	if err != nil {
		return nil, err
	}
	inlined, err := inline(m, schemas, nil)
	if err != nil {
		return nil, err
	}
	return withoutReadOnly(inlined).(map[string]any), nil
}

// withoutReadOnly drops the fields that only appear in responses from every object of a schema, so an agent never sends them
func withoutReadOnly(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, child := range v {
			out[k] = withoutReadOnly(child)
		}
		props, ok := out["properties"].(map[string]any)
		if !ok {
			return out
		}
		var dropped []any
		for name, prop := range props {
			if p, ok := prop.(map[string]any); ok && p["readOnly"] == true {
				delete(props, name)
				dropped = append(dropped, name)
			}
		}
		if required, ok := out["required"].([]any); ok && len(dropped) > 0 {
			out["required"] = slices.DeleteFunc(required, func(name any) bool { return slices.Contains(dropped, name) })
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = withoutReadOnly(child)
		}
		return out
	default:
		return v
	}
}

const refPrefix = "#/components/schemas/"

// inline replaces references with copies of what they point at, and seen holds the references being expanded to catch a type that contains itself
func inline(v any, schemas map[string]any, seen []string) (any, error) {
	switch v := v.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			name, ok := strings.CutPrefix(ref, refPrefix)
			if !ok || schemas[name] == nil {
				return nil, fmt.Errorf("unknown schema reference %q", ref)
			}
			if slices.Contains(seen, name) {
				return nil, fmt.Errorf("schema %q refers to itself, which a tool schema can't express", name)
			}
			return inline(schemas[name], schemas, append(seen, name))
		}
		out := make(map[string]any, len(v))
		for k, child := range v {
			resolved, err := inline(child, schemas, seen)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			resolved, err := inline(child, schemas, seen)
			if err != nil {
				return nil, err
			}
			out[i] = resolved
		}
		return out, nil
	default:
		return v, nil
	}
}
