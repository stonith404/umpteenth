// Package workspaces owns the tenant boundary; v1 has exactly one default workspace (PLAN.md §3.5)
package workspaces

import (
	"context"
	"fmt"
	"sync"

	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/workspaces/workspacesdb"
)

type Dependencies struct {
	DB *database.DB
}

// DefaultID is the fixed ID of the single v1 workspace
// A well-known ID lets every replica create it idempotently without racing on "first row wins"
const DefaultID = "00000000-0000-7000-8000-000000000001"

type Module struct {
	db      *database.DB
	queries *workspacesdb.Queries

	// ensured only skips the idempotent insert; the ID itself is a constant, so no replica can hold a stale value
	mu      sync.Mutex
	ensured bool
}

func New(deps Dependencies) *Module {
	return &Module{db: deps.DB, queries: workspacesdb.New(deps.DB)}
}

// EnsureDefault creates the default workspace on first start and returns its ID
func (m *Module) EnsureDefault(ctx context.Context) (string, error) {
	return m.DefaultWorkspaceID(ctx)
}

// DefaultWorkspaceID returns the single v1 workspace, creating it if missing
func (m *Module) DefaultWorkspaceID(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ensured {
		return DefaultID, nil
	}

	err := m.queries.EnsureWorkspace(ctx, workspacesdb.EnsureWorkspaceParams{ID: DefaultID, Name: "Default", CreatedAt: database.Now()})
	if err != nil {
		return "", fmt.Errorf("failed to create default workspace: %w", err)
	}
	m.ensured = true
	return DefaultID, nil
}

// Reset makes the next call recreate the default workspace, used by the e2e reset endpoint after wiping the database
func (m *Module) Reset() {
	m.mu.Lock()
	m.ensured = false
	m.mu.Unlock()
}
