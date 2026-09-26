// Package openai is the adapter for OpenAI and OpenAI-compatible servers such as Ollama, OpenRouter, vLLM, LM Studio and Groq, built on Chat Completions
package openai

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/respjson"
	"github.com/openai/openai-go/v3/packages/ssestream"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

const (
	// defaultBaseURL is the OpenAI API; other servers are reached by setting Config.BaseURL, such as http://localhost:11434/v1 for Ollama
	defaultBaseURL = "https://api.openai.com/v1"
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
	// openAIAPI reports whether the endpoint is OpenAI's own API, which takes the OpenAI-only fields for every model it serves
	openAIAPI bool
}

var _ llm.Provider = (*Provider)(nil)

// New builds a provider from explicit configuration; the API key may be empty for local servers such as Ollama
// OPENAI_* environment variables never override the configuration, so a workspace never borrows the host's credentials
func New(cfg llm.Config) (*Provider, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	opts := []option.RequestOption{
		option.WithBaseURL(baseURL),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(maxRetries),
		option.WithHeaderDel("OpenAI-Organization"),
		option.WithHeaderDel("OpenAI-Project"),
		option.WithHTTPClient(llm.LimitedHTTPClient(cfg.HTTPClient)),
	}
	return &Provider{client: openai.NewClient(opts...), openAIAPI: isOpenAIAPI(baseURL)}, nil
}

// isOpenAIAPI recognizes OpenAI's API by its host, the same rule providers.modelSourceFor uses to sync models from the catalog
func isOpenAIAPI(baseURL string) bool {
	u, err := url.Parse(baseURL)
	return err == nil && strings.EqualFold(u.Hostname(), "api.openai.com")
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

	// OpenAI's API rejects max_tokens for reasoning models, so it gets max_completion_tokens even for a model the catalog doesn't list, such as one added by hand or dropped once models.dev marks it deprecated
	_, known := llm.LookupModel(llm.KindOpenAI, req.Model)
	params, err := buildParams(req, p.Caps(req.Model), known || p.openAIAPI)
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	// Stream the chunks into an accumulator that also forwards fragments for the live UI
	stream := p.client.Chat.Completions.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	acc := newAccumulator(onDelta)
	for stream.Next() {
		acc.add(stream.Current())
		if acc.size > llm.MaxAnswerBytes {
			return nil, fmt.Errorf("openai: %w", llm.ErrAnswerTooLarge)
		}
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
	// size is how many bytes of text, reasoning, refusal and tool arguments the answer holds so far
	size int
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
		// Only the first non-empty field counts, since vLLM 0.11.1 to 0.15.x sends the same text in both
		for _, field := range reasoningFields {
			if text := extraString(delta.JSON.ExtraFields, field); text != "" {
				a.reasoning.WriteString(text)
				a.size += len(text)
				a.onDelta(llm.Delta{Type: llm.DeltaReasoning, Text: text})
				break
			}
		}

		if delta.Content != "" {
			a.text.WriteString(delta.Content)
			a.size += len(delta.Content)
			a.onDelta(llm.Delta{Type: llm.DeltaText, Text: delta.Content})
		}
		if delta.Refusal != "" {
			a.refusal.WriteString(delta.Refusal)
			a.size += len(delta.Refusal)
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
	a.size += len(tc.Function.Arguments)
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
	for _, call := range a.calls {
		msg.Parts = append(msg.Parts, llm.Part{Type: llm.PartToolCall, ToolCall: &llm.ToolCall{ID: callID(call.id), Name: call.name, Args: toolArgs(call.args.String())}})
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
// It is random, so a call never shares its ID with one from an earlier turn of the same run
func callID(id string) string {
	if id != "" {
		return id
	}
	return "call_" + rand.Text()[:16]
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
	if apiErr, ok := errors.AsType[*openai.Error](err); ok {
		message := apiErr.Message
		if message == "" {
			message = apiErr.RawJSON()
		}
		return &llm.APIError{Provider: llm.KindOpenAI, StatusCode: apiErr.StatusCode, Type: apiErr.Type, Message: message, Err: err}
	}
	if streamErr, ok := errors.AsType[*ssestream.StreamError](err); ok {
		return streamError(streamErr)
	}
	return fmt.Errorf("openai: %w", err)
}

// streamError reads an error event, which carries no HTTP status, so whether a retry may help depends on what the event reports
// OpenAI sends {"error":{"type":"server_error","message":"..."}}, while OpenRouter puts the upstream status in a numeric code
func streamError(streamErr *ssestream.StreamError) *llm.APIError {
	out := &llm.APIError{Provider: llm.KindOpenAI, Message: streamErr.Message, InStream: true, Err: streamErr}
	var event struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			// A string code such as rate_limit_exceeded reads as zero, since it names the error rather than a status
			Code looseNumber `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(streamErr.Event.Data, &event) != nil {
		return out
	}
	if event.Error.Message != "" {
		out.Message = event.Error.Message
	}
	out.Type = event.Error.Type
	out.StatusCode = int(event.Error.Code)
	return out
}
