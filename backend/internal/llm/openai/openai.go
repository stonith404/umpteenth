// Package openai is the adapter for OpenAI and OpenAI-compatible servers such as Ollama, OpenRouter, vLLM, LM Studio and Groq, built on Chat Completions (PLAN.md §5.2)
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/respjson"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

const (
	// DefaultBaseURL is the OpenAI API; other servers are reached by setting Config.BaseURL, such as http://localhost:11434/v1 for Ollama
	DefaultBaseURL = "https://api.openai.com/v1"
	// maxRetries bounds the SDK's own retries of 408, 409, 429, 5xx and connection errors
	maxRetries = 2
	// unknownContext is assumed for models outside the catalog, which are mostly local models with small default windows
	unknownContext = 32_768
)

// reasoningFields are the delta fields in which compatible servers stream reasoning: vLLM and DeepSeek use reasoning_content, Ollama and OpenRouter use reasoning
var reasoningFields = []string{"reasoning_content", "reasoning"}

// Provider calls a Chat Completions endpoint
type Provider struct {
	client openai.Client
}

var _ llm.Provider = (*Provider)(nil)

// New builds a provider from explicit configuration; the API key may be empty for local servers such as Ollama
// OPENAI_* environment variables never override the configuration, so a workspace never borrows the host's credentials
func New(cfg llm.Config) (*Provider, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	opts := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(maxRetries),
		option.WithHeaderDel("OpenAI-Organization"),
		option.WithHeaderDel("OpenAI-Project"),
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	return &Provider{client: openai.NewClient(opts...)}, nil
}

// Caps returns the catalog capabilities, or defaults for unknown models: tool calling but no native JSON schema output
func (p *Provider) Caps(model string) llm.Caps {
	if e, ok := llm.LookupModel(llm.KindOpenAI, model); ok {
		return e.Caps
	}
	return llm.Caps{Tools: true, ParallelTools: true, Context: unknownContext}
}

func (p *Provider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	start := time.Now()
	if onDelta == nil {
		onDelta = func(llm.Delta) {}
	}
	_, known := llm.LookupModel(llm.KindOpenAI, req.Model)
	params, err := buildParams(req, p.Caps(req.Model), known)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	// Stream the chunks into an accumulator that also forwards fragments for the live UI
	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	acc := newAccumulator(onDelta)
	for stream.Next() {
		acc.add(stream.Current())
	}
	if err := stream.Err(); err != nil {
		return nil, mapError(err)
	}

	// A stream without finish_reason was cut off, and its partial message must not be mistaken for an answer
	if acc.finish == "" {
		return nil, fmt.Errorf("openai: %w", llm.ErrIncompleteStream)
	}

	resp := acc.response()
	resp.Latency = time.Since(start)
	if resp.Model == "" {
		resp.Model = req.Model
	}
	return resp, nil
}

// toolCallState collects one streamed tool call, whose arguments arrive in fragments
type toolCallState struct {
	id   string
	name string
	args strings.Builder
}

// accumulator rebuilds the complete answer from chunks
type accumulator struct {
	onDelta   func(llm.Delta)
	model     string
	text      strings.Builder
	reasoning strings.Builder
	refusal   strings.Builder
	calls     []*toolCallState
	byIndex   map[int64]*toolCallState
	finish    string
	usage     openai.CompletionUsage
	hasUsage  bool
}

func newAccumulator(onDelta func(llm.Delta)) *accumulator {
	return &accumulator{onDelta: onDelta, byIndex: map[int64]*toolCallState{}}
}

func (a *accumulator) add(chunk openai.ChatCompletionChunk) {
	if chunk.Model != "" {
		a.model = chunk.Model
	}

	// Usage arrives on a final chunk without choices because of stream_options.include_usage
	if chunk.JSON.Usage.Valid() {
		a.usage = chunk.Usage
		a.hasUsage = true
	}

	// Only the first choice matters since n is never set
	for _, choice := range chunk.Choices {
		if choice.Index != 0 {
			continue
		}
		delta := choice.Delta

		// Collect reasoning from whichever non-standard field the server uses
		for _, field := range reasoningFields {
			if text := extraString(delta.JSON.ExtraFields, field); text != "" {
				a.reasoning.WriteString(text)
				a.onDelta(llm.Delta{Type: llm.DeltaReasoning, Text: text})
			}
		}

		if delta.Content != "" {
			a.text.WriteString(delta.Content)
			a.onDelta(llm.Delta{Type: llm.DeltaText, Text: delta.Content})
		}
		if delta.Refusal != "" {
			a.refusal.WriteString(delta.Refusal)
		}
		for _, tc := range delta.ToolCalls {
			a.addToolCall(tc)
		}
		if choice.FinishReason != "" {
			a.finish = choice.FinishReason
		}
	}
}

// addToolCall merges a tool call fragment by its index
// Some servers reuse index 0 for every call, so a fragment with a new ID starts a new call even at a known index
func (a *accumulator) addToolCall(tc openai.ChatCompletionChunkChoiceDeltaToolCall) {
	state, ok := a.byIndex[tc.Index]
	if !ok || (tc.ID != "" && state.id != "" && tc.ID != state.id) {
		state = &toolCallState{id: tc.ID}
		a.byIndex[tc.Index] = state
		a.calls = append(a.calls, state)
	}
	if state.id == "" {
		state.id = tc.ID
	}

	// Announce the call once its name is known, which is usually the first fragment
	if state.name == "" && tc.Function.Name != "" {
		state.name = tc.Function.Name
		a.onDelta(llm.Delta{Type: llm.DeltaToolCall, ToolName: state.name})
	}
	state.args.WriteString(tc.Function.Arguments)
}

func (a *accumulator) response() *llm.Response {
	msg := llm.Message{Role: llm.RoleAssistant}
	if a.reasoning.Len() > 0 {
		msg.Parts = append(msg.Parts, llm.Part{Type: llm.PartReasoning, Reasoning: &llm.Reasoning{Provider: llm.KindOpenAI, Text: a.reasoning.String()}})
	}
	if a.text.Len() > 0 {
		msg.Parts = append(msg.Parts, llm.TextPart(a.text.String()))
	} else if a.refusal.Len() > 0 {
		msg.Parts = append(msg.Parts, llm.TextPart(a.refusal.String()))
	}
	for i, call := range a.calls {
		msg.Parts = append(msg.Parts, llm.Part{Type: llm.PartToolCall, ToolCall: &llm.ToolCall{ID: callID(call.id, i), Name: call.name, Args: toolArgs(call.args.String())}})
	}

	// Some servers finish with stop even when the message calls tools, and the tool calls are what the caller acts on
	stop := mapStop(a.finish)
	switch {
	case a.refusal.Len() > 0:
		stop = llm.StopRefusal
	case len(a.calls) > 0 && stop == llm.StopEndTurn:
		stop = llm.StopToolUse
	}
	return &llm.Response{Message: msg, Stop: stop, Usage: a.normalizedUsage(), Model: a.model}
}

// normalizedUsage splits prompt_tokens, which includes cached tokens, into uncached input and cache reads
func (a *accumulator) normalizedUsage() llm.Usage {
	if !a.hasUsage {
		return llm.Usage{}
	}
	u := a.usage
	cacheRead := u.PromptTokensDetails.CachedTokens
	cacheWrite := u.PromptTokensDetails.CacheWriteTokens
	return llm.Usage{
		Input:      max(0, u.PromptTokens-cacheRead-cacheWrite),
		Output:     u.CompletionTokens,
		CacheRead:  cacheRead,
		CacheWrite: cacheWrite,
		Reasoning:  u.CompletionTokensDetails.ReasoningTokens,
	}
}

func mapStop(reason string) llm.StopReason {
	switch reason {
	case "stop":
		return llm.StopEndTurn
	case "tool_calls", "function_call":
		return llm.StopToolUse
	case "length":
		return llm.StopMaxTokens
	case "content_filter":
		return llm.StopRefusal
	}
	return llm.StopOther
}

// callID fills in an ID for servers that omit them, since tool results are matched to calls by ID
func callID(id string, i int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("call_%d", i)
}

// toolArgs keeps arguments as JSON; malformed arguments are wrapped in a JSON string so the message can still be stored and the tool can report the error
func toolArgs(raw string) json.RawMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return json.RawMessage(`{}`)
	}
	if json.Valid([]byte(raw)) {
		return json.RawMessage(raw)
	}
	quoted, _ := json.Marshal(raw)
	return quoted
}

// extraString reads a string from a field the SDK does not model
func extraString(fields map[string]respjson.Field, key string) string {
	f, ok := fields[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal([]byte(f.Raw()), &s) != nil {
		return ""
	}
	return s
}

// mapError turns SDK errors into *llm.APIError so callers can branch on the status without importing the SDK
func mapError(err error) error {
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		message := apiErr.Message
		if message == "" {
			message = apiErr.RawJSON()
		}
		return &llm.APIError{Provider: llm.KindOpenAI, StatusCode: apiErr.StatusCode, Type: apiErr.Type, Message: message, Err: err}
	}

	// Errors sent inside the event stream carry no HTTP status
	var streamErr *ssestream.StreamError
	if errors.As(err, &streamErr) {
		return &llm.APIError{Provider: llm.KindOpenAI, Message: streamErr.Message, Err: err}
	}
	return fmt.Errorf("openai: %w", err)
}
