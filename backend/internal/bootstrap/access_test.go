//go:build unit

package bootstrap

import (
	"maps"
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/stonith404/umpteenth/backend/internal/httpserver"
	"github.com/stonith404/umpteenth/backend/internal/principal"
)

// Every operation's access rule, so a new route needs a deliberate decision about who may call it
var (
	anyone        = httpserver.Access{}
	session       = httpserver.Access{SessionOnly: true}
	anyWorkspace  = httpserver.Access{SessionOnly: true, AnyWorkspace: true}
	admin         = httpserver.Access{MinRole: principal.RoleAdmin}
	adminSession  = httpserver.Access{MinRole: principal.RoleAdmin, SessionOnly: true}
	ownerSession  = httpserver.Access{MinRole: principal.RoleOwner, SessionOnly: true}
	instanceAdmin = httpserver.Access{InstanceAdmin: true, SessionOnly: true}
)

var operationAccess = map[string]httpserver.Access{
	// Signing in, and everything anyone signed in may do with their own session
	"list-login-providers": anyone, "login": anyone, "login-callback": anyone, "logout": anyone, "get-current-user": anyone,
	"get-setup": anyone, "begin-passkey-sign-in": anyone, "passkey-sign-in": anyone, "begin-passkey-sign-up": anyone, "passkey-sign-up": anyone, "use-sign-in-link": anyone,
	"update-my-profile": anyWorkspace, "list-my-passkeys": anyWorkspace, "begin-add-passkey": anyWorkspace, "add-passkey": anyWorkspace,
	"rename-passkey": anyWorkspace, "delete-passkey": anyWorkspace,
	"list-my-workspaces": session, "create-workspace": anyWorkspace, "switch-workspace": anyWorkspace,
	"lookup-invite": anyWorkspace, "accept-invite": anyWorkspace, "leave-workspace": session,

	// The workspace itself, its members and invites
	"get-workspace": anyone, "update-workspace": adminSession, "delete-workspace": ownerSession, "transfer-workspace": ownerSession,
	"list-workspace-members": anyone, "update-workspace-member": adminSession, "remove-workspace-member": adminSession,
	"list-workspace-invites": adminSession, "create-workspace-invite": adminSession, "delete-workspace-invite": adminSession,

	// Instance administration
	"list-users": instanceAdmin, "update-user": instanceAdmin, "create-user": instanceAdmin, "create-sign-in-link": instanceAdmin, "list-all-workspaces": instanceAdmin, "delete-any-workspace": instanceAdmin,

	// API tokens act with their creator's role, and only a session can mint or revoke them
	"list-api-tokens": anyone, "create-api-token": session, "delete-api-token": session,

	// Members read the providers and settings, and only admins change them
	"list-providers": anyone, "create-provider": admin, "update-provider": admin, "delete-provider": admin, "test-provider": admin,
	"sync-provider-models": admin, "set-provider-models-enabled": admin,
	"list-models": anyone, "create-model": admin, "update-model": admin, "delete-model": admin, "get-model-catalog": anyone,
	"get-settings": anyone, "update-settings": admin, "test-notification": admin, "get-system-info": anyone,

	// Jobs, runs, secrets and MCP servers are every member's work
	"list-jobs": anyone, "create-job": anyone, "compile-job": anyone, "get-job": anyone, "update-job": anyone, "delete-job": anyone,
	"run-job": anyone, "rotate-webhook-token": anyone, "trigger-webhook": anyone, "get-job-stats": anyone,
	"list-job-state": anyone, "get-job-state": anyone, "put-job-state": anyone, "delete-job-state": anyone,
	"get-job-secrets": anyone, "set-job-secrets": anyone, "get-job-mcp-servers": anyone, "set-job-mcp-servers": anyone,
	"list-job-images": anyone, "get-image": anyone, "rebuild-job-image": anyone,
	"get-playbook": anyone, "update-playbook": anyone, "list-playbook-versions": anyone, "get-playbook-version": anyone, "rollback-playbook": anyone,
	"list-runs": anyone, "get-run": anyone, "cancel-run": anyone, "retry-run": anyone, "learn-from-run": anyone, "delete-run": admin, "delete-runs": admin,
	"list-run-events": anyone, "list-run-artifacts": anyone, "get-run-artifact": anyone, "stream-run": anyone, "stream-workspace-events": anyone,
	"get-stats-overview": anyone,
	"list-secrets":       anyone, "create-secret": anyone, "update-secret": anyone, "delete-secret": anyone,
	"list-mcp-servers": anyone, "create-mcp-server": anyone, "get-mcp-server": anyone, "update-mcp-server": anyone, "delete-mcp-server": anyone,
	"test-mcp-server": anyone, "login-mcp-server": session, "mcp-server-oauth-callback": anyone, "logout-mcp-server": anyone,
}

func TestEveryOperationHasTheExpectedAccessRule(t *testing.T) {
	spec, err := OpenAPI()
	require.NoError(t, err)

	seen := map[string]bool{}
	for path, item := range spec.Paths {
		for _, op := range []*huma.Operation{item.Get, item.Post, item.Put, item.Patch, item.Delete} {
			if op == nil {
				continue
			}
			seen[op.OperationID] = true
			want, ok := operationAccess[op.OperationID]
			if !assert.True(t, ok, "%s %s (%s) has no access rule in operationAccess, decide who may call it", op.Method, path, op.OperationID) {
				continue
			}
			assert.Equal(t, want, httpserver.AccessOf(op), "%s has an unexpected access rule", op.OperationID)
		}
	}
	for _, id := range slices.Sorted(maps.Keys(operationAccess)) {
		assert.True(t, seen[id], "operationAccess lists %s, which no longer exists", id)
	}
}
