//go:build unit

package notifications

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/notifications/notificationsdb"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/settings"
)

func TestRunErrorCannotPingOrLinkInChat(t *testing.T) {
	var body []byte
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer hook.Close()

	url := hook.URL
	m := &Module{deps: Dependencies{
		Settings: fixedSettings{settings.WorkspaceSettings{NotifyWebhookURL: &url}},
		Egress:   egress.New(true),
		AppURL:   "https://umpteenth.example.com",
	}}

	// The sandbox writes the failure reason through ump fail, so a prompt-injected agent chooses every character of it
	reason := "@everyone @here <!channel> <!everyone> credentials expired, sign in at <https://evil.example/login|Umpteenth sign-in> & retry"
	p := m.payload(settings.EventRunFailed, "w1", notificationsdb.GetRunDetailsRow{ID: "r1", JobID: "j1", JobName: "Inbox triage", Number: 3, Status: runner.StatusFailed, Mode: "explore", Error: &reason})
	require.NoError(t, m.deliver(t.Context(), "w1", p))

	var got map[string]any
	require.NoError(t, json.Unmarshal(body, &got))

	// Discord parses every mention in content unless allowed_mentions says otherwise, so the payload must turn mention parsing off
	allowed, _ := got["allowed_mentions"].(map[string]any)
	parse, ok := allowed["parse"].([]any)
	assert.True(t, ok && len(parse) == 0, "allowed_mentions.parse must be an explicit empty list, or Discord pings @everyone and @here from the run error: %s", body)

	// Slack renders <!channel>, <!everyone> and <url|label> in text, so the run error must reach it with &, < and > escaped
	text, _ := got["text"].(string)
	assert.NotContains(t, text, "<!channel>")
	assert.NotContains(t, text, "<!everyone>")
	assert.NotContains(t, text, "<https://evil.example/login|")
	assert.Contains(t, text, "&lt;!channel&gt; &lt;!everyone&gt;")
	assert.Contains(t, text, "&amp; retry")

	// The link to the run still works in both apps
	assert.Contains(t, text, "\nhttps://umpteenth.example.com/runs/r1?workspace=w1")
	content, _ := got["content"].(string)
	assert.Contains(t, content, "\nhttps://umpteenth.example.com/runs/r1?workspace=w1")

	// Receivers that read the structured fields still get the reason as the run recorded it
	assert.Equal(t, reason, p.Run.Error)
}
