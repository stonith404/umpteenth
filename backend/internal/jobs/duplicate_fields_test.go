//go:build unit

package jobs

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/testutil"
)

// Runs pass input and output values by name, so a spec that uses a name twice is refused
func TestCreateRejectsDuplicateInputAndOutputNames(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)

	for name, spec := range map[string]Spec{
		// What the new-job form sends when the same input name is typed twice
		"inputs":  {Inputs: []IOField{{Name: "url", Type: "string"}, {Name: "url", Type: "string"}}},
		"outputs": {Outputs: []IOField{{Name: "title", Type: "string"}, {Name: "title", Type: "string"}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := m.createJob(t.Context(), wid, nil, jobFields{Name: "n", Instruction: "i", Spec: &spec})
			var appErr *apperror.Error
			require.ErrorAs(t, err, &appErr, "a job with duplicate %s was created", name)
		})
	}

	// Distinct names still save
	_, err := m.createJob(t.Context(), wid, nil, jobFields{Name: "n", Instruction: "i", Spec: &Spec{
		Inputs:  []IOField{{Name: "url", Type: "string"}, {Name: "depth", Type: "integer"}},
		Outputs: []IOField{{Name: "title", Type: "string"}},
	}})
	require.NoError(t, err)
}

// A patch can't bring repeated names either
func TestPatchRejectsDuplicateInputNames(t *testing.T) {
	m, _, db := newTestModule(t)
	wid := testutil.SeedWorkspace(t, db)
	jobID := testutil.SeedJob(t, db, wid, ConcurrencySkip)
	ctx := principal.WithPrincipal(t.Context(), principal.Principal{WorkspaceID: wid})
	dup := Spec{Inputs: []IOField{{Name: "url", Type: "string"}, {Name: "url", Type: "string"}}}

	// A spec with repeated names is refused
	_, err := m.update(ctx, &updateInput{ID: jobID, Body: jobPatch{Spec: &dup}})
	var appErr *apperror.Error
	require.ErrorAs(t, err, &appErr, "a patch with duplicate inputs was saved")
}
