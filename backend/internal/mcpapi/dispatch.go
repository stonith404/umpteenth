package mcpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
)

// maxResponseBytes caps what a tool call reads back, which list endpoints stay far below with their page sizes
const maxResponseBytes = 4 << 20

// handler serves a tool call as the REST request it stands for, through the same handler and checks as the REST API
func (t builtTool) handler(next http.Handler) mcp.ToolHandler {
	return func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		c, ok := callerOf(req)
		if !ok {
			// The bearer middleware runs before every call, so this only happens if the server is mounted without it
			return nil, errors.New("tool call without a caller")
		}

		// Arguments that don't fit the operation are reported to the agent, which can correct the call
		httpReq, err := t.route.request(ctx, req.Params.Arguments)
		if err != nil {
			return errorResult(err.Error()), nil
		}

		// The caller is already authenticated, so the request carries the principal instead of credentials
		httpReq = httpReq.WithContext(middleware.WithAuthenticated(httpReq.Context(), c.principal, c.revalidate))
		httpReq.RemoteAddr = c.remoteAddr
		if c.requestID != "" {
			httpReq.Header.Set("X-Request-ID", c.requestID)
		}

		slog.DebugContext(ctx, "MCP tool call", slog.String("tool", t.tool.Name), slog.String("request_id", c.requestID))
		rec := newRecorder()
		next.ServeHTTP(rec, httpReq)
		return result(rec), nil
	}
}

// request builds the REST request for a tool call's arguments
func (r route) request(ctx context.Context, rawArgs json.RawMessage) (*http.Request, error) {
	args := map[string]json.RawMessage{}
	if len(rawArgs) > 0 && string(rawArgs) != "null" {
		// RawMessage values keep numbers as written, so large IDs and timestamps don't pass through a float
		err := json.Unmarshal(rawArgs, &args)
		if err != nil {
			return nil, argumentError("The arguments must be a JSON object")
		}
	}

	// Every argument has to belong to the URL or the body, since a misspelled one would otherwise be dropped without notice
	for name := range args {
		known := slices.Contains(r.pathParams, name) || slices.Contains(r.queryParams, name) || slices.Contains(r.bodyFields, name) || (r.hasBody && r.bodyFields == nil && name == bodyName)
		if !known {
			return nil, argumentError(fmt.Sprintf("Unknown argument %q", name))
		}
	}

	// Path parameters are escaped, so an ID can't change which route the request reaches
	path := r.path
	for _, name := range r.pathParams {
		raw, ok := args[name]
		if !ok {
			return nil, argumentError(fmt.Sprintf("Missing required argument %q", name))
		}
		value, err := scalar(raw)
		if err != nil {
			return nil, argumentError(fmt.Sprintf("Argument %q %s", name, err))
		}
		if value == "" {
			return nil, argumentError(fmt.Sprintf("Argument %q must not be empty", name))
		}
		path = strings.Replace(path, "{"+name+"}", url.PathEscape(value), 1)
	}

	// Query parameters fall back to the tool's own defaults before Huma applies the REST ones
	query := url.Values{}
	for _, name := range r.queryParams {
		raw, ok := args[name]
		if !ok {
			def, hasDefault := r.defaults[name]
			if !hasDefault {
				continue
			}
			raw, _ = json.Marshal(def)
		}
		value, err := scalar(raw)
		if err != nil {
			return nil, argumentError(fmt.Sprintf("Argument %q %s", name, err))
		}
		query.Set(name, value)
	}
	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	// An object body is sent even without fields, since Huma requires the body of most operations
	var body []byte
	switch {
	case r.bodyFields != nil:
		fields := map[string]json.RawMessage{}
		for _, name := range r.bodyFields {
			if raw, ok := args[name]; ok {
				fields[name] = raw
			}
		}
		body, _ = json.Marshal(fields)
	case r.hasBody:
		body = args[bodyName]
	}

	req, err := http.NewRequestWithContext(ctx, r.method, target, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return req, nil
}

// argumentError is a problem with a tool call's arguments, worded for the agent that made the call
type argumentError string

func (e argumentError) Error() string { return string(e) }

// scalar renders a JSON string, number or boolean the way it appears in a URL
func scalar(raw json.RawMessage) (string, error) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, nil
	}
	var v any
	err := json.Unmarshal(raw, &v)
	if err != nil {
		return "", errors.New("must be valid JSON")
	}
	switch v.(type) {
	case float64, bool:
		return string(bytes.TrimSpace(raw)), nil
	default:
		return "", errors.New("must be a string, number or boolean")
	}
}

// result turns the REST response into the tool result the agent reads
func result(rec *recorder) *mcp.CallToolResult {
	// A handler that writes nothing at all answered with an empty 200
	if rec.status == 0 {
		rec.status = http.StatusOK
	}
	if rec.overflow {
		return errorResult("The response is too large, ask for fewer items with pageSize or limit")
	}

	// Successful responses are returned as compact JSON, which costs the agent fewer tokens than indented JSON
	if rec.status >= 200 && rec.status < 300 {
		if rec.body.Len() == 0 {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Done."}}}
		}
		var compact bytes.Buffer
		if json.Compact(&compact, rec.body.Bytes()) != nil {
			compact = rec.body
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: compact.String()}}}
	}

	// Errors keep their message and field details, so the agent can fix its arguments and call again
	var body apperror.Body
	if json.Unmarshal(rec.body.Bytes(), &body) != nil || body.Message == "" {
		return errorResult(fmt.Sprintf("The request failed with status %d", rec.status))
	}
	var text strings.Builder
	text.WriteString(body.Message)
	for _, f := range body.Fields {
		// Huma prefixes a field with where it came from, which a flattened tool argument doesn't have
		field := f.Field
		for _, prefix := range []string{"body.", "path.", "query."} {
			field = strings.TrimPrefix(field, prefix)
		}
		fmt.Fprintf(&text, "\n- %s: %s", field, f.Message)
	}
	if retryAfter := rec.header.Get("Retry-After"); retryAfter != "" {
		fmt.Fprintf(&text, "\nTry again in %s seconds.", retryAfter)
	}
	fmt.Fprintf(&text, "\n(code: %s", body.Code)
	if body.RequestID != "" {
		fmt.Fprintf(&text, ", request ID: %s", body.RequestID)
	}
	text.WriteString(")")
	return errorResult(text.String())
}

func errorResult(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: message}}}
}

// recorder keeps a response in memory, up to maxResponseBytes
type recorder struct {
	header   http.Header
	status   int
	body     bytes.Buffer
	overflow bool
}

func newRecorder() *recorder {
	return &recorder{header: http.Header{}}
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.body.Len()+len(p) > maxResponseBytes {
		r.overflow = true
		return 0, fmt.Errorf("response exceeds %d bytes", maxResponseBytes)
	}
	return r.body.Write(p)
}
