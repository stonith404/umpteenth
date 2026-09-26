package httpserver

import (
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/stonith404/umpteenth/backend/internal/apperror"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// accessMetadataKey is where Restrict keeps an operation's access rule
const accessMetadataKey = "umpteenth.access"

// Access says who may call an operation beyond being signed in, which the auth middleware checks once it knows the caller
// The zero value lets in any member of the workspace, with a session or an API token
type Access struct {
	// MinRole is the lowest workspace role that may call the operation
	MinRole principal.Role
	// SessionOnly keeps API tokens out, for operations that change who has access, so a leaked token can't grant itself lasting access
	SessionOnly bool
	// InstanceAdmin limits the operation to instance admins
	InstanceAdmin bool
	// AnyWorkspace is for operations that don't act on the session's workspace, such as switching to another one, which a browser tab that still shows a previous workspace may call too
	AnyWorkspace bool
}

// Restrict attaches an access rule to an operation and documents it in the operation's description
func Restrict(op huma.Operation, access Access) huma.Operation {
	if op.Metadata == nil {
		op.Metadata = map[string]any{}
	}
	op.Metadata[accessMetadataKey] = access
	op.Description = strings.TrimSpace(op.Description + "\n\n" + access.String())
	return op
}

// AccessOf returns the access rule Restrict attached to an operation, or the zero rule
func AccessOf(op *huma.Operation) Access {
	if op == nil {
		return Access{}
	}
	access, _ := op.Metadata[accessMetadataKey].(Access)
	return access
}

// Check returns a forbidden error when the caller may not call the operation
func (a Access) Check(p principal.Principal) error {
	if a.InstanceAdmin && !p.InstanceAdmin {
		return apperror.Forbidden("Only instance admins can do this")
	}
	if a.SessionOnly && p.TokenID != "" {
		return apperror.Forbidden("This can only be done while signed in, not with an API token")
	}
	if a.MinRole != "" && !p.Role.AtLeast(a.MinRole) {
		if a.MinRole == principal.RoleOwner {
			return apperror.Forbidden("Only the owner of the workspace can do this")
		}
		return apperror.Forbidden("Only admins of the workspace can do this")
	}
	return nil
}

// String describes the rule for the API reference
func (a Access) String() string {
	var parts []string
	if a.InstanceAdmin {
		parts = append(parts, "an instance admin")
	}
	switch a.MinRole {
	case "":
	case principal.RoleOwner:
		parts = append(parts, "the owner role")
	default:
		parts = append(parts, "the "+string(a.MinRole)+" role or above")
	}
	if a.SessionOnly {
		parts = append(parts, "a signed-in session instead of an API token")
	}
	if len(parts) == 0 {
		return ""
	}
	return "Requires " + strings.Join(parts, " and ") + "."
}
