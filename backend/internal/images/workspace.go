package images

import (
	"context"
	"fmt"
	"log/slog"
)

// ReleaseWorkspace returns a function that deletes the build logs of a workspace's images once the rows of the deleted workspace are gone
// Local images need nothing, since PruneLocal removes those no row refers to
func (m *Module) ReleaseWorkspace(ctx context.Context, workspaceID string) (func(context.Context), error) {
	ids, err := m.queries.ListImageIDsOfWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}
	return func(ctx context.Context) {
		for _, id := range ids {
			if err := m.deps.Storage.DeleteAll(ctx, "images/"+id); err != nil {
				slog.WarnContext(ctx, "Failed to delete the build log of a deleted workspace's image", slog.String("image", id), slog.Any("error", err))
			}
		}
	}, nil
}
