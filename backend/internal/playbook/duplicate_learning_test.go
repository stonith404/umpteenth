//go:build unit

package playbook_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// Learnings are told apart by their id, so a version with two learnings sharing one is refused
func TestSaveRejectsDuplicateLearningIDs(t *testing.T) {
	db := testutil.NewDatabaseForTest(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, "skip")
	m := playbook.New(playbook.Dependencies{DB: db})
	meta := playbook.VersionMeta{Author: playbook.AuthorUser}

	for name, ids := range map[string][2]string{
		// A learning copied in the JSON editor that kept its id
		"copied id": {"L1", "L1"},
		// Two learnings added in the JSON editor without an id
		"missing ids": {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			c := playbook.Content{Learnings: []playbook.Learning{
				{ID: ids[0], Kind: "fact", Text: "a", Status: "active"},
				{ID: ids[1], Kind: "fact", Text: "b", Status: "active"},
			}}
			_, err := m.Save(t.Context(), wid, jobID, c, meta)
			var appErr *apperror.Error
			require.ErrorAs(t, err, &appErr, "a playbook with learning ids %q and %q was saved", ids[0], ids[1])
		})
	}

	// Nothing was stored for the rejected content
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM playbook_versions WHERE job_id = $1", jobID).Scan(&count))
	assert.Zero(t, count)

	// Distinct ids still save
	_, err := m.Save(t.Context(), wid, jobID, playbook.Content{Learnings: []playbook.Learning{
		{ID: "L1", Kind: "fact", Text: "a", Status: "active"},
		{ID: "L2", Kind: "fact", Text: "b", Status: "retired"},
	}}, meta)
	require.NoError(t, err)
}
