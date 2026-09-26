//go:build unit

package runs

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2/sse"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs/runsdb"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// newModuleForTest wires the parts of the module that work without an actor host
func newModuleForTest(t *testing.T, db *database.DB, bus events.Bus) *Module {
	t.Helper()
	m := &Module{
		deps:    Dependencies{DB: db, Bus: bus, Storage: storage.NewDatabaseStorage(db)},
		db:      db,
		queries: runsdb.New(db),
	}
	m.store = &store{queries: m.queries}
	return m
}

// seedRun inserts a run with the given status and returns its ID
func seedRun(t *testing.T, db *database.DB, wid, jobID string, number int64, status string, heartbeatAt, finishedAt *int64) string {
	t.Helper()
	id := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at, heartbeat_at, finished_at)
		VALUES ($1, $2, $3, $4, $5, 'explore', 'manual', 0, $6, $7, $8)`,
		id, wid, jobID, number, status, database.Now(), heartbeatAt, finishedAt)
	return id
}

// seedEvents appends count events to a run in one statement, numbered after the ones it already has
func seedEvents(t *testing.T, db *database.DB, runID string, first, count int64) {
	t.Helper()
	testutil.Exec(t, db, `WITH RECURSIVE s(n) AS (SELECT CAST($2 AS BIGINT) UNION ALL SELECT n + 1 FROM s WHERE n < $3)
		INSERT INTO run_events (run_id, seq, ts, type, payload) SELECT $1, n, n, 'log', '{}' FROM s`,
		runID, first, first+count-1)
}

// streamedEvents splits the messages of a stream into event sequence numbers and the end marker
func streamedEvents(msgs []sse.Message) (seqs []int64, end *EndDto) {
	for _, msg := range msgs {
		switch d := msg.Data.(type) {
		case EventDto:
			seqs = append(seqs, d.Seq)
		case EndDto:
			end = &d
		}
	}
	return seqs, end
}

func requireContiguous(t *testing.T, seqs []int64, count int64) {
	t.Helper()
	require.Len(t, seqs, int(count))
	for i, seq := range seqs {
		require.Equal(t, int64(i+1), seq)
	}
}

func TestStreamOfFinishedRunSendsEveryEventBeforeEnd(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := newModuleForTest(t, db, events.NewLocalBus())

	// More than two pages of events, which the stream used to cut off at two pages
	runID := seedRun(t, db, wid, job, 1, runner.StatusSucceeded, nil, new(database.Now()))
	seedEvents(t, db, runID, 1, 2*streamPageSize+500)

	var msgs []sse.Message
	send := sse.Sender(func(msg sse.Message) error {
		msgs = append(msgs, msg)
		return nil
	})
	ctx := principal.WithPrincipal(context.Background(), principal.Principal{WorkspaceID: wid})
	m.streamRun(ctx, &streamInput{ID: runID}, send)

	seqs, end := streamedEvents(msgs)
	requireContiguous(t, seqs, 2*streamPageSize+500)
	require.NotNil(t, end)
	require.Equal(t, runner.StatusSucceeded, end.Status)
	require.Equal(t, EndDto{Status: runner.StatusSucceeded}, msgs[len(msgs)-1].Data)
}

func TestStreamThatSeesTheRunFinishSendsEveryEventBeforeEnd(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	bus := events.NewLocalBus()
	m := newModuleForTest(t, db, bus)

	runID := seedRun(t, db, wid, job, 1, runner.StatusRunning, new(database.Now()), nil)
	seedEvents(t, db, runID, 1, 1)

	// The stream runs in the background, and its first event shows it has subscribed and caught up
	var (
		mu   sync.Mutex
		msgs []sse.Message
	)
	first := make(chan struct{}, 1)
	send := sse.Sender(func(msg sse.Message) error {
		mu.Lock()
		defer mu.Unlock()
		msgs = append(msgs, msg)
		select {
		case first <- struct{}{}:
		default:
		}
		return nil
	})
	ctx, cancel := context.WithTimeout(principal.WithPrincipal(context.Background(), principal.Principal{WorkspaceID: wid}), 20*time.Second)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		m.streamRun(ctx, &streamInput{ID: runID}, send)
	}()
	<-first

	// The run records a burst of events and finishes, then the status notification wakes the stream
	seedEvents(t, db, runID, 2, 2*streamPageSize+499)
	testutil.Exec(t, db, "UPDATE runs SET status = $1, finished_at = $2 WHERE id = $3", runner.StatusSucceeded, database.Now(), runID)
	raw, err := json.Marshal(events.Message{Kind: "status", Status: runner.StatusSucceeded})
	require.NoError(t, err)
	require.NoError(t, bus.Publish(ctx, events.RunTopic(runID), raw))

	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("the stream did not end")
	}
	mu.Lock()
	defer mu.Unlock()
	seqs, end := streamedEvents(msgs)
	requireContiguous(t, seqs, 2*streamPageSize+500)
	require.NotNil(t, end)
	require.Equal(t, EndDto{Status: runner.StatusSucceeded}, msgs[len(msgs)-1].Data)
}

// statusProbeBus records the run's stored status whenever an event notification of that run is published
// A stream ends as soon as it sees a terminal status, so the final event must be persisted while the status is still live
type statusProbeBus struct {
	*events.LocalBus
	t        *testing.T
	db       *database.DB
	runID    string
	mu       sync.Mutex
	statuses []string
}

func (b *statusProbeBus) Publish(ctx context.Context, topic string, msg []byte) error {
	var m events.Message
	if json.Unmarshal(msg, &m) == nil && m.Kind == "event" && topic == events.RunTopic(b.runID) {
		var status string
		err := b.db.QueryRowContext(ctx, "SELECT status FROM runs WHERE id = $1", b.runID).Scan(&status)
		require.NoError(b.t, err)
		b.mu.Lock()
		b.statuses = append(b.statuses, status)
		b.mu.Unlock()
	}
	return b.LocalBus.Publish(ctx, topic, msg)
}

// requireFinalEvent checks the run's status and that its last event records the same final status
func requireFinalEvent(t *testing.T, db *database.DB, runID, status string) {
	t.Helper()
	var stored, typ, payload string
	err := db.QueryRowContext(context.Background(), "SELECT status FROM runs WHERE id = $1", runID).Scan(&stored)
	require.NoError(t, err)
	require.Equal(t, status, stored)
	err = db.QueryRowContext(context.Background(), "SELECT type, payload FROM run_events WHERE run_id = $1 ORDER BY seq DESC LIMIT 1", runID).Scan(&typ, &payload)
	require.NoError(t, err)
	require.Equal(t, events.TypeRunStatus, typ)
	require.Contains(t, payload, `"status":"`+status+`"`)
}

func TestCancelOfQueuedRunRecordsTheFinalEventBeforeTheStatus(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	bus := &statusProbeBus{LocalBus: events.NewLocalBus(), t: t, db: db}
	m := newModuleForTest(t, db, bus)

	runID := seedRun(t, db, wid, job, 1, runner.StatusQueued, nil, nil)
	bus.runID = runID
	require.NoError(t, m.Cancel(context.Background(), wid, runID))

	require.Equal(t, []string{runner.StatusQueued}, bus.statuses)
	requireFinalEvent(t, db, runID, runner.StatusCancelled)
}

func TestReconcilerRecordsTheFinalEventBeforeTheStatus(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	bus := &statusProbeBus{LocalBus: events.NewLocalBus(), t: t, db: db}
	m := newModuleForTest(t, db, bus)

	runID := seedRun(t, db, wid, job, 1, runner.StatusRunning, new(time.Now().Add(-5*time.Minute).UnixMilli()), nil)
	bus.runID = runID
	require.NoError(t, m.reconcile(context.Background()))

	require.Equal(t, []string{runner.StatusRunning}, bus.statuses)
	requireFinalEvent(t, db, runID, runner.StatusFailed)
}

func TestPruneCatchesUpFromItsWatermark(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := newModuleForTest(t, db, events.NewLocalBus())
	ctx := context.Background()

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	number := int64(0)
	seed := func(finished time.Time) string {
		number++
		id := seedRun(t, db, wid, job, number, runner.StatusSucceeded, nil, new(finished.UnixMilli()))
		seedEvents(t, db, id, 1, 3)
		require.NoError(t, m.deps.Storage.Save(ctx, "runs/"+id+"/artifact.txt", strings.NewReader("x")))
		return id
	}
	pruned := func(id string) bool {
		var count int64
		require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM run_events WHERE run_id = $1", id).Scan(&count))
		blobs, err := m.deps.Storage.List(ctx, "runs/"+id)
		require.NoError(t, err)
		return count == 0 && len(blobs) == 0
	}

	// The first prune has no watermark and sweeps all history once, but keeps runs after the cutoff
	old := seed(cutoff.Add(-100 * 24 * time.Hour))
	recent := seed(cutoff.Add(-time.Hour))
	kept := seed(cutoff.Add(time.Hour))
	m.pruneWorkspace(ctx, wid, cutoff)
	require.True(t, pruned(old))
	require.True(t, pruned(recent))
	require.False(t, pruned(kept))

	// Later prunes only look back a little past the watermark, so ancient rows are not revisited every night
	ancient := seed(cutoff.Add(-200 * 24 * time.Hour))
	m.pruneWorkspace(ctx, wid, cutoff)
	require.False(t, pruned(ancient))

	// A lowered retention moves the cutoff forward, and everything up to it is pruned in one pass
	m.pruneWorkspace(ctx, wid, cutoff.Add(10*24*time.Hour))
	require.True(t, pruned(kept))
}

func TestFinishStoresSummariesWithAnyBytes(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	job := testutil.SeedJob(t, db, wid, "skip")
	m := newModuleForTest(t, db, events.NewLocalBus())
	runID := seedRun(t, db, wid, job, 1, runner.StatusRunning, nil, nil)

	// A script's output can end in half a character or contain NUL bytes, which Postgres refuses in text columns
	final := runner.Final{Status: runner.StatusSucceeded, Summary: "done \xe2\x9c\x00 ok", Outputs: []byte("{\"k\":\"\xe2\x9c\"}"), Error: "\x00"}
	ok, err := m.store.Finish(context.Background(), runID, final)
	require.NoError(t, err)
	require.True(t, ok)
}
