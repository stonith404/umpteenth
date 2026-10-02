import { goto } from '$app/navigation';
import type { User, WorkspaceRole } from '#lib/api/types.js';
import { clearNewJobDrafts } from '#lib/utils/job-util.js';
import { resetWorkspaceEvents } from '#lib/utils/workspace-events.js';

const roleRanks: Record<WorkspaceRole, number> = { member: 1, admin: 2, owner: 3 };

export const roleLabels: Record<WorkspaceRole, string> = {
	owner: 'Owner',
	admin: 'Admin',
	member: 'Member'
};

export const roleDescriptions: Record<Exclude<WorkspaceRole, 'owner'>, string> = {
	admin: 'Manages members, providers and settings.',
	member: 'Works with jobs, runs, MCP servers, skills and secrets.'
};

// Whether the user's role in their current workspace allows everything the given role may do
export function hasRole(user: Pick<User, 'workspace'>, role: WorkspaceRole) {
	return roleRanks[user.workspace.role] >= roleRanks[role];
}

// The letter a workspace shows as its icon, like an avatar
export function workspaceInitial(name: string) {
	return name.trim().charAt(0).toUpperCase() || 'W';
}

// Leaves everything of the previous workspace behind once the session moved to another one
// The page may show something that only existed in the previous workspace, so it starts over on the dashboard
export async function enterWorkspace() {
	clearNewJobDrafts();
	resetWorkspaceEvents();
	await goto('/', { refreshAll: true });
}
