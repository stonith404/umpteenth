package runs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/events"
	"github.com/stonith404/umpteenth/backend/internal/runner"
	"github.com/stonith404/umpteenth/backend/internal/runs/runsdb"
)

// errRunNotFinished refuses a run that is still executing or reflecting, whose runner or reflection would otherwise write to a missing row
func errRunNotFinished() error {
	return apperror.Conflict("The run can only be deleted once it has finished")
}

// Delete removes a finished run of the workspace together with its events and files
func (m *Module) Delete(ctx context.Context, workspaceID, runID string) error {
	run, err := m.getRun(ctx, workspaceID, runID)
	if err != nil {
		return err
	}
	if !deletable(run) {
		return errRunNotFinished()
	}
	ok, err := m.deleteRun(ctx, run)
	if err != nil {
		return err
	}
	if !ok {
		return errRunNotFinished()
	}
	return nil
}

// DeleteMany removes the finished runs among the given IDs and reports which ones it deleted and which it skipped
// Runs that are unknown, belong to another workspace or haven't finished are skipped rather than failing the whole request
func (m *Module) DeleteMany(ctx context.Context, workspaceID string, runIDs []string) (deleted, skipped []string, err error) {
	deleted, skipped = []string{}, []string{}
	for _, id := range runIDs {
		run, err := m.queries.GetRun(ctx, runsdb.GetRunParams{WorkspaceID: workspaceID, ID: id})
		if database.IsNotFound(err) {
			skipped = append(skipped, id)
			continue
		} else if err != nil {
			return deleted, skipped, fmt.Errorf("failed to load run: %w", err)
		}

		ok := false
		if deletable(run) {
			ok, err = m.deleteRun(ctx, run)
			if err != nil {
				return deleted, skipped, err
			}
		}
		if ok {
			deleted = append(deleted, id)
		} else {
			skipped = append(skipped, id)
		}
	}
	return deleted, skipped, nil
}

// deletable reports whether nothing will write to the run anymore
func deletable(run runsdb.Run) bool {
	return runner.IsTerminal(run.Status) && run.Reflection != "pending"
}

// deleteRun deletes the run's row, which cascades to its events, and then its files, and reports false if the run started changing in the meantime
func (m *Module) deleteRun(ctx context.Context, run runsdb.Run) (bool, error) {
	// The row goes in one statement guarded by the status, so a run that started reflecting since it was loaded stays
	n, err := m.queries.DeleteFinishedRun(ctx, runsdb.DeleteFinishedRunParams{WorkspaceID: run.WorkspaceID, ID: run.ID})
	if err != nil {
		return false, fmt.Errorf("failed to delete run: %w", err)
	}
	if n == 0 {
		return false, nil
	}

	// A file that fails to delete stays behind unreferenced, which only costs storage
	if err := m.deps.Storage.DeleteAll(context.WithoutCancel(ctx), "runs/"+run.ID); err != nil {
		slog.WarnContext(ctx, "Failed to delete the files of a deleted run", slog.String("run", run.ID), slog.Any("error", err))
	}

	// Open tables and dashboards on every replica drop the run
	events.PublishWorkspace(ctx, m.deps.Bus, run.WorkspaceID, map[string]any{"kind": "run", "runId": run.ID, "jobId": run.JobID, "deleted": true})
	return true, nil
}
