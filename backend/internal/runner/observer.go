package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

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

const (
	// liveOutputInterval is how long tool output waits to be published together with what follows it
	liveOutputInterval = 100 * time.Millisecond
	// maxLiveOutputBytes is how much tool output one publish carries at most, the newest bytes, since the run page only shows the tail
	maxLiveOutputBytes = 12_000
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
	// live holds each tool call's output that waits for its publish, so a command that prints fast becomes a few notifications a second instead of one per write
	live map[string]*liveOutput
}

// liveOutput is the output of one tool call since its last publish
type liveOutput struct {
	tail    []byte
	skipped int
	timer   *time.Timer
}

func newTimelineObserver(ctx context.Context, rec *events.Recorder) *timelineObserver {
	return &timelineObserver{ctx: ctx, rec: rec, calls: map[string]*events.Span{}, live: map[string]*liveOutput{}}
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

// ToolOutput streams live command output for the UI, publishing each call's output at most every liveOutputInterval
func (o *timelineObserver) ToolOutput(callID string, chunk []byte) {
	o.mu.Lock()
	defer o.mu.Unlock()

	// The first output since the last publish schedules the next one
	l := o.live[callID]
	if l == nil {
		l = &liveOutput{}
		o.live[callID] = l
		l.timer = time.AfterFunc(liveOutputInterval, func() { o.publishToolOutput(callID, l) })
	}

	// A command that prints faster than a publish carries keeps only its newest output, cut at a character boundary
	l.tail = append(l.tail, chunk...)
	if over := len(l.tail) - maxLiveOutputBytes; over > 0 {
		for over < len(l.tail) && !utf8.RuneStart(l.tail[over]) {
			over++
		}
		l.skipped += over
		l.tail = append(l.tail[:0], l.tail[over:]...)
	}
}

// publishToolOutput sends the output a tool call printed since its last publish, unless the call has ended meanwhile
func (o *timelineObserver) publishToolOutput(callID string, l *liveOutput) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.live[callID] != l {
		return
	}
	delete(o.live, callID)

	text := string(l.tail)
	if l.skipped > 0 {
		text = fmt.Sprintf("[… %d bytes not shown live …]\n", l.skipped) + text
	}
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
	// The result replaces the live output on the run page, so output still waiting for its publish is dropped
	if l := o.live[call.ID]; l != nil {
		l.timer.Stop()
		delete(o.live, call.ID)
	}
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
