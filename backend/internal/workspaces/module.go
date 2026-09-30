// Package workspaces owns the tenant boundary: workspaces, their members and roles, and invites
package workspaces

import (
	"context"
	"fmt"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/workspaces/workspacesdb"
)

// DefaultID is the fixed ID of the workspace everyone shares when workspaces are turned off
// A well-known ID lets every replica create it idempotently without racing on "first row wins"
const DefaultID = "00000000-0000-7000-8000-000000000001"

// Sessions issues session cookies, which the auth module owns
type Sessions interface {
	// SessionCookie moves the caller's session into another workspace, keeping the session's ID, sign-in provider and end
	SessionCookie(p principal.Principal, workspaceID string) (http.Cookie, error)
}

// Seeder fills a new workspace, which only test builds use to give it the fake model
type Seeder func(ctx context.Context, workspaceID string) error

// Cleanup releases what a workspace holds outside the rows that cascade with it, implemented by the modules owning jobs, runs and files
type Cleanup interface {
	// BeforeDelete refuses while the workspace has active runs and releases what has to go before the rows do
	// The returned function removes what can only go once the rows are gone, and runs in the background
	BeforeDelete(ctx context.Context, workspaceID string) (after func(context.Context), err error)
}

type Dependencies struct {
	DB *database.DB
	// Enabled lets people have several workspaces; without it everyone shares the default workspace
	Enabled bool
	// AppURL is the public URL invite links point at
	AppURL string
}

type Module struct {
	db      *database.DB
	queries *workspacesdb.Queries
	enabled bool
	appURL  string

	sessions Sessions
	seed     Seeder
	cleanup  Cleanup
}

func New(deps Dependencies) *Module {
	return &Module{db: deps.DB, queries: workspacesdb.New(deps.DB), enabled: deps.Enabled, appURL: deps.AppURL}
}

// SetSessions wires the auth module, which is built after this module because it resolves sign-ins through it
func (m *Module) SetSessions(s Sessions) { m.sessions = s }

// SetSeeder wires the function that fills new workspaces
func (m *Module) SetSeeder(s Seeder) { m.seed = s }

// SetCleanup wires the modules a workspace deletion has to go through, which are built after this module
func (m *Module) SetCleanup(c Cleanup) { m.cleanup = c }

// Enabled reports whether people can have several workspaces
func (m *Module) Enabled() bool { return m.enabled }

func (m *Module) RegisterRoutes(api huma.API, auth huma.Middlewares) {
	admin := httpserver.Access{MinRole: principal.RoleAdmin, SessionOnly: true}
	owner := httpserver.Access{MinRole: principal.RoleOwner, SessionOnly: true}
	session := httpserver.Access{SessionOnly: true}
	instanceAdmin := httpserver.Access{InstanceAdmin: true, SessionOnly: true}

	// Moving to another workspace, or looking at an invite to one, doesn't act on the session's workspace, so a tab that still shows a previous one may do it too
	anyWorkspace := httpserver.Access{SessionOnly: true, AnyWorkspace: true}

	// The workspace the caller is in
	httpserver.Register(api, httpserver.Operation("get-workspace", http.MethodGet, "/api/workspace", "Workspaces"), auth, m.getCurrent)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("update-workspace", http.MethodPatch, "/api/workspace", "Workspaces"), admin), auth, m.rename)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-workspace", http.MethodDelete, "/api/workspace", "Workspaces"), owner), auth, m.deleteCurrent)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("transfer-workspace", http.MethodPost, "/api/workspace/transfer", "Workspaces"), owner), auth, m.transfer)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("leave-workspace", http.MethodPost, "/api/workspace/leave", "Workspaces"), session), auth, m.leave)

	// Its members and invites
	httpserver.Register(api, httpserver.Operation("list-workspace-members", http.MethodGet, "/api/workspace/members", "Workspace members"), auth, m.listMembers)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("update-workspace-member", http.MethodPatch, "/api/workspace/members/{userId}", "Workspace members"), admin), auth, m.updateMember)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("remove-workspace-member", http.MethodDelete, "/api/workspace/members/{userId}", "Workspace members"), admin), auth, m.removeMember)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("list-workspace-invites", http.MethodGet, "/api/workspace/invites", "Workspace members"), admin), auth, m.listInvites)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("create-workspace-invite", http.MethodPost, "/api/workspace/invites", "Workspace members"), admin), auth, m.createInvite)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-workspace-invite", http.MethodDelete, "/api/workspace/invites/{id}", "Workspace members"), admin), auth, m.deleteInvite)

	// The caller's workspaces and the invites sent to them
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("list-my-workspaces", http.MethodGet, "/api/workspaces", "Workspaces"), session), auth, m.listMine)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("create-workspace", http.MethodPost, "/api/workspaces", "Workspaces"), anyWorkspace), auth, m.create)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("switch-workspace", http.MethodPost, "/api/workspaces/{id}/switch", "Workspaces"), anyWorkspace), auth, m.switchTo)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("lookup-invite", http.MethodPost, "/api/invites/lookup", "Workspace members"), anyWorkspace), auth, m.lookupInvite)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("accept-invite", http.MethodPost, "/api/invites/accept", "Workspace members"), anyWorkspace), auth, m.acceptInvite)

	// Every workspace of the instance, for instance admins
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("list-all-workspaces", http.MethodGet, "/api/admin/workspaces", "Admin"), instanceAdmin), auth, m.listAll)
	httpserver.Register(api, httpserver.Restrict(httpserver.Operation("delete-any-workspace", http.MethodDelete, "/api/admin/workspaces/{id}", "Admin"), instanceAdmin), auth, m.deleteAny)
}

// EnsureDefault creates the default workspace, which every replica does on start
// With onlyIfNone it is only created on an instance without any workspace, so a deleted one doesn't come back
func (m *Module) EnsureDefault(ctx context.Context, onlyIfNone bool) error {
	var err error
	if onlyIfNone {
		err = m.queries.EnsureWorkspaceIfNone(ctx, workspacesdb.EnsureWorkspaceIfNoneParams{ID: DefaultID, Name: "Default", CreatedAt: database.Now()})
	} else {
		err = m.queries.EnsureWorkspace(ctx, workspacesdb.EnsureWorkspaceParams{ID: DefaultID, Name: "Default", CreatedAt: database.Now()})
	}
	if err != nil {
		return fmt.Errorf("failed to create the default workspace: %w", err)
	}
	return nil
}

// Access is what a user may do in a workspace
type Access struct {
	Role          principal.Role
	InstanceAdmin bool
}

// Access resolves what the user of a session may do in the workspace the session names
// Instance admins act as owners of every workspace
// It fails with NotSignedIn only when the user has no access, so a database error never looks like being signed out
func (m *Module) Access(ctx context.Context, workspaceID, userID string) (Access, error) {
	row, err := m.access(ctx, workspaceID, userID)
	if err != nil {
		return Access{}, err
	}
	switch {
	case row.IsAdmin:
		return Access{Role: principal.RoleOwner, InstanceAdmin: true}, nil
	case row.Role != nil:
		return Access{Role: principal.Role(*row.Role)}, nil
	default:
		return Access{}, apperror.NotSignedIn()
	}
}

// TokenAccess resolves the role an API token acts with, which is its creator's membership role and never an instance admin's
func (m *Module) TokenAccess(ctx context.Context, workspaceID, creatorID string) (principal.Role, error) {
	row, err := m.access(ctx, workspaceID, creatorID)
	if apperror.IsCode(err, apperror.CodeNotSignedIn) || (err == nil && row.Role == nil) {
		return "", apperror.InvalidToken()
	} else if err != nil {
		return "", err
	}
	return principal.Role(*row.Role), nil
}

// access loads the user's standing in the workspace, failing with NotSignedIn for a user who is gone or deactivated or a workspace that is gone or turned off
func (m *Module) access(ctx context.Context, workspaceID, userID string) (workspacesdb.GetAccessRow, error) {
	if userID == "" || (!m.enabled && workspaceID != DefaultID) {
		return workspacesdb.GetAccessRow{}, apperror.NotSignedIn()
	}
	row, err := m.queries.GetAccess(ctx, workspacesdb.GetAccessParams{WorkspaceID: workspaceID, UserID: userID})
	if database.IsNotFound(err) {
		return workspacesdb.GetAccessRow{}, apperror.NotSignedIn()
	} else if err != nil {
		return workspacesdb.GetAccessRow{}, fmt.Errorf("failed to resolve workspace access: %w", err)
	}
	if row.DisabledAt != nil || row.WorkspaceCount == 0 {
		return workspacesdb.GetAccessRow{}, apperror.NotSignedIn()
	}
	return row, nil
}

// Summary is the name and ID of a workspace
type Summary struct {
	ID   string
	Name string
}

// Get loads a workspace's summary
func (m *Module) Get(ctx context.Context, workspaceID string) (Summary, error) {
	ws, err := m.queries.GetWorkspace(ctx, workspaceID)
	if database.IsNotFound(err) {
		return Summary{}, apperror.NotFound("Workspace")
	} else if err != nil {
		return Summary{}, fmt.Errorf("failed to load workspace: %w", err)
	}
	return Summary{ID: ws.ID, Name: ws.Name}, nil
}
