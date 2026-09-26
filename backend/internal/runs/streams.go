package runs

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/sse"

	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
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
}

type streamInput struct {
	ID          string `path:"id"`
	LastEventID string `header:"Last-Event-ID"`
	After       int64  `query:"after" doc:"Resume after this event sequence, for clients that cannot send Last-Event-ID"`
}

const (
	streamPoll      = 2 * time.Second
	streamKeepalive = 20 * time.Second
	// streamPageSize is how many persisted events one database read loads while catching a stream up
	streamPageSize = 1000
)

func (m *Module) registerStreams(api huma.API, auth huma.Middlewares) {
	op := httpserver.Operation("stream-run", http.MethodGet, "/api/runs/{id}/stream", "Runs")
	op.Middlewares = auth
	sse.Register(api, op, map[string]any{"event": EventDto{}, "delta": DeltaDto{}, "status": StatusDto{}, "end": EndDto{}}, m.streamRun)

	wsOp := httpserver.Operation("stream-workspace-events", http.MethodGet, "/api/events", "Runs")
	wsOp.Middlewares = auth
	sse.Register(api, wsOp, map[string]any{"run": WorkspaceEventDto{}}, m.streamWorkspace)
}

// streamRun replays persisted events after the client's last one, then follows the run live
// The bus only nudges this loop; the database is the source of truth, so a client on any replica sees every event exactly once
func (m *Module) streamRun(ctx context.Context, in *streamInput, send sse.Sender) {
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
			if flush() {
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
				if json.Unmarshal(msg.Delta, &d) == nil && send.Data(d) != nil {
					return
				}
			case "status":
				if send.Data(StatusDto{Status: msg.Status}) != nil {
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
			if send.Comment("keepalive") != nil {
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
			if json.Unmarshal(raw, &ev) == nil && send.Data(ev) != nil {
				return
			}
		case <-keepalive.C:
			if send.Comment("keepalive") != nil {
				return
			}
		}
	}
}
