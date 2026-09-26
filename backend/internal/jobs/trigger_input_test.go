//go:build unit

package jobs

import (
	"encoding/json"
	"testing"
	"unicode/utf8"

	"github.com/italypaleale/francis/host/local"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/runs"
	"github.com/stonith404/umpteenth/backend/internal/settings"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
)

// newTestModuleWithRuns wires the real runs module, since the fake queue does not store a run's input or instructions
func newTestModuleWithRuns(t *testing.T) (*Module, *database.DB) {
	db := testutil.NewDatabaseForTest(t)
	var m *Module
	testutil.NewActorHostForTest(t, func(t *testing.T, h *local.Host) {
		rm, err := runs.New(runs.Dependencies{DB: db, Actors: h, Bus: events.NewLocalBus(), Storage: storage.NewDatabaseStorage(db), MaxConcurrentRuns: 1, MaintenanceDisabled: true})
		require.NoError(t, err)
		m, err = New(Dependencies{
			DB:        db,
			Actors:    h,
			Runs:      rm,
			Playbooks: playbook.New(playbook.Dependencies{DB: db}),
			Settings:  settings.New(settings.Dependencies{DB: db, Defaults: settings.Defaults{Image: "img", RetentionDays: 90}}),
		})
		require.NoError(t, err)
	})
	return m, db
}

// A webhook sender that encodes its JSON as Latin-1 still gets a run, on Postgres as on SQLite
func TestWebhookInputWithInvalidUTF8CreatesRun(t *testing.T) {
	m, db := newTestModuleWithRuns(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)
	testutil.Exec(t, db, "UPDATE jobs SET webhook_token_hash = $1 WHERE id = $2", crypto.HashToken("tok"), jobID)

	// The body is valid JSON to encoding/json, but the ü is the single Latin-1 byte 0xFC
	body := []byte("{\"customer\":\"M\xfcller GmbH\"}")
	require.True(t, json.Valid(body))
	out, err := m.webhook(t.Context(), &webhookInput{JobID: jobID, Authorization: "Bearer tok", RawBody: body})
	require.NoError(t, err)
	require.NotEmpty(t, out.Body.RunID)

	// The stored input stays valid JSON, with the byte it could not keep replaced
	var input string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT input FROM runs WHERE id = $1", out.Body.RunID).Scan(&input))
	require.True(t, utf8.ValidString(input), "%q", input)
	var decoded map[string]string
	require.NoError(t, json.Unmarshal([]byte(input), &decoded))
	require.Equal(t, "M�ller GmbH", decoded["customer"])
}

// An API trigger whose instructions decode to a NUL byte still gets a run, since Postgres refuses NUL in text as well
func TestAPITriggerInstructionsWithNULCreatesRun(t *testing.T) {
	m, db := newTestModuleWithRuns(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencyQueue)

	// The JSON escape \u0000 in a request body decodes to a real NUL byte in the instructions
	var body struct {
		Instructions string `json:"instructions"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"instructions":"Greet\u0000 them"}`), &body))
	res, err := m.Trigger(t.Context(), wid, jobID, runs.TriggerRequest{Trigger: runs.TriggerAPI, Instructions: body.Instructions})
	require.NoError(t, err)

	var instructions string
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT instructions FROM runs WHERE id = $1", res.RunID).Scan(&instructions))
	require.Equal(t, "Greet them", instructions)
}
