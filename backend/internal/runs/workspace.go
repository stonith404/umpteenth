package runs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stonith404/umpteenth/backend/internal/runs/runsdb"
)

// ActiveRuns counts the workspace's runs that haven't finished, which have to end before the workspace can be deleted
func (m *Module) ActiveRuns(ctx context.Context, workspaceID string) (int64, error) {
	n, err := m.queries.CountActiveRuns(ctx, workspaceID)
	if err != nil {
		return 0, fmt.Errorf("failed to count active runs: %w", err)
	}
	return n, nil
}

// ReleaseWorkspace deletes the events of a workspace that is about to be deleted, and returns a function that deletes the runs' files once the rows are gone
// Events go one run at a time, since a single cascade over all of them would hold SQLite's write lock long enough to fail other writers such as runner heartbeats
func (m *Module) ReleaseWorkspace(ctx context.Context, workspaceID string) (func(context.Context), error) {
	ids, err := m.queries.ListRunIDs(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list runs: %w", err)
	}
	for _, id := range ids {
		err = m.queries.DeleteRunEventsOf(ctx, runsdb.DeleteRunEventsOfParams{WorkspaceID: workspaceID, ID: id})
		if err != nil {
			return nil, fmt.Errorf("failed to delete run events: %w", err)
		}
	}

	return func(ctx context.Context) {
		// A file that fails to delete stays behind unreferenced, which only costs storage
		for _, id := range ids {
			if err := m.deps.Storage.DeleteAll(ctx, "runs/"+id); err != nil {
				slog.WarnContext(ctx, "Failed to delete the files of a deleted workspace's run", slog.String("run", id), slog.Any("error", err))
			}
		}
		if err := m.queries.DeletePruneWatermark(ctx, "retention-watermark/"+workspaceID); err != nil {
			slog.WarnContext(ctx, "Failed to delete the retention watermark of a deleted workspace", slog.String("workspace", workspaceID), slog.Any("error", err))
		}
	}, nil
}
