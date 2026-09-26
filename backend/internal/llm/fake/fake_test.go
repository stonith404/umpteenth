//go:build unit

package fake

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

func request(text string) llm.Request {
	return llm.Request{Model: "fake-model", Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(text)}}}}
}

func TestScriptIsReplayedInOrder(t *testing.T) {
	p := New()
	p.Enqueue(
		ScriptedResponse{Text: "Looking at the repo now.", Reasoning: "Start with ls", ToolCalls: []ScriptedToolCall{{Name: "bash", Args: map[string]any{"command": "ls"}}, {Name: "read_file", Args: json.RawMessage(`{"path":"README.md"}`)}}, Usage: llm.Usage{Input: 100, Output: 20}},
		ScriptedResponse{Text: "Done."},
	)

	// The first response streams reasoning, text in pieces and both tool call starts
	var deltas []llm.Delta
	resp, err := p.Stream(context.Background(), request("go"), func(d llm.Delta) { deltas = append(deltas, d) })
	require.NoError(t, err)
	assert.Equal(t, llm.StopToolUse, resp.Stop)
	assert.Equal(t, "Looking at the repo now.", resp.Message.Text())
	assert.Equal(t, llm.Usage{Input: 100, Output: 20}, resp.Usage)
	assert.Equal(t, "fake-model", resp.Model)
	assert.Equal(t, []llm.ToolCall{
		{ID: "fake_call_1", Name: "bash", Args: json.RawMessage(`{"command":"ls"}`)},
		{ID: "fake_call_2", Name: "read_file", Args: json.RawMessage(`{"path":"README.md"}`)},
	}, resp.Message.ToolCalls())

	var text string
	var kinds []llm.DeltaType
	for _, d := range deltas {
		if d.Type == llm.DeltaText {
			text += d.Text
		}
		kinds = append(kinds, d.Type)
	}
	assert.Equal(t, "Looking at the repo now.", text)
	assert.Equal(t, llm.DeltaReasoning, kinds[0])
	assert.Greater(t, len(kinds), 4, "text is split into several deltas")
	assert.Equal(t, llm.DeltaToolCall, kinds[len(kinds)-1])

	// The second response ends the turn
	resp, err = p.Stream(context.Background(), request("next"), nil)
	require.NoError(t, err)
	assert.Equal(t, llm.StopEndTurn, resp.Stop)
	assert.Equal(t, "Done.", resp.Message.Text())
	assert.Equal(t, 0, p.Pending())
}

func TestEmptyScriptFinishes(t *testing.T) {
	p := New()
	resp, err := p.Stream(context.Background(), request("go"), nil)
	require.NoError(t, err)

	calls := resp.Message.ToolCalls()
	require.Len(t, calls, 1)
	assert.Equal(t, "finish", calls[0].Name)
	assert.JSONEq(t, `{"status":"success","summary":"fake provider: script empty"}`, string(calls[0].Args))
	assert.Equal(t, llm.StopToolUse, resp.Stop)
}

func TestRequestsAreRecorded(t *testing.T) {
	p := New()
	req := request("first")
	_, err := p.Stream(context.Background(), req, nil)
	require.NoError(t, err)

	// Later appends by the caller do not change what was recorded
	req.Messages[0].Parts[0].Text = "changed"
	req.Messages = append(req.Messages, llm.Message{Role: llm.RoleAssistant})
	recorded := p.Requests()
	require.Len(t, recorded, 1)
	assert.Equal(t, "first", recorded[0].Messages[0].Text())
	assert.Len(t, recorded[0].Messages, 1)

	// Reset clears the script, the recordings and the call counter
	p.Enqueue(ScriptedResponse{Text: "x"})
	p.Reset()
	assert.Empty(t, p.Requests())
	assert.Equal(t, 0, p.Pending())
}

func TestErrorsAndDelay(t *testing.T) {
	p := New()
	p.Enqueue(ScriptedResponse{Error: "boom"}, ScriptedResponse{Error: "slow down", ErrorStatus: 429}, ScriptedResponse{Text: "late", Delay: time.Hour})

	_, err := p.Stream(context.Background(), request("a"), nil)
	require.EqualError(t, err, "boom")

	_, err = p.Stream(context.Background(), request("b"), nil)
	var apiErr *llm.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 429, apiErr.StatusCode)
	assert.True(t, apiErr.Retryable())

	// A delayed response honors cancellation
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = p.Stream(ctx, request("c"), nil)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestScriptedResponseJSON(t *testing.T) {
	var r ScriptedResponse
	err := json.Unmarshal([]byte(`{"text":"hi","toolCalls":[{"name":"finish","args":{"status":"failure","summary":"nope"}}],"stop":"tool_use","usage":{"input":5,"output":2},"delayMs":150,"errorStatus":500}`), &r)
	require.NoError(t, err)
	assert.Equal(t, 150*time.Millisecond, r.Delay)
	assert.Equal(t, "finish", r.ToolCalls[0].Name)
	assert.Equal(t, llm.Usage{Input: 5, Output: 2}, r.Usage)
	assert.Equal(t, 500, r.ErrorStatus)

	data, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"delayMs":150`)
}

func TestConcurrentCallsGetDistinctResponses(t *testing.T) {
	p := New()
	const n = 50
	for i := range n {
		p.Enqueue(ScriptedResponse{Text: fmt.Sprintf("r%d", i)})
	}

	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[string]bool{}
	for range n {
		wg.Go(func() {
			resp, err := p.Stream(context.Background(), request("x"), nil)
			if !assert.NoError(t, err) {
				return
			}
			mu.Lock()
			seen[resp.Message.Text()] = true
			mu.Unlock()
		})
	}
	wg.Wait()
	assert.Len(t, seen, n)
	assert.Len(t, p.Requests(), n)
}

func TestSharedAndFactory(t *testing.T) {
	p, err := Factory(llm.Config{Kind: llm.KindFake})
	require.NoError(t, err)
	assert.Same(t, Shared(), p)

	// SetCaps lets tests switch Structured to the submit-tool fallback
	Shared().SetCaps(llm.Caps{Tools: true})
	assert.False(t, p.Caps("any").JSONSchema)
	Shared().Reset()
	assert.True(t, p.Caps("any").JSONSchema)
}
