import type { AdminUserCreate, QueryOf } from '#lib/api/types.js';
import APIService from './api-service';

// Every user and workspace of the instance, for instance admins
export default class AdminService extends APIService {
	listUsers = (query?: QueryOf<'list-users'>) =>
		this.unwrap(this.api.GET('/api/admin/users', { params: { query } }));

	setDeactivated = (id: string, deactivated: boolean) =>
		this.unwrap(
			this.api.PATCH('/api/admin/users/{id}', { params: { path: { id } }, body: { deactivated } })
		);

	// Only passkey accounts get their admin rights here, sign-in providers decide on their own accounts
	setAdmin = (id: string, isAdmin: boolean) =>
		this.unwrap(
			this.api.PATCH('/api/admin/users/{id}', { params: { path: { id } }, body: { isAdmin } })
		);

	// Creates a passkey account and the sign-in link through which its owner adds their first passkey
	createUser = (body: AdminUserCreate) => this.unwrap(this.api.POST('/api/admin/users', { body }));

	// Replaces the sign-in links of a passkey account with a new one, e.g. for someone who lost their passkeys
	createSignInLink = (id: string) =>
		this.unwrap(this.api.POST('/api/admin/users/{id}/sign-in-link', { params: { path: { id } } }));

	listWorkspaces = (query?: QueryOf<'list-all-workspaces'>) =>
		this.unwrap(this.api.GET('/api/admin/workspaces', { params: { query } }));

	deleteWorkspace = (id: string) =>
		this.unwrap(this.api.DELETE('/api/admin/workspaces/{id}', { params: { path: { id } } }));
}
