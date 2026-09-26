// Package agent runs the provider-agnostic agent loop on the host; the sandbox only executes tools (PLAN.md §6)
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

// Tool is one function the agent can call
type Tool interface {
	Def() llm.ToolDef
	// ReadOnly tools may run in parallel with each other
	ReadOnly() bool
	Run(ctx context.Context, call llm.ToolCall) Result
}

// Result is the outcome of one tool call
type Result struct {
	// Content is what the model sees
	Content string
	IsError bool
	// Finish is set by the finish tool and ends the run
	Finish *Finish
	// Meta is recorded in the timeline but never sent to the model
	Meta map[string]any
}

// Finish is the required end of every agent run
type Finish struct {
	Status  string         `json:"status"`
	Summary string         `json:"summary"`
	Outputs map[string]any `json:"outputs,omitempty"`
}

// Status is how an agent run ended
type Status string

const (
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusTimedOut  Status = "timed_out"
	StatusCancelled Status = "cancelled"
)

// Budget bounds a run; zero values mean unlimited
type Budget struct {
	MaxTurns int
	// MaxCost is in micro-USD
	MaxCost  int64
	Deadline time.Time
	// ExtraCost returns spend made outside the loop that counts toward MaxCost, e.g. ump llm calls from scripts
	ExtraCost func() int64
}

// Observer receives everything worth putting on the timeline
type Observer interface {
	OnDelta(delta llm.Delta)
	OnLLMCall(turn int, resp *llm.Response, cost int64, latency time.Duration, err error)
	OnToolStart(call llm.ToolCall)
	OnToolEnd(call llm.ToolCall, res Result, took time.Duration)
}

// Config describes one agent run
type Config struct {
	Provider llm.Provider
	Model    string
	Price    llm.Price
	System   []llm.Block
	Tools    []Tool
	Budget   Budget
	Effort   llm.Effort
	Observer Observer
	// ContextWindow is the model's context size in tokens, above which the conversation gets compacted; zero only compacts after the provider rejects a prompt as too long
	ContextWindow int
	// RetryBackoff is the wait before each repeat of a transiently failed model call, and its length caps the repeats
	// Nil uses defaultRetryBackoff
	RetryBackoff []time.Duration
}

// defaultRetryBackoff repeats a dropped or overloaded model call three times, because a flaky response or a short network outage should not fail a long run
var defaultRetryBackoff = []time.Duration{2 * time.Second, 10 * time.Second, 30 * time.Second}

// Outcome summarizes a finished agent run
type Outcome struct {
	Status   Status
	Finish   *Finish
	Reason   string
	Turns    int
	Usage    llm.Usage
	Cost     int64
	LLMTime  time.Duration
	ToolTime time.Duration
	Messages []llm.Message
}

const nudgeMessage = "You ended your turn without calling a tool. The run only ends when you call the `finish` tool. If the job is done, call `finish` now with the status, a summary and any outputs; otherwise continue working."

// callModel makes one turn's model call, repeating it after transient provider failures
// Repeating is safe because tools only run once a complete message arrives
func callModel(ctx context.Context, cfg Config, defs []llm.ToolDef, turn int, out *Outcome) (*llm.Response, error) {
	backoff := cfg.RetryBackoff
	if backoff == nil {
		backoff = defaultRetryBackoff
	}

	for attempt := 0; ; attempt++ {
		start := time.Now()
		resp, err := cfg.Provider.Stream(ctx, llm.Request{
			Model:    cfg.Model,
			System:   cfg.System,
			Messages: out.Messages,
			Tools:    defs,
			Effort:   cfg.Effort,
		}, cfg.Observer.OnDelta)
		latency := time.Since(start)
		out.LLMTime += latency

		// Failed attempts are recorded too, so the timeline shows why a turn took longer
		var cost int64
		if resp != nil {
			out.Usage = out.Usage.Add(resp.Usage)
			cost = llm.Cost(resp.Usage, cfg.Price)
			out.Cost += cost
		}
		cfg.Observer.OnLLMCall(turn, resp, cost, latency, err)
		if err == nil || attempt >= len(backoff) || !llm.IsTransient(err) || ctx.Err() != nil {
			return resp, err
		}

		// Wait before the next attempt unless the run is stopped meanwhile
		timer := time.NewTimer(backoff[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, err
		case <-timer.C:
		}
	}
}

// Run drives the model until it calls finish, a budget is exhausted, or ctx is canceled
func Run(ctx context.Context, cfg Config, messages []llm.Message) Outcome {
	out := Outcome{Messages: append([]llm.Message(nil), messages...)}
	tools := make(map[string]Tool, len(cfg.Tools))
	defs := make([]llm.ToolDef, 0, len(cfg.Tools))
	for _, t := range cfg.Tools {
		def := t.Def()
		tools[def.Name] = t
		defs = append(defs, def)
	}

	// Tools are offered sorted by name, so the tool list is byte-stable between runs and stays in the provider's prompt cache
	slices.SortFunc(defs, func(a, b llm.ToolDef) int { return strings.Compare(a.Name, b.Name) })

	// Compaction keeps the run's first message, so it is remembered before the conversation changes
	var first llm.Message
	if len(messages) > 0 {
		first = messages[0]
	}
	nudged := false
	lastCompacted := 0
	for turn := 1; ; turn++ {
		// Budgets are checked before every call so a run never starts a turn it cannot afford
		if reason, status := checkBudget(ctx, cfg.Budget, turn, out.Cost); reason != "" {
			out.Status, out.Reason = status, reason
			return out
		}
		out.Turns = turn

		resp, err := callModel(ctx, cfg, defs, turn, &out)

		// A prompt the provider rejects as too long gets one compaction and a second try
		if err != nil && IsContextOverflow(err) && ctx.Err() == nil {
			if cerr := compact(ctx, cfg, first, turn, 0, &out); cerr == nil {
				lastCompacted = turn
				resp, err = callModel(ctx, cfg, defs, turn, &out)
			}
		}
		if err != nil {
			out.Status, out.Reason = contextStatus(ctx, StatusFailed), "model call failed: "+err.Error()
			return out
		}

		// The conversation is sent again on every turn, so an answer far beyond what any model writes must not grow it without bound, whatever the provider streamed
		if chars := messageChars([]llm.Message{resp.Message}, true); chars > llm.MaxAnswerBytes {
			out.Status, out.Reason = StatusFailed, fmt.Sprintf("the model's answer of %d MiB is larger than the %d MiB one turn may add to the conversation", chars>>20, llm.MaxAnswerBytes>>20)
			return out
		}
		out.Messages = append(out.Messages, resp.Message)

		if resp.Stop == llm.StopRefusal {
			out.Status, out.Reason = StatusFailed, "the model refused to continue"
			return out
		}

		calls := resp.Message.ToolCalls()
		if len(calls) == 0 {
			// A model that ends its turn without a tool is reminded that finish is required, and fails when it does so twice in a row
			if nudged {
				out.Status, out.Reason = StatusFailed, "the model stopped without calling finish"
				return out
			}
			nudged = true
			out.Messages = append(out.Messages, llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(nudgeMessage)}})
			continue
		}
		nudged = false

		// All results go back in one message, which keeps parallel tool use working on every provider
		results, finish, took := runTools(ctx, tools, calls, cfg.Observer)
		out.ToolTime += took
		out.Messages = append(out.Messages, llm.Message{Role: llm.RoleUser, Parts: results})

		if finish != nil {
			out.Finish = finish
			out.Status = StatusSucceeded
			if finish.Status != "success" {
				out.Status = StatusFailed
				out.Reason = "the agent reported failure"
			}
			return out
		}
		if ctx.Err() != nil {
			out.Status, out.Reason = contextStatus(ctx, StatusCancelled), "run was stopped"
			return out
		}

		// A conversation close to the context window is summarized before the next turn, but never twice in a row so a summary that doesn't shrink it enough can't loop
		// The estimate starts from the usage the provider reports, so a conversation whose own size already passes the window counts with that size, whatever an endpoint reports
		if cfg.ContextWindow > 0 && turn > lastCompacted+1 {
			tokens := nextPromptTokens(resp, out.Messages[len(out.Messages)-1:])
			if own := resentTokens(out.Messages); own > int64(cfg.ContextWindow) {
				tokens = max(tokens, own)
			}
			if float64(tokens) > compactAt*float64(cfg.ContextWindow) && compact(ctx, cfg, first, turn, tokens, &out) == nil {
				lastCompacted = turn
			}
		}
	}
}

func checkBudget(ctx context.Context, b Budget, turn int, cost int64) (string, Status) {
	if b.ExtraCost != nil {
		cost += b.ExtraCost()
	}
	switch {
	case ctx.Err() != nil:
		return "run was stopped", contextStatus(ctx, StatusCancelled)
	case !b.Deadline.IsZero() && time.Now().After(b.Deadline):
		return "the run exceeded its time limit", StatusTimedOut
	case b.MaxTurns > 0 && turn > b.MaxTurns:
		return fmt.Sprintf("the run exceeded its limit of %d turns", b.MaxTurns), StatusFailed
	case b.MaxCost > 0 && cost >= b.MaxCost:
		return fmt.Sprintf("the run exceeded its cost limit of $%.2f", float64(b.MaxCost)/1e6), StatusFailed
	}
	return "", ""
}

// contextStatus distinguishes a deadline from an explicit cancellation
func contextStatus(ctx context.Context, fallback Status) Status {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return StatusTimedOut
	case errors.Is(ctx.Err(), context.Canceled):
		return StatusCancelled
	}
	return fallback
}

// runTools executes calls in order, running consecutive read-only calls in parallel
func runTools(ctx context.Context, tools map[string]Tool, calls []llm.ToolCall, obs Observer) ([]llm.Part, *Finish, time.Duration) {
	start := time.Now()
	results := make([]Result, len(calls))

	for i := 0; i < len(calls); {
		// Group a run of consecutive read-only calls, and run everything else alone
		j := i + 1
		if isReadOnly(tools, calls[i]) {
			for j < len(calls) && isReadOnly(tools, calls[j]) {
				j++
			}
		}

		var wg sync.WaitGroup
		for k := i; k < j; k++ {
			wg.Go(func() {
				results[k] = runTool(ctx, tools, calls[k], obs)
			})
		}
		wg.Wait()
		i = j
	}

	parts := make([]llm.Part, len(calls))
	var finish *Finish
	for i, call := range calls {
		parts[i] = llm.Part{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: call.ID, Content: results[i].Content, IsError: results[i].IsError}}
		if results[i].Finish != nil && finish == nil {
			finish = results[i].Finish
		}
	}
	return parts, finish, time.Since(start)
}

func isReadOnly(tools map[string]Tool, call llm.ToolCall) bool {
	t, ok := tools[call.Name]
	return ok && t.ReadOnly()
}

func runTool(ctx context.Context, tools map[string]Tool, call llm.ToolCall, obs Observer) Result {
	obs.OnToolStart(call)
	start := time.Now()

	tool, ok := tools[call.Name]
	var res Result
	switch {
	case !ok:
		res = Errorf("Unknown tool %q. Use one of the tools you were given.", call.Name)
	case len(call.Args) > 0 && !json.Valid(call.Args):
		res = Errorf("The arguments for %s are not valid JSON.", call.Name)
	default:
		res = safeRun(ctx, tool, call)
	}

	obs.OnToolEnd(call, res, time.Since(start))
	return res
}

// safeRun turns a panicking tool into an error result, since tools run on goroutines of their own where a panic would take down the whole server
func safeRun(ctx context.Context, tool Tool, call llm.ToolCall) (res Result) {
	defer func() {
		if p := recover(); p != nil {
			slog.ErrorContext(ctx, "Tool panicked", slog.String("tool", call.Name), slog.Any("panic", p), slog.String("stack", string(debug.Stack())))
			res = Errorf("The tool %s failed unexpectedly.", call.Name)
		}
	}()
	return tool.Run(ctx, call)
}

// Errorf builds an error result the model can react to
func Errorf(format string, args ...any) Result {
	return Result{Content: fmt.Sprintf(format, args...), IsError: true}
}

// DecodeArgs decodes tool arguments, treating empty arguments as an empty object
func DecodeArgs(call llm.ToolCall, into any) error {
	if len(call.Args) == 0 {
		return nil
	}
	return json.Unmarshal(call.Args, into)
}

// Truncate keeps the head and tail of s, reporting whether anything was cut
// Both cuts move to character boundaries, so the result stays valid UTF-8 when s is
func Truncate(s string, head, tail int) (string, bool) {
	if len(s) <= head+tail {
		return s, false
	}
	end, start := head, len(s)-tail
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	for start < len(s) && !utf8.RuneStart(s[start]) {
		start++
	}
	return s[:end] + fmt.Sprintf("\n\n[… %d characters omitted …]\n\n", start-end) + s[start:], true
}
