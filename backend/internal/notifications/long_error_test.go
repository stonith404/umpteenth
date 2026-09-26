//go:build unit

package notifications

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/anthropic"
	"github.com/stonith404/umpteenth/backend/internal/notifications/notificationsdb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// discordMaxContent is the most characters Discord accepts in a webhook message's content
const discordMaxContent = 2000

// fakeTask hands a queued notification to handle like the taskpool does
type fakeTask struct{ in task }

func (f fakeTask) ID() string         { return "t1" }
func (f fakeTask) Capability() string { return "" }
func (f fakeTask) Decode(into any) error {
	b, err := json.Marshal(f.in)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

// discordHook acts like a Discord webhook, which answers 400 to content over 2000 characters
type discordHook struct {
	*httptest.Server
	mu        sync.Mutex
	delivered []Payload
	rejected  int
}

func newDiscordHook(t *testing.T) *discordHook {
	h := &discordHook{}
	h.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var msg Payload
		_ = json.NewDecoder(r.Body).Decode(&msg)
		h.mu.Lock()
		defer h.mu.Unlock()
		if utf8.RuneCountInString(msg.Content) > discordMaxContent {
			h.rejected++
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"message":"Invalid Form Body","code":50035,"errors":{"content":{"_errors":[{"code":"BASE_TYPE_MAX_LENGTH","message":"Must be 2000 or fewer in length."}]}}}`)
			return
		}
		h.delivered = append(h.delivered, msg)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(h.Close)
	return h
}

// seedFailedRun stores a failed run with the error and returns the workspace and run IDs
func seedFailedRun(t *testing.T, db *database.DB, runError string) (string, string) {
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	runID := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, finished_at, error)
		VALUES ($1, $2, $3, 7, 'failed', 'explore', 'manual', 0, $4, $4, $5)`,
		runID, wid, jobID, database.Now(), runError)
	return wid, runID
}

// A failed run's notification must reach a Discord webhook however long the run's error is, since a rejected delivery is never retried
func TestRunFailedNotificationReachesDiscordWithALongError(t *testing.T) {
	// An agent that calls ump fail with a stack trace gets it cut to maxFailure, which becomes the run's error as it is
	trace := "Traceback (most recent call last):\n" + strings.Repeat("  File \"/workspace/sync.py\", line 42, in fetch_page\n    resp = session.get(url, timeout=30)\n", 60) + "requests.exceptions.ConnectionError: HTTPSConnectionPool(host='api.example.com', port=443): Max retries exceeded"
	live := &runner.LiveRun{}
	live.Fail(trace)
	umpFailError := live.Failure()

	// An Anthropic-kind provider behind a proxy that answers with an HTML error page puts the whole page into the error
	htmlPage := "<!DOCTYPE html>\n<html lang=\"en\"><head><title>api.example.com | 502: Bad gateway</title><style>" + strings.Repeat("body{margin:0;padding:0;font-family:system-ui,sans-serif}.box{border:1px solid #ccc;padding:1rem}", 20) + "</style></head><body><h1>Bad gateway</h1><p>The web server reported a bad gateway error.</p></body></html>"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=UTF-8")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, htmlPage)
	}))
	defer upstream.Close()
	provider, err := anthropic.New(llm.Config{BaseURL: upstream.URL, APIKey: "test"})
	require.NoError(t, err)
	_, callErr := provider.Stream(t.Context(), llm.Request{Model: "claude-sonnet-4-5", Messages: []llm.Message{{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart("hi")}}}}, nil)
	require.Error(t, callErr)
	modelError := "model call failed: " + callErr.Error()

	// An error in another language is cut on a character boundary
	localized := strings.Repeat("Die Größe der Antwort überschreitet das zulässige Maß. ", 60)

	for name, runError := range map[string]string{"ump fail": umpFailError, "html error page": modelError, "localized": localized} {
		t.Run(name, func(t *testing.T) {
			db := testutil.NewDatabaseForTest(t)
			hook := newDiscordHook(t)
			webhookURL := hook.URL
			m := &Module{
				deps: Dependencies{
					DB:       db,
					Settings: fixedSettings{settings.WorkspaceSettings{NotifyWebhookURL: &webhookURL, NotifyOn: []string{settings.EventRunFailed}}},
					Egress:   egress.New(true),
					AppURL:   "https://umpteenth.example.com",
				},
				queries: notificationsdb.New(db),
			}
			wid, runID := seedFailedRun(t, db, runError)
			t.Logf("run error: %d characters, starting with %q", utf8.RuneCountInString(runError), runError[:min(len(runError), 90)])

			// handle returning nil tells the taskpool the delivery is done, so the webhook must have taken the message by then
			err := m.handle(t.Context(), fakeTask{task{WorkspaceID: wid, RunID: runID, Event: settings.EventRunFailed}})
			require.NoError(t, err)
			hook.mu.Lock()
			defer hook.mu.Unlock()
			assert.Zero(t, hook.rejected, "Discord rejected the run.failed notification with 400 and handle dropped it without a retry")
			require.Len(t, hook.delivered, 1, "the run.failed notification never reached Discord")

			// The message still names the failed run and links to it, so the full error is one click away
			content := hook.delivered[0].Content
			assert.NotContains(t, content, "\uFFFD", "the cut split a character, which the JSON encoder replaced")
			assert.Contains(t, content, "Test job run #7 failed")
			assert.True(t, strings.HasSuffix(content, "\nhttps://umpteenth.example.com/runs/"+runID+"?workspace="+wid))

			// Only the message is cut, the payload still carries the whole error
			require.NotNil(t, hook.delivered[0].Run)
			assert.Equal(t, runError, hook.delivered[0].Run.Error)
		})
	}
}

// The cut never splits a character or goes over the limit, wherever it falls
func TestFitKeepsWholeCharacters(t *testing.T) {
	s := strings.Repeat("Größe 任务失败 ", 10)
	assert.Equal(t, s, fit(s, len(s)))
	for n := len("…"); n < len(s); n++ {
		out := fit(s, n)
		assert.True(t, utf8.ValidString(out), "cut at %d bytes", n)
		assert.LessOrEqual(t, len(out), n)
		assert.True(t, strings.HasSuffix(out, "…"))
	}
}
