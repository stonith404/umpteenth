//go:build unit

package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// liveDeltas collects the live deltas a run publishes until none arrived for a while
func liveDeltas(t *testing.T, msgs <-chan []byte) []deltaMessage {
	t.Helper()
	var out []deltaMessage
	for {
		select {
		case raw := <-msgs:
			var msg events.Message
			require.NoError(t, json.Unmarshal(raw, &msg))
			if msg.Kind != "delta" {
				continue
			}
			var d deltaMessage
			require.NoError(t, json.Unmarshal(msg.Delta, &d))
			out = append(out, d)
		case <-time.After(3 * liveOutputInterval):
			return out
		}
	}
}

func TestToolOutputIsBatchedAndBounded(t *testing.T) {
	bus := events.NewLocalBus()
	msgs, unsubscribe := bus.Subscribe(events.RunTopic("r1"))
	t.Cleanup(unsubscribe)
	obs := newTimelineObserver(context.Background(), events.NewRecorder(testutil.NewDatabaseForTest(t), bus, nil, "r1"))

	// A flood of small writes becomes a few publishes of their newest bytes
	line := "0123456789\n"
	for range 10_000 {
		obs.ToolOutput("c1", []byte(line))
	}
	deltas := liveDeltas(t, msgs)
	assert.Less(t, len(deltas), 50)
	var text strings.Builder
	for _, d := range deltas {
		assert.Equal(t, "tool_output", d.Type)
		assert.Equal(t, "c1", d.CallID)
		text.WriteString(d.Text)
	}
	assert.Less(t, text.Len(), 10_000*len(line)/2)
	assert.Contains(t, text.String(), "bytes not shown live …]\n")
	assert.True(t, strings.HasSuffix(text.String(), strings.Repeat(line, 3)))

	// Output of a call that ended before its publish is dropped, since the result replaces it
	obs.ToolOutput("c2", []byte("late"))
	obs.OnToolEnd(llm.ToolCall{ID: "c2", Name: "bash"}, agent.Result{Content: "done"}, time.Millisecond)
	assert.Empty(t, liveDeltas(t, msgs))
}
