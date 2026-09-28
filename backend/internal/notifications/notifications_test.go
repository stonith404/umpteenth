//go:build unit

package notifications

import (
	"context"
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

func TestEventsFor(t *testing.T) {
	all := []string{settings.EventRunFailed, settings.EventRunFellBack, settings.EventJobDemoted}
	yes := func() bool { return true }
	no := func() bool { return false }

	assert.Equal(t, []string{settings.EventRunFailed}, eventsFor(all, runner.Final{Status: runner.StatusFailed}, no))
	assert.Equal(t, []string{settings.EventRunFailed}, eventsFor(all, runner.Final{Status: runner.StatusTimedOut}, no))
	assert.Empty(t, eventsFor(all, runner.Final{Status: runner.StatusSucceeded}, yes))
	assert.Empty(t, eventsFor(all, runner.Final{Status: runner.StatusCancelled}, yes))

	// A fallback that finished the job is worth a note, and the second in a row demotes the job
	assert.Equal(t, []string{settings.EventRunFellBack}, eventsFor(all, runner.Final{Status: runner.StatusSucceeded, FellBack: true}, no))
	assert.Equal(t, []string{settings.EventRunFellBack, settings.EventJobDemoted}, eventsFor(all, runner.Final{Status: runner.StatusSucceeded, FellBack: true}, yes))

	// Only the events the workspace chose are sent
	assert.Equal(t, []string{settings.EventJobDemoted}, eventsFor([]string{settings.EventJobDemoted}, runner.Final{Status: runner.StatusSucceeded, FellBack: true}, yes))
}

type fixedSettings struct{ ws settings.WorkspaceSettings }

func (f fixedSettings) Get(context.Context, string) (settings.WorkspaceSettings, error) {
	return f.ws, nil
}

type fixedSecrets map[string]string

func (f fixedSecrets) Resolve(_ context.Context, _, name string) (string, error) {
	return f[name], nil
}

func TestDeliverySignsAndClassifiesAnswers(t *testing.T) {
	var got struct {
		body      []byte
		signature string
		event     string
	}
	status := http.StatusOK
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.body, _ = io.ReadAll(r.Body)
		got.signature = r.Header.Get("X-Umpteenth-Signature")
		got.event = r.Header.Get("X-Umpteenth-Event")
		w.WriteHeader(status)
	}))
	defer hook.Close()

	url := hook.URL
	secret := "SIGNING"
	m := &Module{deps: Dependencies{
		Settings: fixedSettings{settings.WorkspaceSettings{NotifyWebhookURL: &url, NotifySecret: &secret}},
		Secrets:  fixedSecrets{"SIGNING": "s3cret"},
		Egress:   egress.New(true),
		AppURL:   "https://umpteenth.example.com/",
	}}
	errText := "the run exceeded its time limit"
	p := m.payload(settings.EventRunFailed, "w1", notificationsdb.GetRunDetailsRow{ID: "r1", JobID: "j1", JobName: "Nightly report", Number: 12, Status: runner.StatusTimedOut, Mode: "scripted", Error: &errText})

	// The message reads well in Slack and Discord, and links to the run
	assert.Equal(t, "Umpteenth: Nightly report run #12 timed out: the run exceeded its time limit\nhttps://umpteenth.example.com/runs/r1?workspace=w1", p.Text)
	assert.Equal(t, p.Text, p.Content)

	// The receiver can check the signature over the exact body
	require.NoError(t, m.deliver(t.Context(), "w1", p))
	assert.Equal(t, settings.EventRunFailed, got.event)
	assert.Equal(t, sign("s3cret", got.body), got.signature)
	var decoded Payload
	require.NoError(t, json.Unmarshal(got.body, &decoded))
	assert.Equal(t, "https://umpteenth.example.com/runs/r1?workspace=w1", decoded.Run.URL)

	// Server errors are retried, a rejected message is not
	status = http.StatusBadGateway
	err := m.deliver(t.Context(), "w1", p)
	require.Error(t, err)
	assert.NotErrorIs(t, err, errPermanent)
	status = http.StatusBadRequest
	assert.ErrorIs(t, m.deliver(t.Context(), "w1", p), errPermanent)
}

func TestDeliveryDoesNotFollowRedirects(t *testing.T) {
	var moved []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			moved = append(moved, r.Method)
			return
		}
		http.Redirect(w, r, "/moved", http.StatusFound)
	}))
	defer hook.Close()
	url := hook.URL + "/hook"
	m := &Module{deps: Dependencies{Settings: fixedSettings{settings.WorkspaceSettings{NotifyWebhookURL: &url}}, Egress: egress.New(true)}}

	// Following the redirect would deliver an empty GET and count it as delivered
	err := m.deliver(t.Context(), "w1", Payload{Event: "test"})
	require.ErrorIs(t, err, errPermanent)
	assert.Contains(t, err.Error(), "302 Found")
	assert.Empty(t, moved)
}

func TestFailedDeliveryLeavesTheURLOut(t *testing.T) {
	// Webhook URLs such as Slack's carry their credential in the path, and failed deliveries are logged
	hook := httptest.NewServer(http.NotFoundHandler())
	url := hook.URL + "/services/T000/B000/SECRET"
	hook.Close()
	m := &Module{deps: Dependencies{Settings: fixedSettings{settings.WorkspaceSettings{NotifyWebhookURL: &url}}, Egress: egress.New(true)}}

	err := m.deliver(t.Context(), "w1", Payload{Event: "test"})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "SECRET")
}
