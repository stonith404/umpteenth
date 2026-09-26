package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

const (
	// maxCacheBreakpoints is the API limit of cache_control markers per request, the automatic top-level one included
	maxCacheBreakpoints = 4
	// defaultMaxTokens leaves room for adaptive thinking plus the reply, since requests are always streamed
	defaultMaxTokens = 64_000
	// minThinkingBudget is the smallest budget_tokens the API accepts
	minThinkingBudget = 1024
)

// buildParams translates a provider-agnostic request into a Messages API request
func buildParams(req llm.Request) (anthropic.MessageNewParams, error) {
	t := traitsFor(req.Model)
	params := anthropic.MessageNewParams{
		Model:     req.Model,
		MaxTokens: int64(maxTokens(req.MaxTokens, t)),
	}

	// Decide how a forced tool is honored: natively where the model allows it, otherwise by asking for it in the last user turn
	forced := req.ForceTool != "" && t.forcedToolChoice
	steered := req.ForceTool != "" && !t.forcedToolChoice

	// Convert the conversation, keeping reasoning blocks exactly as the API produced them
	messages, err := convertMessages(req.Messages)
	if err != nil {
		return params, err
	}
	if steered {
		messages = appendUserBlock(messages, anthropic.NewTextBlock(fmt.Sprintf("Respond by calling the `%s` tool.", req.ForceTool)))
	}
	params.Messages = messages

	// Convert the system prompt and tools, which form the cacheable prefix
	system, systemBreakpoints := convertSystem(req.System)
	tools, err := convertTools(req.Tools)
	if err != nil {
		return params, err
	}
	applyCacheBreakpoints(system, systemBreakpoints, tools)
	params.System = system
	params.Tools = tools

	// Cache the growing conversation tail with the automatic breakpoint, which moves forward on every turn
	params.CacheControl = anthropic.NewCacheControlEphemeralParam()

	// Configure thinking and effort for the model's generation
	params.Thinking = thinkingConfig(t, req.Effort, forced, params.MaxTokens)
	if t.effort && req.Effort != "" {
		params.OutputConfig.Effort = anthropic.OutputConfigEffort(req.Effort)
	}

	// Request native structured output; Structured only sets OutputSchema for models whose caps claim support
	if len(req.OutputSchema) > 0 {
		var schema map[string]any
		err := json.Unmarshal(req.OutputSchema, &schema)
		if err != nil {
			return params, fmt.Errorf("invalid output schema: %w", err)
		}
		params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: structuredSchema(schema).(map[string]any)}
	}

	if forced {
		params.ToolChoice = anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: req.ForceTool}}
	}
	return params, nil
}

// maxTokens applies the default and clamps to the model's output limit, which the API would otherwise reject
func maxTokens(requested int, t traits) int {
	if requested <= 0 {
		requested = defaultMaxTokens
	}
	return min(requested, t.maxOutput)
}

// thinkingConfig maps the model's thinking mode and the requested effort to the thinking parameter
func thinkingConfig(t traits, effort llm.Effort, forced bool, maxTokens int64) anthropic.ThinkingConfigParamUnion {
	adaptive := anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{Display: anthropic.ThinkingConfigAdaptiveDisplaySummarized}}
	switch t.thinking {
	case thinkingAlwaysOn:
		return adaptive
	case thinkingAdaptive:
		// Forced tool use is incompatible with thinking, so it is switched off for that one call
		if forced {
			return anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
		}
		return adaptive
	case thinkingBudget:
		// Budget models only think for medium effort, and never while a tool call is forced
		budget := int64(0)
		if effort == llm.EffortMedium {
			budget = 4096
		}
		budget = min(budget, maxTokens/2)
		if forced || budget < minThinkingBudget {
			return anthropic.ThinkingConfigParamUnion{}
		}
		return anthropic.ThinkingConfigParamOfEnabled(budget)
	}
	return anthropic.ThinkingConfigParamUnion{}
}

// convertSystem builds the system blocks and returns the indices of blocks that end a stable prefix
// Empty blocks are dropped because the API rejects empty text, and their breakpoint moves to the previous block
func convertSystem(blocks []llm.Block) ([]anthropic.TextBlockParam, []int) {
	var out []anthropic.TextBlockParam
	var breakpoints []int
	for _, b := range blocks {
		if b.Text != "" {
			out = append(out, anthropic.TextBlockParam{Text: b.Text})
		}
		if b.CacheBreakpoint && len(out) > 0 {
			last := len(out) - 1
			if len(breakpoints) == 0 || breakpoints[len(breakpoints)-1] != last {
				breakpoints = append(breakpoints, last)
			}
		}
	}
	return out, breakpoints
}

// convertTools passes each JSON schema through byte for byte, keeping the tools prefix stable for the cache
func convertTools(defs []llm.ToolDef) ([]anthropic.ToolUnionParam, error) {
	var out []anthropic.ToolUnionParam
	for _, d := range defs {
		schema := d.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		if !json.Valid(schema) {
			return nil, fmt.Errorf("tool %s has an invalid JSON schema", d.Name)
		}
		tool := anthropic.ToolParam{
			Name:        d.Name,
			InputSchema: param.Override[anthropic.ToolInputSchemaParam](schema),
		}
		if d.Description != "" {
			tool.Description = anthropic.String(d.Description)
		}
		out = append(out, anthropic.ToolUnionParam{OfTool: &tool})
	}
	return out, nil
}

// applyCacheBreakpoints marks the stable prefix with at most three explicit breakpoints, leaving the fourth to the automatic conversation breakpoint
// The last system breakpoint covers tools plus the whole stable system prompt, so it goes first
// The tools breakpoint comes next because tools render first and change least, then the remaining system breakpoints from latest to earliest
func applyCacheBreakpoints(system []anthropic.TextBlockParam, systemBreakpoints []int, tools []anthropic.ToolUnionParam) {
	budget := maxCacheBreakpoints - 1
	mark := func(i int) {
		system[i].CacheControl = anthropic.NewCacheControlEphemeralParam()
		budget--
	}

	if n := len(systemBreakpoints); n > 0 {
		mark(systemBreakpoints[n-1])
	}
	if len(tools) > 0 && budget > 0 {
		tools[len(tools)-1].OfTool.CacheControl = anthropic.NewCacheControlEphemeralParam()
		budget--
	}
	for i := len(systemBreakpoints) - 2; i >= 0 && budget > 0; i-- {
		mark(systemBreakpoints[i])
	}
}

// convertMessages maps the history to API messages; parallel tool calls stay in one assistant message and their results in one user message
func convertMessages(msgs []llm.Message) ([]anthropic.MessageParam, error) {
	out := make([]anthropic.MessageParam, 0, len(msgs))
	for i, m := range msgs {
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(m.Parts))
		for _, p := range m.Parts {
			block, ok, err := convertPart(p)
			if err != nil {
				return nil, fmt.Errorf("message %d: %w", i, err)
			}
			if ok {
				blocks = append(blocks, block)
			}
		}
		if len(blocks) == 0 {
			continue
		}

		role := anthropic.MessageParamRoleUser
		if m.Role == llm.RoleAssistant {
			role = anthropic.MessageParamRoleAssistant
		}
		out = append(out, anthropic.MessageParam{Role: role, Content: blocks})
	}
	return out, nil
}

// convertPart maps one part to a content block; ok is false for parts the API has no use for, such as reasoning from other providers
func convertPart(p llm.Part) (anthropic.ContentBlockParamUnion, bool, error) {
	switch p.Type {
	case llm.PartText:
		if p.Text == "" {
			return anthropic.ContentBlockParamUnion{}, false, nil
		}
		return anthropic.NewTextBlock(p.Text), true, nil

	case llm.PartToolCall:
		if p.ToolCall == nil {
			return anthropic.ContentBlockParamUnion{}, false, errors.New("tool call part without tool call")
		}
		args := p.ToolCall.Args
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		if !json.Valid(args) {
			return anthropic.ContentBlockParamUnion{}, false, fmt.Errorf("tool call %s has invalid JSON arguments", p.ToolCall.ID)
		}
		return anthropic.NewToolUseBlock(p.ToolCall.ID, args, p.ToolCall.Name), true, nil

	case llm.PartToolResult:
		if p.ToolResult == nil {
			return anthropic.ContentBlockParamUnion{}, false, errors.New("tool result part without tool result")
		}
		result := anthropic.ToolResultBlockParam{ToolUseID: p.ToolResult.CallID}
		if p.ToolResult.Content != "" {
			result.Content = []anthropic.ToolResultBlockParamContentUnion{{OfText: &anthropic.TextBlockParam{Text: p.ToolResult.Content}}}
		}
		if p.ToolResult.IsError {
			result.IsError = anthropic.Bool(true)
		}
		return anthropic.ContentBlockParamUnion{OfToolResult: &result}, true, nil

	case llm.PartReasoning:
		if p.Reasoning == nil || p.Reasoning.Provider != llm.KindAnthropic || len(p.Reasoning.Opaque) == 0 {
			return anthropic.ContentBlockParamUnion{}, false, nil
		}
		block, err := replayReasoning(p.Reasoning.Opaque)
		return block, err == nil, err
	}
	return anthropic.ContentBlockParamUnion{}, false, nil
}

// replayReasoning turns a stored thinking or redacted_thinking block back into a request block with its text, signature or data unchanged
func replayReasoning(opaque json.RawMessage) (anthropic.ContentBlockParamUnion, error) {
	var block anthropic.ContentBlockUnion
	err := block.UnmarshalJSON(opaque)
	if err != nil {
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("invalid stored reasoning block: %w", err)
	}
	switch block.Type {
	case "thinking", "redacted_thinking":
		return block.ToParam(), nil
	}
	return anthropic.ContentBlockParamUnion{}, fmt.Errorf("stored reasoning block has unexpected type %q", block.Type)
}

// appendUserBlock adds a block to the final user turn, or starts a user turn when the conversation ends with the assistant
func appendUserBlock(msgs []anthropic.MessageParam, block anthropic.ContentBlockParamUnion) []anthropic.MessageParam {
	if n := len(msgs); n > 0 && msgs[n-1].Role == anthropic.MessageParamRoleUser {
		msgs[n-1].Content = append(msgs[n-1].Content, block)
		return msgs
	}
	return append(msgs, anthropic.NewUserMessage(block))
}

// unsupportedSchemaKeywords are constraints structured outputs reject; the answer is still decoded by the caller, so dropping them only loosens the grammar
var unsupportedSchemaKeywords = map[string]bool{
	"minimum": true, "maximum": true, "exclusiveMinimum": true, "exclusiveMaximum": true, "multipleOf": true,
	"minLength": true, "maxLength": true, "maxItems": true, "uniqueItems": true, "minProperties": true, "maxProperties": true,
}

// schemaMapKeywords hold a map from names to subschemas rather than a schema
var schemaMapKeywords = map[string]bool{"properties": true, "$defs": true, "definitions": true, "patternProperties": true}

// schemaValueKeywords hold literal data that must not be rewritten
var schemaValueKeywords = map[string]bool{"enum": true, "const": true, "default": true, "examples": true}

// structuredSchema adapts a JSON schema to the structured outputs subset: objects get additionalProperties false and unsupported constraints are dropped
func structuredSchema(v any) any {
	switch s := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(s)+1)
		for k, val := range s {
			switch {
			case unsupportedSchemaKeywords[k]:
				continue
			case k == "minItems":
				// Only minItems of 0 or 1 is supported
				if n, ok := val.(float64); ok && n > 1 {
					continue
				}
				out[k] = val
			case schemaValueKeywords[k]:
				out[k] = val
			case schemaMapKeywords[k]:
				if m, ok := val.(map[string]any); ok {
					sub := make(map[string]any, len(m))
					for name, schema := range m {
						sub[name] = structuredSchema(schema)
					}
					out[k] = sub
					continue
				}
				out[k] = val
			default:
				out[k] = structuredSchema(val)
			}
		}
		if isObjectSchema(out) {
			if _, ok := out["additionalProperties"]; !ok {
				out["additionalProperties"] = false
			}
		}
		return out
	case []any:
		out := make([]any, len(s))
		for i, item := range s {
			out[i] = structuredSchema(item)
		}
		return out
	}
	return v
}

func isObjectSchema(s map[string]any) bool {
	if _, ok := s["properties"]; ok {
		return true
	}
	switch t := s["type"].(type) {
	case string:
		return t == "object"
	case []any:
		for _, item := range t {
			if item == "object" {
				return true
			}
		}
	}
	return false
}
