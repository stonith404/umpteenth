package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/shared"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// buildParams translates a provider-agnostic request into a Chat Completions request that every OpenAI-compatible server understands
// known reports whether the model is an OpenAI catalog model, which unlocks OpenAI-only fields such as max_completion_tokens
func buildParams(req llm.Request, caps llm.Caps, known bool) (openai.ChatCompletionNewParams, error) {
	params := openai.ChatCompletionNewParams{
		Model:         req.Model,
		StreamOptions: openai.ChatCompletionStreamOptionsParam{IncludeUsage: openai.Bool(true)},
	}

	// Render the system blocks as one system message, since several servers only honor a single leading one
	// OpenAI-compatible servers cache prefixes automatically, so cache breakpoints need no markup here
	var system []string
	for _, b := range req.System {
		if b.Text != "" {
			system = append(system, b.Text)
		}
	}
	if len(system) > 0 {
		params.Messages = append(params.Messages, openai.SystemMessage(strings.Join(system, "\n\n")))
	}

	// Convert the conversation
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return params, err
	}
	params.Messages = append(params.Messages, messages...)

	// Convert the tools and force one when asked
	for _, d := range req.Tools {
		tool, err := convertTool(d)
		if err != nil {
			return params, err
		}
		params.Tools = append(params.Tools, tool)
	}
	if req.ForceTool != "" {
		params.ToolChoice = openai.ToolChoiceOptionFunctionToolChoice(openai.ChatCompletionNamedToolChoiceFunctionParam{Name: req.ForceTool})
	}

	// Request native structured output; Structured only sets OutputSchema for models whose caps claim support
	if len(req.OutputSchema) > 0 {
		var schema map[string]any
		err := json.Unmarshal(req.OutputSchema, &schema)
		if err != nil {
			return params, fmt.Errorf("invalid output schema: %w", err)
		}
		// Strict mode makes the model follow the schema exactly, but the API rejects schemas that leave properties optional or objects open
		jsonSchema := shared.ResponseFormatJSONSchemaJSONSchemaParam{Name: schemaName(req.OutputName), Schema: schema}
		if strictCompatible(schema) {
			jsonSchema.Strict = openai.Bool(true)
		}
		params.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{JSONSchema: jsonSchema}}
	}

	// OpenAI deprecated max_tokens in favor of max_completion_tokens, while many compatible servers only know max_tokens
	if req.MaxTokens > 0 {
		if known {
			params.MaxCompletionTokens = openai.Int(int64(req.MaxTokens))
		} else {
			params.MaxTokens = openai.Int(int64(req.MaxTokens))
		}
	}

	// Only reasoning models accept reasoning_effort, others reject the request
	if caps.Reasoning && req.Effort != "" {
		params.ReasoningEffort = shared.ReasoningEffort(req.Effort)
	}
	return params, nil
}

// convertMessages maps the history to Chat Completions messages
// Tool results become one tool message each, placed right after the assistant message that holds their calls
func convertMessages(msgs []llm.Message) ([]openai.ChatCompletionMessageParamUnion, error) {
	var out []openai.ChatCompletionMessageParamUnion
	for i, m := range msgs {
		var texts []string
		var calls []openai.ChatCompletionMessageToolCallUnionParam
		var results []openai.ChatCompletionMessageParamUnion
		for _, p := range m.Parts {
			switch p.Type {
			case llm.PartText:
				if p.Text != "" {
					texts = append(texts, p.Text)
				}
			case llm.PartToolCall:
				if p.ToolCall == nil {
					return nil, fmt.Errorf("message %d: tool call part without tool call", i)
				}
				args := string(p.ToolCall.Args)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, openai.ChatCompletionMessageToolCallUnionParam{OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
					ID:       p.ToolCall.ID,
					Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{Name: p.ToolCall.Name, Arguments: args},
				}})
			case llm.PartToolResult:
				if p.ToolResult == nil {
					return nil, fmt.Errorf("message %d: tool result part without tool result", i)
				}
				results = append(results, openai.ToolMessage(toolResultContent(p.ToolResult), p.ToolResult.CallID))
			}
			// Reasoning parts are dropped because Chat Completions has no field to send them back in
		}

		if m.Role == llm.RoleAssistant {
			asst := openai.ChatCompletionAssistantMessageParam{ToolCalls: calls}
			if len(texts) > 0 || len(calls) == 0 {
				asst.Content.OfString = openai.String(strings.Join(texts, "\n\n"))
			}
			out = append(out, openai.ChatCompletionMessageParamUnion{OfAssistant: &asst})
			continue
		}

		out = append(out, results...)
		if len(texts) > 0 {
			out = append(out, openai.UserMessage(strings.Join(texts, "\n\n")))
		}
	}
	return out, nil
}

// toolResultContent marks failures in the text, since tool messages have no error flag
func toolResultContent(r *llm.ToolResult) string {
	if r.IsError && !strings.HasPrefix(strings.ToLower(r.Content), "error") {
		return "Error: " + r.Content
	}
	return r.Content
}

func convertTool(d llm.ToolDef) (openai.ChatCompletionToolUnionParam, error) {
	schema := map[string]any{"type": "object", "properties": map[string]any{}}
	if len(d.Schema) > 0 {
		schema = nil
		err := json.Unmarshal(d.Schema, &schema)
		if err != nil {
			return openai.ChatCompletionToolUnionParam{}, fmt.Errorf("tool %s has an invalid JSON schema: %w", d.Name, err)
		}
	}
	fn := shared.FunctionDefinitionParam{Name: d.Name, Parameters: shared.FunctionParameters(schema)}
	if d.Description != "" {
		fn.Description = openai.String(d.Description)
	}
	return openai.ChatCompletionFunctionTool(fn), nil
}

// schemaName derives the response_format name, which only allows letters, digits, underscores and dashes
func schemaName(outputName string) string {
	if outputName == "" {
		return "result"
	}
	var b strings.Builder
	for _, r := range outputName {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	name := b.String()
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// strictCompatible reports whether a schema meets the rules of strict structured output: every object lists all its properties as required and allows no others
func strictCompatible(node any) bool {
	switch n := node.(type) {
	case map[string]any:
		if props, ok := n["properties"].(map[string]any); ok {
			if n["additionalProperties"] != false {
				return false
			}
			required, _ := n["required"].([]any)
			if len(required) != len(props) {
				return false
			}
		} else if n["type"] == "object" {
			// An object without properties would accept anything, which strict mode doesn't allow
			return false
		}
		for _, v := range n {
			if !strictCompatible(v) {
				return false
			}
		}
	case []any:
		for _, v := range n {
			if !strictCompatible(v) {
				return false
			}
		}
	}
	return true
}
