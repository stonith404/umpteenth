//go:build unit

package agent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
)

type nopObserver struct{}

func (nopObserver) OnDelta(llm.Delta)                                         {}
func (nopObserver) OnLLMCall(int, *llm.Response, int64, time.Duration, error) {}
func (nopObserver) OnToolStart(llm.ToolCall)                                  {}
func (nopObserver) OnToolEnd(llm.ToolCall, Result, time.Duration)             {}

// slowTool records how many calls run at the same time
type slowTool struct {
	name     string
	readOnly bool
	running  atomic.Int32
	maxSeen  atomic.Int32
	mu       sync.Mutex
	order    []string
}

func (s *slowTool) Def() llm.ToolDef {
	return llm.ToolDef{Name: s.name, Schema: json.RawMessage(`{"type":"object"}`)}
}
func (s *slowTool) ReadOnly() bool { return s.readOnly }
func (s *slowTool) Run(_ context.Context, call llm.ToolCall) Result {
	n := s.running.Add(1)
	for {
		m := s.maxSeen.Load()
		if n <= m || s.maxSeen.CompareAndSwap(m, n) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	s.running.Add(-1)
	s.mu.Lock()
	s.order = append(s.order, call.ID)
	s.mu.Unlock()
	return Result{Content: "ok " + call.ID}
}

func userMsg(text string) []llm.Message {
	return []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(text)}}}
}

func TestRunFinishesWithOutputs(t *testing.T) {
	p := fake.New()
	p.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done", "outputs": map[string]any{"n": 3}}}}, Usage: llm.Usage{Input: 1_000_000, Output: 1_000_000}})

	out := Run(context.Background(), Config{Provider: p, Model: "m", Price: llm.Price{In: 1_000_000, Out: 2_000_000}, Tools: []Tool{FinishTool()}, Observer: nopObserver{}}, userMsg("go"))

	require.Equal(t, StatusSucceeded, out.Status)
	require.Equal(t, "done", out.Finish.Summary)
	require.EqualValues(t, 3, out.Finish.Outputs["n"])
	// 1M input at $1 plus 1M output at $2 is $3, in micro-USD
	require.EqualValues(t, 3_000_000, out.Cost)
	require.Equal(t, 1, out.Turns)
}

func TestTransientModelFailuresAreRetriedWithinTheTurn(t *testing.T) {
	finish := fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done"}}}}
	cfg := func(p *fake.Provider) Config {
		return Config{Provider: p, Model: "m", Tools: []Tool{FinishTool()}, Observer: nopObserver{}, RetryBackoff: []time.Duration{time.Millisecond, time.Millisecond}}
	}

	// An overloaded server twice in a row still finishes the run in one turn
	p := fake.New()
	p.Enqueue(fake.ScriptedResponse{ErrorStatus: 529}, fake.ScriptedResponse{ErrorStatus: 503}, finish)
	out := Run(context.Background(), cfg(p), userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Equal(t, 1, out.Turns)
	require.Len(t, p.Requests(), 3)

	// A third transient failure exhausts the retries
	p = fake.New()
	p.Enqueue(fake.ScriptedResponse{ErrorStatus: 503}, fake.ScriptedResponse{ErrorStatus: 503}, fake.ScriptedResponse{ErrorStatus: 503}, finish)
	out = Run(context.Background(), cfg(p), userMsg("go"))
	require.Equal(t, StatusFailed, out.Status)
	require.Len(t, p.Requests(), 3)

	// A client error is permanent and is not repeated
	p = fake.New()
	p.Enqueue(fake.ScriptedResponse{ErrorStatus: 400}, finish)
	out = Run(context.Background(), cfg(p), userMsg("go"))
	require.Equal(t, StatusFailed, out.Status)
	require.Len(t, p.Requests(), 1)
}

func TestRunNudgesOnceThenFails(t *testing.T) {
	p := fake.New()
	p.Enqueue(fake.ScriptedResponse{Text: "I think I'm done"}, fake.ScriptedResponse{Text: "Really done"})

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{FinishTool()}, Observer: nopObserver{}}, userMsg("go"))

	require.Equal(t, StatusFailed, out.Status)
	require.Contains(t, out.Reason, "without calling finish")
	// The second request carries the nudge as the last user message
	reqs := p.Requests()
	require.Len(t, reqs, 2)
	last := reqs[1].Messages[len(reqs[1].Messages)-1]
	require.Equal(t, llm.RoleUser, last.Role)
	require.Contains(t, last.Text(), "finish")
}

func TestReadOnlyToolsRunInParallelAndResultsKeepOrder(t *testing.T) {
	reader := &slowTool{name: "reader", readOnly: true}
	writer := &slowTool{name: "writer"}
	p := fake.New()
	p.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "reader"}, {Name: "reader"}, {Name: "reader"}, {Name: "writer"}, {Name: "writer"}}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "s"}}}},
	)

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, writer, FinishTool()}, Observer: nopObserver{}}, userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)

	// Consecutive read-only calls overlap, writes never do
	require.EqualValues(t, 3, reader.maxSeen.Load())
	require.EqualValues(t, 1, writer.maxSeen.Load())

	// All results come back in one message, in the order of the calls
	results := out.Messages[2]
	require.Equal(t, llm.RoleUser, results.Role)
	require.Len(t, results.Parts, 5)
	for i, part := range results.Parts {
		require.Equal(t, llm.PartToolResult, part.Type)
		require.Equal(t, "ok "+out.Messages[1].Parts[i+0].ToolCall.ID, part.ToolResult.Content)
	}
}

func TestBudgetsStopTheRun(t *testing.T) {
	p := fake.New()
	for range 5 {
		p.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "reader"}}, Usage: llm.Usage{Output: 1_000_000}})
	}
	reader := &slowTool{name: "reader", readOnly: true}

	// The turn limit ends the run before the fourth call
	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader}, Budget: Budget{MaxTurns: 3}, Observer: nopObserver{}}, userMsg("go"))
	require.Equal(t, StatusFailed, out.Status)
	require.Contains(t, out.Reason, "turns")
	require.Equal(t, 3, out.Turns)

	// The cost limit ends the run once the spend reaches it
	p.Reset()
	for range 5 {
		p.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "reader"}}, Usage: llm.Usage{Output: 1_000_000}})
	}
	out = Run(context.Background(), Config{Provider: p, Model: "m", Price: llm.Price{Out: 1_000_000}, Tools: []Tool{reader}, Budget: Budget{MaxCost: 2_000_000}, Observer: nopObserver{}}, userMsg("go"))
	require.Equal(t, StatusFailed, out.Status)
	require.Contains(t, out.Reason, "cost limit")
	require.EqualValues(t, 2_000_000, out.Cost)
}

func TestUnknownToolAndBadArgsAreReportedToTheModel(t *testing.T) {
	p := fake.New()
	p.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "nope"}}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "maybe"}}}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "failure", "summary": "gave up"}}}},
	)
	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{FinishTool()}, Observer: nopObserver{}}, userMsg("go"))

	require.Equal(t, StatusFailed, out.Status)
	require.Equal(t, "gave up", out.Finish.Summary)
	require.True(t, out.Messages[2].Parts[0].ToolResult.IsError)
	require.Contains(t, out.Messages[2].Parts[0].ToolResult.Content, "Unknown tool")
	require.True(t, out.Messages[4].Parts[0].ToolResult.IsError)
}

func TestCancelledContextStopsTheRun(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := Run(ctx, Config{Provider: fake.New(), Model: "m", Tools: []Tool{FinishTool()}, Observer: nopObserver{}}, userMsg("go"))
	require.Equal(t, StatusCancelled, out.Status)
}

func TestHeadTailKeepsBothEnds(t *testing.T) {
	h := newHeadTail(5, 5)
	_, _ = h.Write([]byte("0123456789abcdefghij"))
	s, truncated := h.String()
	require.True(t, truncated)
	require.Contains(t, s, "01234")
	require.Contains(t, s, "fghij")
	require.Contains(t, s, "10 bytes omitted")

	h = newHeadTail(5, 5)
	_, _ = h.Write([]byte("short"))
	s, truncated = h.String()
	require.False(t, truncated)
	require.Equal(t, "short", s)
}

func TestSpendOutsideTheLoopCountsTowardTheCostLimit(t *testing.T) {
	// Scripts already spent the whole budget through ump llm, so the agent doesn't start another turn
	p := fake.New()
	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{FinishTool()}, Observer: nopObserver{},
		Budget: Budget{MaxCost: 1000, ExtraCost: func() int64 { return 1000 }}}, userMsg("go"))
	require.Equal(t, StatusFailed, out.Status)
	require.Contains(t, out.Reason, "cost limit")
	require.Empty(t, p.Requests())
}

func TestTruncateKeepsCharactersWhole(t *testing.T) {
	s := strings.Repeat("✓", 100)
	for _, cut := range [][2]int{{10, 0}, {0, 10}, {7, 11}} {
		out, truncated := Truncate(s, cut[0], cut[1])
		require.True(t, truncated)
		require.True(t, utf8.ValidString(out), "cut %v", cut)
	}
}

func TestCappedBufferStopsGrowing(t *testing.T) {
	b := &cappedBuffer{max: 10}
	for range 1000 {
		n, err := b.Write([]byte("0123456789"))
		require.NoError(t, err)
		require.Equal(t, 10, n)
	}
	require.Equal(t, "0123456789", b.String())
}
