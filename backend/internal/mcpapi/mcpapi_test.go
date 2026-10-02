//go:build unit

package mcpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/humatest"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/httpserver"
)

func TestRequestMapsArgumentsOntoTheRoute(t *testing.T) {
	r := route{
		method:      http.MethodPatch,
		path:        "/api/jobs/{id}",
		pathParams:  []string{"id"},
		queryParams: []string{"limit"},
		bodyFields:  []string{"name", "spec"},
		hasBody:     true,
		defaults:    map[string]any{"limit": 50},
	}

	req, err := r.request(t.Context(), json.RawMessage(`{"id":"a/b?c","name":"Report","spec":{"goal":"x"}}`))
	require.NoError(t, err)
	assert.Equal(t, "/api/jobs/a%2Fb%3Fc", req.URL.EscapedPath(), "path parameters can't change the route")
	assert.Equal(t, "50", req.URL.Query().Get("limit"), "the tool's default applies when the argument is left out")
	body, _ := io.ReadAll(req.Body)
	assert.JSONEq(t, `{"name":"Report","spec":{"goal":"x"}}`, string(body))
	assert.Equal(t, "application/json", req.Header.Get("Content-Type"))

	// Numbers keep their exact digits on the way into the query string
	req, err = r.request(t.Context(), json.RawMessage(`{"id":"j","limit":9007199254740993}`))
	require.NoError(t, err)
	assert.Equal(t, "9007199254740993", req.URL.Query().Get("limit"))
	body, _ = io.ReadAll(req.Body)
	assert.JSONEq(t, `{}`, string(body), "an object body is sent even without fields")

	for args, problem := range map[string]string{
		`{"name":"x"}`:            `Missing required argument "id"`,
		`{"id":""}`:               `Argument "id" must not be empty`,
		`{"id":{"a":1}}`:          `Argument "id" must be a string, number or boolean`,
		`{"id":"j","colour":"x"}`: `Unknown argument "colour"`,
		`[1,2]`:                   `The arguments must be a JSON object`,
	} {
		_, err := r.request(t.Context(), json.RawMessage(args))
		assert.EqualError(t, err, problem, args)
	}
}

func TestRequestPassesAListBodyAsIs(t *testing.T) {
	r := route{method: http.MethodPut, path: "/api/jobs/{id}/skills", pathParams: []string{"id"}, hasBody: true}
	req, err := r.request(t.Context(), json.RawMessage(`{"id":"j","body":[{"skillId":"s"}]}`))
	require.NoError(t, err)
	body, _ := io.ReadAll(req.Body)
	assert.JSONEq(t, `[{"skillId":"s"}]`, string(body))
}

func TestResultIsWhatTheAgentReads(t *testing.T) {
	text := func(res *mcp.CallToolResult) string {
		require.Len(t, res.Content, 1)
		return res.Content[0].(*mcp.TextContent).Text
	}

	rec := newRecorder()
	rec.WriteHeader(http.StatusOK)
	_, _ = rec.Write([]byte("{\n  \"id\": \"j\"\n}"))
	res := result(rec)
	assert.False(t, res.IsError)
	assert.Equal(t, `{"id":"j"}`, text(res))

	rec = newRecorder()
	rec.WriteHeader(http.StatusNoContent)
	assert.Equal(t, "Done.", text(result(rec)))

	// Field errors lose Huma's location prefix, and a rate limit says when to try again
	rec = newRecorder()
	rec.Header().Set("Retry-After", "12")
	rec.WriteHeader(http.StatusBadRequest)
	_, _ = rec.Write([]byte(`{"code":"validation_failed","message":"Request validation failed","fields":[{"field":"body.spec.goal","code":"invalid","message":"expected string"}],"requestId":"r1"}`))
	res = result(rec)
	assert.True(t, res.IsError)
	assert.Equal(t, "Request validation failed\n- spec.goal: expected string\nTry again in 12 seconds.\n(code: validation_failed, request ID: r1)", text(res))

	rec = newRecorder()
	rec.WriteHeader(http.StatusBadGateway)
	_, _ = rec.Write([]byte("<html>"))
	assert.Equal(t, "The request failed with status 502", text(result(rec)))
}

// specWith registers one operation on a fresh API and returns its spec
func specWith[I any](t *testing.T, op huma.Operation) *huma.OpenAPI {
	t.Helper()
	_, api := humatest.New(t)
	huma.Register(api, op, func(context.Context, *I) (*struct{}, error) { return nil, nil })
	return api.OpenAPI()
}

func TestBuildToolRefusesWhatAToolCantServe(t *testing.T) {
	session := specWith[struct{}](t, httpserver.Restrict(huma.Operation{OperationID: "s", Method: http.MethodPost, Path: "/s"}, httpserver.Access{SessionOnly: true}))
	_, err := buildTool(session.Paths["/s"].Post, toolOptions{}, nil)
	assert.ErrorContains(t, err, "signed-in session")

	header := specWith[struct {
		Since string `header:"Last-Event-ID"`
	}](t, huma.Operation{OperationID: "h", Method: http.MethodGet, Path: "/h"})
	_, err = buildTool(header.Paths["/h"].Get, toolOptions{}, nil)
	assert.ErrorContains(t, err, "in the header")

	raw := specWith[struct {
		RawBody []byte `contentType:"application/zip"`
	}](t, huma.Operation{OperationID: "r", Method: http.MethodPost, Path: "/r"})
	_, err = buildTool(raw.Paths["/r"].Post, toolOptions{}, nil)
	assert.ErrorContains(t, err, "isn't JSON")
}

func TestBuildToolFlattensAnObjectBody(t *testing.T) {
	type item struct {
		Name string `json:"name"`
		Kind string `json:"kind,omitempty" readOnly:"true"`
	}
	spec := specWith[struct {
		ID   string `path:"id"`
		Body struct {
			Title string `json:"title" doc:"The title"`
			Items []item `json:"items"`
		}
	}](t, huma.Operation{OperationID: "update-thing", Method: http.MethodPatch, Path: "/things/{id}"})
	schemas, err := componentSchemas(spec)
	require.NoError(t, err)

	built, err := buildTool(spec.Paths["/things/{id}"].Patch, toolOptions{}, schemas)
	require.NoError(t, err)
	assert.Equal(t, "update_thing", built.tool.Name)
	assert.Equal(t, "Update thing", built.tool.Title)
	assert.Equal(t, []string{"id"}, built.route.pathParams)
	assert.Equal(t, []string{"items", "title"}, built.route.bodyFields)

	schema, err := json.Marshal(built.tool.InputSchema)
	require.NoError(t, err)
	assert.NotContains(t, string(schema), "$ref")
	assert.NotContains(t, string(schema), `"kind"`, "read-only fields are left out")
	assert.Contains(t, string(schema), `"The title"`)
	assert.ElementsMatch(t, []string{"id", "title", "items"}, built.tool.InputSchema.(map[string]any)["required"])

	// A PATCH that isn't marked destructive reaches neither the open world nor deletes anything
	assert.False(t, built.tool.Annotations.ReadOnlyHint)
	assert.False(t, *built.tool.Annotations.DestructiveHint)
	assert.False(t, *built.tool.Annotations.OpenWorldHint)
}
