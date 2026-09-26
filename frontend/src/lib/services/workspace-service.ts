import type { QueryOf, WorkspaceInviteCreate, WorkspaceRole } from '$lib/api/types';
import APIService from './api-service';

// The session's workspace lives in the session cookie, so every call that moves to another workspace answers with a new cookie
export default class WorkspaceService extends APIService {
	// The workspace the session is in
	rename = (name: string) => this.unwrap(this.api.PATCH('/api/workspace', { body: { name } }));

	delete = () => this.unwrap(this.api.DELETE('/api/workspace'));

	transfer = (userId: string) =>
		this.unwrap(this.api.POST('/api/workspace/transfer', { body: { userId } }));

	leave = () => this.unwrap(this.api.POST('/api/workspace/leave'));

	// Its members and invites
	listMembers = (query?: QueryOf<'list-workspace-members'>) =>
		this.unwrap(this.api.GET('/api/workspace/members', { params: { query } }));

	updateMember = (userId: string, role: Exclude<WorkspaceRole, 'owner'>) =>
		this.unwrap(
			this.api.PATCH('/api/workspace/members/{userId}', {
				params: { path: { userId } },
				body: { role }
			})
		);

	removeMember = (userId: string) =>
		this.unwrap(
			this.api.DELETE('/api/workspace/members/{userId}', { params: { path: { userId } } })
		);

	listInvites = () => this.unwrap(this.api.GET('/api/workspace/invites'));

	createInvite = (body: WorkspaceInviteCreate) =>
		this.unwrap(this.api.POST('/api/workspace/invites', { body }));

	deleteInvite = (id: string) =>
		this.unwrap(this.api.DELETE('/api/workspace/invites/{id}', { params: { path: { id } } }));

	// The caller's workspaces
	listMine = () => this.unwrap(this.api.GET('/api/workspaces'));

	create = (name: string) => this.unwrap(this.api.POST('/api/workspaces', { body: { name } }));

	switchTo = (id: string) =>
		this.unwrap(this.api.POST('/api/workspaces/{id}/switch', { params: { path: { id } } }));

	// Invite links carry their token in the body, since request paths end up in the server's logs
	lookupInvite = (token: string) =>
		this.unwrap(this.api.POST('/api/invites/lookup', { body: { token } }));

	acceptInvite = (token: string) =>
		this.unwrap(this.api.POST('/api/invites/accept', { body: { token } }));
}
