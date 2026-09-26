//go:build unit

package notifications

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/egress"
	"github.com/stonith404/umpteenth/backend/internal/jobs"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// runQueue inserts the run rows the jobs module creates and remembers each run's mode, standing in for the runs module
type runQueue struct {
	db    *database.DB
	mu    sync.Mutex
	modes map[string]string
}

func (q *runQueue) Create(ctx context.Context, n runs.NewRun) (string, error) {
	id := database.NewID()
	_, err := q.db.ExecContext(ctx, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		id, n.WorkspaceID, n.JobID, n.Number, n.Status, n.Mode, n.Trigger, n.PlaybookVersion, database.Now())
	q.mu.Lock()
	defer q.mu.Unlock()
	q.modes[id] = n.Mode
	return id, err
}

func (q *runQueue) Submit(context.Context, string) error { return nil }

func (q *runQueue) Cancel(context.Context, string, string) error { return nil }

func (q *runQueue) mode(runID string) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.modes[runID]
}

// webhookSettings is the workspace's real settings with a webhook configured, so the default events apply
type webhookSettings struct {
	*settings.Module
	url string
}

func (s webhookSettings) Get(ctx context.Context, workspaceID string) (settings.WorkspaceSettings, error) {
	ws, err := s.Module.Get(ctx, workspaceID)
	ws.NotifyWebhookURL = &s.url
	return ws, err
}

func TestJobDemotedIsOnlySentWhenTheJobTurnsAssisted(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewDatabaseForTest(t)

	// The webhook records every event it receives
	var (
		mu       sync.Mutex
		received []string
	)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received = append(received, r.Header.Get("X-Umpteenth-Event"))
		mu.Unlock()
	}))
	defer hook.Close()
	events := func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), received...)
	}

	// The jobs module decides modes and demotion as in production, and the notifications module asks it
	queue := &runQueue{db: db, modes: map[string]string{}}
	pb := playbook.New(playbook.Dependencies{DB: db})
	ws := settings.New(settings.Dependencies{DB: db, Defaults: settings.Defaults{Image: "img", RetentionDays: 90}})
	var (
		jm *jobs.Module
		nm *Module
	)
	testutil.NewActorHostForTest(t, func(t *testing.T, h *local.Host) {
		var err error
		jm, err = jobs.New(jobs.Dependencies{DB: db, Actors: h, Runs: queue, Playbooks: pb, Settings: ws})
		require.NoError(t, err)
		nm, err = New(Dependencies{DB: db, Actors: h, Settings: webhookSettings{ws, hook.URL}, Demotion: jm, Egress: egress.New(true), AppURL: "https://umpteenth.example.com"})
		require.NoError(t, err)
	})

	// A graduated job with a main script whose runs all fall back to the agent
	graduatedJob := func(wid string) string {
		t.Helper()
		jobID := testutil.SeedJob(t, db, wid, jobs.ConcurrencySkip)
		main := "#!/bin/sh\nexit 1\n"
		_, err := pb.Save(ctx, wid, jobID, playbook.Content{Main: &main}, playbook.VersionMeta{Author: playbook.AuthorReflection, Graduated: true})
		require.NoError(t, err)
		return jobID
	}

	// Each run starts in the mode the job picks, and finishes like the runner records a scripted run that fell back
	fallBack := func(wid, jobID string) (mode string) {
		t.Helper()
		res, err := jm.Trigger(ctx, wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerManual})
		require.NoError(t, err)
		require.Equal(t, runner.StatusQueued, res.Status)
		mode = queue.mode(res.RunID)
		final := runner.Final{Status: runner.StatusSucceeded, FellBack: mode == runner.ModeScripted}
		testutil.Exec(t, db, "UPDATE runs SET status = $1, fell_back = $2, finished_at = $3 WHERE id = $4", final.Status, final.FellBack, database.Now(), res.RunID)
		run := runner.Run{ID: res.RunID, WorkspaceID: wid, JobID: jobID, Mode: mode}
		jm.RunFinished(ctx, run, final)
		nm.RunFinished(ctx, run, final)
		return mode
	}
	waitFor := func(want []string) {
		t.Helper()
		assert.EventuallyWithT(t, func(c *assert.CollectT) {
			assert.ElementsMatch(c, want, events())
		}, 10*time.Second, 50*time.Millisecond)
		// Give a delivery that shouldn't happen the time to arrive
		time.Sleep(500 * time.Millisecond)
		assert.ElementsMatch(t, want, events())
	}

	// A job that isn't pinned turns Assisted after the second fallback in a row, and the webhook hears about it once
	wid := testutil.SeedWorkspace(t, db)
	jobID := graduatedJob(wid)
	assert.Equal(t, runner.ModeScripted, fallBack(wid, jobID))
	assert.Equal(t, runner.ModeScripted, fallBack(wid, jobID))
	assert.Equal(t, runner.ModeAssisted, fallBack(wid, jobID))
	waitFor([]string{settings.EventJobDemoted})

	// A job with graduation turned off never runs its main script, so it is never demoted and nothing claims it was
	mu.Lock()
	received = nil
	mu.Unlock()
	wid = testutil.SeedWorkspace(t, db)
	jobID = graduatedJob(wid)
	testutil.Exec(t, db, "UPDATE jobs SET graduate = FALSE WHERE id = $1", jobID)
	for range 4 {
		assert.Equal(t, runner.ModeAssisted, fallBack(wid, jobID))
	}
	waitFor(nil)
}
