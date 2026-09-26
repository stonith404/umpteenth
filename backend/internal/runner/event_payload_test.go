//go:build unit

package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	sandboxfake "github.com/stonith404/umpteenth/backend/internal/sandbox/fake"
)

// eventPayloads returns the stored payload of every event of a type, in order, as the events API serves them
func (h *harness) eventPayloads(t *testing.T, runID, eventType string) []map[string]any {
	t.Helper()
	rows, err := h.db.QueryContext(context.Background(), "SELECT payload FROM run_events WHERE run_id = $1 AND type = $2 ORDER BY seq", runID, eventType)
	require.NoError(t, err)
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var raw string
		require.NoError(t, rows.Scan(&raw))
		var p map[string]any
		require.NoError(t, json.Unmarshal([]byte(raw), &p))
		out = append(out, p)
	}
	require.NoError(t, rows.Err())
	return out
}

// htmlErrorPage builds a realistic HTML error page of about size bytes, like the challenge page a CDN answers a scraper with
func htmlErrorPage(size int) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"en\"><head><title>Just a moment...</title></head><body>\n")
	for i := 0; b.Len() < size; i++ {
		fmt.Fprintf(&b, "<div class=\"row\"><a href=\"/p/%d\">item %d</a> &amp; <span>more</span></div>\n", i, i)
	}
	b.WriteString("</body></html>\n")
	return b.String()
}

// The agent's finish event keeps its status, summary and outputs even when the outputs are large, since the run page draws the finish step from them
func TestLargeFinishEventKeepsItsStatus(t *testing.T) {
	h := newHarness(t)
	var items []any
	for i := range 400 {
		items = append(items, map[string]any{"title": fmt.Sprintf("Listing %d", i), "url": fmt.Sprintf("https://shop.example/items/%d", i)})
	}
	h.provider.Enqueue(fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{
		"status":  "success",
		"summary": "Collected 400 listings",
		"outputs": map[string]any{"items": items},
	}}}})
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))
	status, errMsg, _, _, _ := h.run(t, id)
	require.Equal(t, runner.StatusSucceeded, status, errMsg)

	finishes := h.eventPayloads(t, id, events.TypeFinish)
	require.Len(t, finishes, 1)
	finish := finishes[0]
	assert.Equal(t, "success", finish["status"], "the run page shows a successful run as 'Finished with a failure' when the finish event lost its status")
	assert.Equal(t, "Collected 400 listings", finish["summary"])
}

// A failed command whose output grows past the inline limit once JSON-escaped keeps its error flag, call ID and exit code
func TestLargeToolResultKeepsItsErrorFlag(t *testing.T) {
	h := newHarness(t)
	page := htmlErrorPage(11_000)
	h.adapter.On("curl -sS --fail-with-body https://shop.example/items", sandboxfake.Reply{Stdout: page + "curl: (22) The requested URL returned error: 403\n", ExitCode: 22})
	h.provider.Enqueue(
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "bash", Args: map[string]any{"command": "curl -sS --fail-with-body https://shop.example/items"}}}, Usage: llm.Usage{Input: 100, Output: 10}},
		fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "failure", "summary": "Blocked by the shop"}}}, Usage: llm.Usage{Input: 100, Output: 10}},
	)
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))

	results := h.eventPayloads(t, id, events.TypeToolResult)
	require.NotEmpty(t, results)
	bash := results[0]
	assert.Equal(t, true, bash["isError"], "a failed command must not show as a success in the timeline and the reflection transcript")
	assert.NotEmpty(t, bash["callId"])
	assert.Equal(t, "bash", bash["name"])
	meta, _ := bash["meta"].(map[string]any)
	assert.EqualValues(t, 22, meta["exitCode"])
	assert.Contains(t, bash["content"], "exit code: 22")
}

// A long model turn keeps its cost and usage, which the run page sums up for the live totals of a running run
func TestLargeLLMCallKeepsItsCost(t *testing.T) {
	h := newHarness(t)
	text := strings.Repeat("Here is what I found on the page: \"<b>price</b>\" & more.\n", 200)
	h.provider.Enqueue(fake.ScriptedResponse{
		Text:      text,
		Reasoning: strings.Repeat("thinking about the next step\n", 150),
		ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done"}}},
		Usage:     llm.Usage{Input: 20_000, Output: 4_000},
	})
	id := h.seedRun(t, runner.StatusQueued)

	require.NoError(t, h.runner.Execute(context.Background(), id))

	calls := h.eventPayloads(t, id, events.TypeLLMCall)
	require.Len(t, calls, 1)
	call := calls[0]
	assert.NotNil(t, call["cost"])
	usage, _ := call["usage"].(map[string]any)
	assert.EqualValues(t, 20_000, usage["input"])
	assert.EqualValues(t, 1, call["turn"])
}
