//go:build e2etest

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/e2etest"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/fake"
	"github.com/stonith404/umpteenth/backend/internal/providers"
	"github.com/stonith404/umpteenth/backend/internal/workspaces"
)

func init() {
	// The fake provider only exists in e2e builds, so production can never be pointed at it
	providerFactories[llm.KindFake] = fake.Factory

	registerTestRoutes = func(api huma.API, db *database.DB, svc *services) {
		// The script lives in the database, so every replica of an HA test stack replays the same one
		fake.Shared().UseQueue(&dbQueue{db: db})

		e2etest.Register(api, e2etest.Dependencies{
			DB:         db,
			Users:      svc.auth,
			Workspaces: svc.workspaces,
			BeforeReset: func(ctx context.Context) error {
				return cancelLiveRuns(ctx, db, svc)
			},
			OnReset: func(ctx context.Context) error {
				if _, err := fake.Shared().Script(ctx, true, nil); err != nil {
					return err
				}
				if err := svc.skills.SetGitHubArchives(ctx, ""); err != nil {
					return err
				}
				return seedFakeProvider(ctx, svc, workspaces.DefaultID)
			},
		})

		// Workspaces created during a test run on the fake model too, so specs can run jobs in them
		svc.workspaces.SetSeeder(func(ctx context.Context, wid string) error { return seedFakeProvider(ctx, svc, wid) })
		svc.jobs.RegisterTestRoutes(api)
		svc.skills.RegisterTestRoutes(api)
		registerLLMScript(api)
	}
}

// liveRunStatuses are the statuses of runs a runner may still be working on
const liveRunStatuses = "status IN ('queued', 'provisioning', 'running', 'verifying')"

// cancelLiveRuns stops every run that is still going before a reset deletes it
// A runner that outlives its rows keeps calling the fake model, and would take the scripted answers the next spec queued
func cancelLiveRuns(ctx context.Context, db *database.DB, svc *services) error {
	rows, err := db.QueryContext(ctx, "SELECT workspace_id, id FROM runs WHERE "+liveRunStatuses)
	if err != nil {
		return fmt.Errorf("failed to list live runs: %w", err)
	}
	var live [][2]string
	for rows.Next() {
		var run [2]string
		if err := rows.Scan(&run[0], &run[1]); err != nil {
			rows.Close()
			return fmt.Errorf("failed to read a live run: %w", err)
		}
		live = append(live, run)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to list live runs: %w", err)
	}

	// A run can end between the query and its cancel, so a refused cancel is expected and ignored
	for _, run := range live {
		_ = svc.runs.Cancel(ctx, run[0], run[1])
	}

	// Runners record how their runs ended on their own, so the reset waits for them rather than deleting rows they still write to
	// The wait is bounded, since a stuck run must not stall every later spec
	deadline := time.Now().Add(10 * time.Second)
	for len(live) > 0 && time.Now().Before(deadline) {
		var left int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM runs WHERE "+liveRunStatuses).Scan(&left); err != nil {
			return fmt.Errorf("failed to count live runs: %w", err)
		}
		if left == 0 {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return nil
}

// seedFakeProvider makes the scripted fake model the workspace's default
func seedFakeProvider(ctx context.Context, svc *services, wid string) error {
	_, err := svc.providers.Service().CreateProvider(ctx, wid, "Fake", llm.KindFake, "", "", []providers.ModelSeed{{Model: "fake-model", Label: "Fake model", Caps: fake.DefaultCaps}})
	if err != nil {
		return err
	}
	id, _, err := svc.providers.Service().FindModel(ctx, wid, llm.KindFake, "fake-model")
	if err != nil {
		return err
	}
	for _, key := range []string{"agentModelId", "utilityModelId"} {
		if err := svc.settings.Set(ctx, wid, key, id); err != nil {
			return err
		}
	}
	return nil
}

type llmScriptInput struct {
	// The body is decoded by hand, since scripted responses leave most fields empty
	RawBody []byte
}

type llmScriptOutput struct {
	Body struct {
		Pending int `json:"pending"`
	}
}

// registerLLMScript lets tests queue the fake model's answers
func registerLLMScript(api huma.API) {
	httpserver.Register(api, httpserver.Operation("test-llm-script", http.MethodPost, "/api/test/llm-script", "Test"), nil, func(ctx context.Context, in *llmScriptInput) (*llmScriptOutput, error) {
		var body struct {
			Responses []fake.ScriptedResponse `json:"responses"`
			Reset     bool                    `json:"reset"`
		}
		if err := json.Unmarshal(in.RawBody, &body); err != nil {
			return nil, apperror.InvalidRequestBody(err)
		}
		pending, err := fake.Shared().Script(ctx, body.Reset, body.Responses)
		if err != nil {
			return nil, err
		}
		out := &llmScriptOutput{}
		out.Body.Pending = pending
		return out, nil
	})
}

// dbQueue keeps the fake model's script in the kv table, ordered by a zero-padded key
type dbQueue struct {
	db *database.DB
}

const fakeQueuePrefix = "e2e-fake-llm:"

func (q *dbQueue) Push(ctx context.Context, responses []fake.ScriptedResponse) error {
	base := time.Now().UnixNano()
	return q.db.InTx(ctx, func(tx *database.Tx) error {
		for i, r := range responses {
			raw, err := json.Marshal(r)
			if err != nil {
				return err
			}
			_, err = tx.ExecContext(ctx, "INSERT INTO kv (key, value) VALUES ($1, $2)", fmt.Sprintf("%s%020d:%06d", fakeQueuePrefix, base, i), string(raw))
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func (q *dbQueue) Pop(ctx context.Context) (fake.ScriptedResponse, bool, error) {
	// Two replicas can race for the same row, and the loser simply takes the next one
	for range 10 {
		var raw string
		err := q.db.QueryRowContext(ctx,
			"DELETE FROM kv WHERE key = (SELECT key FROM kv WHERE key LIKE $1 ORDER BY key LIMIT 1) RETURNING value", fakeQueuePrefix+"%").Scan(&raw)
		if database.IsNotFound(err) {
			var left int
			if err := q.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM kv WHERE key LIKE $1", fakeQueuePrefix+"%").Scan(&left); err != nil || left == 0 {
				return fake.ScriptedResponse{}, false, err
			}
			continue
		}
		if err != nil {
			return fake.ScriptedResponse{}, false, err
		}
		var r fake.ScriptedResponse
		err = json.Unmarshal([]byte(raw), &r)
		return r, err == nil, err
	}
	return fake.ScriptedResponse{}, false, errors.New("the scripted response queue is contended")
}

func (q *dbQueue) Clear(ctx context.Context) error {
	_, err := q.db.ExecContext(ctx, "DELETE FROM kv WHERE key LIKE $1", fakeQueuePrefix+"%")
	return err
}

func (q *dbQueue) Len(ctx context.Context) (int, error) {
	var n int
	err := q.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM kv WHERE key LIKE $1", fakeQueuePrefix+"%").Scan(&n)
	return n, err
}
