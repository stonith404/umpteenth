//go:build unit

package events

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/storage"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

func TestConcurrentEventsAreAllRecorded(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	runID := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'running', 'explore', 'manual', 0, $4)`,
		runID, wid, jobID, database.Now())
	rec := NewRecorder(db, NewLocalBus(), storage.NewDatabaseStorage(db), runID)

	// Parallel tool calls and broker requests emit at the same time
	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() { rec.Emit(t.Context(), Event{Type: TypeLog, Payload: map[string]any{"message": "x"}}) })
	}
	wg.Wait()

	var count int64
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM run_events WHERE run_id = $1`, runID).Scan(&count))
	require.EqualValues(t, 30, count)
}

// An oversized payload is shortened rather than replaced, since the timeline and reflection read an event's status, error flag, call ID and cost by name
func TestOversizedPayloadKeepsItsFields(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	runID := database.NewID()
	testutil.Exec(t, db, `INSERT INTO runs (id, workspace_id, job_id, number, status, mode, trigger, playbook_version, queued_at) VALUES ($1, $2, $3, 1, 'running', 'explore', 'manual', 0, $4)`,
		runID, wid, jobID, database.Now())
	fileStorage := storage.NewDatabaseStorage(db)
	rec := NewRecorder(db, NewLocalBus(), fileStorage, runID)

	// HTML and binary output grow past the limit once JSON-escaped, even under the observer's cap on the raw text
	content := "exit code: 22\n" + strings.Repeat("<a href=\"/p\">\x80\x1b[0m</a>\n", 500) + "curl: (22) The requested URL returned error: 403"
	require.Less(t, len(content), 12_000)
	seq := rec.Emit(t.Context(), Event{Type: TypeToolResult, Payload: map[string]any{
		"callId": "call_1", "name": "bash", "content": content, "isError": true, "meta": map[string]any{"exitCode": 22},
	}})
	require.NotZero(t, seq)

	// The reflection transcript clips the start of a finish payload, so its status has to stay first
	var items []map[string]string
	for i := range 400 {
		items = append(items, map[string]string{"title": fmt.Sprintf("Listing %d", i), "url": fmt.Sprintf("https://shop.example/items/%d", i)})
	}
	finish := struct {
		Status  string         `json:"status"`
		Summary string         `json:"summary"`
		Outputs map[string]any `json:"outputs"`
	}{Status: "success", Summary: "Collected 400 listings", Outputs: map[string]any{"items": items}}
	require.NotZero(t, rec.Emit(t.Context(), Event{Type: TypeFinish, Payload: finish}))

	rows, err := db.QueryContext(t.Context(), `SELECT payload FROM run_events WHERE run_id = $1 ORDER BY seq`, runID)
	require.NoError(t, err)
	defer rows.Close()
	var payloads []string
	for rows.Next() {
		var payload string
		require.NoError(t, rows.Scan(&payload))
		require.LessOrEqual(t, len(payload), maxPayloadBytes)
		payloads = append(payloads, payload)
	}
	require.NoError(t, rows.Err())
	require.Len(t, payloads, 2)

	// The tool result keeps its flags and the start and end of its output
	var result struct {
		CallID    string         `json:"callId"`
		Name      string         `json:"name"`
		Content   string         `json:"content"`
		IsError   bool           `json:"isError"`
		Meta      map[string]int `json:"meta"`
		Truncated bool           `json:"truncated"`
		BlobKey   string         `json:"blobKey"`
	}
	require.NoError(t, json.Unmarshal([]byte(payloads[0]), &result))
	require.Equal(t, "call_1", result.CallID)
	require.Equal(t, "bash", result.Name)
	require.True(t, result.IsError)
	require.Equal(t, 22, result.Meta["exitCode"])
	require.True(t, result.Truncated)
	require.True(t, strings.HasPrefix(result.Content, "exit code: 22\n"), "the start of the output is kept")
	require.True(t, strings.HasSuffix(result.Content, "returned error: 403"), "the end of the output is kept")
	require.Contains(t, result.Content, "characters omitted")

	// The blob keeps the whole payload
	blob, err := storage.ReadAll(t.Context(), fileStorage, result.BlobKey)
	require.NoError(t, err)
	var whole struct {
		Content string `json:"content"`
	}
	require.NoError(t, json.Unmarshal(blob, &whole))
	require.Equal(t, strings.ToValidUTF8(content, "\uFFFD"), whole.Content)

	// The finish keeps its fields in order and the first items of its list whole
	require.True(t, strings.HasPrefix(payloads[1], `{"status":"success","summary":"Collected 400 listings","outputs":{"items":[{`), payloads[1][:100])
	var shortened struct {
		Outputs struct {
			Items []map[string]string `json:"items"`
		} `json:"outputs"`
		Truncated bool `json:"truncated"`
	}
	require.NoError(t, json.Unmarshal([]byte(payloads[1]), &shortened))
	require.True(t, shortened.Truncated)
	require.NotEmpty(t, shortened.Outputs.Items)
	require.Less(t, len(shortened.Outputs.Items), 400)
	require.Equal(t, items[:len(shortened.Outputs.Items)], shortened.Outputs.Items, "the list keeps its first items whole")
}
