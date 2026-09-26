// Package events records run events and fans live notifications out to SSE clients on every replica (PLAN.md §3.4)
package events

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Bus carries small live notifications between replicas
// It is never the source of truth: persisted events are, and subscribers reconcile from the database
type Bus interface {
	Publish(ctx context.Context, topic string, msg []byte) error
	// Subscribe returns a channel of messages for the topic; slow subscribers drop messages rather than block publishers
	Subscribe(topic string) (<-chan []byte, func())
	// Run keeps the bus connected until ctx is canceled
	Run(ctx context.Context) error
}

// RunTopic is the topic of one run's timeline
func RunTopic(runID string) string { return "run:" + runID }

// WorkspaceTopic is the topic of run status changes in a workspace
func WorkspaceTopic(workspaceID string) string { return "ws:" + workspaceID }

// hub is the in-process fan-out shared by both bus implementations
type hub struct {
	mu   sync.RWMutex
	subs map[string]map[chan []byte]struct{}
}

func newHub() *hub {
	return &hub{subs: map[string]map[chan []byte]struct{}{}}
}

func (h *hub) subscribe(topic string) (<-chan []byte, func()) {
	ch := make(chan []byte, 256)
	h.mu.Lock()
	if h.subs[topic] == nil {
		h.subs[topic] = map[chan []byte]struct{}{}
	}
	h.subs[topic][ch] = struct{}{}
	h.mu.Unlock()

	var once sync.Once
	return ch, func() {
		once.Do(func() {
			h.mu.Lock()
			delete(h.subs[topic], ch)
			if len(h.subs[topic]) == 0 {
				delete(h.subs, topic)
			}
			h.mu.Unlock()
		})
	}
}

func (h *hub) dispatch(topic string, msg []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subs[topic] {
		select {
		case ch <- msg:
		default:
			// Dropping is safe because subscribers re-read persisted events
		}
	}
}

// LocalBus delivers messages within this process, which is correct for SQLite where only one replica exists
type LocalBus struct {
	hub *hub
}

func NewLocalBus() *LocalBus {
	return &LocalBus{hub: newHub()}
}

func (b *LocalBus) Publish(_ context.Context, topic string, msg []byte) error {
	b.hub.dispatch(topic, msg)
	return nil
}

func (b *LocalBus) Subscribe(topic string) (<-chan []byte, func()) {
	return b.hub.subscribe(topic)
}

func (b *LocalBus) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

const (
	pgChannel = "umpteenth_events"
	// Postgres limits NOTIFY payloads to 8000 bytes
	maxNotifyPayload = 7900
)

// PostgresBus fans messages out to every replica with LISTEN/NOTIFY
type PostgresBus struct {
	pool *pgxpool.Pool
	hub  *hub
}

func NewPostgresBus(pool *pgxpool.Pool) *PostgresBus {
	return &PostgresBus{pool: pool, hub: newHub()}
}

func (b *PostgresBus) Publish(ctx context.Context, topic string, msg []byte) error {
	payload := topic + "\n" + string(msg)
	if len(payload) > maxNotifyPayload {
		return fmt.Errorf("bus message for %s is too large (%d bytes)", topic, len(payload))
	}
	_, err := b.pool.Exec(ctx, "SELECT pg_notify($1, $2)", pgChannel, payload)
	return err
}

func (b *PostgresBus) Subscribe(topic string) (<-chan []byte, func()) {
	return b.hub.subscribe(topic)
}

// Run holds a dedicated LISTEN connection and reconnects with backoff when it drops
func (b *PostgresBus) Run(ctx context.Context) error {
	const minBackoff, maxBackoff = time.Second, 30 * time.Second
	backoff := minBackoff
	for {
		started := time.Now()
		err := b.listen(ctx)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // the listener stops because the service is shutting down
		}

		// A connection that held for a while dropped for a new reason, so the backoff starts over
		if time.Since(started) > maxBackoff {
			backoff = minBackoff
		}
		slog.WarnContext(ctx, "Event bus connection lost, reconnecting", slog.Any("error", err), slog.Duration("in", backoff))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (b *PostgresBus) listen(ctx context.Context) error {
	pooled, err := b.pool.Acquire(ctx)
	if err != nil {
		return err
	}

	// The connection leaves the pool for good, so no query ever runs on it while it is subscribed to the channel
	// A canceled ctx closes it without waiting for the server, and a failed one is already closed
	conn := pooled.Hijack()
	defer conn.Close(ctx) //nolint:errcheck // the connection is discarded either way

	_, err = conn.Exec(ctx, "LISTEN "+pgChannel)
	if err != nil {
		return err
	}

	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		topic, msg, ok := strings.Cut(n.Payload, "\n")
		if ok {
			b.hub.dispatch(topic, []byte(msg))
		}
	}
}
