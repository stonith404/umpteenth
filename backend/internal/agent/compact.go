package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// Compaction thresholds, as shares of the model's context window (PLAN.md §6.1)
const (
	// compactAt is how full the next prompt may get before the conversation is summarized, leaving room for the next answer and tool results
	compactAt = 0.7
	// keepShare caps how much of the window the last exchange may take to be kept verbatim next to the summary
	keepShare = 0.2
	// transcriptShare caps the rendered conversation the summary call reads, so the call itself fits even when the conversation no longer does
	transcriptShare = 0.5
	// charsPerToken estimates token counts from text, erring on the side of more tokens
	charsPerToken = 3
	// compactionMaxTokens bounds the summary
	compactionMaxTokens = 4000
	// fallbackContextWindow sizes a compaction after an overflow when the model's window is unknown, which fits every current tool-calling model
	fallbackContextWindow = 128_000
)

// Per-item limits of the rendered transcript, which keep one huge tool result from crowding out the rest
const (
	transcriptArgChars    = 1_500
	transcriptResultChars = 4_000
	transcriptTextChars   = 6_000
)

const compactionSystem = `You compact the working conversation of an autonomous agent that ran out of context space.
The agent will continue from your summary alone, with the same tools and a fresh conversation, so anything you leave out is lost.

Write a dense, factual handover in Markdown with these sections:
1. Goal: the job and what counts as done.
2. Progress: what has been done so far and what it produced, in order.
3. Facts: exact values the rest of the job needs, such as IDs, URLs, file paths, commands that worked, numbers and names. Copy them verbatim.
4. Dead ends: what failed and why, so it isn't repeated.
5. Next steps: what remains, starting with the step that was in progress.

Do not invent anything and do not call tools.`

// Compaction describes one summary of the conversation that replaced its history
type Compaction struct {
	Turn int
	// PromptTokens is the estimated size of the conversation that was summarized
	PromptTokens int64
	// Messages is how many messages the summary replaced
	Messages int
	Summary  string
	Usage    llm.Usage
	Cost     int64
	Latency  time.Duration
	Err      error
}

// CompactionObserver is implemented by observers that record compactions
type CompactionObserver interface {
	OnCompaction(c Compaction)
}

// contextOverflow matches the errors providers answer when a prompt no longer fits the model
var contextOverflow = regexp.MustCompile(`(?i)(context[ _-]?length|context[ _-]?window|prompt is too long|maximum context|input is too long|too many (input )?tokens)`)

// IsContextOverflow reports whether a model call failed because the prompt was too long for the model
func IsContextOverflow(err error) bool {
	apiErr, ok := errors.AsType[*llm.APIError](err)
	if !ok || (apiErr.StatusCode != 400 && apiErr.StatusCode != 413) {
		return false
	}
	return contextOverflow.MatchString(apiErr.Type + " " + apiErr.Message)
}

// nextPromptTokens estimates the size of the next prompt from the last call's usage and what was appended since
func nextPromptTokens(resp *llm.Response, appended []llm.Message) int64 {
	u := resp.Usage
	tokens := u.Input + u.CacheRead + u.CacheWrite + u.Output
	return tokens + estimateTokens(appended)
}

func estimateTokens(messages []llm.Message) int64 {
	return messageChars(messages, true) / charsPerToken
}

// resentTokens estimates the tokens messages take when the conversation is sent again
// Adapters only send reasoning back in its opaque form, which already holds the text, so the readable text doesn't count
func resentTokens(messages []llm.Message) int64 {
	return messageChars(messages, false) / charsPerToken
}

// messageChars adds up the text, tool calls, tool results and reasoning of messages, with or without the readable text of reasoning
func messageChars(messages []llm.Message, reasoningText bool) int64 {
	var chars int
	for _, m := range messages {
		for _, p := range m.Parts {
			switch {
			case p.Type == llm.PartText:
				chars += len(p.Text)
			case p.Type == llm.PartToolCall && p.ToolCall != nil:
				chars += len(p.ToolCall.Name) + len(p.ToolCall.Args)
			case p.Type == llm.PartToolResult && p.ToolResult != nil:
				chars += len(p.ToolResult.Content)
			case p.Type == llm.PartReasoning && p.Reasoning != nil:
				chars += len(p.Reasoning.Opaque)
				if reasoningText {
					chars += len(p.Reasoning.Text)
				}
			}
		}
	}
	return int64(chars)
}

// compact replaces the conversation with the run's first message, a summary of everything since, and the last exchange when it is small enough
// The first message is kept verbatim since it carries the trigger, the input and the instructions of the run
// A later compaction starts from that same first message again, and the summarizer reads the earlier summary as part of the conversation instead
func compact(ctx context.Context, cfg Config, first llm.Message, turn int, promptTokens int64, out *Outcome) error {
	messages := out.Messages
	if len(messages) < 3 {
		return errors.New("the conversation is too short to compact")
	}
	window := cfg.ContextWindow
	if window <= 0 {
		window = fallbackContextWindow
	}

	// The last exchange is kept when it fits, since it holds the tool results the agent is about to act on
	// Its reasoning is left out, since providers bind reasoning to the conversation that produced it and reject it once the summary replaces that conversation
	var tail []llm.Message
	history := messages[1:]
	if n := len(messages); messages[n-2].Role == llm.RoleAssistant && messages[n-1].Role == llm.RoleUser {
		if estimateTokens(messages[n-2:]) <= int64(float64(window)*keepShare) {
			tail = withoutReasoning(messages[n-2:])
			history = messages[1 : n-2]
		}
	}
	if len(history) == 0 {
		return errors.New("nothing to compact besides the last exchange")
	}

	// Summarize the rendered history with a separate call that has no tools
	transcript := renderTranscript(history, int(float64(window)*transcriptShare)*charsPerToken)
	start := time.Now()
	resp, err := cfg.Provider.Stream(ctx, llm.Request{
		Model:     cfg.Model,
		System:    []llm.Block{{Text: compactionSystem}},
		Messages:  []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(transcriptPrompt(messages[0].Text(), transcript))}}},
		MaxTokens: compactionMaxTokens,
		Effort:    llm.EffortLow,
	}, nil)
	c := Compaction{Turn: turn, PromptTokens: promptTokens, Messages: len(history), Latency: time.Since(start)}
	out.LLMTime += c.Latency
	if resp != nil {
		c.Usage = resp.Usage
		c.Cost = llm.Cost(resp.Usage, cfg.Price)
		out.Usage = out.Usage.Add(resp.Usage)
		out.Cost += c.Cost
	}
	if err == nil {
		c.Summary = strings.TrimSpace(resp.Message.Text())
		if c.Summary == "" {
			err = errors.New("the model returned an empty summary")
		}
	}
	c.Err = err
	if obs, ok := cfg.Observer.(CompactionObserver); ok {
		obs.OnCompaction(c)
	}
	if err != nil {
		return err
	}

	// The summary joins the first message, so the conversation still starts with the user and alternates as providers require
	opener := llm.Message{Role: llm.RoleUser, Parts: append(append([]llm.Part(nil), first.Parts...), llm.TextPart(summaryMessage(c.Summary, len(tail) > 0)))}
	out.Messages = append([]llm.Message{opener}, tail...)
	return nil
}

// withoutReasoning copies messages without their reasoning parts, leaving the originals untouched
func withoutReasoning(messages []llm.Message) []llm.Message {
	out := make([]llm.Message, len(messages))
	for i, m := range messages {
		out[i] = llm.Message{Role: m.Role, Parts: slices.DeleteFunc(slices.Clone(m.Parts), func(p llm.Part) bool { return p.Type == llm.PartReasoning })}
	}
	return out
}

func transcriptPrompt(task, transcript string) string {
	return "The first message of the run, with the task and any earlier handover:\n\n<task>\n" + task + "\n</task>\n\n" +
		"The conversation since then, with long tool output shortened:\n\n<conversation>\n" + transcript + "\n</conversation>\n\n" +
		"Write the handover now."
}

func summaryMessage(summary string, keptTail bool) string {
	msg := "\n\n---\n\nYour earlier conversation in this run was compacted to fit the model's context window. This is the handover you wrote:\n\n<summary>\n" + summary + "\n</summary>\n\n"
	if keptTail {
		return msg + "Your last message and its tool results follow. Continue the job from there, and don't repeat work the summary says is done."
	}
	return msg + "Continue the job from the next steps, and don't repeat work the summary says is done."
}

// renderTranscript turns messages into plain text for the summary call, shortening long parts and then the whole to maxChars
func renderTranscript(messages []llm.Message, maxChars int) string {
	var b strings.Builder
	names := map[string]string{}
	for _, m := range messages {
		for _, p := range m.Parts {
			switch {
			case p.Type == llm.PartText && strings.TrimSpace(p.Text) != "":
				text, _ := Truncate(p.Text, transcriptTextChars/2, transcriptTextChars/2)
				if m.Role == llm.RoleAssistant {
					fmt.Fprintf(&b, "[assistant]\n%s\n\n", text)
				} else {
					fmt.Fprintf(&b, "[user]\n%s\n\n", text)
				}
			case p.Type == llm.PartToolCall && p.ToolCall != nil:
				names[p.ToolCall.ID] = p.ToolCall.Name
				args, _ := Truncate(string(p.ToolCall.Args), transcriptArgChars, 0)
				fmt.Fprintf(&b, "[tool call %s]\n%s\n\n", p.ToolCall.Name, args)
			case p.Type == llm.PartToolResult && p.ToolResult != nil:
				label := "tool result " + names[p.ToolResult.CallID]
				if p.ToolResult.IsError {
					label += ", error"
				}
				content, _ := Truncate(p.ToolResult.Content, transcriptResultChars/2, transcriptResultChars/2)
				fmt.Fprintf(&b, "[%s]\n%s\n\n", label, content)
			}
		}
	}

	// Beyond the cap, the start and the most recent part of the run matter most
	s, _ := Truncate(b.String(), maxChars/3, maxChars-maxChars/3)
	return s
}
