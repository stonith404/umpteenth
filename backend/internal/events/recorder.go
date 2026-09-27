package events

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/storage"
)

// Event types
const (
	TypeRunStatus      = "run.status"
	TypeImageWait      = "sandbox.image_wait"
	TypeSandboxCreate  = "sandbox.create"
	TypeSandboxSetup   = "sandbox.setup"
	TypeSandboxDestroy = "sandbox.destroy"
	TypeLLMCall        = "llm.call"
	TypeCompaction     = "agent.compaction"
	TypeToolCall       = "tool.call"
	TypeToolResult     = "tool.result"
	TypeMCPCall        = "mcp.call"
	TypeBrokerCall     = "broker.call"
	TypeScriptStep     = "script.step"
	TypeVerifyResult   = "verify.result"
	TypeFallback       = "fallback"
	TypeFinish         = "finish"
	TypeError          = "error"
	TypeLog            = "log"
)

// maxPayloadBytes caps what is stored inline; larger payloads are shortened to fit and kept whole in FileStorage
const maxPayloadBytes = 16 * 1024

// Event is one timeline entry
type Event struct {
	Type   string
	SpanID string
	// Ms is the duration of the span that ends with this event
	Ms      *int64
	Payload any
}

// Message is what the bus carries on a run topic
type Message struct {
	Kind   string `json:"kind"`
	Seq    int64  `json:"seq,omitempty"`
	Status string `json:"status,omitempty"`
	// Delta carries a live fragment such as streamed tokens or tool output, which is not persisted
	Delta json.RawMessage `json:"delta,omitempty"`
}

// Recorder persists the events of one run and notifies live subscribers
// Sequence numbers are assigned by the database, so events emitted from any replica interleave safely
// A nil Recorder discards events, for sandbox work that belongs to no run, such as a shadow run of a proposed main script
type Recorder struct {
	db      *database.DB
	bus     Bus
	storage storage.FileStorage
	runID   string

	// mu serializes this recorder's inserts, since concurrent ones read the same next sequence number on Postgres and most of them would be lost after their retries
	mu sync.Mutex
}

func NewRecorder(db *database.DB, bus Bus, fileStorage storage.FileStorage, runID string) *Recorder {
	return &Recorder{db: db, bus: bus, storage: fileStorage, runID: runID}
}

// Emit persists an event and notifies subscribers, returning its sequence number
// Failures are logged rather than returned, because losing a timeline entry must never fail a run
func (r *Recorder) Emit(ctx context.Context, e Event) int64 {
	if r == nil {
		return 0
	}

	// A canceled run still records how it ended, such as the interrupted tool's result, so only a short timeout bounds the write
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	payload, err := json.Marshal(e.Payload)
	if err != nil {
		payload, _ = json.Marshal(map[string]string{"error": err.Error()})
	}

	// Oversized payloads are stored whole as a blob and shortened inline
	// They keep their shape, since the timeline and reflection read the status, error flag, call ID and cost of an event whose text was cut
	if len(payload) > maxPayloadBytes {
		key := fmt.Sprintf("runs/%s/events/%s.json", r.runID, database.NewID())
		if saveErr := r.storage.Save(ctx, key, bytes.NewReader(payload)); saveErr != nil {
			key = ""
		}
		payload = shortenPayload(payload, key)
	}

	var seq int64
	r.mu.Lock()
	defer r.mu.Unlock()
	for range 5 {
		err = r.db.QueryRowContext(ctx,
			`INSERT INTO run_events (run_id, seq, ts, type, span_id, ms, payload)
			 VALUES ($1, (SELECT COALESCE(MAX(seq), 0) + 1 FROM run_events WHERE run_id = $1), $2, $3, $4, $5, $6)
			 RETURNING seq`,
			r.runID, database.Now(), e.Type, nullIfEmpty(e.SpanID), e.Ms, string(payload),
		).Scan(&seq)
		if err == nil || !database.IsUniqueViolation(err) {
			break
		}
	}
	if err != nil {
		slog.WarnContext(ctx, "Failed to record run event", slog.String("run", r.runID), slog.String("type", e.Type), slog.Any("error", err))
		return 0
	}

	r.publish(ctx, Message{Kind: "event", Seq: seq})
	return seq
}

// Delta publishes a live fragment without persisting it
func (r *Recorder) Delta(ctx context.Context, delta any) {
	if r == nil {
		return
	}
	raw, err := json.Marshal(delta)
	if err != nil || len(raw) > maxNotifyPayload-200 {
		return
	}
	r.publish(ctx, Message{Kind: "delta", Delta: raw})
}

func (r *Recorder) publish(ctx context.Context, msg Message) {
	raw, _ := json.Marshal(msg)
	err := r.bus.Publish(ctx, RunTopic(r.runID), raw)
	if err != nil {
		slog.DebugContext(ctx, "Failed to publish run notification", slog.Any("error", err))
	}
}

// Span measures one timed step of a run
type Span struct {
	ID    string
	start time.Time
}

// StartSpan begins a timed step
func StartSpan() *Span {
	return &Span{ID: database.NewID(), start: time.Now()}
}

// Elapsed returns the span duration in milliseconds, for the event that ends it
func (s *Span) Elapsed() *int64 {
	ms := time.Since(s.start).Milliseconds()
	return &ms
}

// PublishWorkspace notifies live tables and dashboards of a run change
func PublishWorkspace(ctx context.Context, bus Bus, workspaceID string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = bus.Publish(ctx, WorkspaceTopic(workspaceID), raw)
}

func nullIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
