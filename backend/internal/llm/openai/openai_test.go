//go:build unit

package openai

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
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// reply is one canned HTTP response of the fake Chat Completions API
type reply struct {
	status  int
	fixture string
	body    string
}

// fakeAPI serves canned replies in order, repeating the last one, and records every request
type fakeAPI struct {
	t       *testing.T
	mu      sync.Mutex
	replies []reply
	bodies  [][]byte
	headers []http.Header
}

func newFakeAPI(t *testing.T, apiKey string, replies ...reply) (*fakeAPI, *Provider) {
	t.Helper()
	api := &fakeAPI{t: t, replies: replies}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	p, err := New(llm.Config{APIKey: apiKey, BaseURL: srv.URL + "/v1"})
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

	if r.URL.Path != "/v1/chat/completions" {
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
	w.Header().Set("Content-Type", "text/event-stream")
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

func collect(deltas *[]llm.Delta) func(llm.Delta) {
	return func(d llm.Delta) { *deltas = append(*deltas, d) }
}

func TestStreamText(t *testing.T) {
	api, p := newFakeAPI(t, "sk-test", reply{fixture: "openai_text.sse"})

	var deltas []llm.Delta
	resp, err := p.Stream(context.Background(), llm.Request{
		Model:     "gpt-4.1",
		System:    []llm.Block{{Text: "You are Umpteenth.", CacheBreakpoint: true}, {Text: "Job: report the build."}},
		Messages:  []llm.Message{userText("Did the build pass?")},
		MaxTokens: 1000,
		Effort:    llm.EffortMedium,
	}, collect(&deltas))
	require.NoError(t, err)

	assert.Equal(t, "The build passed.", resp.Message.Text())
	assert.Equal(t, llm.StopEndTurn, resp.Stop)
	assert.Equal(t, "gpt-4.1-2025-04-14", resp.Model)
	assert.Equal(t, []llm.Delta{{Type: llm.DeltaText, Text: "The build"}, {Type: llm.DeltaText, Text: " passed."}}, deltas)

	// prompt_tokens includes the cached tokens, which are split out of Input
	assert.Equal(t, llm.Usage{Input: 86, Output: 300, CacheRead: 1920}, resp.Usage)

	// The request streams with usage, joins the system blocks and uses the OpenAI-only fields for a known model
	body := api.body(0)
	assert.Equal(t, "Bearer sk-test", api.headers[0].Get("Authorization"))
	assert.True(t, gjson.GetBytes(body, "stream").Bool())
	assert.True(t, gjson.GetBytes(body, "stream_options.include_usage").Bool())
	assert.JSONEq(t, `{"role":"system","content":"You are Umpteenth.\n\nJob: report the build."}`, gjson.GetBytes(body, "messages.0").Raw)
	assert.JSONEq(t, `{"role":"user","content":"Did the build pass?"}`, gjson.GetBytes(body, "messages.1").Raw)
	assert.Equal(t, int64(1000), gjson.GetBytes(body, "max_completion_tokens").Int())
	assert.False(t, gjson.GetBytes(body, "max_tokens").Exists())
	assert.False(t, gjson.GetBytes(body, "reasoning_effort").Exists(), "GPT-4.1 is not a reasoning model")
	assert.False(t, gjson.GetBytes(body, "tools").Exists())
}

func TestStreamParallelToolCallsAcrossChunks(t *testing.T) {
	api, p := newFakeAPI(t, "sk-test", reply{fixture: "openai_tools.sse"}, reply{fixture: "openai_text.sse"})

	var deltas []llm.Delta
	req := llm.Request{
		Model:    "gpt-5",
		Messages: []llm.Message{userText("Weather in Paris and Berlin?")},
		Tools:    []llm.ToolDef{weatherTool},
		Effort:   llm.EffortLow,
	}
	resp, err := p.Stream(context.Background(), req, collect(&deltas))
	require.NoError(t, err)

	// Argument fragments are joined per index into two complete calls
	calls := resp.Message.ToolCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, llm.ToolCall{ID: "call_DdmO9pD3xa9XTPNJ32zg2hcA", Name: "get_weather", Args: json.RawMessage(`{"city": "Paris"}`)}, calls[0])
	assert.Equal(t, llm.ToolCall{ID: "call_Kq8vN2wX5mT1rY7cB4zL9pHs", Name: "get_weather", Args: json.RawMessage(`{"city": "Berlin"}`)}, calls[1])
	assert.Equal(t, llm.StopToolUse, resp.Stop)
	assert.Equal(t, llm.Usage{Input: 512, Output: 220, Reasoning: 192}, resp.Usage)
	assert.Equal(t, []llm.Delta{{Type: llm.DeltaToolCall, ToolName: "get_weather"}, {Type: llm.DeltaToolCall, ToolName: "get_weather"}}, deltas)

	// The request declares the tool and sets reasoning_effort for a reasoning model
	body := api.body(0)
	assert.JSONEq(t, `{"type":"function","function":{"name":"get_weather","description":"Get the weather for a city","parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}}`, gjson.GetBytes(body, "tools.0").Raw)
	assert.Equal(t, "low", gjson.GetBytes(body, "reasoning_effort").String())
	assert.False(t, gjson.GetBytes(body, "tool_choice").Exists())

	// Send both results back, one failed
	req.Messages = append(req.Messages, resp.Message, llm.Message{Role: llm.RoleUser, Parts: []llm.Part{
		{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: calls[0].ID, Content: "18°C"}},
		{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: calls[1].ID, Content: "service unavailable", IsError: true}},
	}})
	_, err = p.Stream(context.Background(), req, nil)
	require.NoError(t, err)

	// Both calls stay in one assistant message, and each result follows as a tool message
	body = api.body(1)
	messages := gjson.GetBytes(body, "messages").Array()
	require.Len(t, messages, 4)
	assert.Equal(t, "assistant", messages[1].Get("role").String())
	assert.False(t, messages[1].Get("content").Exists(), "an assistant turn with only tool calls has no content")
	assert.JSONEq(t, `[
		{"id":"call_DdmO9pD3xa9XTPNJ32zg2hcA","type":"function","function":{"name":"get_weather","arguments":"{\"city\": \"Paris\"}"}},
		{"id":"call_Kq8vN2wX5mT1rY7cB4zL9pHs","type":"function","function":{"name":"get_weather","arguments":"{\"city\": \"Berlin\"}"}}
	]`, messages[1].Get("tool_calls").Raw)
	assert.JSONEq(t, `{"role":"tool","tool_call_id":"call_DdmO9pD3xa9XTPNJ32zg2hcA","content":"18°C"}`, messages[2].Raw)
	assert.JSONEq(t, `{"role":"tool","tool_call_id":"call_Kq8vN2wX5mT1rY7cB4zL9pHs","content":"Error: service unavailable"}`, messages[3].Raw)
}

func TestOllamaReasoningAndWholeToolCalls(t *testing.T) {
	api, p := newFakeAPI(t, "", reply{fixture: "ollama_tools.sse"})

	var deltas []llm.Delta
	resp, err := p.Stream(context.Background(), llm.Request{
		Model:     "qwen3.8:27b-q4_K_M",
		Messages:  []llm.Message{userText("Weather in Paris and Berlin?")},
		Tools:     []llm.ToolDef{weatherTool},
		Effort:    llm.EffortMedium,
		MaxTokens: 400,
	}, collect(&deltas))
	require.NoError(t, err)

	// Ollama streams reasoning in the non-standard reasoning field, which becomes a text-only reasoning part
	require.Equal(t, llm.PartReasoning, resp.Message.Parts[0].Type)
	assert.Equal(t, &llm.Reasoning{Provider: llm.KindOpenAI, Text: "User wants weather in Paris and"}, resp.Message.Parts[0].Reasoning)
	assert.Equal(t, llm.Delta{Type: llm.DeltaReasoning, Text: "User"}, deltas[0])

	calls := resp.Message.ToolCalls()
	require.Len(t, calls, 2)
	assert.Equal(t, llm.ToolCall{ID: "call_hk5h6clw", Name: "get_weather", Args: json.RawMessage(`{"city":"Paris"}`)}, calls[0])
	assert.Equal(t, llm.ToolCall{ID: "call_9te04pvf", Name: "get_weather", Args: json.RawMessage(`{"city":"Berlin"}`)}, calls[1])
	assert.Equal(t, llm.StopToolUse, resp.Stop)
	assert.Equal(t, llm.Usage{Input: 283, Output: 78}, resp.Usage)

	// No key means no Authorization header, unknown models get max_tokens and no reasoning_effort
	body := api.body(0)
	assert.Empty(t, api.headers[0].Get("Authorization"))
	assert.Equal(t, int64(400), gjson.GetBytes(body, "max_tokens").Int())
	assert.False(t, gjson.GetBytes(body, "max_completion_tokens").Exists())
	assert.False(t, gjson.GetBytes(body, "reasoning_effort").Exists())

	// Reasoning is not sent back, since Chat Completions has no field for it
	_, err = p.Stream(context.Background(), llm.Request{Model: "qwen3.8:27b-q4_K_M", Messages: []llm.Message{userText("Hi"), resp.Message}}, nil)
	require.NoError(t, err)
	assert.NotContains(t, string(api.body(1)), "User wants")
}

func TestVLLMReasoningContent(t *testing.T) {
	_, p := newFakeAPI(t, "", reply{fixture: "vllm_reasoning.sse"})
	resp, err := p.Stream(context.Background(), llm.Request{Model: "Qwen/Qwen3-32B", Messages: []llm.Message{userText("Say hi")}}, nil)
	require.NoError(t, err)

	require.Len(t, resp.Message.Parts, 2)
	assert.Equal(t, "The user wants a greeting. Keep it short.", resp.Message.Parts[0].Reasoning.Text)
	assert.Equal(t, "Hello!", resp.Message.Text())
	assert.Equal(t, llm.Usage{Input: 24, Output: 21}, resp.Usage)
}

// vLLM 0.11.1 to 0.15.x copies reasoning into the deprecated reasoning_content, so every reasoning delta carries the same text twice
func TestVLLMReasoningInBothFieldsIsReadOnce(t *testing.T) {
	_, p := newFakeAPI(t, "", reply{fixture: "vllm_reasoning_both.sse"})
	var deltas []llm.Delta
	resp, err := p.Stream(context.Background(), llm.Request{Model: "Qwen/Qwen3-32B", Messages: []llm.Message{userText("Say hi")}}, collect(&deltas))
	require.NoError(t, err)

	require.Len(t, resp.Message.Parts, 2)
	assert.Equal(t, "The user wants a greeting. Keep it short.", resp.Message.Parts[0].Reasoning.Text)
	assert.Equal(t, "Hello!", resp.Message.Text())
	assert.Equal(t, []llm.Delta{
		{Type: llm.DeltaReasoning, Text: "The user wants a greeting."},
		{Type: llm.DeltaReasoning, Text: " Keep it short."},
		{Type: llm.DeltaText, Text: "Hello!"},
	}, deltas)
}

func TestMissingToolCallIDsAreFilledIn(t *testing.T) {
	_, p := newFakeAPI(t, "", reply{fixture: "missing_ids.sse"})
	resp, err := p.Stream(context.Background(), llm.Request{Model: "local-model", Messages: []llm.Message{userText("Weather?")}, Tools: []llm.ToolDef{weatherTool}}, nil)
	require.NoError(t, err)

	// finish_reason stop with tool calls still means the caller must run the tools
	calls := resp.Message.ToolCalls()
	require.Len(t, calls, 2)
	assert.True(t, strings.HasPrefix(calls[0].ID, "call_"), calls[0].ID)
	assert.NotEqual(t, calls[0].ID, calls[1].ID)
	assert.Equal(t, llm.StopToolUse, resp.Stop)

	// Malformed arguments are kept as a JSON string so the message stays storable
	assert.JSONEq(t, `"{\"city\":"`, string(calls[1].Args))
	_, err = json.Marshal(resp.Message)
	require.NoError(t, err)
}

func TestRefusal(t *testing.T) {
	_, p := newFakeAPI(t, "sk-test", reply{fixture: "refusal.sse"})
	resp, err := p.Stream(context.Background(), llm.Request{Model: "gpt-4.1", Messages: []llm.Message{userText("...")}}, nil)
	require.NoError(t, err)
	assert.Equal(t, llm.StopRefusal, resp.Stop)
	assert.Equal(t, "I'm sorry, I can't help with that.", resp.Message.Text())

	// Structured reports the refusal as ErrRefusal
	var out map[string]any
	_, err = llm.Structured(context.Background(), p, llm.Request{Model: "gpt-4.1", Messages: []llm.Message{userText("...")}, OutputSchema: json.RawMessage(`{"type":"object"}`)}, &out)
	require.ErrorIs(t, err, llm.ErrRefusal)
}

var criteriaSchema = json.RawMessage(`{"type":"object","properties":{"criteria":{"type":"array","items":{"type":"string"}},"confidence":{"type":"number"}},"required":["criteria","confidence"]}`)

type criteria struct {
	Criteria   []string `json:"criteria"`
	Confidence float64  `json:"confidence"`
}

func TestStructuredNative(t *testing.T) {
	api, p := newFakeAPI(t, "sk-test", reply{fixture: "structured_native.sse"})
	require.True(t, p.Caps("gpt-4.1").JSONSchema)

	var out criteria
	_, err := llm.Structured(context.Background(), p, llm.Request{
		Model:        "gpt-4.1",
		Messages:     []llm.Message{userText("Compile the success criteria.")},
		OutputSchema: criteriaSchema,
		OutputName:   "success criteria",
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, criteria{Criteria: []string{"PR list is posted"}, Confidence: 0.9}, out)

	body := api.body(0)
	format := gjson.GetBytes(body, "response_format")
	assert.Equal(t, "json_schema", format.Get("type").String())
	assert.Equal(t, "success_criteria", format.Get("json_schema.name").String())
	assert.JSONEq(t, string(criteriaSchema), format.Get("json_schema.schema").Raw)
	assert.False(t, gjson.GetBytes(body, "tools").Exists())
}

func TestStructuredFallbackForcesSubmitTool(t *testing.T) {
	api, p := newFakeAPI(t, "", reply{fixture: "structured_fallback.sse"})
	require.False(t, p.Caps("llama3.3:70b").JSONSchema)

	var out criteria
	_, err := llm.Structured(context.Background(), p, llm.Request{
		Model:        "llama3.3:70b",
		Messages:     []llm.Message{userText("Compile the success criteria.")},
		OutputSchema: criteriaSchema,
		OutputName:   "criteria",
	}, &out)
	require.NoError(t, err)
	assert.Equal(t, criteria{Criteria: []string{"PR list is posted"}, Confidence: 0.75}, out)

	// The submit tool carries the schema and is forced, with no response_format
	body := api.body(0)
	assert.Equal(t, "submit_criteria", gjson.GetBytes(body, "tools.0.function.name").String())
	assert.JSONEq(t, string(criteriaSchema), gjson.GetBytes(body, "tools.0.function.parameters").Raw)
	assert.JSONEq(t, `{"type":"function","function":{"name":"submit_criteria"}}`, gjson.GetBytes(body, "tool_choice").Raw)
	assert.False(t, gjson.GetBytes(body, "response_format").Exists())
	assert.Contains(t, gjson.GetBytes(body, "messages.0.content").String(), "`submit_criteria`")
}

func TestStructuredFallbackRetriesAnswerCutOffAtLength(t *testing.T) {
	api, p := newFakeAPI(t, "", reply{fixture: "structured_fallback_length.sse"}, reply{fixture: "structured_fallback.sse"})
	require.False(t, p.Caps("llama3.3:70b").JSONSchema)
	req := llm.Request{
		Model:        "llama3.3:70b",
		Messages:     []llm.Message{userText("Compile the success criteria.")},
		MaxTokens:    4096,
		OutputSchema: criteriaSchema,
		OutputName:   "criteria",
	}

	// The retry follows the cut-off answer, whose partial arguments come back wrapped in a JSON string
	var out criteria
	_, err := llm.Structured(context.Background(), p, req, &out)
	require.NoError(t, err)
	assert.Equal(t, criteria{Criteria: []string{"PR list is posted"}, Confidence: 0.75}, out)
	require.Equal(t, 2, api.requests())
	assert.Contains(t, gjson.GetBytes(api.body(1), "messages.2.content").String(), "cut off at the output token limit")

	// The json.RawMessage target the broker uses for ump llm would take that string as the answer, so a retry cut off too must fail
	_, p = newFakeAPI(t, "", reply{fixture: "structured_fallback_length.sse"})
	var raw json.RawMessage
	_, err = llm.Structured(context.Background(), p, req, &raw)
	require.ErrorContains(t, err, "cut off at the output token limit")
}

// capsOverride reports JSON-schema support the way a caps override from the database would
type capsOverride struct{ *Provider }

func (c capsOverride) Caps(model string) llm.Caps {
	caps := c.Provider.Caps(model)
	caps.JSONSchema = true
	return caps
}

func TestStructuredNativeWithCapsOverride(t *testing.T) {
	api, p := newFakeAPI(t, "", reply{fixture: "structured_native.sse"})

	// Structured picks the mechanism from the wrapper's caps, and the adapter must follow it for an unknown model
	var out criteria
	_, err := llm.Structured(context.Background(), capsOverride{p}, llm.Request{Model: "qwen3.8:27b-q4_K_M", Messages: []llm.Message{userText("Compile.")}, OutputSchema: criteriaSchema}, &out)
	require.NoError(t, err)
	assert.Equal(t, "json_schema", gjson.GetBytes(api.body(0), "response_format.type").String())
	assert.False(t, gjson.GetBytes(api.body(0), "tools").Exists())
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		attempts  int
		retryable bool
	}{
		{name: "unauthorized", status: 401, body: `{"error":{"message":"Incorrect API key provided: sk-test.","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`, attempts: 1},
		{name: "rate limited", status: 429, body: `{"error":{"message":"Rate limit reached for gpt-4.1 in organization org-abc on tokens per min (TPM).","type":"tokens","param":null,"code":"rate_limit_exceeded"}}`, attempts: 3, retryable: true},
		{name: "server error", status: 500, body: `{"error":{"message":"The server had an error while processing your request.","type":"server_error","param":null,"code":null}}`, attempts: 3, retryable: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			api, p := newFakeAPI(t, "sk-test", reply{status: tc.status, body: tc.body})
			_, err := p.Stream(context.Background(), llm.Request{Model: "gpt-4.1", Messages: []llm.Message{userText("Hi")}}, nil)

			// The error carries the status and the API's message, after at most two retries
			var apiErr *llm.APIError
			require.ErrorAs(t, err, &apiErr)
			assert.Equal(t, tc.status, apiErr.StatusCode)
			assert.Equal(t, tc.retryable, apiErr.Retryable())
			assert.True(t, llm.Unbilled(err), "an error status before the answer costs nothing")
			assert.Contains(t, err.Error(), fmt.Sprintf("status %d", tc.status))
			assert.Contains(t, err.Error(), gjson.Get(tc.body, "error.message").String())
			assert.Equal(t, tc.attempts, api.requests())
		})
	}
}

func TestStreamErrors(t *testing.T) {
	// An OpenRouter error inside the stream becomes an APIError with the upstream status from its code, so an overloaded upstream is retried
	_, p := newFakeAPI(t, "", reply{fixture: "stream_error.sse"})
	_, err := p.Stream(context.Background(), llm.Request{Model: "openai/gpt-5", Messages: []llm.Message{userText("Hi")}}, nil)
	var apiErr *llm.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 502, apiErr.StatusCode)
	assert.Equal(t, "Upstream provider overloaded", apiErr.Message)
	assert.True(t, llm.IsTransient(err))
	assert.True(t, apiErr.InStream, "the answer had already started, so the call may have been billed")

	// A stream cut off before finish_reason is an error, not a partial answer
	_, p = newFakeAPI(t, "", reply{fixture: "truncated.sse"})
	_, err = p.Stream(context.Background(), llm.Request{Model: "gpt-4.1", Messages: []llm.Message{userText("Hi")}}, nil)
	require.ErrorContains(t, err, "stream ended")
}

func TestStreamCutOffMidAnswerIsTransient(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "openai_text.sse"))
	require.NoError(t, err)
	for _, tc := range []struct {
		name  string
		proto int64
	}{
		{name: "http/1.1", proto: 1},
		{name: "http/2", proto: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The server sends half an answer and aborts, which over HTTP/2 resets the stream the way a provider's edge proxy does when its upstream fails
			var proto atomic.Int64
			srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				proto.Store(int64(r.ProtoMajor))
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write(data[:len(data)/2])
				w.(http.Flusher).Flush()
				panic(http.ErrAbortHandler)
			}))
			srv.EnableHTTP2 = tc.proto == 2
			srv.StartTLS()
			t.Cleanup(srv.Close)

			// The client clones the default transport like the egress guard does, so it speaks HTTP/2 whenever the server offers it
			transport := http.DefaultTransport.(*http.Transport).Clone()
			transport.TLSClientConfig = srv.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
			p, err := New(llm.Config{APIKey: "test-key", BaseURL: srv.URL + "/v1", HTTPClient: &http.Client{Transport: transport}})
			require.NoError(t, err)

			_, err = p.Stream(context.Background(), llm.Request{Model: "gpt-4.1", Messages: []llm.Message{userText("Hi")}}, nil)
			require.Equal(t, tc.proto, proto.Load())
			require.Error(t, err)
			assert.True(t, llm.IsTransient(err), "a dropped stream must be retried, got %v", err)
		})
	}
}

func TestCapsDefaults(t *testing.T) {
	_, p := newFakeAPI(t, "", reply{fixture: "openai_text.sse"})

	// Unknown models can call tools but get the submit-tool fallback for structured output
	assert.Equal(t, llm.Caps{Tools: true, ParallelTools: true, Context: 32_768}, p.Caps("qwen3.8:27b-q4_K_M"))

	caps := p.Caps("gpt-5-2025-08-07")
	assert.True(t, caps.JSONSchema)
	assert.True(t, caps.Reasoning)
	assert.Equal(t, 272_000, caps.Context, "the input limit, since GPT-5 shares its window with the answer")
}

func TestStrictModeOnlyForStrictCompatibleSchemas(t *testing.T) {
	parse := func(s string) any {
		var v any
		require.NoError(t, json.Unmarshal([]byte(s), &v))
		return v
	}
	assert.True(t, strictCompatible(parse(`{"type":"object","additionalProperties":false,"required":["a","b"],"properties":{"a":{"type":"string"},"b":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["c"],"properties":{"c":{"anyOf":[{"type":"string"},{"type":"null"}]}}}}}}`)))
	assert.False(t, strictCompatible(parse(`{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`)))
	assert.False(t, strictCompatible(parse(`{"type":"object","required":["a"],"properties":{"a":{"type":"string"}}}`)))
	assert.False(t, strictCompatible(parse(`{"type":"object"}`)))
	assert.False(t, strictCompatible(parse(`{"type":"object","additionalProperties":false,"required":["a"],"properties":{"a":{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}}}`)))
}

func TestStreamStopsAtTheAnswerLimit(t *testing.T) {
	// Chunks well within the response limit add up to more text than any model writes in one answer
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := `data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"content":"` + strings.Repeat("x", 64<<10) + `"}}]}` + "\n\n"
		for range 70 {
			_, _ = io.WriteString(w, chunk)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	p, err := New(llm.Config{BaseURL: srv.URL + "/v1"})
	require.NoError(t, err)

	_, err = p.Stream(context.Background(), llm.Request{Model: "gpt-4.1", Messages: []llm.Message{userText("Hi")}}, nil)
	require.ErrorIs(t, err, llm.ErrAnswerTooLarge)
}

func TestStreamStopsAtTheToolCallLimit(t *testing.T) {
	// One chunk holds far more tiny calls than any model makes, well within the answer limit
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		calls := make([]string, 0, 1000)
		for i := range 1000 {
			calls = append(calls, fmt.Sprintf(`{"index":%d,"id":"c%d","type":"function","function":{"name":"read_file","arguments":"{}"}}`, i, i))
		}
		_, _ = io.WriteString(w, `data: {"id":"c","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"tool_calls":[`+strings.Join(calls, ",")+`]},"finish_reason":"tool_calls"}]}`+"\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	p, err := New(llm.Config{BaseURL: srv.URL + "/v1"})
	require.NoError(t, err)

	announced := 0
	_, err = p.Stream(context.Background(), llm.Request{Model: "gpt-4.1", Messages: []llm.Message{userText("Hi")}}, func(d llm.Delta) {
		if d.Type == llm.DeltaToolCall {
			announced++
		}
	})
	require.ErrorIs(t, err, llm.ErrTooManyToolCalls)
	assert.Equal(t, llm.MaxToolCalls, announced, "calls past the limit are not announced")
}
