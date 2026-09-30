package workspaces

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/listquery"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

type workspaceDto struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Role principal.Role `json:"role" enum:"owner,admin,member" doc:"The caller's role in the workspace"`
}

type workspaceOutput struct {
	Body workspaceDto
}

// switchOutput answers requests that move the session into another workspace
type switchOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
	Body      workspaceDto
}

type nameBody struct {
	Name string `json:"name" minLength:"1" maxLength:"100"`
}

// workspaceName trims a name from the API, whose length check runs before trimming
func workspaceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", apperror.InvalidField("name", "required", "must not be empty")
	}
	return name, nil
}

// caller returns the authenticated principal, which every route behind the auth middleware has
func caller(ctx context.Context) (principal.Principal, error) {
	p, ok := principal.From(ctx)
	if !ok {
		return principal.Principal{}, apperror.NotSignedIn()
	}
	return p, nil
}

// sessionIn issues a session for the caller in the workspace and describes the workspace with the role they have there
func (m *Module) sessionIn(ctx context.Context, p principal.Principal, workspaceID string) (*switchOutput, error) {
	access, err := m.Access(ctx, workspaceID, p.UserID)
	if err != nil {
		return nil, err
	}
	ws, err := m.Get(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	// The new session ends when the one it replaces does, since only a sign-in through the provider may start a new lifetime
	cookie, err := m.sessions.SessionCookie(p.UserID, workspaceID, p.LoginProvider, time.Unix(p.SessionExpiresAt, 0))
	if err != nil {
		return nil, err
	}
	return &switchOutput{SetCookie: []http.Cookie{cookie}, Body: workspaceDto{ID: ws.ID, Name: ws.Name, Role: access.Role}}, nil
}

func (m *Module) getCurrent(ctx context.Context, _ *struct{}) (*workspaceOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	ws, err := m.Get(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	return &workspaceOutput{Body: workspaceDto{ID: ws.ID, Name: ws.Name, Role: p.Role}}, nil
}

type renameInput struct {
	Body nameBody
}

func (m *Module) rename(ctx context.Context, in *renameInput) (*workspaceOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	name, err := workspaceName(in.Body.Name)
	if err != nil {
		return nil, err
	}
	err = m.Rename(ctx, p.WorkspaceID, name)
	if err != nil {
		return nil, err
	}
	return &workspaceOutput{Body: workspaceDto{ID: p.WorkspaceID, Name: name, Role: p.Role}}, nil
}

func (m *Module) deleteCurrent(ctx context.Context, _ *struct{}) (*switchOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	err = m.Delete(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}

	// The session moves on to another workspace, which is a new personal one when none is left
	next, err := m.relocate(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return m.sessionIn(ctx, p, next)
}

type transferInput struct {
	Body struct {
		UserID string `json:"userId" doc:"The member who becomes the owner"`
	}
}

func (m *Module) transfer(ctx context.Context, in *transferInput) (*struct{}, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	return nil, m.Transfer(ctx, p.WorkspaceID, in.Body.UserID)
}

func (m *Module) leave(ctx context.Context, _ *struct{}) (*switchOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	err = m.Leave(ctx, p.WorkspaceID, p.UserID)
	if err != nil {
		return nil, err
	}
	next, err := m.relocate(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	return m.sessionIn(ctx, p, next)
}

type memberDto struct {
	UserID      string         `json:"userId"`
	Name        *string        `json:"name"`
	Email       *string        `json:"email"`
	Picture     *string        `json:"picture"`
	Role        principal.Role `json:"role" enum:"owner,admin,member"`
	JoinedAt    int64          `json:"joinedAt"`
	LastLoginAt *int64         `json:"lastLoginAt"`
	Deactivated bool           `json:"deactivated" doc:"Whether an instance admin deactivated the user"`
}

type listMembersInput struct {
	httpserver.ListParams
}

var membersSpec = &listquery.Spec{
	Select:        "SELECT u.id, u.name, u.email, u.picture, m.role, m.created_at, u.last_login_at, u.disabled_at FROM workspace_members m JOIN users u ON u.id = m.user_id",
	From:          "FROM workspace_members m JOIN users u ON u.id = m.user_id",
	Sorts:         map[string]string{"name": "LOWER(COALESCE(u.name, u.email, ''))", "role": "CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 ELSE 2 END", "joinedAt": "m.created_at", "lastLoginAt": "u.last_login_at"},
	NullableSorts: []string{"lastLoginAt"},
	DefaultSort:   "role,name",
	Search:        []string{"u.name", "u.email"},
	TieBreaker:    "u.id",
}

func (m *Module) listMembers(ctx context.Context, in *listMembersInput) (*httpserver.PaginatedOutput[memberDto], error) {
	q := listquery.New(membersSpec).WhereEq("m.workspace_id", principal.WorkspaceID(ctx))
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (memberDto, error) {
		var d memberDto
		var disabledAt *int64
		err := rows.Scan(&d.UserID, &d.Name, &d.Email, &d.Picture, &d.Role, &d.JoinedAt, &d.LastLoginAt, &disabledAt)
		d.Deactivated = disabledAt != nil
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

type updateMemberInput struct {
	UserID string `path:"userId"`
	Body   struct {
		Role principal.Role `json:"role" enum:"admin,member" doc:"The owner changes by handing over ownership instead"`
	}
}

func (m *Module) updateMember(ctx context.Context, in *updateMemberInput) (*struct{}, error) {
	return nil, m.SetRole(ctx, principal.WorkspaceID(ctx), in.UserID, in.Body.Role)
}

type memberInput struct {
	UserID string `path:"userId"`
}

func (m *Module) removeMember(ctx context.Context, in *memberInput) (*struct{}, error) {
	return nil, m.Remove(ctx, principal.WorkspaceID(ctx), in.UserID)
}

type inviteDto struct {
	ID        string         `json:"id"`
	Email     *string        `json:"email" doc:"The address an email invite waits for, empty for an invite link"`
	Role      principal.Role `json:"role" enum:"admin,member"`
	InvitedBy *string        `json:"invitedBy"`
	CreatedAt int64          `json:"createdAt"`
	ExpiresAt int64          `json:"expiresAt"`
}

type listInvitesOutput struct {
	Body []inviteDto
}

func (m *Module) listInvites(ctx context.Context, _ *struct{}) (*listInvitesOutput, error) {
	if !m.enabled {
		return nil, errDisabled()
	}
	rows, err := m.queries.ListInvites(ctx, principal.WorkspaceID(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to list invites: %w", err)
	}
	out := &listInvitesOutput{Body: make([]inviteDto, 0, len(rows))}
	for _, r := range rows {
		invitedBy := r.CreatedByName
		if invitedBy == nil {
			invitedBy = r.CreatedByEmail
		}
		out.Body = append(out.Body, inviteDto{ID: r.ID, Email: r.Email, Role: principal.Role(r.Role), InvitedBy: invitedBy, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt})
	}
	return out, nil
}

type createInviteInput struct {
	Body struct {
		Email         string         `json:"email,omitempty" maxLength:"320" doc:"Invites this address, whose owner joins the next time they sign in with it verified; omit it for an invite link"`
		Role          principal.Role `json:"role" enum:"admin,member"`
		ExpiresInDays int            `json:"expiresInDays" minimum:"1" maximum:"30" default:"7"`
	}
}

type createInviteOutput struct {
	Body struct {
		URL string `json:"url,omitempty" doc:"The invite link, shown once"`
	}
}

func (m *Module) createInvite(ctx context.Context, in *createInviteInput) (*createInviteOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	ttl := time.Duration(in.Body.ExpiresInDays) * 24 * time.Hour
	res, err := m.Invite(ctx, p.WorkspaceID, p.UserID, in.Body.Email, in.Body.Role, ttl)
	if err != nil {
		return nil, err
	}

	out := &createInviteOutput{}
	if res.Token != "" {
		out.Body.URL = m.appURL + InvitePath + res.Token
	}
	return out, nil
}

type inviteIDInput struct {
	ID string `path:"id"`
}

func (m *Module) deleteInvite(ctx context.Context, in *inviteIDInput) (*struct{}, error) {
	return nil, m.DeleteInvite(ctx, principal.WorkspaceID(ctx), in.ID)
}

type listMineOutput struct {
	Body []workspaceDto
}

func (m *Module) listMine(ctx context.Context, _ *struct{}) (*listMineOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := m.queries.ListUserWorkspaces(ctx, p.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspaces: %w", err)
	}

	// Instance admins act as owners everywhere, and with workspaces turned off only the default workspace counts
	out := &listMineOutput{Body: make([]workspaceDto, 0, len(rows)+1)}
	for _, r := range rows {
		if !m.enabled && r.ID != DefaultID {
			continue
		}
		role := principal.Role(r.Role)
		if p.InstanceAdmin {
			role = principal.RoleOwner
		}
		out.Body = append(out.Body, workspaceDto{ID: r.ID, Name: r.Name, Role: role})
	}

	// An instance admin can open a workspace they aren't a member of, which the switcher still has to show
	if !slices.ContainsFunc(out.Body, func(w workspaceDto) bool { return w.ID == p.WorkspaceID }) {
		ws, err := m.Get(ctx, p.WorkspaceID)
		if err != nil {
			return nil, err
		}
		out.Body = append(out.Body, workspaceDto{ID: ws.ID, Name: ws.Name, Role: p.Role})
	}
	return out, nil
}

type createInput struct {
	Body nameBody
}

func (m *Module) create(ctx context.Context, in *createInput) (*switchOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	name, err := workspaceName(in.Body.Name)
	if err != nil {
		return nil, err
	}
	id, err := m.Create(ctx, p.UserID, name)
	if err != nil {
		return nil, err
	}
	return m.sessionIn(ctx, p, id)
}

type workspaceIDInput struct {
	ID string `path:"id"`
}

func (m *Module) switchTo(ctx context.Context, in *workspaceIDInput) (*switchOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	err = m.Switch(ctx, in.ID, p.UserID)
	if err != nil {
		return nil, err
	}
	return m.sessionIn(ctx, p, in.ID)
}

// tokenInput carries an invite token in the body, since request paths end up in the request log
type tokenInput struct {
	Body struct {
		Token string `json:"token" minLength:"1" maxLength:"200"`
	}
}

type invitePreviewOutput struct {
	Body struct {
		WorkspaceName string         `json:"workspaceName"`
		Role          principal.Role `json:"role" enum:"admin,member"`
		InvitedBy     *string        `json:"invitedBy"`
		ExpiresAt     int64          `json:"expiresAt"`
		Expired       bool           `json:"expired"`
		AlreadyMember bool           `json:"alreadyMember"`
	}
}

func (m *Module) lookupInvite(ctx context.Context, in *tokenInput) (*invitePreviewOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	preview, err := m.LookupInvite(ctx, in.Body.Token, p.UserID)
	if err != nil {
		return nil, err
	}
	out := &invitePreviewOutput{}
	out.Body.WorkspaceName = preview.WorkspaceName
	out.Body.Role = preview.Role
	out.Body.InvitedBy = preview.InvitedBy
	out.Body.ExpiresAt = preview.ExpiresAt
	out.Body.Expired = preview.Expired
	out.Body.AlreadyMember = preview.AlreadyMember
	return out, nil
}

func (m *Module) acceptInvite(ctx context.Context, in *tokenInput) (*switchOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	id, err := m.AcceptInvite(ctx, in.Body.Token, p.UserID)
	if err != nil {
		return nil, err
	}
	return m.sessionIn(ctx, p, id)
}

type adminWorkspaceDto struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	CreatedAt   int64   `json:"createdAt"`
	MemberCount int64   `json:"memberCount"`
	OwnerID     *string `json:"ownerId"`
	OwnerName   *string `json:"ownerName"`
	OwnerEmail  *string `json:"ownerEmail"`
}

type listAllInput struct {
	httpserver.ListParams
}

var allWorkspacesSpec = &listquery.Spec{
	Select: `SELECT w.id, w.name, w.created_at, (SELECT COUNT(*) FROM workspace_members c WHERE c.workspace_id = w.id), o.user_id, u.name, u.email
		FROM workspaces w
		LEFT JOIN workspace_members o ON o.workspace_id = w.id AND o.role = 'owner'
		LEFT JOIN users u ON u.id = o.user_id`,
	From:        "FROM workspaces w LEFT JOIN workspace_members o ON o.workspace_id = w.id AND o.role = 'owner' LEFT JOIN users u ON u.id = o.user_id",
	CountFrom:   "FROM workspaces w",
	Sorts:       map[string]string{"name": "LOWER(w.name)", "createdAt": "w.created_at", "members": "(SELECT COUNT(*) FROM workspace_members c WHERE c.workspace_id = w.id)"},
	DefaultSort: "name",
	Search:      []string{"w.name"},
	TieBreaker:  "w.id",
}

func (m *Module) listAll(ctx context.Context, in *listAllInput) (*httpserver.PaginatedOutput[adminWorkspaceDto], error) {
	q := listquery.New(allWorkspacesSpec)
	if !m.enabled {
		q.WhereEq("w.id", DefaultID)
	}
	items, total, err := listquery.Run(ctx, m.db, q, in.ToQuery(), func(rows *sql.Rows) (adminWorkspaceDto, error) {
		var d adminWorkspaceDto
		err := rows.Scan(&d.ID, &d.Name, &d.CreatedAt, &d.MemberCount, &d.OwnerID, &d.OwnerName, &d.OwnerEmail)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	return httpserver.NewPaginated(items, in.ListParams, total), nil
}

// deleteAnyOutput moves the session on only when the admin deleted the workspace they were in
type deleteAnyOutput struct {
	SetCookie []http.Cookie `header:"Set-Cookie"`
}

func (m *Module) deleteAny(ctx context.Context, in *workspaceIDInput) (*deleteAnyOutput, error) {
	p, err := caller(ctx)
	if err != nil {
		return nil, err
	}
	err = m.Delete(ctx, in.ID)
	if err != nil {
		return nil, err
	}
	if in.ID != p.WorkspaceID {
		return &deleteAnyOutput{}, nil
	}
	next, err := m.relocate(ctx, p.UserID)
	if err != nil {
		return nil, err
	}
	cookie, err := m.sessions.SessionCookie(p.UserID, next, p.LoginProvider, time.Unix(p.SessionExpiresAt, 0))
	if err != nil {
		return nil, err
	}
	return &deleteAnyOutput{SetCookie: []http.Cookie{cookie}}, nil
}
