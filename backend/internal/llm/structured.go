package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Structured performs a model call whose answer is JSON matching req.OutputSchema and decodes it into `into`
// Models with native JSON-schema output answer in the message text; every other tool-capable model is given a submit tool with the schema and forced to call it
// A malformed answer is retried once with the error fed back to the model
// The returned response is the last attempt, with Usage and Latency summed over all attempts so callers can account for the full cost
// A refusal returns the response together with ErrRefusal
func Structured(ctx context.Context, p Provider, req Request, into any) (*Response, error) {
	if len(req.OutputSchema) == 0 {
		return nil, errors.New("llm: Structured requires an OutputSchema")
	}

	// Pick native JSON output or the submit-tool fallback once, so both attempts use the same mechanism
	native := p.Caps(req.Model).JSONSchema
	toolName := ""
	if native {
		req.ForceTool = ""
	} else {
		toolName = SubmitToolName(req.OutputName)
		req.Tools = append(slices.Clone(req.Tools), ToolDef{
			Name:        toolName,
			Description: "Submit your final answer. The arguments are the answer and must match the schema.",
			Schema:      req.OutputSchema,
		})
		req.ForceTool = toolName
		req.OutputSchema = nil

		// Some OpenAI-compatible servers ignore tool_choice, so the instruction is also spelled out in the conversation
		req.Messages = appendUserText(req.Messages, fmt.Sprintf("Answer by calling the `%s` tool; its arguments are your answer.", toolName))
	}

	var usage Usage
	var latency time.Duration
	for attempt := 0; ; attempt++ {
		resp, err := p.Stream(ctx, req, nil)
		if err != nil {
			return nil, err
		}
		usage = usage.Add(resp.Usage)
		latency += resp.Latency
		resp.Usage = usage
		resp.Latency = latency

		// A refusal is final, retrying would only ask the same question again
		if resp.Stop == StopRefusal {
			return resp, ErrRefusal
		}

		// Extract and decode the answer
		raw, err := extractStructured(resp.Message, native, toolName)
		if err == nil {
			err = decodeStructured(raw, into)
		}
		if err == nil {
			return resp, nil
		}
		if attempt >= 1 {
			return resp, fmt.Errorf("llm: invalid structured output after retry: %w", err)
		}

		// Feed the error back as a user turn so the model can correct itself on the retry
		req.Messages = append(slices.Clone(req.Messages), resp.Message, retryMessage(resp.Message, toolName, err))
	}
}

// SubmitToolName returns the name of the submit tool used by the Structured fallback
func SubmitToolName(outputName string) string {
	if outputName == "" {
		outputName = "result"
	}
	name := "submit_" + invalidToolChars.ReplaceAllString(outputName, "_")
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

// invalidToolChars matches characters that tool names may not contain on any provider
var invalidToolChars = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// appendUserText adds text to the last user turn, or starts a new user turn after an assistant turn, without mutating the caller's history
func appendUserText(msgs []Message, text string) []Message {
	msgs = slices.Clone(msgs)
	if n := len(msgs); n > 0 && msgs[n-1].Role == RoleUser {
		last := msgs[n-1]
		last.Parts = append(slices.Clone(last.Parts), TextPart(text))
		msgs[n-1] = last
		return msgs
	}
	return append(msgs, Message{Role: RoleUser, Parts: []Part{TextPart(text)}})
}

// extractStructured returns the raw JSON answer from the message text or from the submit tool call
func extractStructured(msg Message, native bool, toolName string) (json.RawMessage, error) {
	if !native {
		for _, call := range msg.ToolCalls() {
			if call.Name == toolName {
				return call.Args, nil
			}
		}
		return nil, fmt.Errorf("the model did not call the %s tool", toolName)
	}

	text := strings.TrimSpace(msg.Text())
	if text == "" {
		return nil, errors.New("the model returned an empty answer")
	}
	return json.RawMessage(unwrapJSON(text)), nil
}

// unwrapJSON strips the Markdown code fences or surrounding prose that some models add around JSON answers
func unwrapJSON(text string) string {
	if json.Valid([]byte(text)) {
		return text
	}

	// Remove a Markdown code fence such as ```json ... ```
	if strings.HasPrefix(text, "```") {
		inner := text[3:]
		if nl := strings.IndexByte(inner, '\n'); nl >= 0 {
			inner = inner[nl+1:]
		}
		inner = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(inner), "```"))
		if json.Valid([]byte(inner)) {
			return inner
		}
	}

	// Fall back to the outermost JSON object or array in the text
	start := strings.IndexAny(text, "{[")
	if start >= 0 {
		closing := byte('}')
		if text[start] == '[' {
			closing = ']'
		}
		if end := strings.LastIndexByte(text, closing); end > start && json.Valid([]byte(text[start:end+1])) {
			return text[start : end+1]
		}
	}
	return text
}

// decodeStructured unmarshals the answer, or only checks that it is valid JSON when the caller passed no target
func decodeStructured(raw json.RawMessage, into any) error {
	if into == nil {
		if !json.Valid(raw) {
			return errors.New("the answer is not valid JSON")
		}
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	err := dec.Decode(into)
	if err != nil {
		return fmt.Errorf("the answer is not valid JSON for the schema: %w", err)
	}
	return nil
}

// retryMessage builds the user turn that reports the error and answers every tool call of the failed attempt, as providers require a result for each call
func retryMessage(failed Message, toolName string, cause error) Message {
	var parts []Part
	for _, call := range failed.ToolCalls() {
		var content string
		switch {
		case toolName == "":
			content = "Error: tools are not available for this answer, reply with the JSON value instead."
		case call.Name == toolName:
			content = fmt.Sprintf("Error: %s. Call %s again with arguments that match the schema.", cause, toolName)
		default:
			content = fmt.Sprintf("Error: only the %s tool may be called now.", toolName)
		}
		parts = append(parts, Part{Type: PartToolResult, ToolResult: &ToolResult{CallID: call.ID, Content: content, IsError: true}})
	}

	if toolName == "" {
		parts = append(parts, TextPart(fmt.Sprintf("Your previous answer could not be used: %s. Reply again with only the JSON value that matches the requested schema.", cause)))
	} else {
		parts = append(parts, TextPart(fmt.Sprintf("Your previous answer could not be used: %s. Answer by calling the `%s` tool; its arguments are your answer.", cause, toolName)))
	}
	return Message{Role: RoleUser, Parts: parts}
}
