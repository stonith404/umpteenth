//go:build unit

package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
)

// compactionRecorder keeps the compactions an agent run reports
type compactionRecorder struct {
	nopObserver
	seen []Compaction
}

func (r *compactionRecorder) OnCompaction(c Compaction) { r.seen = append(r.seen, c) }

func readerCall() fake.ScriptedResponse {
	return fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "reader"}}}
}

func finishCall() fake.ScriptedResponse {
	return fake.ScriptedResponse{ToolCalls: []fake.ScriptedToolCall{{Name: "finish", Args: map[string]any{"status": "success", "summary": "done"}}}}
}

func TestLongConversationsAreCompacted(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	obs := &compactionRecorder{}

	// Two small turns, then a turn whose prompt passes 70% of the 10k window
	small := readerCall()
	small.Usage = llm.Usage{Input: 1_000, Output: 100}
	big := readerCall()
	big.Usage = llm.Usage{Input: 2_000, CacheRead: 5_000, Output: 200}
	summary := fake.ScriptedResponse{Text: "## Goal\nRead things\n## Next steps\nFinish", Usage: llm.Usage{Input: 3_000, Output: 50}}
	p.Enqueue(small, small, big, summary, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Price: llm.Price{In: 1_000_000}, Tools: []Tool{reader, FinishTool()}, Observer: obs, ContextWindow: 10_000}, userMsg("read the files"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Equal(t, 4, out.Turns)

	// The summary call has no tools and reads the rendered conversation
	reqs := p.Requests()
	require.Len(t, reqs, 5)
	summaryReq := reqs[3]
	require.Empty(t, summaryReq.Tools)
	require.Len(t, summaryReq.Messages, 1)
	require.Contains(t, summaryReq.Messages[0].Text(), "read the files")
	require.Contains(t, summaryReq.Messages[0].Text(), "[tool call reader]")
	require.Contains(t, summaryReq.Messages[0].Text(), "[tool result reader]")

	// The next turn starts from the first message with the summary, followed by the last exchange as it was
	next := reqs[4].Messages
	require.Len(t, next, 3)
	require.Equal(t, llm.RoleUser, next[0].Role)
	require.Contains(t, next[0].Text(), "read the files")
	require.Contains(t, next[0].Text(), "Read things")
	require.Equal(t, llm.RoleAssistant, next[1].Role)
	require.Len(t, next[1].ToolCalls(), 1)
	require.Equal(t, next[1].ToolCalls()[0].ID, next[2].Parts[0].ToolResult.CallID)

	// The summary is reported and paid for like any other call
	require.Len(t, obs.seen, 1)
	require.Equal(t, 3, obs.seen[0].Turn)
	require.Equal(t, 4, obs.seen[0].Messages)
	require.Greater(t, obs.seen[0].PromptTokens, int64(7_000))
	require.EqualValues(t, 3_000, obs.seen[0].Cost)
	require.EqualValues(t, 1_000+1_000+2_000+3_000, out.Usage.Input)
}

func TestConversationsAreCompactedWhenTheProviderReportsNoUsage(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	obs := &compactionRecorder{}

	// Each answer adds about 4k estimated tokens while reporting none, so the third one takes the conversation past the 10k window
	long := readerCall()
	long.Text = strings.Repeat("x", 12_000)
	p.Enqueue(long, long, long, fake.ScriptedResponse{Text: "summary"}, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, FinishTool()}, Observer: obs, ContextWindow: 10_000}, userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Len(t, obs.seen, 1)
	require.Equal(t, 3, obs.seen[0].Turn)
}

func TestReasoningThatIsNotSentBackDoesNotTriggerCompaction(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	obs := &compactionRecorder{}

	// Readable reasoning alone would pass the 10k window, but adapters don't send it back, and the reported usage stays low
	thinking := readerCall()
	thinking.Reasoning = strings.Repeat("r", 12_000)
	thinking.Usage = llm.Usage{Input: 1_000, Output: 100}
	p.Enqueue(thinking, thinking, thinking, thinking, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, FinishTool()}, Observer: obs, ContextWindow: 10_000}, userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Empty(t, obs.seen)
}

func TestOversizedAnswersFailTheRun(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	huge := readerCall()
	huge.Text = strings.Repeat("x", llm.MaxAnswerBytes+1)
	p.Enqueue(huge, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, FinishTool()}, Observer: nopObserver{}, ContextWindow: 100_000}, userMsg("go"))
	require.Equal(t, StatusFailed, out.Status)
	require.Contains(t, out.Reason, "one turn may add to the conversation")
	require.Len(t, p.Requests(), 1)
	require.Len(t, out.Messages, 1)
}

func TestSmallConversationsAreNotCompacted(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	small := readerCall()
	small.Usage = llm.Usage{Input: 1_000, Output: 100}
	p.Enqueue(small, small, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, FinishTool()}, Observer: nopObserver{}, ContextWindow: 100_000}, userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Len(t, p.Requests(), 3)
}

func TestCompactionIsNotRepeatedOnTheNextTurn(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	obs := &compactionRecorder{}

	// Every turn is over the threshold, so compaction happens on every second turn at most
	big := readerCall()
	big.Usage = llm.Usage{Input: 9_000}
	summary := fake.ScriptedResponse{Text: "summary"}
	p.Enqueue(big, big, summary, big, big, summary, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, FinishTool()}, Observer: obs, ContextWindow: 10_000}, userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Len(t, obs.seen, 2)
	require.Equal(t, 2, obs.seen[0].Turn)
	require.Equal(t, 4, obs.seen[1].Turn)

	// The second summary reads the first one and replaces it, so summaries don't pile up in the first message
	reqs := p.Requests()
	require.Contains(t, reqs[5].Messages[0].Text(), "summary")
	opener := reqs[6].Messages[0]
	require.Len(t, opener.Parts, 2)
	require.Equal(t, 1, strings.Count(opener.Text(), "<summary>"))
}

func TestAFailedSummaryKeepsTheConversation(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	obs := &compactionRecorder{}
	big := readerCall()
	big.Usage = llm.Usage{Input: 9_000}
	p.Enqueue(readerCall(), big, fake.ScriptedResponse{Error: "boom", ErrorStatus: 400}, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, FinishTool()}, Observer: obs, ContextWindow: 10_000}, userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Len(t, obs.seen, 1)
	require.Error(t, obs.seen[0].Err)

	// The turn after the failed summary still sends the whole conversation
	reqs := p.Requests()
	require.Len(t, reqs[3].Messages, 5)
}

func TestAPromptThatIsTooLongIsCompactedAndRetried(t *testing.T) {
	p := fake.New()
	reader := &slowTool{name: "reader", readOnly: true}
	obs := &compactionRecorder{}

	// The window is unknown, so only the provider's refusal triggers the compaction
	overflow := fake.ScriptedResponse{Error: "prompt is too long: 210000 tokens > 200000 maximum", ErrorStatus: 400}
	p.Enqueue(readerCall(), readerCall(), overflow, fake.ScriptedResponse{Text: "summary"}, finishCall())

	out := Run(context.Background(), Config{Provider: p, Model: "m", Tools: []Tool{reader, FinishTool()}, Observer: obs}, userMsg("go"))
	require.Equal(t, StatusSucceeded, out.Status)
	require.Equal(t, 3, out.Turns)
	require.Len(t, obs.seen, 1)

	// The retried turn sends the compacted conversation
	reqs := p.Requests()
	require.Len(t, reqs, 5)
	require.Len(t, reqs[4].Messages, 3)
	require.Contains(t, reqs[4].Messages[0].Text(), "summary")
}

func TestIsContextOverflow(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&llm.APIError{StatusCode: 400, Message: "This model's maximum context length is 128000 tokens"}, true},
		{&llm.APIError{StatusCode: 400, Type: "context_length_exceeded"}, true},
		{&llm.APIError{StatusCode: 400, Message: "prompt is too long: 210000 tokens > 200000 maximum"}, true},
		{&llm.APIError{StatusCode: 413, Message: "Input is too long for requested model"}, true},
		{&llm.APIError{StatusCode: 400, Message: "invalid tool schema"}, false},
		{&llm.APIError{StatusCode: 429, Message: "too many tokens per minute"}, false},
		{errors.New("prompt is too long"), false},
	} {
		require.Equal(t, tc.want, IsContextOverflow(tc.err), tc.err.Error())
	}
}

func TestTranscriptKeepsBothEndsOfALongRun(t *testing.T) {
	var messages []llm.Message
	for i := range 200 {
		id := "c" + strings.Repeat("x", i%3)
		messages = append(messages,
			llm.Message{Role: llm.RoleAssistant, Parts: []llm.Part{{Type: llm.PartToolCall, ToolCall: &llm.ToolCall{ID: id, Name: "bash"}}}},
			llm.Message{Role: llm.RoleUser, Parts: []llm.Part{{Type: llm.PartToolResult, ToolResult: &llm.ToolResult{CallID: id, Content: strings.Repeat("y", 10_000)}}}},
		)
	}
	messages[0].Parts = append(messages[0].Parts, llm.TextPart("FIRST"))
	messages[len(messages)-1].Parts = append(messages[len(messages)-1].Parts, llm.TextPart("LAST"))

	s := renderTranscript(messages, 30_000)
	require.Less(t, len(s), 31_000)
	require.Contains(t, s, "FIRST")
	require.Contains(t, s, "LAST")
	require.Contains(t, s, "characters omitted")
}
