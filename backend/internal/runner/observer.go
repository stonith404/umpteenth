package runner

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// Payload limits keep the timeline readable; oversized payloads still spill into FileStorage in the recorder
const (
	maxArgsChars   = 4_000
	maxResultChars = 12_000
	maxTextChars   = 12_000
)

// timelineObserver turns agent callbacks into run events and live deltas
type timelineObserver struct {
	ctx context.Context
	rec *events.Recorder

	mu    sync.Mutex
	calls map[string]*events.Span
	// Deltas are batched briefly so a fast token stream doesn't become one notification per token
	pending     strings.Builder
	pendingType llm.DeltaType
	lastFlush   time.Time
}

func newTimelineObserver(ctx context.Context, rec *events.Recorder) *timelineObserver {
	return &timelineObserver{ctx: ctx, rec: rec, calls: map[string]*events.Span{}}
}

type deltaMessage struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	CallID string `json:"callId,omitempty"`
}

func (o *timelineObserver) OnDelta(d llm.Delta) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if d.Type == llm.DeltaToolCall {
		o.flushLocked()
		o.rec.Delta(o.ctx, deltaMessage{Type: "tool_call", Text: d.ToolName})
		return
	}
	if o.pendingType != d.Type {
		o.flushLocked()
		o.pendingType = d.Type
	}
	o.pending.WriteString(d.Text)
	if o.pending.Len() > 2000 || time.Since(o.lastFlush) > 150*time.Millisecond {
		o.flushLocked()
	}
}

func (o *timelineObserver) flushLocked() {
	if o.pending.Len() == 0 {
		return
	}
	o.rec.Delta(o.ctx, deltaMessage{Type: string(o.pendingType), Text: o.pending.String()})
	o.pending.Reset()
	o.lastFlush = time.Now()
}

// ToolOutput streams live command output for the UI
func (o *timelineObserver) ToolOutput(callID string, chunk []byte) {
	text := string(chunk)
	for len(text) > 0 {
		n := min(len(text), 3000)
		o.rec.Delta(o.ctx, deltaMessage{Type: "tool_output", CallID: callID, Text: text[:n]})
		text = text[n:]
	}
}

type toolCallRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (o *timelineObserver) OnLLMCall(turn int, resp *llm.Response, cost int64, latency time.Duration, err error) {
	o.mu.Lock()
	o.flushLocked()
	o.mu.Unlock()

	ms := latency.Milliseconds()
	payload := map[string]any{"turn": turn, "latencyMs": ms, "cost": cost}
	if err != nil {
		payload["error"] = err.Error()
	}
	if resp != nil {
		text, _ := agent.Truncate(resp.Message.Text(), maxTextChars/2, maxTextChars/2)
		var reasoning strings.Builder
		var calls []toolCallRef
		for _, p := range resp.Message.Parts {
			switch {
			case p.Type == llm.PartReasoning && p.Reasoning != nil:
				reasoning.WriteString(p.Reasoning.Text)
			case p.Type == llm.PartToolCall && p.ToolCall != nil:
				calls = append(calls, toolCallRef{ID: p.ToolCall.ID, Name: p.ToolCall.Name})
			}
		}
		reasoningText, _ := agent.Truncate(reasoning.String(), 3000, 1000)
		payload["model"] = resp.Model
		payload["stop"] = resp.Stop
		payload["usage"] = resp.Usage
		payload["text"] = text
		payload["reasoning"] = reasoningText
		payload["toolCalls"] = calls
	}
	o.rec.Emit(o.ctx, events.Event{Type: events.TypeLLMCall, Ms: &ms, Payload: payload})
}

// OnCompaction records a summary of the conversation, so the timeline shows where the agent's memory was shortened and what it kept
func (o *timelineObserver) OnCompaction(c agent.Compaction) {
	ms := c.Latency.Milliseconds()
	summary, _ := agent.Truncate(c.Summary, maxTextChars, 0)
	payload := map[string]any{
		"turn":         c.Turn,
		"promptTokens": c.PromptTokens,
		"messages":     c.Messages,
		"summary":      summary,
		"usage":        c.Usage,
		"cost":         c.Cost,
	}
	if c.Err != nil {
		payload["error"] = c.Err.Error()
	}
	o.rec.Emit(o.ctx, events.Event{Type: events.TypeCompaction, Ms: &ms, Payload: payload})
}

func (o *timelineObserver) OnToolStart(call llm.ToolCall) {
	span := events.StartSpan()
	o.mu.Lock()
	o.calls[call.ID] = span
	o.mu.Unlock()

	args, _ := agent.Truncate(string(call.Args), maxArgsChars, 0)
	var parsed any
	if json.Unmarshal([]byte(args), &parsed) != nil {
		parsed = args
	}
	o.rec.Emit(o.ctx, events.Event{Type: events.TypeToolCall, SpanID: span.ID, Payload: map[string]any{
		"callId": call.ID,
		"name":   call.Name,
		"args":   parsed,
	}})
}

func (o *timelineObserver) OnToolEnd(call llm.ToolCall, res agent.Result, took time.Duration) {
	o.mu.Lock()
	span := o.calls[call.ID]
	delete(o.calls, call.ID)
	o.mu.Unlock()

	ms := took.Milliseconds()
	spanID := ""
	if span != nil {
		spanID = span.ID
	}
	content, _ := agent.Truncate(res.Content, maxResultChars/2, maxResultChars/2)
	payload := map[string]any{
		"callId":  call.ID,
		"name":    call.Name,
		"content": content,
		"isError": res.IsError,
		"meta":    res.Meta,
	}
	if res.Finish != nil {
		payload["finish"] = res.Finish
	}
	o.rec.Emit(o.ctx, events.Event{Type: events.TypeToolResult, SpanID: spanID, Ms: &ms, Payload: payload})
}
