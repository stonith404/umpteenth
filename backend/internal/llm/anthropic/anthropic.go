// Package anthropic is the native Claude adapter for the llm interface, built on the official Anthropic SDK (PLAN.md §5.2)
package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// maxRetries bounds the SDK's own retries of 408, 409, 429, 5xx and connection errors
const maxRetries = 2

// Provider calls the Claude Messages API
type Provider struct {
	client anthropic.Client
}

var _ llm.Provider = (*Provider)(nil)

// New builds a provider from explicit configuration only; ANTHROPIC_* environment variables are ignored so a workspace never borrows the host's credentials
func New(cfg llm.Config) (*Provider, error) {
	opts := []option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithAPIKey(cfg.APIKey),
		option.WithMaxRetries(maxRetries),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}
	return &Provider{client: anthropic.NewClient(opts...)}, nil
}

// Caps returns the catalog capabilities, or conservative defaults for Claude models the catalog does not know
func (p *Provider) Caps(model string) llm.Caps {
	if e, ok := llm.LookupModel(llm.KindAnthropic, model); ok {
		return e.Caps
	}
	return llm.Caps{Tools: true, ParallelTools: true, Reasoning: true, PromptCache: true, Vision: true, Context: 200_000}
}

func (p *Provider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	start := time.Now()
	params, err := buildParams(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic: %w", err)
	}

	// Stream the response, accumulating the final message while forwarding fragments for the live UI
	stream := p.client.Messages.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	var msg anthropic.Message
	for stream.Next() {
		event := stream.Current()
		err := msg.Accumulate(event)
		if err != nil {
			return nil, fmt.Errorf("anthropic: invalid stream: %w", err)
		}
		if onDelta != nil {
			emitDelta(event, onDelta)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, mapError(err)
	}

	// A stream that ends without message_delta was cut off, and its partial message must not be mistaken for an answer
	if msg.StopReason == "" {
		return nil, fmt.Errorf("anthropic: %w", llm.ErrIncompleteStream)
	}

	message, err := convertResponse(msg)
	if err != nil {
		return nil, err
	}
	return &llm.Response{
		Message: message,
		Stop:    mapStop(msg.StopReason),
		Usage:   mapUsage(msg.Usage),
		Latency: time.Since(start),
		Model:   msg.Model,
	}, nil
}

// emitDelta forwards text, thinking and tool call starts; tool arguments are not streamed to the UI
func emitDelta(event anthropic.MessageStreamEventUnion, onDelta func(llm.Delta)) {
	switch event.Type {
	case "content_block_start":
		if event.ContentBlock.Type == "tool_use" {
			onDelta(llm.Delta{Type: llm.DeltaToolCall, ToolName: event.ContentBlock.Name})
		}
	case "content_block_delta":
		switch event.Delta.Type {
		case "text_delta":
			onDelta(llm.Delta{Type: llm.DeltaText, Text: event.Delta.Text})
		case "thinking_delta":
			onDelta(llm.Delta{Type: llm.DeltaReasoning, Text: event.Delta.Thinking})
		}
	}
}

// convertResponse maps content blocks to parts in their original order
// Thinking and redacted_thinking blocks are stored whole in Reasoning.Opaque, signature included, so the next turn can send them back unchanged
func convertResponse(msg anthropic.Message) (llm.Message, error) {
	out := llm.Message{Role: llm.RoleAssistant}
	for _, block := range msg.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				out.Parts = append(out.Parts, llm.TextPart(block.Text))
			}
		case "thinking", "redacted_thinking":
			var opaque bytes.Buffer
			err := json.Compact(&opaque, []byte(block.RawJSON()))
			if err != nil {
				return out, fmt.Errorf("anthropic: invalid %s block: %w", block.Type, err)
			}
			out.Parts = append(out.Parts, llm.Part{Type: llm.PartReasoning, Reasoning: &llm.Reasoning{
				Provider: llm.KindAnthropic,
				Opaque:   json.RawMessage(opaque.Bytes()),
				Text:     block.Thinking,
			}})
		case "tool_use":
			args := block.Input
			if len(args) == 0 || !json.Valid(args) {
				args = json.RawMessage(`{}`)
			}
			out.Parts = append(out.Parts, llm.Part{Type: llm.PartToolCall, ToolCall: &llm.ToolCall{ID: block.ID, Name: block.Name, Args: args}})
		}
	}
	return out, nil
}

func mapStop(reason anthropic.StopReason) llm.StopReason {
	switch reason {
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence:
		return llm.StopEndTurn
	case anthropic.StopReasonToolUse:
		return llm.StopToolUse
	case anthropic.StopReasonMaxTokens, anthropic.StopReasonModelContextWindowExceeded:
		return llm.StopMaxTokens
	case anthropic.StopReasonRefusal:
		return llm.StopRefusal
	}
	return llm.StopOther
}

// mapUsage relies on the API's input_tokens already excluding cache reads and writes
func mapUsage(u anthropic.Usage) llm.Usage {
	return llm.Usage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  u.CacheReadInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
		Reasoning:  u.OutputTokensDetails.ThinkingTokens,
	}
}

// mapError turns SDK errors into *llm.APIError so callers can branch on the status without importing the SDK
func mapError(err error) error {
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		return fmt.Errorf("anthropic: %w", err)
	}

	// The error body looks like {"type":"error","error":{"type":"rate_limit_error","message":"..."}}
	var body struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	raw := apiErr.RawJSON()
	message := raw
	errType := string(apiErr.Type())
	if json.Unmarshal([]byte(raw), &body) == nil && body.Error.Message != "" {
		message = body.Error.Message
		if errType == "" {
			errType = body.Error.Type
		}
	}
	return &llm.APIError{Provider: llm.KindAnthropic, StatusCode: apiErr.StatusCode, Type: errType, Message: message, Err: err}
}
