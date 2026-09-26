// Package fake is a deterministic llm.Provider for unit and end-to-end tests that replays scripted responses in order
package fake

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// emptyScriptSummary is the finish summary returned when no scripted response is left
const emptyScriptSummary = "fake provider: script empty"

// ScriptedToolCall is one tool call of a scripted response; Args is marshaled to JSON unless it already is JSON
type ScriptedToolCall struct {
	Name string `json:"name"`
	Args any    `json:"args,omitempty"`
}

// ScriptedResponse is one queued model answer
type ScriptedResponse struct {
	Text      string             `json:"text,omitempty"`
	Reasoning string             `json:"reasoning,omitempty"`
	ToolCalls []ScriptedToolCall `json:"toolCalls,omitempty"`
	// Stop defaults to tool_use when the response has tool calls and to end_turn otherwise
	Stop  llm.StopReason `json:"stop,omitempty"`
	Usage llm.Usage      `json:"usage,omitzero"`
	// Delay holds the response back, so tests can observe a run while the model is "thinking"; JSON carries it as delayMs
	Delay time.Duration `json:"-"`
	// Error makes the call fail with this message instead of answering
	Error string `json:"error,omitempty"`
	// ErrorStatus turns Error into an *llm.APIError with this HTTP status, such as 429
	ErrorStatus int `json:"errorStatus,omitempty"`
}

type scriptedResponseJSON struct {
	scriptedResponseAlias
	DelayMs int64 `json:"delayMs,omitempty"`
}

type scriptedResponseAlias ScriptedResponse

// MarshalJSON encodes Delay as delayMs so the e2e endpoint can script delays in plain milliseconds
func (r ScriptedResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(scriptedResponseJSON{scriptedResponseAlias: scriptedResponseAlias(r), DelayMs: r.Delay.Milliseconds()})
}

func (r *ScriptedResponse) UnmarshalJSON(data []byte) error {
	var v scriptedResponseJSON
	err := json.Unmarshal(data, &v)
	if err != nil {
		return err
	}
	*r = ScriptedResponse(v.scriptedResponseAlias)
	r.Delay = time.Duration(v.DelayMs) * time.Millisecond
	return nil
}

// DefaultCaps are the capabilities the fake reports for every model unless changed with SetCaps
var DefaultCaps = llm.Caps{Tools: true, ParallelTools: true, Reasoning: true, JSONSchema: true, PromptCache: true, Context: 200_000}

// Queue stores the script outside the process, so every replica of an HA test stack replays the same one
type Queue interface {
	Push(ctx context.Context, responses []ScriptedResponse) error
	// Pop removes and returns the oldest response, or reports false when the script is empty
	Pop(ctx context.Context) (ScriptedResponse, bool, error)
	Clear(ctx context.Context) error
	Len(ctx context.Context) (int, error)
}

// Provider replays a FIFO script of responses and records every request; it is safe for concurrent use
type Provider struct {
	mu       sync.Mutex
	script   []ScriptedResponse
	queue    Queue
	requests []llm.Request
	caps     llm.Caps
	calls    int
}

// UseQueue moves the script into an external queue, which Script then fills and Stream reads
// Enqueue, Reset and Pending keep working on the in-memory script that unit tests use
func (p *Provider) UseQueue(q Queue) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.queue = q
}

// New returns an empty fake provider
func New() *Provider {
	return &Provider{caps: DefaultCaps}
}

var shared = New()

// Shared returns the process-wide fake used by runs in e2etest builds, so the test endpoint can script it
func Shared() *Provider { return shared }

// Factory builds providers of kind fake; every provider row shares the process-wide instance
func Factory(llm.Config) (llm.Provider, error) { return shared, nil }

// Enqueue appends responses to the in-memory script
func (p *Provider) Enqueue(responses ...ScriptedResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.script = append(p.script, responses...)
}

// Reset clears the in-memory script, the recorded requests, the tool call counter and the capabilities
func (p *Provider) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.script = nil
	p.requests = nil
	p.calls = 0
	p.caps = DefaultCaps
}

// Pending returns how many responses are still in the in-memory script
func (p *Provider) Pending() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.script)
}

// Script optionally resets the provider and appends responses wherever the script lives, returning how many are pending
func (p *Provider) Script(ctx context.Context, reset bool, responses []ScriptedResponse) (int, error) {
	if reset {
		p.Reset()
	}
	p.mu.Lock()
	q := p.queue
	p.mu.Unlock()
	if q == nil {
		p.Enqueue(responses...)
		return p.Pending(), nil
	}

	if reset {
		if err := q.Clear(ctx); err != nil {
			return 0, fmt.Errorf("fake: failed to clear the script: %w", err)
		}
	}
	if len(responses) > 0 {
		if err := q.Push(ctx, responses); err != nil {
			return 0, fmt.Errorf("fake: failed to extend the script: %w", err)
		}
	}
	return q.Len(ctx)
}

// Requests returns a copy of every request received so far, in order
func (p *Provider) Requests() []llm.Request {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.requests)
}

// SetCaps changes the capabilities reported for every model, for example to exercise the Structured fallback
func (p *Provider) SetCaps(caps llm.Caps) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.caps = caps
}

func (p *Provider) Caps(string) llm.Caps {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.caps
}

func (p *Provider) Stream(ctx context.Context, req llm.Request, onDelta func(llm.Delta)) (*llm.Response, error) {
	start := time.Now()
	if onDelta == nil {
		onDelta = func(llm.Delta) {}
	}

	// Record the request and take the next scripted response in one critical section so concurrent callers each get a distinct response
	p.mu.Lock()
	p.requests = append(p.requests, cloneRequest(req))
	next, ok, err := p.popLocked(ctx)
	if err != nil {
		p.mu.Unlock()
		return nil, fmt.Errorf("fake: %w", err)
	}
	if !ok {
		next = emptyScriptResponse()
	}
	firstCall := p.calls
	p.calls += len(next.ToolCalls)
	p.mu.Unlock()

	// Hold the response back while honoring cancellation
	// With text to stream, half of the delay is spread between its chunks so the live UI can be tested as it fills in
	textChunks := chunks(next.Text)
	wait, gap := next.Delay, time.Duration(0)
	if next.Text != "" && len(textChunks) > 1 {
		wait = next.Delay / 2
		gap = (next.Delay - wait) / time.Duration(len(textChunks)-1)
	}
	if err := sleepCtx(ctx, wait); err != nil {
		return nil, err
	}

	// Fail the call when the script says so
	if next.Error != "" || next.ErrorStatus != 0 {
		return nil, scriptedError(next)
	}

	// Build the message and stream its pieces the way a real provider would
	var parts []llm.Part
	if next.Reasoning != "" {
		onDelta(llm.Delta{Type: llm.DeltaReasoning, Text: next.Reasoning})
		parts = append(parts, llm.Part{Type: llm.PartReasoning, Reasoning: &llm.Reasoning{Provider: llm.KindFake, Text: next.Reasoning}})
	}
	if next.Text != "" {
		for i, chunk := range textChunks {
			if i > 0 {
				if err := sleepCtx(ctx, gap); err != nil {
					return nil, err
				}
			}
			onDelta(llm.Delta{Type: llm.DeltaText, Text: chunk})
		}
		parts = append(parts, llm.TextPart(next.Text))
	}
	for i, call := range next.ToolCalls {
		args, err := marshalArgs(call.Args)
		if err != nil {
			return nil, fmt.Errorf("fake: tool call %s: %w", call.Name, err)
		}
		onDelta(llm.Delta{Type: llm.DeltaToolCall, ToolName: call.Name})
		parts = append(parts, llm.Part{Type: llm.PartToolCall, ToolCall: &llm.ToolCall{ID: fmt.Sprintf("fake_call_%d", firstCall+i+1), Name: call.Name, Args: args}})
	}

	// Derive the stop reason when the script leaves it open
	stop := next.Stop
	if stop == "" {
		stop = llm.StopEndTurn
		if len(next.ToolCalls) > 0 {
			stop = llm.StopToolUse
		}
	}

	model := req.Model
	if model == "" {
		model = "fake"
	}
	return &llm.Response{
		Message: llm.Message{Role: llm.RoleAssistant, Parts: parts},
		Stop:    stop,
		Usage:   next.Usage,
		Latency: time.Since(start),
		Model:   model,
	}, nil
}

// emptyScriptResponse ends an agent run cleanly through the required finish tool
func emptyScriptResponse() ScriptedResponse {
	return ScriptedResponse{ToolCalls: []ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": emptyScriptSummary}}}}
}

func scriptedError(r ScriptedResponse) error {
	msg := r.Error
	if msg == "" {
		msg = "scripted error"
	}
	if r.ErrorStatus != 0 {
		return &llm.APIError{Provider: llm.KindFake, StatusCode: r.ErrorStatus, Message: msg}
	}
	return errors.New(msg)
}

// marshalArgs keeps arguments that already are JSON and marshals everything else
func marshalArgs(args any) (json.RawMessage, error) {
	switch v := args.(type) {
	case nil:
		return json.RawMessage(`{}`), nil
	case json.RawMessage:
		if !json.Valid(v) {
			return nil, errors.New("args are not valid JSON")
		}
		return v, nil
	case []byte:
		if !json.Valid(v) {
			return nil, errors.New("args are not valid JSON")
		}
		return json.RawMessage(v), nil
	}
	return json.Marshal(args)
}

// popLocked takes the next scripted response from the queue in use; callers must hold p.mu
func (p *Provider) popLocked(ctx context.Context) (ScriptedResponse, bool, error) {
	if p.queue != nil {
		return p.queue.Pop(ctx)
	}
	if len(p.script) == 0 {
		return ScriptedResponse{}, false, nil
	}
	next := p.script[0]
	p.script = p.script[1:]
	return next, true, nil
}

// sleepCtx waits for d unless ctx ends first
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d > 0 {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
	}
	return ctx.Err()
}

// chunks splits text into word-sized pieces so consumers see more than one text delta
func chunks(text string) []string {
	var out []string
	for len(text) > 0 {
		i := strings.IndexByte(text[1:], ' ')
		if i < 0 {
			out = append(out, text)
			break
		}
		out = append(out, text[:i+1])
		text = text[i+1:]
	}
	return out
}

// cloneRequest copies the slices of a request so later appends by the caller do not change what was recorded
func cloneRequest(req llm.Request) llm.Request {
	req.System = slices.Clone(req.System)
	req.Tools = slices.Clone(req.Tools)
	req.Messages = slices.Clone(req.Messages)
	for i, m := range req.Messages {
		req.Messages[i].Parts = slices.Clone(m.Parts)
	}
	return req
}
