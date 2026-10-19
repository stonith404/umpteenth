package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// reasoningItem is what a reasoning part keeps of OpenAI's reasoning item, enough to send it back on the next turn
type reasoningItem struct {
	ID               string `json:"id"`
	EncryptedContent string `json:"encrypted_content"`
}

func (p *Provider) streamResponses(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	params, err := responsesParams(req, p.Caps(req.Model))
	if err != nil {
		return nil, fmt.Errorf("openai: %w", err)
	}

	// Forward fragments for the live UI and enforce the answer limits, while the final event carries the complete answer
	stream := p.client.Responses.NewStreaming(ctx, params)
	defer func() { _ = stream.Close() }()
	var final *responses.Response
	size, calls := 0, 0
	for stream.Next() {
		event := stream.Current()
		switch event.Type {
		case "response.output_text.delta":
			size += len(event.Delta)
			onDelta(llm.Delta{Type: llm.DeltaText, Text: event.Delta})
		case "response.refusal.delta", "response.function_call_arguments.delta":
			size += len(event.Delta)
		case "response.output_item.added":
			if event.Item.Type != "function_call" {
				break
			}
			if calls >= llm.MaxToolCalls {
				return nil, fmt.Errorf("openai: %w", llm.ErrTooManyToolCalls)
			}
			calls++
			onDelta(llm.Delta{Type: llm.DeltaToolCall, ToolName: event.Item.Name})
		case "response.completed", "response.incomplete":
			final = &event.Response
		case "response.failed":
			return nil, &llm.APIError{Provider: llm.KindOpenAI, Type: string(event.Response.Error.Code), Message: event.Response.Error.Message, InStream: true}
		case "error":
			return nil, &llm.APIError{Provider: llm.KindOpenAI, Type: event.Code, Message: event.Message, InStream: true}
		}
		if size > llm.MaxAnswerBytes {
			return nil, fmt.Errorf("openai: %w", llm.ErrAnswerTooLarge)
		}
	}
	if err := stream.Err(); err != nil {
		return nil, mapError(err)
	}

	// A stream without a final event was cut off, and its partial answer must not be mistaken for one
	if final == nil {
		return nil, fmt.Errorf("openai: %w", llm.ErrIncompleteStream)
	}
	return responseFrom(final), nil
}

// responsesParams translates a provider-agnostic request into a Responses API request
func responsesParams(req llm.Request, caps llm.Caps) (responses.ResponseNewParams, error) {
	params := responses.ResponseNewParams{
		Model: req.Model,
		// Nothing is stored at OpenAI, so reasoning travels from turn to turn as encrypted content instead
		Store: openai.Bool(false),
	}

	var system []string
	for _, b := range req.System {
		if b.Text != "" {
			system = append(system, b.Text)
		}
	}
	if len(system) > 0 {
		params.Instructions = openai.String(strings.Join(system, "\n\n"))
	}

	input, err := convertInput(req.Messages)
	if err != nil {
		return params, err
	}
	params.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: input}

	// Strict function tools would need schemas that list every property as required, which the tools' schemas don't
	for _, d := range req.Tools {
		schema, err := toolSchema(d)
		if err != nil {
			return params, err
		}
		fn := responses.FunctionToolParam{Name: d.Name, Parameters: schema, Strict: openai.Bool(false)}
		if d.Description != "" {
			fn.Description = openai.String(d.Description)
		}
		params.Tools = append(params.Tools, responses.ToolUnionParam{OfFunction: &fn})
	}
	if req.ForceTool != "" {
		params.ToolChoice = responses.ResponseNewParamsToolChoiceUnion{OfFunctionTool: &responses.ToolChoiceFunctionParam{Name: req.ForceTool}}
	}

	if len(req.OutputSchema) > 0 {
		schema, strict, err := outputSchema(req.OutputSchema)
		if err != nil {
			return params, err
		}
		params.Text = responses.ResponseTextConfigParam{Format: responses.ResponseFormatTextConfigUnionParam{OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{Name: schemaName(req.OutputName), Schema: schema, Strict: strict}}}
	}

	if req.MaxTokens > 0 {
		params.MaxOutputTokens = openai.Int(int64(req.MaxTokens))
	}

	// Only reasoning models accept reasoning settings, others reject the request
	if caps.Reasoning {
		if req.Effort != "" {
			params.Reasoning = shared.ReasoningParam{Effort: shared.ReasoningEffort(req.Effort)}
		}
		params.Include = []responses.ResponseIncludable{responses.ResponseIncludableReasoningEncryptedContent}
	}
	return params, nil
}

// convertInput maps the history to Responses input items, one per text, tool call, tool result and reasoning part
func convertInput(msgs []llm.Message) (responses.ResponseInputParam, error) {
	var out responses.ResponseInputParam
	for i, m := range msgs {
		role := responses.EasyInputMessageRoleUser
		if m.Role == llm.RoleAssistant {
			role = responses.EasyInputMessageRoleAssistant
		}
		for _, p := range m.Parts {
			switch p.Type {
			case llm.PartText:
				if p.Text != "" {
					out = append(out, responses.ResponseInputItemUnionParam{OfMessage: &responses.EasyInputMessageParam{Role: role, Content: responses.EasyInputMessageContentUnionParam{OfString: openai.String(p.Text)}}})
				}
			case llm.PartToolCall:
				if p.ToolCall == nil {
					return nil, fmt.Errorf("message %d: tool call part without tool call", i)
				}
				args := string(p.ToolCall.Args)
				if args == "" {
					args = "{}"
				}
				out = append(out, responses.ResponseInputItemUnionParam{OfFunctionCall: &responses.ResponseFunctionToolCallParam{CallID: p.ToolCall.ID, Name: p.ToolCall.Name, Arguments: args}})
			case llm.PartToolResult:
				if p.ToolResult == nil {
					return nil, fmt.Errorf("message %d: tool result part without tool result", i)
				}
				out = append(out, responses.ResponseInputItemUnionParam{OfFunctionCallOutput: &responses.ResponseInputItemFunctionCallOutputParam{
					CallID: openai.String(p.ToolResult.CallID),
					Output: responses.ResponseInputItemFunctionCallOutputOutputUnionParam{OfString: openai.String(toolResultContent(p.ToolResult))},
				}})
			case llm.PartReasoning:
				// Reasoning from another provider or a compatible server carries nothing OpenAI can read back
				if p.Reasoning == nil || p.Reasoning.Provider != llm.KindOpenAI || len(p.Reasoning.Opaque) == 0 {
					continue
				}
				var item reasoningItem
				if json.Unmarshal(p.Reasoning.Opaque, &item) != nil || item.EncryptedContent == "" {
					continue
				}
				out = append(out, responses.ResponseInputItemUnionParam{OfReasoning: &responses.ResponseReasoningItemParam{
					ID:               item.ID,
					Summary:          []responses.ResponseReasoningItemSummaryParam{},
					EncryptedContent: openai.String(item.EncryptedContent),
				}})
			}
		}
	}
	return out, nil
}

// responseFrom turns the final response into a message in output order: reasoning, then text, then tool calls
func responseFrom(r *responses.Response) *llm.Response {
	msg := llm.Message{Role: llm.RoleAssistant}
	var text []string
	var calls []llm.Part
	refused := false
	for _, item := range r.Output {
		switch item.Type {
		case "reasoning":
			if item.EncryptedContent == "" {
				continue
			}
			opaque, _ := json.Marshal(reasoningItem{ID: item.ID, EncryptedContent: item.EncryptedContent})
			var summary []string
			for _, s := range item.Summary {
				summary = append(summary, s.Text)
			}
			msg.Parts = append(msg.Parts, llm.Part{Type: llm.PartReasoning, Reasoning: &llm.Reasoning{Provider: llm.KindOpenAI, Opaque: opaque, Text: strings.Join(summary, "\n\n")}})
		case "message":
			for _, c := range item.Content {
				switch c.Type {
				case "output_text":
					text = append(text, c.Text)
				case "refusal":
					text = append(text, c.Refusal)
					refused = true
				}
			}
		case "function_call":
			call := item.AsFunctionCall()
			calls = append(calls, llm.Part{Type: llm.PartToolCall, ToolCall: &llm.ToolCall{ID: call.CallID, Name: call.Name, Args: toolArgs(call.Arguments)}})
		}
	}
	if joined := strings.Join(text, "\n\n"); joined != "" {
		msg.Parts = append(msg.Parts, llm.TextPart(joined))
	}
	msg.Parts = append(msg.Parts, calls...)

	stop := llm.StopEndTurn
	switch {
	case refused, r.IncompleteDetails.Reason == "content_filter":
		stop = llm.StopRefusal
	case r.IncompleteDetails.Reason == "max_output_tokens":
		stop = llm.StopMaxTokens
	case r.Status == responses.ResponseStatusIncomplete:
		stop = llm.StopOther
	case len(calls) > 0:
		stop = llm.StopToolUse
	}

	// input_tokens includes cached tokens, which are split out of Input
	u := r.Usage
	cacheRead := u.InputTokensDetails.CachedTokens
	cacheWrite := u.InputTokensDetails.CacheWriteTokens
	usage := llm.Usage{
		Input:      max(0, u.InputTokens-cacheRead-cacheWrite),
		Output:     u.OutputTokens,
		CacheRead:  cacheRead,
		CacheWrite: cacheWrite,
		Reasoning:  u.OutputTokensDetails.ReasoningTokens,
	}
	return &llm.Response{Message: msg, Stop: stop, Usage: usage, Model: r.Model}
}
