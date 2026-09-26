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
)

func init() {
	// The fake provider only exists in e2e builds, so production can never be pointed at it
	providerFactories[llm.KindFake] = fake.Factory

	registerTestRoutes = func(api huma.API, db *database.DB, svc *services) {
		e2etest.Register(api, e2etest.Dependencies{
			DB:         db,
			Users:      svc.auth,
			Workspaces: svc.workspaces,
			OnReset:    append([]func(ctx context.Context) error{func(ctx context.Context) error { return seedFakeProvider(ctx, svc) }}, svc.resetHooks...),
			Extensions: append([]func(api huma.API){svc.jobs.RegisterTestRoutes, registerLLMScript}, svc.testRoutes...),
		})
	}
}

// seedFakeProvider makes the scripted fake model the workspace default after a reset
func seedFakeProvider(ctx context.Context, svc *services) error {
	// The script lives in the database, so every replica of an HA test stack replays the same one
	fake.Shared().UseQueue(&dbQueue{db: svc.db})
	if _, err := fake.Shared().Script(ctx, true, nil); err != nil {
		return err
	}
	wid, err := svc.workspaces.DefaultWorkspaceID(ctx)
	if err != nil {
		return err
	}
	_, err = svc.providers.Service().CreateProvider(ctx, wid, "Fake", llm.KindFake, "", "", []providers.ModelSeed{{Model: "fake-model", Label: "Fake model", Caps: fake.DefaultCaps}})
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
