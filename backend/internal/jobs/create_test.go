//go:build unit

package jobs

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/playbook"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// failingPlaybooks refuses every save, like a database that fails after the job row was written
type failingPlaybooks struct {
	Playbooks
}

func (failingPlaybooks) Save(context.Context, string, string, playbook.Content, playbook.VersionMeta) (int64, error) {
	return 0, errors.New("disk full")
}

func TestCreateRemovesAJobWhoseSetupFailed(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	m.deps.Playbooks = failingPlaybooks{m.deps.Playbooks}

	_, err := m.createJob(t.Context(), wid, nil, jobFields{Name: "n", Instruction: "i", Spec: &Spec{Dockerfile: new("FROM alpine")}})
	require.ErrorContains(t, err, "disk full")

	// A client that retries gets one job, not a half-created one plus the retry
	var count int
	require.NoError(t, db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM jobs WHERE workspace_id = $1", wid).Scan(&count))
	require.Zero(t, count)
}
