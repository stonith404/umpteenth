//go:build unit

package broker

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/agent"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/mcp"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/sandbox"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// brokerHarness serves the broker with one live run that records its timeline in a test database
type brokerHarness struct {
	server *httptest.Server
	db     *database.DB
	runID  string
	live   *runner.LiveRun
}

func newBrokerHarness(t *testing.T, state runner.JobState) *brokerHarness {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	runID := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'running', 'scripted', 'manual', 0, $4)`,
		runID, wid, jobID, database.Now())

	live := &runner.LiveRun{
		Run:      runner.Run{ID: runID},
		Recorder: events.NewRecorder(db, events.NewLocalBus(), storage.NewDatabaseStorage(db), runID),
		State:    state,
	}
	registry := runner.NewRegistry()
	registry.Register(runID, live)
	server := httptest.NewServer(New(Dependencies{Runs: tokenRuns{crypto.HashToken("ump-token"): runID}, Live: registry}).Handler())
	t.Cleanup(server.Close)
	return &brokerHarness{server: server, db: db, runID: runID, live: live}
}

// send makes a request the way ump does and returns the status and body
func (h *brokerHarness) send(method, path, body string) (int, string, error) {
	req, err := http.NewRequest(method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer ump-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), err
}

func (h *brokerHarness) call(t *testing.T, method, path, body string) (int, string) {
	status, out, err := h.send(method, path, body)
	require.NoError(t, err)
	return status, out
}

// timeline returns the type and payload of each event the run recorded, in order
func (h *brokerHarness) timeline(t *testing.T) [][2]string {
	rows, err := h.db.QueryContext(context.Background(), "SELECT type, payload FROM run_events WHERE run_id = $1 ORDER BY seq", h.runID)
	require.NoError(t, err)
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var e [2]string
		require.NoError(t, rows.Scan(&e[0], &e[1]))
		out = append(out, e)
	}
	require.NoError(t, rows.Err())
	return out
}

// blockingTool is an MCP tool whose calls last until the test lets them finish
type blockingTool struct {
	started chan struct{}
	finish  chan struct{}
}

func (t *blockingTool) Def() llm.ToolDef { return llm.ToolDef{Name: mcp.ToolName("slow", "wait")} }
func (t *blockingTool) ReadOnly() bool   { return true }
func (t *blockingTool) Run(ctx context.Context, _ llm.ToolCall) agent.Result {
	t.started <- struct{}{}
	select {
	case <-t.finish:
	case <-ctx.Done():
	}
	return agent.Result{Content: "done"}
}

func TestBrokerCallsStopFillingTheTimelineAtTheLimit(t *testing.T) {
	h := newProxyHarness(t, sandbox.NetworkAllowlist, []string{"api.example.com"})
	api := func() {
		req, err := http.NewRequest(http.MethodGet, h.server.URL+"/v1/mcp/tools", nil)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer secret-token")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, "calls past the limit are served, just not recorded")
	}
	refused := func() {
		resp, err := h.client("secret-token").Get("http://evil.example.org/")
		require.NoError(t, err)
		_ = resp.Body.Close()
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	}

	// API calls and proxy refusals share one limit per run
	for range runner.MaxBrokerEvents - 1 {
		api()
	}
	refused()
	refused()
	for range 20 {
		api()
	}

	// The timeline keeps the first calls and notes once that the rest are left out
	recorded := h.proxyEvents(t)
	require.Len(t, recorded, runner.MaxBrokerEvents)
	assert.Contains(t, recorded[0], `"GET /v1/mcp/tools"`)
	assert.Contains(t, recorded[len(recorded)-1], "not on this job's allow-list")
	var notes []string
	rows, err := h.db.QueryContext(context.Background(), "SELECT payload FROM run_events WHERE run_id = $1 AND type = 'log'", h.runID)
	require.NoError(t, err)
	defer rows.Close()
	for rows.Next() {
		var p string
		require.NoError(t, rows.Scan(&p))
		notes = append(notes, p)
	}
	require.NoError(t, rows.Err())
	require.Len(t, notes, 1)
	assert.Contains(t, notes[0], "the timeline leaves out later ones")
}

func TestCallsThatMayChangeSomethingStayOnTheTimeline(t *testing.T) {
	h := newBrokerHarness(t, memoryState{})
	h.live.SetTools([]agent.Tool{
		&mcpTool{name: mcp.ToolName("github", "search"), readOnly: true},
		&mcpTool{name: mcp.ToolName("slack", "post_message")},
	})

	// Cheap reads use up the run's limit of broker calls
	for range runner.MaxBrokerEvents {
		status, _ := h.call(t, http.MethodGet, "/v1/state/item", "")
		require.Equal(t, http.StatusOK, status)
	}

	// Past the limit, a tool call that may change something and the run's first state write are still recorded, which reflection relies on to know the run changed the job's state
	h.call(t, http.MethodPost, "/v1/mcp/call", `{"server":"github","tool":"search","arguments":{}}`)
	h.call(t, http.MethodPost, "/v1/mcp/call", `{"server":"slack","tool":"post_message","arguments":{"text":"hi"}}`)
	h.call(t, http.MethodGet, "/v1/state/item", "")
	status, body := h.call(t, http.MethodPut, "/v1/state/seen", `{"value":"42"}`)
	require.Equal(t, http.StatusOK, status, body)
	h.call(t, http.MethodPut, "/v1/state/seen", `{"value":"43"}`)

	recorded := h.timeline(t)
	require.Equal(t, runner.MaxBrokerEvents+3, len(recorded))
	past := recorded[runner.MaxBrokerEvents:]
	assert.Equal(t, events.TypeLog, past[0][0])
	assert.Contains(t, past[0][1], "apart from MCP calls that may change something, paid ump llm calls and the first state write")
	assert.Equal(t, events.TypeBrokerCall, past[1][0])
	assert.Contains(t, past[1][1], `"tool":"post_message"`)
	assert.Equal(t, events.TypeBrokerCall, past[2][0])
	assert.Contains(t, past[2][1], `"endpoint":"PUT /v1/state/seen"`)
}

func TestBrokerRequestsOfARunTakeTurns(t *testing.T) {
	h := newBrokerHarness(t, memoryState{})
	slow := &blockingTool{started: make(chan struct{}, 100), finish: make(chan struct{})}
	h.live.SetTools([]agent.Tool{slow})

	// The slow calls finish before the server closes, which waits for them, even when the test fails early
	finish := sync.OnceFunc(func() { close(slow.finish) })
	t.Cleanup(finish)
	var wg sync.WaitGroup
	statuses := make(chan int, 100)
	send := func(method, path, body string) {
		wg.Go(func() {
			status, _, err := h.send(method, path, body)
			assert.NoError(t, err)
			statuses <- status
		})
	}

	// Slow calls take up the requests a run may have in flight, until the next one no longer starts
	inFlight := 0
	for waiting := false; !waiting; {
		send(http.MethodPost, "/v1/mcp/call", `{"server":"slow","tool":"wait","arguments":{}}`)
		select {
		case <-slow.started:
			inFlight++
			require.Less(t, inFlight, 50, "the requests a run has in flight are bounded")
		case <-time.After(200 * time.Millisecond):
			waiting = true
		}
	}

	// Any other request of the run waits as well, and all of them go ahead once the slow calls finish
	send(http.MethodGet, "/v1/state/item", "")
	select {
	case <-statuses:
		t.Fatal("a request went ahead while the run had too many in flight")
	case <-time.After(100 * time.Millisecond):
	}
	finish()
	wg.Wait()
	close(statuses)
	answered := 0
	for status := range statuses {
		assert.Equal(t, http.StatusOK, status)
		answered++
	}
	assert.Equal(t, inFlight+2, answered)
}

func TestBrokerResponsesAreNotHTMLEscaped(t *testing.T) {
	h := newBrokerHarness(t, memoryState{})

	// Markup a sandbox stored comes back byte for byte, since escaping each <, > and & to six bytes would multiply the size of a response
	h.call(t, http.MethodPut, "/v1/state/page", `{"value":"<p>a & b</p>"}`)
	_, body := h.call(t, http.MethodGet, "/v1/state/page", "")
	assert.Contains(t, body, `"value":"<p>a & b</p>"`)
}

func TestStateListingIsTheWholeStateAsOneObject(t *testing.T) {
	state := memoryState{"page": "<p>a & b</p>", "bell": "\u0001\u0007", "lines": "one\ntwo\n", "ü": "ö", "empty": ""}
	h := newBrokerHarness(t, state)

	// The entries are written one at a time, yet the response is the object that encoding the whole state at once gives
	status, body := h.call(t, http.MethodGet, "/v1/state", "")
	require.Equal(t, http.StatusOK, status, body)
	var want bytes.Buffer
	require.NoError(t, newEncoder(&want).Encode(map[string]string(state)))
	assert.Equal(t, want.String(), body)

	// An empty state is an empty object
	h = newBrokerHarness(t, memoryState{})
	_, body = h.call(t, http.MethodGet, "/v1/state", "")
	assert.Equal(t, "{}\n", body)
}

func TestPaidLLMCallsStayOnTheTimeline(t *testing.T) {
	h := newBrokerHarness(t, memoryState{})
	p := fake.New()
	h.live.Utility, h.live.UtilModel = p, runner.Model{Name: "fake-model", Price: llm.Price{In: 1_000_000, Out: 1_000_000}}

	// Cheap reads use up the run's limit of broker calls
	for range runner.MaxBrokerEvents {
		status, _ := h.call(t, http.MethodGet, "/v1/state/item", "")
		require.Equal(t, http.StatusOK, status)
	}

	// Past the limit, a call the provider turned away stays off the timeline, while a paid one is recorded so the run page's live cost keeps adding up
	p.Enqueue(fake.ScriptedResponse{Error: "rate limited", ErrorStatus: http.StatusTooManyRequests})
	status, _ := h.call(t, http.MethodPost, "/v1/llm", `{"prompt":"hi"}`)
	require.Equal(t, http.StatusBadGateway, status)
	p.Enqueue(fake.ScriptedResponse{Text: "ok", Usage: llm.Usage{Input: 10, Output: 5}})
	status, body := h.call(t, http.MethodPost, "/v1/llm", `{"prompt":"hi"}`)
	require.Equal(t, http.StatusOK, status, body)

	recorded := h.timeline(t)
	require.Equal(t, runner.MaxBrokerEvents+2, len(recorded))
	past := recorded[runner.MaxBrokerEvents:]
	assert.Equal(t, events.TypeLog, past[0][0])
	assert.Equal(t, events.TypeBrokerCall, past[1][0])
	assert.Contains(t, past[1][1], `"endpoint":"POST /v1/llm"`)
	assert.Contains(t, past[1][1], `"cost":15`)
}
