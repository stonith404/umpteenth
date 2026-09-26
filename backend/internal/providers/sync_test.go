//go:build unit

package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/llm"
	"github.com/stonith404/umpteenth/backend/internal/llm/openai"
	"github.com/stonith404/umpteenth/backend/internal/providers/providersdb"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func newTestService(t *testing.T) (*Service, *database.DB, string) {
	t.Helper()
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	factories := map[string]Factory{
		llm.KindOpenAI:    func(cfg llm.Config) (llm.Provider, error) { return openai.New(cfg) },
		llm.KindAnthropic: func(cfg llm.Config) (llm.Provider, error) { return openai.New(cfg) },
	}
	return newService(db, []byte("0123456789abcdef0123456789abcdef"), factories), db, wid
}

// useCatalog swaps in a small catalog for one test
func useCatalog(t *testing.T, fetchedAt int64, entries ...llm.CatalogEntry) {
	t.Helper()
	restore := llm.SwapCatalog(&llm.ModelCatalog{FetchedAt: fetchedAt, Models: entries, Families: map[string]llm.Caps{
		"qwen332b": {Tools: true, ParallelTools: true, Reasoning: true, Context: 131_072},
	}})
	t.Cleanup(restore)
}

func claude(model string, in int64) llm.CatalogEntry {
	return llm.CatalogEntry{Kind: llm.KindAnthropic, Model: model, Label: model, Price: llm.Price{In: in, Out: in * 5}, Caps: llm.Caps{Tools: true, Context: 200_000}}
}

type modelState struct {
	Enabled, Synced, FollowCatalog, Unlisted bool
	PriceIn                                  int64
}

func modelStates(t *testing.T, s *Service, wid, providerID string) map[string]modelState {
	t.Helper()
	rows, err := s.queries.ListProviderModels(context.Background(), providersdb.ListProviderModelsParams{WorkspaceID: wid, ProviderID: providerID})
	require.NoError(t, err)
	out := map[string]modelState{}
	for _, row := range rows {
		m, err := s.queries.GetModel(context.Background(), providersdb.GetModelParams{WorkspaceID: wid, ID: row.ID})
		require.NoError(t, err)
		out[row.Model] = modelState{Enabled: m.Enabled, Synced: row.Synced, FollowCatalog: row.FollowCatalog, Unlisted: row.UnlistedAt != nil, PriceIn: row.PriceIn}
	}
	return out
}

func TestModelSourceFor(t *testing.T) {
	str := func(s string) *string { return &s }
	assert.Equal(t, SourceCatalog, modelSourceFor(llm.KindAnthropic, nil))
	assert.Equal(t, SourceCatalog, modelSourceFor(llm.KindAnthropic, str("https://proxy.example.com")))
	assert.Equal(t, SourceCatalog, modelSourceFor(llm.KindOpenAI, nil))
	assert.Equal(t, SourceCatalog, modelSourceFor(llm.KindOpenAI, str("https://api.openai.com/v1")))
	assert.Equal(t, SourceServer, modelSourceFor(llm.KindOpenAI, str("http://localhost:11434/v1")))
	assert.Equal(t, SourceServer, modelSourceFor(llm.KindOpenAI, str("https://openrouter.ai/api/v1")))
	assert.Equal(t, SourceManual, modelSourceFor(llm.KindFake, nil))
}

func TestCatalogSyncFollowsTheCatalog(t *testing.T) {
	ctx := context.Background()
	s, _, wid := newTestService(t)
	useCatalog(t, 100, claude("claude-a", 1_000_000), claude("claude-b", 2_000_000))

	pid, err := s.CreateProvider(ctx, wid, "Anthropic", llm.KindAnthropic, "", "", nil)
	require.NoError(t, err)
	_, err = s.CreateModel(ctx, wid, pid, "claude-custom", "", llm.Price{In: 7}, llm.Caps{Tools: true, Context: 1000})
	require.NoError(t, err)

	// The first sync adds every catalog model, enabled and following the catalog
	result, err := s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Added: 2}, result)
	states := modelStates(t, s, wid, pid)
	assert.Equal(t, modelState{Enabled: true, Synced: true, FollowCatalog: true, PriceIn: 1_000_000}, states["claude-a"])
	assert.Equal(t, modelState{Enabled: true, PriceIn: 7}, states["claude-custom"], "a model added by hand is not synced")

	// An admin disables one model and prices the other by hand
	a, _, _ := s.FindModel(ctx, wid, llm.KindAnthropic, "claude-a")
	b, _, _ := s.FindModel(ctx, wid, llm.KindAnthropic, "claude-b")
	_, err = s.queries.SetModelEnabled(ctx, providersdb.SetModelEnabledParams{Enabled: false, WorkspaceID: wid, ID: a})
	require.NoError(t, err)
	err = s.queries.MarkModelListed(ctx, providersdb.MarkModelListedParams{FollowCatalog: false, WorkspaceID: wid, ID: b})
	require.NoError(t, err)

	// A new catalog raises both prices, drops claude-b and adds claude-c
	useCatalog(t, 200, claude("claude-a", 3_000_000), claude("claude-c", 4_000_000))
	result, err = s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Added: 1, Updated: 1, Unlisted: 1}, result)
	states = modelStates(t, s, wid, pid)
	assert.Equal(t, modelState{Enabled: false, Synced: true, FollowCatalog: true, PriceIn: 3_000_000}, states["claude-a"], "disabling keeps the model following the catalog")
	assert.Equal(t, modelState{Enabled: true, Synced: true, Unlisted: true, PriceIn: 2_000_000}, states["claude-b"], "an unlisted model keeps its data")
	assert.True(t, states["claude-c"].Enabled)
	assert.False(t, states["claude-custom"].Unlisted, "a model added by hand is never unlisted")

	// The catalog brings claude-b back as it was
	useCatalog(t, 300, claude("claude-a", 3_000_000), claude("claude-b", 9_000_000), claude("claude-c", 4_000_000))
	result, err = s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{}, result)
	assert.Equal(t, modelState{Enabled: true, Synced: true, PriceIn: 2_000_000}, modelStates(t, s, wid, pid)["claude-b"])

	p, err := s.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: pid})
	require.NoError(t, err)
	assert.NotNil(t, p.SyncedAt)
	assert.Nil(t, p.SyncError)
}

func TestCatalogSyncAdoptsModelsAddedBefore(t *testing.T) {
	ctx := context.Background()
	s, db, wid := newTestService(t)
	useCatalog(t, 100, claude("claude-a", 1_000_000), claude("claude-b", 2_000_000))

	pid, err := s.CreateProvider(ctx, wid, "Anthropic", llm.KindAnthropic, "", "", nil)
	require.NoError(t, err)
	_, err = s.CreateModel(ctx, wid, pid, "claude-a", "Old label", llm.Price{In: 5}, llm.Caps{Tools: true, Context: 1000})
	require.NoError(t, err)
	seeded, err := s.CreateModel(ctx, wid, pid, "claude-b", "Old label", llm.Price{In: 5}, llm.Caps{Tools: true, Context: 1000})
	require.NoError(t, err)

	// The migration marks the models older builds seeded from their catalog as following it
	testutil.Exec(t, db, `UPDATE models SET follow_catalog = TRUE WHERE id = $1`, seeded)

	// A model an admin added by hand keeps its prices once the catalog lists it, while a seeded one takes the catalog's
	result, err := s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Updated: 1}, result)
	states := modelStates(t, s, wid, pid)
	assert.Equal(t, modelState{Enabled: true, Synced: true, PriceIn: 5}, states["claude-a"])
	assert.Equal(t, modelState{Enabled: true, Synced: true, FollowCatalog: true, PriceIn: 2_000_000}, states["claude-b"])
}

func TestConcurrentSyncsAddAModelOnce(t *testing.T) {
	ctx := t.Context()
	s, _, wid := newTestService(t)
	pid, err := s.CreateProvider(ctx, wid, "Anthropic", llm.KindAnthropic, "", "", nil)
	require.NoError(t, err)

	// The sync that loses the race to add a model neither fails nor overwrites the row the other one wrote
	for i, price := range []int64{1, 2} {
		n, err := s.queries.CreateSyncedModel(ctx, providersdb.CreateSyncedModelParams{
			ID: database.NewID(), WorkspaceID: wid, ProviderID: pid, Model: "claude-a", PriceIn: price, Caps: "{}", CreatedAt: database.Now(),
		})
		require.NoError(t, err)
		assert.EqualValues(t, 1-i, n)
	}
	assert.Equal(t, modelState{Enabled: true, Synced: true, PriceIn: 1}, modelStates(t, s, wid, pid)["claude-a"])
}

func TestServerSyncListsTheServerModels(t *testing.T) {
	ctx := context.Background()
	s, _, wid := newTestService(t)
	useCatalog(t, 100, claude("claude-a", 1_000_000))

	var body atomic.Value
	body.Store(`{"data": [{"id": "qwen3:32b"}, {"id": "my-finetune"}, {"id": "Qwen/Qwen3-32B", "max_model_len": 40960}]}`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		raw := body.Load().(string)
		if raw == "" {
			http.Error(w, "down", http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(raw))
	}))
	t.Cleanup(srv.Close)

	pid, err := s.CreateProvider(ctx, wid, "Ollama", llm.KindOpenAI, srv.URL+"/v1", "", nil)
	require.NoError(t, err)
	result, err := s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Added: 3}, result)

	// models.dev fills in the family traits, the server's context window wins, and unknown models get the conservative default
	caps := func(model string) llm.Caps {
		id, ok, err := s.FindModel(ctx, wid, llm.KindOpenAI, model)
		require.NoError(t, err)
		require.True(t, ok, model)
		_, m, err := s.Resolve(ctx, wid, id)
		require.NoError(t, err)
		return m.Caps
	}
	assert.Equal(t, llm.Caps{Tools: true, ParallelTools: true, Reasoning: true, Context: 131_072}, caps("qwen3:32b"))
	assert.Equal(t, 40960, caps("Qwen/Qwen3-32B").Context)
	assert.Equal(t, llm.Caps{Tools: true, ParallelTools: true, Context: fallbackContext}, caps("my-finetune"))
	assert.False(t, modelStates(t, s, wid, pid)["qwen3:32b"].FollowCatalog, "server models keep the metadata they were discovered with")

	// A server that is down changes nothing and leaves the error on the provider
	body.Store("")
	result, err = s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Error)
	assert.Len(t, modelStates(t, s, wid, pid), 3)
	p, err := s.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: pid})
	require.NoError(t, err)
	require.NotNil(t, p.SyncError)

	// A model removed from the server is unlisted, and the next good sync clears the error
	body.Store(`{"data": [{"id": "qwen3:32b"}, {"id": "Qwen/Qwen3-32B"}]}`)
	result, err = s.SyncProvider(ctx, wid, pid)
	require.NoError(t, err)
	assert.Equal(t, SyncResult{Unlisted: 1}, result)
	assert.True(t, modelStates(t, s, wid, pid)["my-finetune"].Unlisted)
	p, err = s.queries.GetProvider(ctx, providersdb.GetProviderParams{WorkspaceID: wid, ID: pid})
	require.NoError(t, err)
	assert.Nil(t, p.SyncError)
}

func TestDisabledAndUnlistedModelsCantBePicked(t *testing.T) {
	ctx := context.Background()
	s, db, wid := newTestService(t)
	useCatalog(t, 100, claude("claude-a", 1_000_000))
	pid, err := s.CreateProvider(ctx, wid, "Anthropic", llm.KindAnthropic, "", "", nil)
	require.NoError(t, err)
	id, err := s.CreateModel(ctx, wid, pid, "claude-a", "", llm.Price{}, llm.Caps{Tools: true, JSONSchema: true, Context: 1000})
	require.NoError(t, err)

	ok, err := s.ModelExists(ctx, wid, id)
	require.NoError(t, err)
	assert.True(t, ok)

	// The stored capabilities decide how the model is called, not the adapter's own lookup
	p, _, err := s.Resolve(ctx, wid, id)
	require.NoError(t, err)
	assert.True(t, p.Caps("claude-a").JSONSchema)

	// A disabled model can't be picked or resolved, but can still be tested
	testutil.Exec(t, db, `UPDATE models SET enabled = FALSE WHERE id = $1`, id)
	ok, err = s.ModelExists(ctx, wid, id)
	require.NoError(t, err)
	assert.False(t, ok)
	_, _, err = s.Resolve(ctx, wid, id)
	assert.True(t, apperror.IsCode(err, apperror.CodeConflict))
	_, _, err = s.resolve(ctx, wid, id, true)
	require.NoError(t, err)

	// An unlisted model can't be picked, but is still tried when something already points at it
	testutil.Exec(t, db, `UPDATE models SET enabled = TRUE, unlisted_at = 1 WHERE id = $1`, id)
	ok, err = s.ModelExists(ctx, wid, id)
	require.NoError(t, err)
	assert.False(t, ok)
	_, _, err = s.Resolve(ctx, wid, id)
	require.NoError(t, err)
}

func TestDefaultModelsCantBeDisabled(t *testing.T) {
	ctx := context.Background()
	s, db, wid := newTestService(t)
	testutil.Exec(t, db, `INSERT INTO settings (workspace_id, key, value) VALUES ($1, 'utilityModelId', '"m1"')`, wid)

	err := s.checkNotDefault(ctx, wid, map[string]string{"m1": "Claude Haiku"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Claude Haiku is the workspace's utility model")
	require.NoError(t, s.checkNotDefault(ctx, wid, map[string]string{"m2": "Other"}))
}

func TestCatalogStoreKeepsTheNewestCatalog(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newTestService(t)
	useCatalog(t, 100, claude("claude-a", 1_000_000))

	// A refresh stores what models.dev serves and installs it
	fixture, err := json.Marshal(map[string]any{
		"anthropic": map[string]any{"models": map[string]any{
			"claude-new": map[string]any{"id": "claude-new", "name": "Claude New", "tool_call": true,
				"modalities": map[string]any{"input": []string{"text"}, "output": []string{"text"}},
				"limit":      map[string]any{"context": 500_000}, "cost": map[string]any{"input": 2, "output": 10}},
		}},
		"openai": map[string]any{"models": map[string]any{}},
	})
	require.NoError(t, err)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(fixture) }))
	t.Cleanup(srv.Close)
	s.catalog.url = srv.URL
	require.NoError(t, s.catalog.refresh(ctx))
	_, ok := llm.LookupModel(llm.KindAnthropic, "claude-new")
	assert.True(t, ok)

	// A replica still on an older catalog loads the stored one
	stored := llm.CurrentCatalog()
	useCatalog(t, 100, claude("claude-a", 1_000_000))
	require.NoError(t, s.catalog.load(ctx))
	assert.Equal(t, stored.FetchedAt, llm.CurrentCatalog().FetchedAt)

	// An older document never replaces the stored one
	err = s.queries.SaveModelCatalog(ctx, providersdb.SaveModelCatalogParams{Source: catalogSource, Data: "{}", FetchedAt: 1})
	require.NoError(t, err)
	row, err := s.queries.GetModelCatalog(ctx, catalogSource)
	require.NoError(t, err)
	assert.Equal(t, stored.FetchedAt, row.FetchedAt)
	assert.WithinDuration(t, time.Now(), time.Unix(row.FetchedAt, 0), time.Minute)
}
