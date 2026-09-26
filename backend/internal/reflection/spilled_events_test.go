//go:build unit

package reflection

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// listingPage is a table-based listing page like Hacker News' front page, whose markup JSON-encodes to far more bytes than it has
func listingPage(stories int) string {
	var b strings.Builder
	b.WriteString(`<html lang="en"><head><title>News</title></head><body><table id="hnmain" border="0" cellpadding="0" cellspacing="0" width="85%"><tr><td><table class="itemlist">` + "\n")
	for i := 1; i <= stories; i++ {
		id := 40000000 + i
		fmt.Fprintf(&b, `<tr class="athing" id="%d"><td align="right" valign="top" class="title"><span class="rank">%d.</span></td><td valign="top" class="votelinks"><center><a id="up_%d" href="vote?id=%d&amp;how=up&amp;goto=news"><div class="votearrow" title="upvote"></div></a></center></td><td class="title"><span class="titleline"><a href="https://example.com/story-%d">Story %d title about something</a><span class="sitebit comhead"> (<a href="from?site=example.com"><span class="sitestr">example.com</span></a>)</span></span></td></tr>`+"\n", id, i, id, id, i, i)
		fmt.Fprintf(&b, `<tr><td colspan="2"></td><td class="subtext"><span class="subline"><span class="score" id="score_%d">%d points</span> by <a href="user?id=user%d" class="hnuser">user%d</a> <span class="age"><a href="item?id=%d">3 hours ago</a></span> | <a href="item?id=%d">%d&nbsp;comments</a></span></td></tr><tr class="spacer" style="height:5px"></tr>`+"\n", id, 100+i, i, i, id, id, i)
	}
	b.WriteString("</table></td></tr></table></body></html>\n")
	return b.String()
}

// Tool results that JSON encoding makes too large for a run_events row must still show reflection what they said and that they failed
func TestReflectionSeesLargeToolResultsTheRecorderMovedToStorage(t *testing.T) {
	ctx := t.Context()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	pb := playbook.New(playbook.Dependencies{DB: db})
	runID := seedRun(t, db, wid, jobID, 1, runner.ModeAssisted, 0)

	// The run fetched a listing page and the call failed, recorded the way the runner's timeline observer records it
	rec := events.NewRecorder(db, events.NewLocalBus(), storage.NewDatabaseStorage(db), runID)
	out := "exit code: 22\ncurl: (22) The requested URL returned error: 503\n" + listingPage(40)
	content, cut := agent.Truncate(out, 6000, 6000)
	require.True(t, cut, "the observer only keeps 12,000 bytes of a tool result")
	ms := int64(800)
	rec.Emit(ctx, events.Event{Type: events.TypeToolCall, SpanID: "s1", Payload: map[string]any{
		"callId": "c1", "name": "bash", "args": map[string]any{"command": "curl -s --fail-with-body https://news.example.com"},
	}})
	rec.Emit(ctx, events.Event{Type: events.TypeToolResult, SpanID: "s1", Ms: &ms, Payload: map[string]any{
		"callId": "c1", "name": "bash", "content": content, "isError": true, "meta": map[string]any{"exitCode": 22},
	}})

	// Reflection runs on the recorded events
	provider := fake.New()
	provider.Enqueue(submit(t, map[string]any{"summary": "Nothing to change", "usedLearnings": []string{}, "ops": []any{}}))
	m := newModule(db, pb, provider, runner.JobConfig{Instruction: "Summarize the top stories", BaseImage: "sandbox:latest", ModelID: "m1"})
	reflectOn(t, m, wid, runID)

	// The model saw the page and that the call failed
	requests := provider.Requests()
	require.NotEmpty(t, requests)
	prompt := requests[0].Messages[0].Text()
	require.Contains(t, prompt, "[call bash]", "the transcript lists the call")
	require.Contains(t, prompt, "-> error after 0.8s", "the failed call must not be reported as ok")
	require.Contains(t, prompt, "Story 1 title about something", "the transcript must show the output the run saw")
}
