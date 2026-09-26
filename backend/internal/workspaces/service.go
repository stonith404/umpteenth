package workspaces

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/database"
	"github.com/stonith404/umpteenth/backend/internal/principal"
	"github.com/stonith404/umpteenth/backend/internal/utils/crypto"
	"github.com/stonith404/umpteenth/backend/internal/workspaces/workspacesdb"
)

// InvitePath is the page an invite link opens, followed by the invite's token
const InvitePath = "/invite/"

// maxNameLength bounds workspace names, which the API also checks on input
const maxNameLength = 100

// errDisabled answers everything that needs several workspaces while they are turned off
func errDisabled() error {
	return apperror.Forbidden("Workspaces are turned off on this instance")
}

// LoginInfo is who just signed in
type LoginInfo struct {
	UserID string
	// VerifiedEmail is the user's email address when their sign-in provider vouched for it, which is what matches email invites
	VerifiedEmail string
	// Redirect is where the login returns to, which is the invite page when the login started from an invite link
	Redirect string
}

// ResolveLogin picks the workspace a sign-in lands in and returns where the browser goes next
// With workspaces turned on it first accepts the invites the sign-in brings along, and a user left without any workspace gets a personal one
// Someone who has a workspace already stays in it, and an invite link they started the sign-in from waits for them to confirm it on the invite page
func (m *Module) ResolveLogin(ctx context.Context, info LoginInfo) (workspaceID, redirect string, err error) {
	redirect = info.Redirect

	// With workspaces turned off everyone joins the default workspace, which its first member owns
	if !m.enabled {
		err = m.db.InTx(ctx, func(tx *database.Tx) error {
			return joinDefault(ctx, workspacesdb.New(tx), info.UserID)
		})
		if err != nil {
			return "", "", fmt.Errorf("failed to join the default workspace: %w", err)
		}
		return DefaultID, redirect, nil
	}

	var created string
	err = m.db.InTx(ctx, func(tx *database.Tx) error {
		q := workspacesdb.New(tx)
		now := database.Now()

		// Concurrent sign-ins of the same user take turns, so they can't both create a personal workspace
		err := q.LockUser(ctx, info.UserID)
		if err != nil {
			return err
		}

		// Only a user without a workspace yet lands in the one an invite joins, since any page can start a sign-in with an invite link in the browser of someone who is signed in already
		count, err := q.CountUserMemberships(ctx, info.UserID)
		if err != nil {
			return err
		}
		newcomer := count == 0

		// Invites to the user's verified address join them right away, and one sent to someone who is a member already is used up as well
		joined := ""
		if info.VerifiedEmail != "" {
			email := normalizeEmail(info.VerifiedEmail)
			invites, err := q.ListEmailInvites(ctx, workspacesdb.ListEmailInvitesParams{Email: &email, Now: now})
			if err != nil {
				return err
			}
			for _, inv := range invites {
				n, err := q.AddMember(ctx, workspacesdb.AddMemberParams{WorkspaceID: inv.WorkspaceID, UserID: info.UserID, Role: inv.Role, CreatedAt: now})
				if err != nil {
					return err
				}
				_, err = q.DeleteInvite(ctx, workspacesdb.DeleteInviteParams{WorkspaceID: inv.WorkspaceID, ID: inv.ID})
				if err != nil {
					return err
				}
				if n > 0 && newcomer {
					joined = inv.WorkspaceID
				}
			}
		}

		// A newcomer's login that started from an invite link accepts it and goes to the workspace instead of back to the invite page
		// Anyone else goes back to the invite page, which asks before they join, and so does an unknown or expired invite, whose page explains what went wrong
		if token, ok := strings.CutPrefix(info.Redirect, InvitePath); ok && token != "" && newcomer {
			wid, err := accept(ctx, q, token, info.UserID, now)
			switch {
			case err == nil:
				joined, redirect = wid, "/"
			case apperror.IsCode(err, apperror.CodeNotFound), apperror.IsCode(err, apperror.CodeConflict):
			default:
				return err
			}
		}

		workspaceID, created, err = landing(ctx, q, info.UserID, joined, now)
		return err
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to pick the workspace to sign in to: %w", err)
	}

	m.seedWorkspace(ctx, created)
	return workspaceID, redirect, nil
}

// relocate picks a new workspace for a session whose workspace the user just left or deleted
func (m *Module) relocate(ctx context.Context, userID string) (string, error) {
	var workspaceID, created string
	err := m.db.InTx(ctx, func(tx *database.Tx) error {
		q := workspacesdb.New(tx)
		err := q.LockUser(ctx, userID)
		if err != nil {
			return err
		}
		workspaceID, created, err = landing(ctx, q, userID, "", database.Now())
		return err
	})
	if err != nil {
		return "", fmt.Errorf("failed to pick the next workspace: %w", err)
	}

	m.seedWorkspace(ctx, created)
	return workspaceID, nil
}

// joinDefault makes the user a member of the default workspace, as its owner if it has none yet
func joinDefault(ctx context.Context, q *workspacesdb.Queries, userID string) error {
	now := database.Now()
	n, err := q.ClaimOwnerless(ctx, workspacesdb.ClaimOwnerlessParams{WorkspaceID: DefaultID, UserID: userID, CreatedAt: now})
	if err != nil {
		return err
	}
	if n == 0 {
		_, err = q.AddMember(ctx, workspacesdb.AddMemberParams{WorkspaceID: DefaultID, UserID: userID, Role: string(principal.RoleMember), CreatedAt: now})
		if err != nil {
			return err
		}
	}
	return q.SetLastWorkspace(ctx, workspacesdb.SetLastWorkspaceParams{ID: userID, WorkspaceID: new(DefaultID)})
}

// landing picks the workspace a user lands in: the one they just joined, else the one they were in last, else their oldest
// A user without any workspace claims a default workspace nobody owns yet, which is how a fresh instance gets its owner, or otherwise gets a personal workspace, whose ID it returns as created
func landing(ctx context.Context, q *workspacesdb.Queries, userID, joined string, now int64) (workspaceID, created string, err error) {
	// Make sure the user has a workspace at all
	count, err := q.CountUserMemberships(ctx, userID)
	if err != nil {
		return "", "", err
	}
	if count == 0 {
		n, err := q.ClaimOwnerless(ctx, workspacesdb.ClaimOwnerlessParams{WorkspaceID: DefaultID, UserID: userID, CreatedAt: now})
		if err != nil {
			return "", "", err
		}
		if n > 0 {
			joined = DefaultID
		} else {
			user, err := q.GetLoginUser(ctx, userID)
			if err != nil {
				return "", "", err
			}
			created = database.NewID()
			err = q.CreateWorkspace(ctx, workspacesdb.CreateWorkspaceParams{ID: created, Name: personalName(user.Name, user.Email), CreatedAt: now})
			if err != nil {
				return "", "", err
			}
			_, err = q.AddMember(ctx, workspacesdb.AddMemberParams{WorkspaceID: created, UserID: userID, Role: string(principal.RoleOwner), CreatedAt: now})
			if err != nil {
				return "", "", err
			}
			joined = created
		}
	}

	// Prefer the workspace just joined, then the last one the user is still a member of, then the oldest
	workspaceID = joined
	if workspaceID == "" {
		user, err := q.GetLoginUser(ctx, userID)
		if err != nil {
			return "", "", err
		}
		if user.LastWorkspaceID != nil {
			_, err = q.GetMemberRole(ctx, workspacesdb.GetMemberRoleParams{WorkspaceID: *user.LastWorkspaceID, UserID: userID})
			if err == nil {
				workspaceID = *user.LastWorkspaceID
			} else if !database.IsNotFound(err) {
				return "", "", err
			}
		}
	}
	if workspaceID == "" {
		workspaceID, err = q.OldestMembership(ctx, userID)
		if err != nil {
			return "", "", err
		}
	}

	err = q.SetLastWorkspace(ctx, workspacesdb.SetLastWorkspaceParams{ID: userID, WorkspaceID: &workspaceID})
	return workspaceID, created, err
}

// personalName names the workspace a new user gets, after their name or else their email address
func personalName(name, email *string) string {
	owner := ""
	switch {
	case name != nil && strings.TrimSpace(*name) != "":
		owner = strings.TrimSpace(*name)
	case email != nil && *email != "":
		owner, _, _ = strings.Cut(*email, "@")
	}
	if owner == "" {
		return "Personal workspace"
	}
	suffix := "'s workspace"
	if runes := []rune(owner); len(runes)+len(suffix) > maxNameLength {
		owner = string(runes[:maxNameLength-len(suffix)])
	}
	return owner + suffix
}

// seedWorkspace runs the seeder on a new workspace after its transaction committed, since the seeder writes outside it
// A failure only costs the new workspace what the seeder adds, so it is logged instead of failing the request
func (m *Module) seedWorkspace(ctx context.Context, workspaceID string) {
	if workspaceID == "" || m.seed == nil {
		return
	}
	err := m.seed(ctx, workspaceID)
	if err != nil {
		slog.WarnContext(ctx, "Failed to seed a new workspace", slog.String("workspace", workspaceID), slog.Any("error", err))
	}
}

// Create makes a workspace owned by the user
func (m *Module) Create(ctx context.Context, userID, name string) (string, error) {
	if !m.enabled {
		return "", errDisabled()
	}

	id := database.NewID()
	err := m.db.InTx(ctx, func(tx *database.Tx) error {
		q := workspacesdb.New(tx)
		now := database.Now()
		err := q.CreateWorkspace(ctx, workspacesdb.CreateWorkspaceParams{ID: id, Name: name, CreatedAt: now})
		if err != nil {
			return err
		}
		_, err = q.AddMember(ctx, workspacesdb.AddMemberParams{WorkspaceID: id, UserID: userID, Role: string(principal.RoleOwner), CreatedAt: now})
		if err != nil {
			return err
		}
		return q.SetLastWorkspace(ctx, workspacesdb.SetLastWorkspaceParams{ID: userID, WorkspaceID: &id})
	})
	if err != nil {
		return "", fmt.Errorf("failed to create workspace: %w", err)
	}

	m.seedWorkspace(ctx, id)
	return id, nil
}

// Rename changes a workspace's name
func (m *Module) Rename(ctx context.Context, workspaceID, name string) error {
	n, err := m.queries.RenameWorkspace(ctx, workspacesdb.RenameWorkspaceParams{ID: workspaceID, Name: name})
	if err != nil {
		return fmt.Errorf("failed to rename workspace: %w", err)
	}
	if n == 0 {
		return apperror.NotFound("Workspace")
	}
	return nil
}

// Delete removes a workspace with everything in it
func (m *Module) Delete(ctx context.Context, workspaceID string) error {
	if !m.enabled {
		return errDisabled()
	}

	// Refuse while runs are active, and release what the rows don't take with them
	var after func(context.Context)
	if m.cleanup != nil {
		var err error
		after, err = m.cleanup.BeforeDelete(ctx, workspaceID)
		if err != nil {
			return err
		}
	}

	// Every tenant row goes with the workspace through its foreign key
	n, err := m.queries.DeleteWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("failed to delete workspace: %w", err)
	}
	if n == 0 {
		return apperror.NotFound("Workspace")
	}

	// Files can take a while to delete, so they go after the response in the background
	if after != nil {
		go func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Minute)
			defer cancel()
			after(cleanupCtx)
		}()
	}
	return nil
}

// Transfer makes a member the owner of the workspace, and the previous owner an admin
func (m *Module) Transfer(ctx context.Context, workspaceID, toUserID string) error {
	return m.db.InTx(ctx, func(tx *database.Tx) error {
		q := workspacesdb.New(tx)

		// Only a member can take over, and the owner taking over changes nothing
		role, err := q.GetMemberRole(ctx, workspacesdb.GetMemberRoleParams{WorkspaceID: workspaceID, UserID: toUserID})
		if database.IsNotFound(err) {
			return apperror.NotFound("Member")
		} else if err != nil {
			return fmt.Errorf("failed to load member: %w", err)
		}
		if principal.Role(role) == principal.RoleOwner {
			return nil
		}

		// The unique owner index is checked per statement, so the current owner steps down first
		owner, err := q.GetOwner(ctx, workspaceID)
		if err == nil {
			_, err = q.SetMemberRole(ctx, workspacesdb.SetMemberRoleParams{WorkspaceID: workspaceID, UserID: owner, Role: string(principal.RoleAdmin)})
			if err != nil {
				return fmt.Errorf("failed to demote the owner: %w", err)
			}
		} else if !database.IsNotFound(err) {
			return fmt.Errorf("failed to load the owner: %w", err)
		}
		_, err = q.SetMemberRole(ctx, workspacesdb.SetMemberRoleParams{WorkspaceID: workspaceID, UserID: toUserID, Role: string(principal.RoleOwner)})
		if err != nil {
			return fmt.Errorf("failed to promote the new owner: %w", err)
		}
		return nil
	})
}

// Leave removes the user from the workspace, which the owner can only do after handing it over
func (m *Module) Leave(ctx context.Context, workspaceID, userID string) error {
	if !m.enabled {
		return errDisabled()
	}
	role, err := m.queries.GetMemberRole(ctx, workspacesdb.GetMemberRoleParams{WorkspaceID: workspaceID, UserID: userID})
	if database.IsNotFound(err) {
		return apperror.Conflict("You aren't a member of this workspace")
	} else if err != nil {
		return fmt.Errorf("failed to load membership: %w", err)
	}
	if principal.Role(role) == principal.RoleOwner {
		return apperror.Conflict("Hand over ownership before leaving the workspace")
	}
	_, err = m.queries.RemoveMember(ctx, workspacesdb.RemoveMemberParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to leave workspace: %w", err)
	}
	return nil
}

// SetRole changes a member's role, which can't make or unmake the owner since that takes a handover
func (m *Module) SetRole(ctx context.Context, workspaceID, userID string, role principal.Role) error {
	if role != principal.RoleAdmin && role != principal.RoleMember {
		return apperror.InvalidField("role", "invalid", "must be admin or member, the owner changes by handing over ownership")
	}
	current, err := m.memberRole(ctx, workspaceID, userID)
	if err != nil {
		return err
	}
	if current == principal.RoleOwner {
		return apperror.Forbidden("The owner's role changes by handing over ownership")
	}
	_, err = m.queries.SetMemberRole(ctx, workspacesdb.SetMemberRoleParams{WorkspaceID: workspaceID, UserID: userID, Role: string(role)})
	if err != nil {
		return fmt.Errorf("failed to change role: %w", err)
	}
	return nil
}

// Remove takes a member out of the workspace
// With workspaces turned off people would only rejoin at their next sign-in, so they are deactivated instead
func (m *Module) Remove(ctx context.Context, workspaceID, userID string) error {
	if !m.enabled {
		return apperror.Forbidden("Everyone who signs in joins this workspace, so deactivate the user instead")
	}
	current, err := m.memberRole(ctx, workspaceID, userID)
	if err != nil {
		return err
	}
	if current == principal.RoleOwner {
		return apperror.Forbidden("The owner can't be removed, only hand over ownership")
	}
	_, err = m.queries.RemoveMember(ctx, workspacesdb.RemoveMemberParams{WorkspaceID: workspaceID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}
	return nil
}

func (m *Module) memberRole(ctx context.Context, workspaceID, userID string) (principal.Role, error) {
	role, err := m.queries.GetMemberRole(ctx, workspacesdb.GetMemberRoleParams{WorkspaceID: workspaceID, UserID: userID})
	if database.IsNotFound(err) {
		return "", apperror.NotFound("Member")
	} else if err != nil {
		return "", fmt.Errorf("failed to load member: %w", err)
	}
	return principal.Role(role), nil
}

// Switch confirms the user may open the workspace and remembers it as the one to sign in to next time
func (m *Module) Switch(ctx context.Context, workspaceID, userID string) error {
	if !m.enabled && workspaceID != DefaultID {
		return errDisabled()
	}
	_, err := m.Access(ctx, workspaceID, userID)
	if apperror.IsCode(err, apperror.CodeNotSignedIn) {
		return apperror.NotFound("Workspace")
	} else if err != nil {
		return err
	}
	err = m.queries.SetLastWorkspace(ctx, workspacesdb.SetLastWorkspaceParams{ID: userID, WorkspaceID: &workspaceID})
	if err != nil {
		return fmt.Errorf("failed to remember the workspace: %w", err)
	}
	return nil
}

// InviteResult is what creating an invite produced
type InviteResult struct {
	// InviteID is empty when the invite added someone right away
	InviteID string
	// Token is the secret of an invite link, which is only ever shown here
	Token string
	// MemberAdded is set when the email address belonged to a user, who joined right away
	MemberAdded bool
}

// Invite invites someone to the workspace, through a link when email is empty or else through their email address
func (m *Module) Invite(ctx context.Context, workspaceID, createdBy, email string, role principal.Role, ttl time.Duration) (InviteResult, error) {
	if !m.enabled {
		return InviteResult{}, errDisabled()
	}
	if role != principal.RoleAdmin && role != principal.RoleMember {
		return InviteResult{}, apperror.InvalidField("role", "invalid", "must be admin or member")
	}
	now := database.Now()
	expiresAt := now + ttl.Milliseconds()

	// A link invite is a secret only the person it is sent to knows, so only its hash is stored
	if email == "" {
		token := crypto.RandomToken(32)
		hash := crypto.HashToken(token)
		id := database.NewID()
		err := m.queries.CreateLinkInvite(ctx, workspacesdb.CreateLinkInviteParams{
			ID: id, WorkspaceID: workspaceID, Role: string(role), TokenHash: &hash, CreatedBy: &createdBy, CreatedAt: now, ExpiresAt: expiresAt,
		})
		if err != nil {
			return InviteResult{}, fmt.Errorf("failed to create invite: %w", err)
		}
		return InviteResult{InviteID: id, Token: token}, nil
	}

	// An address that belongs to exactly one verified user adds them right away, and otherwise the invite waits for a sign-in with it
	email = normalizeEmail(email)
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return InviteResult{}, apperror.InvalidField("email", "invalid", "must be an email address")
	}
	users, err := m.queries.FindVerifiedUsersByEmail(ctx, &email)
	if err != nil {
		return InviteResult{}, fmt.Errorf("failed to look up the invited user: %w", err)
	}
	if len(users) == 1 {
		n, err := m.queries.AddMember(ctx, workspacesdb.AddMemberParams{WorkspaceID: workspaceID, UserID: users[0], Role: string(role), CreatedAt: now})
		if err != nil {
			return InviteResult{}, fmt.Errorf("failed to add member: %w", err)
		}
		if n == 0 {
			return InviteResult{}, apperror.Conflict("That person is already a member of this workspace")
		}
		return InviteResult{MemberAdded: true}, nil
	}
	id, err := m.queries.UpsertEmailInvite(ctx, workspacesdb.UpsertEmailInviteParams{
		ID: database.NewID(), WorkspaceID: workspaceID, Role: string(role), Email: &email, CreatedBy: &createdBy, CreatedAt: now, ExpiresAt: expiresAt,
	})
	if err != nil {
		return InviteResult{}, fmt.Errorf("failed to create invite: %w", err)
	}
	return InviteResult{InviteID: id}, nil
}

// DeleteInvite revokes an invite
func (m *Module) DeleteInvite(ctx context.Context, workspaceID, inviteID string) error {
	n, err := m.queries.DeleteInvite(ctx, workspacesdb.DeleteInviteParams{WorkspaceID: workspaceID, ID: inviteID})
	if err != nil {
		return fmt.Errorf("failed to delete invite: %w", err)
	}
	if n == 0 {
		return apperror.NotFound("Invite")
	}
	return nil
}

// InvitePreview is what an invite link offers, shown before it is accepted
type InvitePreview struct {
	WorkspaceID   string
	WorkspaceName string
	Role          principal.Role
	InvitedBy     *string
	ExpiresAt     int64
	Expired       bool
	AlreadyMember bool
}

// LookupInvite shows what an invite link offers to the user who opened it
func (m *Module) LookupInvite(ctx context.Context, token, userID string) (InvitePreview, error) {
	if !m.enabled {
		return InvitePreview{}, errDisabled()
	}
	hash := crypto.HashToken(token)
	inv, err := m.queries.GetInviteByTokenHash(ctx, &hash)
	if database.IsNotFound(err) {
		return InvitePreview{}, apperror.NotFound("Invite")
	} else if err != nil {
		return InvitePreview{}, fmt.Errorf("failed to load invite: %w", err)
	}

	_, err = m.queries.GetMemberRole(ctx, workspacesdb.GetMemberRoleParams{WorkspaceID: inv.WorkspaceID, UserID: userID})
	if err != nil && !database.IsNotFound(err) {
		return InvitePreview{}, fmt.Errorf("failed to load membership: %w", err)
	}
	invitedBy := inv.CreatedByName
	if invitedBy == nil {
		invitedBy = inv.CreatedByEmail
	}
	return InvitePreview{
		WorkspaceID:   inv.WorkspaceID,
		WorkspaceName: inv.WorkspaceName,
		Role:          principal.Role(inv.Role),
		InvitedBy:     invitedBy,
		ExpiresAt:     inv.ExpiresAt,
		Expired:       inv.ExpiresAt <= database.Now(),
		AlreadyMember: err == nil,
	}, nil
}

// AcceptInvite joins the user to the workspace of an invite link and returns the workspace
func (m *Module) AcceptInvite(ctx context.Context, token, userID string) (string, error) {
	if !m.enabled {
		return "", errDisabled()
	}
	var workspaceID string
	err := m.db.InTx(ctx, func(tx *database.Tx) error {
		q := workspacesdb.New(tx)
		var err error
		workspaceID, err = accept(ctx, q, token, userID, database.Now())
		if err != nil {
			return err
		}
		return q.SetLastWorkspace(ctx, workspacesdb.SetLastWorkspaceParams{ID: userID, WorkspaceID: &workspaceID})
	})
	return workspaceID, err
}

// accept joins the user to the workspace of an invite link
// The link is used up unless the user was a member already, so it can still reach the person it was meant for
func accept(ctx context.Context, q *workspacesdb.Queries, token, userID string, now int64) (string, error) {
	hash := crypto.HashToken(token)
	inv, err := q.GetInviteByTokenHash(ctx, &hash)
	if database.IsNotFound(err) {
		return "", apperror.NotFound("Invite")
	} else if err != nil {
		return "", fmt.Errorf("failed to load invite: %w", err)
	}
	if inv.ExpiresAt <= now {
		return "", apperror.Conflict("This invite has expired, ask for a new one")
	}

	n, err := q.AddMember(ctx, workspacesdb.AddMemberParams{WorkspaceID: inv.WorkspaceID, UserID: userID, Role: inv.Role, CreatedAt: now})
	if err != nil {
		return "", fmt.Errorf("failed to join workspace: %w", err)
	}
	if n > 0 {
		_, err = q.DeleteInvite(ctx, workspacesdb.DeleteInviteParams{WorkspaceID: inv.WorkspaceID, ID: inv.ID})
		if err != nil {
			return "", fmt.Errorf("failed to use up invite: %w", err)
		}
	}
	return inv.WorkspaceID, nil
}

// normalizeEmail makes addresses comparable, since providers and people differ in how they capitalize them
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
