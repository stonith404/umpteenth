import type { QueryOf } from '$lib/api/types';
import APIService from './api-service';

// Every user and workspace of the instance, for instance admins
export default class AdminService extends APIService {
	listUsers = (query?: QueryOf<'list-users'>) =>
		this.unwrap(this.api.GET('/api/admin/users', { params: { query } }));

	setDeactivated = (id: string, deactivated: boolean) =>
		this.unwrap(
			this.api.PATCH('/api/admin/users/{id}', { params: { path: { id } }, body: { deactivated } })
		);

	listWorkspaces = (query?: QueryOf<'list-all-workspaces'>) =>
		this.unwrap(this.api.GET('/api/admin/workspaces', { params: { query } }));

	deleteWorkspace = (id: string) =>
		this.unwrap(this.api.DELETE('/api/admin/workspaces/{id}', { params: { path: { id } } }));
}
