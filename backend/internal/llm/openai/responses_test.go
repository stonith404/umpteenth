//go:build unit

package openai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// fakeResponsesAPI answers every request to OpenAI's Responses API with the same event stream
type fakeResponsesAPI struct {
	stream string
	mu     sync.Mutex
	bodies [][]byte
}

func (f *fakeResponsesAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/v1/responses" {
		http.Error(w, "unexpected path "+r.URL.Path, http.StatusNotFound)
		return
	}
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, f.stream)
}

func (f *fakeResponsesAPI) body(i int) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[i]
}

// newOpenAIAPI returns a provider of OpenAI's API on the Responses API, whose requests reach a fake serving the stream
func newOpenAIAPI(t *testing.T, stream string) (*fakeResponsesAPI, *Provider) {
	t.Helper()
	api := &fakeResponsesAPI{stream: stream}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	target, err := url.Parse(srv.URL)
	require.NoError(t, err)
	p, err := New(llm.Config{APIKey: "sk-test", Responses: true, HTTPClient: &http.Client{Transport: redirectTransport{target: target}}})
	require.NoError(t, err)
	return api, p
}

// OpenAI's Chat Completions endpoint rejects reasoning effort together with function tools for GPT-5.4 models, which the Responses API accepts
func TestOpenAIAPIReasonsWithToolsThroughResponses(t *testing.T) {
	data, err := os.ReadFile("testdata/responses_tools.sse")
	require.NoError(t, err)
	api, p := newOpenAIAPI(t, string(data))
	question := userText("What's the weather in Paris and Berlin?")
	req := llm.Request{
		Model:     "gpt-5.4-mini",
		System:    []llm.Block{{Text: "You are Umpteenth."}, {Text: "Job: report the weather."}},
		Messages:  []llm.Message{question},
		Tools:     []llm.ToolDef{weatherTool},
		MaxTokens: 4096,
		Effort:    llm.EffortMedium,
	}

	var deltas []llm.Delta
	resp, err := p.Stream(context.Background(), req, collect(&deltas))
	require.NoError(t, err)

	body := api.body(0)
	assert.Equal(t, "medium", gjson.GetBytes(body, "reasoning.effort").String())
	assert.JSONEq(t, `{"type":"function","name":"get_weather","description":"Get the weather for a city","strict":false,"parameters":{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}}`, gjson.GetBytes(body, "tools.0").Raw)
	assert.JSONEq(t, `["reasoning.encrypted_content"]`, gjson.GetBytes(body, "include").Raw)
	assert.False(t, gjson.GetBytes(body, "store").Bool())
	assert.True(t, gjson.GetBytes(body, "store").Exists(), "OpenAI stores responses unless told not to")
	assert.Equal(t, "You are Umpteenth.\n\nJob: report the weather.", gjson.GetBytes(body, "instructions").String())
	assert.JSONEq(t, `[{"role":"user","content":"What's the weather in Paris and Berlin?"}]`, gjson.GetBytes(body, "input").Raw)
	assert.Equal(t, int64(4096), gjson.GetBytes(body, "max_output_tokens").Int())

	assert.Equal(t, llm.StopToolUse, resp.Stop)
	assert.Equal(t, "gpt-5.4-mini-2026-03-17", resp.Model)
	assert.Equal(t, llm.Usage{Input: 464, Output: 220, CacheRead: 2048, Reasoning: 192}, resp.Usage)
	assert.Equal(t, []llm.Delta{{Type: llm.DeltaToolCall, ToolName: "get_weather"}, {Type: llm.DeltaToolCall, ToolName: "get_weather"}}, deltas)
	require.Len(t, resp.Message.Parts, 3)
	assert.Equal(t, llm.PartReasoning, resp.Message.Parts[0].Type)
	assert.Equal(t, &llm.ToolCall{ID: "call_DdmO9pD3xa9XTPNJ32zg2hcA", Name: "get_weather", Args: []byte(`{"city":"Paris"}`)}, resp.Message.Parts[1].ToolCall)
	assert.Equal(t, &llm.ToolCall{ID: "call_Kq8vN2wX5mT1rY7cB4zL9pHs", Name: "get_weather", Args: []byte(`{"city":"Berlin"}`)}, resp.Message.Parts[2].ToolCall)

	// The next turn sends the encrypted reasoning back before the calls it led to, followed by their results
	results := llm.Message{Role: llm.RoleUser, Parts: []llm.Part{
		{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: "call_DdmO9pD3xa9XTPNJ32zg2hcA", Content: "18°C, sunny"}},
		{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: "call_Kq8vN2wX5mT1rY7cB4zL9pHs", Content: "city not found", IsError: true}},
	}}
	req.Messages = []llm.Message{question, resp.Message, results}
	_, err = p.Stream(context.Background(), req, nil)
	require.NoError(t, err)
	assert.JSONEq(t, `[
		{"role":"user","content":"What's the weather in Paris and Berlin?"},
		{"type":"reasoning","id":"rs_0a1","summary":[],"encrypted_content":"gAAAAABo-encrypted"},
		{"type":"function_call","call_id":"call_DdmO9pD3xa9XTPNJ32zg2hcA","name":"get_weather","arguments":"{\"city\":\"Paris\"}"},
		{"type":"function_call","call_id":"call_Kq8vN2wX5mT1rY7cB4zL9pHs","name":"get_weather","arguments":"{\"city\":\"Berlin\"}"},
		{"type":"function_call_output","call_id":"call_DdmO9pD3xa9XTPNJ32zg2hcA","output":"18°C, sunny"},
		{"type":"function_call_output","call_id":"call_Kq8vN2wX5mT1rY7cB4zL9pHs","output":"Error: city not found"}
	]`, gjson.GetBytes(api.body(1), "input").Raw)
}

func TestOpenAIAPIStructuredOutput(t *testing.T) {
	data, err := os.ReadFile("testdata/responses_structured.sse")
	require.NoError(t, err)
	api, p := newOpenAIAPI(t, string(data))

	var out criteria
	_, err = llm.Structured(context.Background(), p, llm.Request{Model: "gpt-5.4-mini", Messages: []llm.Message{userText("Compile the success criteria.")}, OutputSchema: criteriaSchema, OutputName: "job spec"}, &out)
	require.NoError(t, err)
	assert.Equal(t, criteria{Criteria: []string{"PR list is posted"}, Confidence: 0.9}, out)

	format := gjson.GetBytes(api.body(0), "text.format")
	assert.Equal(t, "json_schema", format.Get("type").String())
	assert.Equal(t, "job_spec", format.Get("name").String())
	assert.False(t, format.Get("strict").Exists(), "the schema leaves objects open, which strict mode rejects")
	assert.False(t, gjson.GetBytes(api.body(0), "tools").Exists())
}

func TestOpenAIAPIStreamEndings(t *testing.T) {
	const created = "event: response.created\ndata: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{\"id\":\"resp_1\",\"status\":\"in_progress\",\"model\":\"gpt-5.4-mini\",\"output\":[]}}\n\n"

	t.Run("cut off at the token limit", func(t *testing.T) {
		_, p := newOpenAIAPI(t, created+"event: response.incomplete\ndata: {\"type\":\"response.incomplete\",\"sequence_number\":1,\"response\":{\"id\":\"resp_1\",\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"model\":\"gpt-5.4-mini\",\"output\":[{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"The build\"}]}]}}\n\n")
		resp, err := p.Stream(context.Background(), llm.Request{Model: "gpt-5.4-mini", Messages: []llm.Message{userText("Hi")}}, nil)
		require.NoError(t, err)
		assert.Equal(t, llm.StopMaxTokens, resp.Stop)
		assert.Equal(t, "The build", resp.Message.Text())
	})

	t.Run("failed response", func(t *testing.T) {
		_, p := newOpenAIAPI(t, created+"event: response.failed\ndata: {\"type\":\"response.failed\",\"sequence_number\":1,\"response\":{\"id\":\"resp_1\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"The model failed to generate a response.\"},\"model\":\"gpt-5.4-mini\",\"output\":[]}}\n\n")
		_, err := p.Stream(context.Background(), llm.Request{Model: "gpt-5.4-mini", Messages: []llm.Message{userText("Hi")}}, nil)
		var apiErr *llm.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, "The model failed to generate a response.", apiErr.Message)
		assert.True(t, llm.IsTransient(err))
		assert.False(t, llm.Unbilled(err))
	})

	t.Run("error event", func(t *testing.T) {
		_, p := newOpenAIAPI(t, created+"event: error\ndata: {\"type\":\"error\",\"sequence_number\":1,\"code\":\"rate_limit_exceeded\",\"message\":\"Rate limit reached for gpt-5.4-mini.\",\"param\":null}\n\n")
		_, err := p.Stream(context.Background(), llm.Request{Model: "gpt-5.4-mini", Messages: []llm.Message{userText("Hi")}}, nil)
		var apiErr *llm.APIError
		require.ErrorAs(t, err, &apiErr)
		assert.Equal(t, "Rate limit reached for gpt-5.4-mini.", apiErr.Message)
		assert.True(t, llm.IsTransient(err))
	})

	t.Run("stream ended without a final event", func(t *testing.T) {
		_, p := newOpenAIAPI(t, created)
		_, err := p.Stream(context.Background(), llm.Request{Model: "gpt-5.4-mini", Messages: []llm.Message{userText("Hi")}}, nil)
		require.ErrorIs(t, err, llm.ErrIncompleteStream)
	})
}
