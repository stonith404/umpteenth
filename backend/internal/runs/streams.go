package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/middleware"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs/runsdb"
)

// DeltaDto is a live, unpersisted fragment such as streamed tokens or command output
type DeltaDto struct {
	Type   string `json:"type"`
	Text   string `json:"text,omitempty"`
	CallID string `json:"callId,omitempty"`
}

// StatusDto announces a run status change
type StatusDto struct {
	Status string `json:"status"`
}

// EndDto tells the client the run is final and all its events were sent
type EndDto struct {
	Status string `json:"status"`
}

// WorkspaceEventDto is a run change in the workspace, for live tables and the dashboard
type WorkspaceEventDto struct {
	Kind   string `json:"kind" enum:"run,reflection"`
	RunID  string `json:"runId,omitempty"`
	JobID  string `json:"jobId,omitempty"`
	Status string `json:"status,omitempty"`
	// Deleted marks a run that was deleted, so tables drop it instead of refetching it
	Deleted bool `json:"deleted,omitempty"`
}

type streamInput struct {
	ID          string `path:"id"`
	LastEventID string `header:"Last-Event-ID"`
	After       int64  `query:"after" doc:"Resume after this event sequence, for clients that cannot send Last-Event-ID"`
}

const (
	streamPoll      = 2 * time.Second
	streamKeepalive = 20 * time.Second
	streamAuthCheck = 10 * time.Second
	// streamPageSize is how many persisted events one database read loads while catching a stream up
	streamPageSize = 1000
)

type streamAuthorizationCheck func(context.Context) error

// streamAuthorizationContext cancels streams shortly after their credential is revoked or expires, and as soon as the server starts shutting down
func streamAuthorizationContext(ctx context.Context) (context.Context, context.CancelFunc) {
	streamCtx, cancel := httpserver.StreamContext(ctx)
	ticker := time.NewTicker(streamAuthCheck)
	go func() {
		defer ticker.Stop()
		watchStreamAuthorization(streamCtx, cancel, ticker.C, middleware.RevalidateCredential)
	}()
	return streamCtx, cancel
}

func watchStreamAuthorization(ctx context.Context, cancel context.CancelFunc, checks <-chan time.Time, check streamAuthorizationCheck) {
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-checks:
			if !ok {
				return
			}
			if check(ctx) != nil {
				cancel()
				return
			}
		}
	}
}

func (m *Module) registerStreams(api huma.API, auth huma.Middlewares) {
	op := httpserver.Operation("stream-run", http.MethodGet, "/api/runs/{id}/stream", "Runs")
	op.Middlewares = append(slices.Clone(auth), m.requireRun)
	op.Errors = []int{http.StatusNotFound}
	sse.Register(api, op, map[string]any{"event": EventDto{}, "delta": DeltaDto{}, "status": StatusDto{}, "end": EndDto{}}, m.streamRun)

	wsOp := httpserver.Operation("stream-workspace-events", http.MethodGet, "/api/events", "Runs")
	wsOp.Middlewares = auth
	sse.Register(api, wsOp, map[string]any{"run": WorkspaceEventDto{}}, m.streamWorkspace)
}

// requireRun answers 404 before the stream starts, since a stream handler can't choose its status and EventSource reconnects forever on an empty 200
func (m *Module) requireRun(ctx huma.Context, next func(huma.Context)) {
	_, err := m.getRun(ctx.Context(), principal.WorkspaceID(ctx.Context()), ctx.Param("id"))
	if err != nil {
		httpserver.WriteError(ctx, err)
		return
	}
	next(ctx)
}

// streamRun replays persisted events after the client's last one, then follows the run live
// The bus only nudges this loop; the database is the source of truth, so a client on any replica sees every event exactly once
func (m *Module) streamRun(ctx context.Context, in *streamInput, send sse.Sender) {
	ctx, cancel := streamAuthorizationContext(ctx)
	defer cancel()

	wid := principal.WorkspaceID(ctx)
	run, err := m.queries.GetRun(ctx, runsdb.GetRunParams{WorkspaceID: wid, ID: in.ID})
	if err != nil {
		return
	}

	lastSeq := in.After
	if id, err := strconv.ParseInt(in.LastEventID, 10, 64); err == nil && id > lastSeq {
		lastSeq = id
	}

	// Subscribe before replaying, so nothing published during the replay is missed
	msgs, unsubscribe := m.deps.Bus.Subscribe(events.RunTopic(in.ID))
	defer unsubscribe()

	// flush sends every persisted event after lastSeq page by page, so callers can rely on the client being fully caught up
	flush := func() bool {
		for {
			evs, err := m.loadEvents(ctx, in.ID, lastSeq, streamPageSize)
			if err != nil {
				return false
			}
			for _, e := range evs {
				if ctx.Err() != nil {
					return false
				}
				if send(sse.Message{ID: int(e.Seq), Data: e}) != nil {
					return false
				}
				lastSeq = e.Seq
			}

			// A short page means nothing newer was persisted yet
			if len(evs) < streamPageSize {
				return true
			}
		}
	}
	if !flush() {
		return
	}

	status := run.Status
	poll := time.NewTicker(streamPoll)
	defer poll.Stop()
	keepalive := time.NewTicker(streamKeepalive)
	defer keepalive.Stop()

	for {
		// Terminal runs end the stream once every event was delivered
		if runner.IsTerminal(status) {
			if flush() && ctx.Err() == nil {
				_ = send.Data(EndDto{Status: status})
			}
			return
		}

		select {
		case <-ctx.Done():
			return
		case raw := <-msgs:
			var msg events.Message
			if json.Unmarshal(raw, &msg) != nil {
				continue
			}
			switch msg.Kind {
			case "event":
				if msg.Seq > lastSeq && !flush() {
					return
				}
			case "delta":
				var d DeltaDto
				if json.Unmarshal(msg.Delta, &d) == nil {
					if ctx.Err() != nil || send.Data(d) != nil {
						return
					}
				}
			case "status":
				if ctx.Err() != nil || send.Data(StatusDto{Status: msg.Status}) != nil {
					return
				}
				status = m.currentStatus(ctx, wid, in.ID, status)
			}
		case <-poll.C:
			// Polling covers dropped notifications and runs finished by another replica
			if !flush() {
				return
			}
			status = m.currentStatus(ctx, wid, in.ID, status)
		case <-keepalive.C:
			if ctx.Err() != nil || send.Comment("keepalive") != nil {
				return
			}
		}
	}
}

func (m *Module) currentStatus(ctx context.Context, wid, runID, fallback string) string {
	run, err := m.queries.GetRun(ctx, runsdb.GetRunParams{WorkspaceID: wid, ID: runID})
	if err != nil {
		return fallback
	}
	return run.Status
}

// streamWorkspace forwards run changes in the caller's workspace
func (m *Module) streamWorkspace(ctx context.Context, _ *struct{}, send sse.Sender) {
	ctx, cancel := streamAuthorizationContext(ctx)
	defer cancel()

	msgs, unsubscribe := m.deps.Bus.Subscribe(events.WorkspaceTopic(principal.WorkspaceID(ctx)))
	defer unsubscribe()

	keepalive := time.NewTicker(streamKeepalive)
	defer keepalive.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case raw := <-msgs:
			var ev WorkspaceEventDto
			if json.Unmarshal(raw, &ev) == nil {
				if ctx.Err() != nil || send.Data(ev) != nil {
					return
				}
			}
		case <-keepalive.C:
			if ctx.Err() != nil || send.Comment("keepalive") != nil {
				return
			}
		}
	}
}
