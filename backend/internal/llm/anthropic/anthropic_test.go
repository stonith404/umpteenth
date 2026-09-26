//go:build unit

package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// reply is one canned HTTP response of the fake Messages API
type reply struct {
	status  int
	fixture string
	body    string
}

// fakeAPI serves canned replies in order, repeating the last one, and records every request body
type fakeAPI struct {
	t       *testing.T
	mu      sync.Mutex
	replies []reply
	bodies  [][]byte
	headers []http.Header
}

func newFakeAPI(t *testing.T, replies ...reply) (*fakeAPI, *Provider) {
	t.Helper()
	api := &fakeAPI{t: t, replies: replies}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	p, err := New(llm.Config{Kind: llm.KindAnthropic, APIKey: "test-key", BaseURL: srv.URL})
	require.NoError(t, err)
	return api, p
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	a.mu.Lock()
	a.bodies = append(a.bodies, body)
	a.headers = append(a.headers, r.Header.Clone())
	rep := a.replies[min(len(a.bodies), len(a.replies))-1]
	a.mu.Unlock()

	if r.URL.Path != "/v1/messages" {
		http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		return
	}
	if rep.status != 0 && rep.status != http.StatusOK {
		// Retry-After-Ms keeps the SDK's bounded retries fast
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After-Ms", "1")
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
		return
	}
	data, err := os.ReadFile(filepath.Join("testdata", rep.fixture))
	if err != nil {
		a.t.Errorf("read fixture: %v", err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	_, _ = w.Write(data)
}

func (a *fakeAPI) requests() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.bodies)
}

func (a *fakeAPI) body(i int) []byte {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.bodies[i]
}

func userText(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(text)}}
}

func collect(deltas *[]llm.Delta) func(llm.Delta) {
	return func(d llm.Delta) { *deltas = append(*deltas, d) }
}

var weatherTool = llm.ToolDef{Name: "get_weather", Description: "Get the weather for a city", Schema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}

func TestStreamText(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "text.sse"})

	var deltas []llm.Delta
	resp, err := p.Stream(context.Background(), llm.Request{
		Model: "claude-opus-5-5",
		System: []llm.Block{
			{Text: "You are Umpteenth.", CacheBreakpoint: true},
			{Text: "Job: report the build status."},
		},
		Messages: []llm.Message{userText("Did the build pass?")},
		Tools:    []llm.ToolDef{{Name: "bash", Schema: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"}}}`)}, weatherTool},
		Effort:   llm.EffortHigh,
	}, collect(&deltas))
	require.NoError(t, err)

	// The response and deltas reflect the streamed text
	assert.Equal(t, "The build passed on the first try.", resp.Message.Text())
	assert.Equal(t, llm.RoleAssistant, resp.Message.Role)
	assert.Equal(t, llm.StopEndTurn, resp.Stop)
	assert.Equal(t, "claude-opus-5-5", resp.Model)
	require.Len(t, deltas, 3)
	assert.Equal(t, llm.Delta{Type: llm.DeltaText, Text: "The build"}, deltas[0])

	// input_tokens already excludes cache reads and writes, which are reported separately
	assert.Equal(t, llm.Usage{Input: 412, Output: 9, CacheRead: 8192, CacheWrite: 1536}, resp.Usage)

	// The request carries the key, streams, and applies the cache and thinking configuration
	body := api.body(0)
	assert.Equal(t, "test-key", api.headers[0].Get("X-Api-Key"))
	assert.True(t, gjson.GetBytes(body, "stream").Bool())
	assert.Equal(t, int64(64_000), gjson.GetBytes(body, "max_tokens").Int())
	assert.Equal(t, "ephemeral", gjson.GetBytes(body, "cache_control.type").String(), "automatic caching of the conversation tail")
	assert.Equal(t, "ephemeral", gjson.GetBytes(body, "system.0.cache_control.type").String(), "breakpoint on the stable system block")
	assert.False(t, gjson.GetBytes(body, "system.1.cache_control").Exists(), "the volatile system block is not cached")
	assert.Equal(t, "ephemeral", gjson.GetBytes(body, "tools.1.cache_control.type").String(), "breakpoint on the last tool")
	assert.False(t, gjson.GetBytes(body, "tools.0.cache_control").Exists())
	assert.JSONEq(t, string(weatherTool.Schema), gjson.GetBytes(body, "tools.1.input_schema").Raw, "tool schemas pass through unchanged")
	assert.Equal(t, "adaptive", gjson.GetBytes(body, "thinking.type").String())
	assert.Equal(t, "summarized", gjson.GetBytes(body, "thinking.display").String())
	assert.Equal(t, "high", gjson.GetBytes(body, "output_config.effort").String())
	assert.False(t, gjson.GetBytes(body, "tool_choice").Exists())
}

func TestStreamParallelToolCallsAndReasoningRoundTrip(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "tools_thinking.sse"}, reply{fixture: "text.sse"})

	var deltas []llm.Delta
	req := llm.Request{
		Model:    "claude-opus-5-5",
		Messages: []llm.Message{userText("Weather in Paris and Berlin?")},
		Tools:    []llm.ToolDef{weatherTool},
	}
	resp, err := p.Stream(context.Background(), req, collect(&deltas))
	require.NoError(t, err)

	// Parts keep the order of the content blocks
	parts := resp.Message.Parts
	require.Len(t, parts, 5)
	assert.Equal(t, llm.PartReasoning, parts[0].Type)
	assert.Equal(t, llm.PartReasoning, parts[1].Type)
	assert.Equal(t, llm.PartText, parts[2].Type)
	assert.Equal(t, llm.StopToolUse, resp.Stop)
	assert.Equal(t, llm.Usage{Input: 1250, Output: 187, CacheRead: 20480, Reasoning: 96}, resp.Usage)

	// Thinking is summarized for the UI and kept whole, signature included, for the replay
	thinking := parts[0].Reasoning
	assert.Equal(t, llm.KindAnthropic, thinking.Provider)
	assert.Equal(t, "I need the weather in both cities, so I will call the tool twice in parallel.", thinking.Text)
	assert.Equal(t, "thinking", gjson.GetBytes(thinking.Opaque, "type").String())
	assert.True(t, strings.HasPrefix(gjson.GetBytes(thinking.Opaque, "signature").String(), "EqQBCgIYAhIM"))
	redacted := parts[1].Reasoning
	assert.Equal(t, "redacted_thinking", gjson.GetBytes(redacted.Opaque, "type").String())
	assert.Empty(t, redacted.Text)

	// Both parallel calls are complete, with arguments assembled from fragments
	calls := resp.Message.ToolCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, llm.ToolCall{ID: "toolu_01A09q90qw90lq917835lq9", Name: "get_weather", Args: json.RawMessage(`{"city": "Paris"}`)}, calls[0])
	assert.Equal(t, "toolu_01B7fKq2mZ8Xy3Lr5Tn9Vw4c", calls[1].ID)
	assert.JSONEq(t, `{"city":"Berlin"}`, string(calls[1].Args))

	// Deltas report thinking, text and both tool call starts
	var toolStarts []string
	var reasoning string
	for _, d := range deltas {
		switch d.Type {
		case llm.DeltaToolCall:
			toolStarts = append(toolStarts, d.ToolName)
		case llm.DeltaReasoning:
			reasoning += d.Text
		}
	}
	assert.Equal(t, []string{"get_weather", "get_weather"}, toolStarts)
	assert.Equal(t, thinking.Text, reasoning)

	// Continue the conversation with both results in one user message
	req.Messages = append(req.Messages, resp.Message, llm.Message{Role: llm.RoleUser, Parts: []llm.Part{
		{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: calls[0].ID, Content: "18°C, sunny"}},
		{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: calls[1].ID, Content: "weather service unavailable", IsError: true}},
	}})
	_, err = p.Stream(context.Background(), req, nil)
	require.NoError(t, err)
	body := api.body(1)

	// The thinking blocks go back exactly as the API produced them
	fixtureThinking := `{"type":"thinking","thinking":"I need the weather in both cities, so I will call the tool twice in parallel.","signature":"EqQBCgIYAhIM1gbcDa9GJwZA2b3hGgxBdjrkzLoky3dl1pkiMOYds1yYpYNhlRkm0BEP3RA3VuDEEQJnOCjTGdXmnQ7i5A9yhJ2DPbPGR8cVJjPPbCIQn5DAgBEgwPJmHtBQ7xgfjB/9EgcCKQQJyZfqAqKZs9dwlC6xoMCP"}`
	fixtureRedacted := `{"type":"redacted_thinking","data":"EmwKAhgBEgy3va3pzix/LafPsn4aDFIT2Xlxh0L5L8rLVyIwxtE3rAFBa8cr3qpPkNRj2YfWXGmKDxH4mPnZ5sQ7vB5URj2pLmN0q+Ra3tNgv5nxHQs"}`
	assistant := gjson.GetBytes(body, "messages.1")
	assert.Equal(t, "assistant", assistant.Get("role").String())
	assert.JSONEq(t, fixtureThinking, assistant.Get("content.0").Raw)
	assert.JSONEq(t, fixtureRedacted, assistant.Get("content.1").Raw)
	assert.JSONEq(t, `{"type":"text","text":"Checking both cities."}`, assistant.Get("content.2").Raw)

	// Both tool_use blocks stay in the one assistant message
	assert.JSONEq(t, `{"type":"tool_use","id":"toolu_01A09q90qw90lq917835lq9","name":"get_weather","input":{"city":"Paris"}}`, assistant.Get("content.3").Raw)
	assert.Equal(t, "toolu_01B7fKq2mZ8Xy3Lr5Tn9Vw4c", assistant.Get("content.4.id").String())
	assert.Len(t, assistant.Get("content").Array(), 5)

	// Both tool_result blocks go back in a single user message
	results := gjson.GetBytes(body, "messages.2")
	assert.Equal(t, "user", results.Get("role").String())
	require.Len(t, results.Get("content").Array(), 2)
	assert.JSONEq(t, `{"type":"tool_result","tool_use_id":"toolu_01A09q90qw90lq917835lq9","content":[{"type":"text","text":"18°C, sunny"}]}`, results.Get("content.0").Raw)
	assert.JSONEq(t, `{"type":"tool_result","tool_use_id":"toolu_01B7fKq2mZ8Xy3Lr5Tn9Vw4c","is_error":true,"content":[{"type":"text","text":"weather service unavailable"}]}`, results.Get("content.1").Raw)
}

func TestReasoningFromOtherProvidersIsNotReplayed(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "text.sse"})
	_, err := p.Stream(context.Background(), llm.Request{
		Model: "claude-opus-5-5",
		Messages: []llm.Message{
			userText("Hi"),
			{Role: llm.RoleAssistant, Parts: []llm.Part{
				{Type: llm.PartReasoning, Reasoning: &llm.Reasoning{Provider: llm.KindOpenAI, Text: "local model thoughts"}},
				llm.TextPart("Hello"),
			}},
			userText("Again"),
		},
	}, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"type":"text","text":"Hello"}]`, gjson.GetBytes(api.body(0), "messages.1.content").Raw)
}

func TestRefusal(t *testing.T) {
	_, p := newFakeAPI(t, reply{fixture: "refusal.sse"})

	resp, err := p.Stream(context.Background(), llm.Request{Model: "claude-opus-5-5", Messages: []llm.Message{userText("...")}}, nil)
	require.NoError(t, err)
	assert.Equal(t, llm.StopRefusal, resp.Stop)
	assert.Empty(t, resp.Message.Parts)

	// Structured turns a refusal into ErrRefusal without retrying
	var out map[string]any
	resp, err = llm.Structured(context.Background(), p, llm.Request{Model: "claude-opus-5-5", Messages: []llm.Message{userText("...")}, OutputSchema: json.RawMessage(`{"type":"object"}`)}, &out)
	require.ErrorIs(t, err, llm.ErrRefusal)
	assert.Equal(t, llm.StopRefusal, resp.Stop)
}

var criteriaSchema = json.RawMessage(`{"type":"object","properties":{"criteria":{"type":"array","items":{"type":"string","maxLength":200},"minItems":1},"confidence":{"type":"number","minimum":0,"maximum":1}},"required":["criteria","confidence"]}`)

type criteria struct {
	Criteria   []string `json:"criteria"`
	Confidence float64  `json:"confidence"`
}

func TestStructuredNative(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "structured_native.sse"})
	require.True(t, p.Caps("claude-opus-5-5").JSONSchema)

	var out criteria
	resp, err := llm.Structured(context.Background(), p, llm.Request{
		Model:        "claude-opus-5-5",
		Messages:     []llm.Message{userText("Compile the success criteria.")},
		OutputSchema: criteriaSchema,
		OutputName:   "criteria",
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, criteria{Criteria: []string{"PR list is posted"}, Confidence: 0.9}, out)
	assert.Equal(t, int64(42), resp.Usage.Output)

	// The schema goes out as output_config.format, adapted to the structured outputs subset
	body := api.body(0)
	format := gjson.GetBytes(body, "output_config.format")
	assert.Equal(t, "json_schema", format.Get("type").String())
	assert.False(t, format.Get("schema.additionalProperties").Bool())
	assert.True(t, format.Get("schema.additionalProperties").Exists())
	assert.False(t, format.Get("schema.properties.confidence.minimum").Exists())
	assert.False(t, format.Get("schema.properties.criteria.items.maxLength").Exists())
	assert.Equal(t, int64(1), format.Get("schema.properties.criteria.minItems").Int())
	assert.False(t, gjson.GetBytes(body, "tools").Exists())
	assert.False(t, gjson.GetBytes(body, "tool_choice").Exists())
}

func TestStructuredFallbackForcesSubmitTool(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "structured_fallback.sse"})
	require.False(t, p.Caps("claude-opus-4-7").JSONSchema)

	var out criteria
	_, err := llm.Structured(context.Background(), p, llm.Request{
		Model:        "claude-opus-4-7",
		Messages:     []llm.Message{userText("Compile the success criteria.")},
		OutputSchema: criteriaSchema,
		OutputName:   "criteria",
		Effort:       llm.EffortMedium,
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, criteria{Criteria: []string{"PR list is posted"}, Confidence: 0.75}, out)

	// The submit tool carries the schema, the call is forced, and thinking is off because forced tool use rejects it
	body := api.body(0)
	assert.Equal(t, "submit_criteria", gjson.GetBytes(body, "tools.0.name").String())
	assert.JSONEq(t, string(criteriaSchema), gjson.GetBytes(body, "tools.0.input_schema").Raw)
	assert.JSONEq(t, `{"type":"tool","name":"submit_criteria"}`, gjson.GetBytes(body, "tool_choice").Raw)
	assert.Equal(t, "disabled", gjson.GetBytes(body, "thinking.type").String())
	assert.False(t, gjson.GetBytes(body, "output_config.format").Exists())
}

func TestForcedToolIsSteeredOnModelsThatRejectIt(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "text.sse"})
	_, err := p.Stream(context.Background(), llm.Request{
		Model:     "claude-opus-5-5",
		Messages:  []llm.Message{userText("Weather in Paris?")},
		Tools:     []llm.ToolDef{weatherTool},
		ForceTool: "get_weather",
	}, nil)
	require.NoError(t, err)

	// Claude Opus 5.5 returns a 400 for forced tool_choice, so the call is requested in the last user turn instead
	body := api.body(0)
	assert.False(t, gjson.GetBytes(body, "tool_choice").Exists())
	assert.Equal(t, "adaptive", gjson.GetBytes(body, "thinking.type").String())
	content := gjson.GetBytes(body, "messages.0.content").Array()
	require.Len(t, content, 2)
	assert.Contains(t, content[1].Get("text").String(), "`get_weather`")
}

func TestBudgetThinkingModel(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "text.sse"})
	_, err := p.Stream(context.Background(), llm.Request{Model: "claude-haiku-4-5-20251001", Messages: []llm.Message{userText("Hi")}, Effort: llm.EffortHigh}, nil)
	require.NoError(t, err)
	_, err = p.Stream(context.Background(), llm.Request{Model: "claude-haiku-4-5", Messages: []llm.Message{userText("Hi")}, Effort: llm.EffortLow, MaxTokens: 100_000}, nil)
	require.NoError(t, err)

	// Haiku 4.5 rejects effort and adaptive thinking, so high effort becomes a thinking budget
	body := api.body(0)
	assert.JSONEq(t, `{"type":"enabled","budget_tokens":16384}`, gjson.GetBytes(body, "thinking").Raw)
	assert.False(t, gjson.GetBytes(body, "output_config").Exists())

	// Low effort does not think, and max_tokens is clamped to the model's output limit
	body = api.body(1)
	assert.False(t, gjson.GetBytes(body, "thinking").Exists())
	assert.Equal(t, int64(64_000), gjson.GetBytes(body, "max_tokens").Int())
}

func TestCacheBreakpointsStayWithinLimit(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "text.sse"})
	_, err := p.Stream(context.Background(), llm.Request{
		Model: "claude-opus-5-5",
		System: []llm.Block{
			{Text: "base", CacheBreakpoint: true},
			{Text: "job", CacheBreakpoint: true},
			{Text: "playbook", CacheBreakpoint: true},
			{Text: "", CacheBreakpoint: true},
		},
		Messages: []llm.Message{userText("Hi")},
		Tools:    []llm.ToolDef{weatherTool},
	}, nil)
	require.NoError(t, err)

	// Three explicit markers plus the automatic one make the API maximum of four
	body := api.body(0)
	system := gjson.GetBytes(body, "system").Array()
	require.Len(t, system, 3, "empty system blocks are dropped")
	assert.False(t, system[0].Get("cache_control").Exists(), "the earliest system breakpoint is dropped first")
	assert.True(t, system[1].Get("cache_control").Exists())
	assert.True(t, system[2].Get("cache_control").Exists(), "the breakpoint of the empty block moves to the previous block")
	assert.True(t, gjson.GetBytes(body, "tools.0.cache_control").Exists())
	assert.True(t, gjson.GetBytes(body, "cache_control").Exists())
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		attempts  int
		errType   string
		retryable bool
	}{
		{name: "unauthorized", status: 401, body: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`, attempts: 1, errType: "authentication_error"},
		{name: "rate limited", status: 429, body: `{"type":"error","error":{"type":"rate_limit_error","message":"Number of request tokens has exceeded your per-minute rate limit"}}`, attempts: 3, errType: "rate_limit_error", retryable: true},
		{name: "server error", status: 500, body: `{"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, attempts: 3, errType: "api_error", retryable: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api, p := newFakeAPI(t, reply{status: tc.status, body: tc.body})
			_, err := p.Stream(context.Background(), llm.Request{Model: "claude-opus-5-5", Messages: []llm.Message{userText("Hi")}}, nil)

			// The error carries the status and the API's message, after at most two retries
			var apiErr *llm.APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, tc.status, apiErr.StatusCode)
			assert.Equal(t, tc.errType, apiErr.Type)
			assert.Equal(t, tc.retryable, apiErr.Retryable())
			assert.Contains(t, err.Error(), fmt.Sprintf("status %d", tc.status))
			assert.Contains(t, err.Error(), gjson.Get(tc.body, "error.message").String())
			assert.Equal(t, tc.attempts, api.requests())
		})
	}
}

func TestStreamErrors(t *testing.T) {
	// An error event in the middle of the stream is an overloaded API
	_, p := newFakeAPI(t, reply{fixture: "overloaded.sse"})
	_, err := p.Stream(context.Background(), llm.Request{Model: "claude-opus-5-5", Messages: []llm.Message{userText("Hi")}}, nil)
	var apiErr *llm.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, "overloaded_error", apiErr.Type)
	assert.True(t, apiErr.Retryable())

	// A stream cut off before message_delta is an error, not a partial answer
	_, p = newFakeAPI(t, reply{fixture: "truncated.sse"})
	_, err = p.Stream(context.Background(), llm.Request{Model: "claude-opus-5-5", Messages: []llm.Message{userText("Hi")}}, nil)
	require.ErrorContains(t, err, "stream ended")
}

func TestUnknownModelDefaults(t *testing.T) {
	api, p := newFakeAPI(t, reply{fixture: "text.sse"})
	caps := p.Caps("claude-future-9")
	assert.True(t, caps.Tools)
	assert.False(t, caps.JSONSchema)

	_, err := p.Stream(context.Background(), llm.Request{Model: "claude-future-9", Messages: []llm.Message{userText("Hi")}, Effort: llm.EffortLow}, nil)
	require.NoError(t, err)
	body := api.body(0)
	assert.False(t, gjson.GetBytes(body, "thinking").Exists())
	assert.Equal(t, "low", gjson.GetBytes(body, "output_config.effort").String())
	assert.Equal(t, int64(32_000), gjson.GetBytes(body, "max_tokens").Int())
}
